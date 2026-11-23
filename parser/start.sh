#!/bin/sh
# Starts the parser service: two processes, which stop together.
#
#   1. The MLX server runs the vision model PaddleOCR-VL-1.6 on the GPU of
#      the Mac. It reads the text of one block.
#   2. The parse API (PaddleX) finds the blocks of a page with a layout
#      model, sends each block to the MLX server, and answers with the
#      blocks and their boxes: POST /layout-parsing.
#
# The first start downloads the models: 2.1 GB.
set -eu
cd "$(dirname "$0")"

host="${REJEA_PARSER_HOST:-127.0.0.1}"
port="${REJEA_PARSER_PORT:-8112}"
vlm_port="${REJEA_PARSER_VLM_PORT:-8111}"
model="PaddlePaddle/PaddleOCR-VL-1.6"

# The pipeline file has the address of the MLX server.
work="$(mktemp -d -t rejea-parser)"
config="$work/pipeline.yaml"
sed "s#http://127.0.0.1:8111/#http://127.0.0.1:$vlm_port/#" pipeline.yaml > "$config"

vlm=""
api=""
# shellcheck disable=SC2329 # The traps below call stop.
stop() {
	trap - EXIT INT TERM
	# A process that stopped by itself makes kill fail. The stop continues.
	kill "$api" 2>/dev/null || true
	kill "$vlm" 2>/dev/null || true
	wait 2>/dev/null || true
	rm -rf "$work"
}
trap 'stop; exit 0' INT TERM
trap stop EXIT

# Only the parse API uses the MLX server, so it listens on this machine
# only.
.venv-mlx/bin/mlx_vlm.server --host 127.0.0.1 --port "$vlm_port" --model "$model" &
vlm=$!
echo "parser: the MLX server loads the model $model"
until curl --silent --fail "http://127.0.0.1:$vlm_port/health" | grep -q '"model_ready":true'; do
	kill -0 "$vlm" 2>/dev/null || { echo "parser: the MLX server stopped" >&2; exit 1; }
	sleep 2
done

.venv-paddle/bin/paddlex --serve --pipeline "$config" --host "$host" --port "$port" &
api=$!

# The service stops when one of the two processes stops.
while kill -0 "$vlm" 2>/dev/null && kill -0 "$api" 2>/dev/null; do
	sleep 2
done
echo "parser: a process stopped, the service stops" >&2
exit 1
