#!/bin/bash
# Rejea's parser (PaddleOCR-VL-1.6) on one NVIDIA GPU: the vLLM model server, 8 parse API processes, one address.
# Sources: PaddleOCR-VL guide (pipeline settings for vllm-server), vLLM recipe for PaddlePaddle/PaddleOCR-VL-1.6.
SECRET="<a long random text>"  # the address answers only under this path
fault() { echo "FAULT: $1"; exit 1; }
t0=$(date +%s); step() { echo "STEP $(( $(date +%s) - t0 ))s: $1"; }
nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv,noheader || fault "no GPU"

step "install the model server (vLLM) and the parse software, at the same time"
( pip install -q uv && python -m venv /opt/vllm && uv pip install -q --python /opt/vllm/bin/python "vllm==0.21.0" "transformers==5.8.1" ) > /tmp/install-vllm.log 2>&1 &
vp=$!
python -m venv /opt/paddle > /tmp/install-paddle.log 2>&1 \
  && /opt/paddle/bin/pip install -q paddlepaddle-gpu==3.2.1 -i https://www.paddlepaddle.org.cn/packages/stable/cu126/ >> /tmp/install-paddle.log 2>&1 \
  && /opt/paddle/bin/pip install -q "paddleocr[doc-parser]==3.7.0" "paddlex==3.7.2" safetensors >> /tmp/install-paddle.log 2>&1 \
  && /opt/paddle/bin/paddlex --install serving >> /tmp/install-paddle.log 2>&1 || { tail -30 /tmp/install-paddle.log; fault "the parse software did not install"; }
step "parse software installed"
wait $vp || { tail -30 /tmp/install-vllm.log; fault "the model server did not install"; }
step "model server installed"

/opt/vllm/bin/vllm serve PaddlePaddle/PaddleOCR-VL-1.6 --served-model-name PaddleOCR-VL-1.6-0.9B PaddlePaddle/PaddleOCR-VL-1.6 \
  --trust-remote-code --max-num-batched-tokens 16384 --no-enable-prefix-caching --mm-processor-cache-gb 0 \
  --gpu-memory-utilization 0.45 --host 127.0.0.1 --port 8000 > /tmp/vllm.log 2>&1 &
vllm=$!

cfg=/tmp/pipeline.yaml
/opt/paddle/bin/python - "$cfg" <<'PY'
import sys, re, paddlex, os
src = os.path.join(os.path.dirname(paddlex.__file__), "configs", "pipelines", "PaddleOCR-VL-1.6.yaml")
text = open(src).read()
new, n = re.subn(r"(\n(\s+)backend:) native", r"\1 vllm-server\n\2server_url: http://127.0.0.1:8000/v1", text)
assert n == 1, "the default settings have no 'backend: native' line"
open(sys.argv[1], "w").write(new)
PY
[ -s "$cfg" ] || fault "no pipeline settings"

for i in $(seq 1 600); do
  kill -0 $vllm 2>/dev/null || { tail -40 /tmp/vllm.log; fault "the model server stopped"; }
  [ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8000/health)" = "200" ] && break
  [ $i = 600 ] && { tail -40 /tmp/vllm.log; fault "the model server did not start in 20 minutes"; }
  sleep 2
done
step "model server ready"

for p in 8081 8082 8083 8084 8085 8086 8087 8088; do
  /opt/paddle/bin/paddlex --serve --pipeline "$cfg" --host 127.0.0.1 --port $p > /tmp/api-$p.log 2>&1 &
done
for p in 8081 8082 8083 8084 8085 8086 8087 8088; do
  for i in $(seq 1 450); do
    [ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$p/health)" = "200" ] && break
    [ $i = 450 ] && { tail -40 /tmp/api-$p.log; fault "the parse API on port $p did not start in 15 minutes"; }
    sleep 2
  done
done
step "8 parse API processes ready"

cat > /tmp/proxy.py <<'PY'
"""A small load balancer with the Python standard library only.
It accepts a request only under /<secret>/, removes that prefix, and sends the request to the parse API process that has the fewest requests in work.
Use: proxy.py <secret> <listen port> <access log> <backend port>..."""
import asyncio, sys, time
secret, listen, log = sys.argv[1], int(sys.argv[2]), sys.argv[3]
ports = [int(p) for p in sys.argv[4:]]
busy = {p: 0 for p in ports}
prefix = ("/" + secret).encode()

async def pipe(reader, writer, first=None):
    try:
        while True:
            data = await reader.read(65536)
            if not data:
                break
            if first is not None and not first:
                first.append(data[:16])
            writer.write(data)
            await writer.drain()
    except Exception:
        pass
    finally:
        try:
            writer.close()
        except Exception:
            pass

async def handle(cr, cw):
    try:
        head = await cr.readuntil(b"\r\n\r\n")
        lines = head[:-4].split(b"\r\n")
        parts = lines[0].split(b" ")
        if len(parts) != 3 or not parts[1].startswith(prefix + b"/"):
            cw.write(b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
            await cw.drain()
            cw.close()
            return
    except Exception:
        cw.close()
        return
    parts[1] = parts[1][len(prefix):]
    headers = [l for l in lines[1:] if not l.lower().startswith(b"connection:")] + [b"Connection: close"]
    port = min(ports, key=lambda p: busy[p])
    busy[port] += 1
    start, first = time.time(), []
    try:
        br, bw = await asyncio.open_connection("127.0.0.1", port)
        bw.write(b" ".join(parts) + b"\r\n" + b"\r\n".join(headers) + b"\r\n\r\n")
        await bw.drain()
        await asyncio.gather(pipe(cr, bw), pipe(br, cw, first))
    except Exception:
        try:
            cw.close()
        except Exception:
            pass
    finally:
        busy[port] -= 1
        status = first[0].split(b" ")[1].decode() if first and first[0].count(b" ") else "000"
        with open(log, "a") as f:
            f.write(f'{port} "{parts[0].decode()} {parts[1].decode()} HTTP/1.1" {status} {time.time() - start:.2f}s\n')

async def main():
    server = await asyncio.start_server(handle, "127.0.0.1", listen, limit=1 << 20)
    async with server:
        await server.serve_forever()

asyncio.run(main())
PY
python /tmp/proxy.py "$SECRET" 8080 /tmp/access.log 8081 8082 8083 8084 8085 8086 8087 8088 > /tmp/proxy.log 2>&1 &
sleep 2
[ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/$SECRET/health)" = "200" ] || { cat /tmp/proxy.log; fault "the address inside the machine does not answer"; }

curl -sL -o /tmp/cloudflared https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 && chmod +x /tmp/cloudflared || fault "no tunnel program"
/tmp/cloudflared tunnel --url http://127.0.0.1:8080 --no-autoupdate > /tmp/tunnel.log 2>&1 &
for i in $(seq 1 60); do
  url=$(grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' /tmp/tunnel.log | head -1); [ -n "$url" ] && break; sleep 2
done
[ -n "$url" ] || { tail -20 /tmp/tunnel.log; fault "no public address"; }
step "ready"
echo "ADDRESS: $url"

while true; do
  kill -0 $vllm 2>/dev/null || { tail -40 /tmp/vllm.log; fault "the model server stopped"; }
  echo "STATUS $(( $(date +%s) - t0 ))s | pages parsed: $(grep -c 'layout-parsing HTTP/1.[01]" 200' /tmp/access.log 2>/dev/null) | GPU: $(nvidia-smi --query-gpu=utilization.gpu,memory.used --format=csv,noheader) | ADDRESS: $url"
  sleep 60
done
