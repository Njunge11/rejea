#!/bin/sh
# Installs the parser service: PaddleOCR in one virtual environment and
# MLX-VLM in a second one. The two cannot share an environment, because
# each installs its own OpenCV package into the same Python module.
#
# The steps are from the PaddleOCR-VL guide for Apple Silicon:
# https://www.paddleocr.ai/latest/en/version3.x/pipeline_usage/PaddleOCR-VL-Apple-Silicon.html
set -eu
cd "$(dirname "$0")"

python="${REJEA_PARSER_PYTHON:-python3.12}"

# The constraints files pin each package, so that a later install gets the
# same versions that were proved.
"$python" -m venv .venv-paddle
PIP_CONSTRAINT="$PWD/constraints-paddle.txt"
export PIP_CONSTRAINT
.venv-paddle/bin/python -m pip install --quiet --upgrade pip
# The guide installs PaddlePaddle from the package index of Paddle. The file
# on PyPI is the same file (same SHA-256), and PyPI also has the pinned
# versions of the other packages.
.venv-paddle/bin/python -m pip install --quiet paddlepaddle==3.2.1 "paddleocr[doc-parser]==3.7.0"
.venv-paddle/bin/paddlex --install serving

"$python" -m venv .venv-mlx
PIP_CONSTRAINT="$PWD/constraints-mlx.txt"
.venv-mlx/bin/python -m pip install --quiet --upgrade pip
.venv-mlx/bin/python -m pip install --quiet mlx-vlm==0.8.0

echo "The parser service is installed. Start it with: make parser"
