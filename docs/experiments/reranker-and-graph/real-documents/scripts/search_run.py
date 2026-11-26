"""Send each question to Rejea's search and save the best 50 chunks with their document and pages.
Use: search_run.py <base url> <questions.json> <out.json> <true|false: reranker> <questions at the same time>"""
import json, sys, time, threading, urllib.request, urllib.error
from concurrent.futures import ThreadPoolExecutor
base, qs_path, out_path, rerank, workers = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4] == "true", int(sys.argv[5])
questions = json.load(open(qs_path))
try: done = {r["n"]: r for r in json.load(open(out_path))}
except FileNotFoundError: done = {}
lock = threading.Lock(); again = [0]
def ask(q):
    body = json.dumps({"query": q["question"], "limit": 50, "rerank": rerank}).encode()
    for attempt in range(4):
        try:
            req = urllib.request.Request(base + "/search", body, {"content-type": "application/json"}, method="POST")
            res = json.load(urllib.request.urlopen(req, timeout=120))["results"]; break
        except urllib.error.HTTPError as e:
            text = e.read().decode()[:300]
            if attempt == 3: raise RuntimeError(f"question {q['n']}: HTTP {e.code}: {text}")
            with lock: again[0] += 1
            print(f"  question {q['n']}: HTTP {e.code}, again in {5 * (attempt + 1)} s: {text[:120]}", flush=True); time.sleep(5 * (attempt + 1))
    row = {"n": q["n"], "results": [{"chunk": r["chunk_id"], "doc": r["document"]["filename"].rsplit(".", 1)[0], "page_start": r["page_start"], "page_end": r["page_end"]} for r in res]}
    with lock:
        done[q["n"]] = row
        if len(done) % 25 == 0: print(f"  {len(done)} of {len(questions)}", flush=True)
start = time.time(); todo = [q for q in questions if q["n"] not in done]
# A thread pool sends the questions at the same time: a search waits on the network (the vector of the question, the reranker).
with ThreadPoolExecutor(max_workers=workers) as pool: list(pool.map(ask, todo))
json.dump([done[k] for k in sorted(done)], open(out_path, "w"))
print(f"saved {len(done)} questions to {out_path} | {len(todo)} sent, {workers} at the same time, in {time.time() - start:.1f} s | sent again: {again[0]}")
