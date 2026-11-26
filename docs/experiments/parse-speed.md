# Experiment: how fast is the parse, and does parallel work make it faster?

Run on 2026-11-26.

## What is tested

Rejea's parser reads one PDF page at a time. The test measures four things:

1. The speed today, with one page in work at a time.
2. The speed when Rejea has more pages in work at the same time, at several sizes, to find the size where more gives no gain.
3. The same runs with the parser on an NVIDIA GPU machine at AWS, in place of the Mac.
4. The two other jobs of Rejea (file and index) at several numbers of workers.

**Result in short.** On the Mac, more pages at the same time give no gain: 13 seconds for a page at each size. On a GPU machine, 1 page at a time needs 3.9 seconds for a page, and 8 at the same time need 1.3 seconds. Most of the gain is there at 4. The index job also gains from more workers (148 seconds with 1, 43 with 8). The file job needs almost no time at each size.

"Pages in work at the same time" is a setting of Rejea: `REJEA_PARSE_WORKERS`. Each parse worker takes one document and sends its pages to the parser one after the other. So N workers have N pages in work at the same time. The default is 1.

## Method

The method is the one of the standard benchmarks for model servers: first one request at a time, then more at the same time in steps, and at each step the throughput and the time for one request ([MLPerf Inference](https://arxiv.org/abs/1911.02549) scenarios "single-stream" and "offline"; [NVIDIA Perf Analyzer](https://docs.nvidia.com/deeplearning/triton-inference-server/user-guide/docs/perf_analyzer/docs/cli.html), concurrency range; [vLLM benchmark sweeps](https://docs.vllm.ai/en/stable/benchmarking/sweeps/)). The size where the throughput stops to grow while the time for one page still grows is the point of diminishing returns.

- **The same input in each run.** 8 PDFs with 46 pages: [`files.json`](parse-speed/files.json). They are financial documents of 5 or 6 pages from the public set [UniDoc-Bench](https://huggingface.co/datasets/Salesforce/UniDoc-Bench) (finance part), selected at random with the seed 20261126 from its 132 PDFs of 4 to 6 pages.
- **The real path.** Each run sends the 8 PDFs through a Rejea service with its own upload flow. Rejea makes the page pictures (216 DPI) and calls the parser. A new, empty database for each run prevents that a file counts as a duplicate.
- **Only one thing changes** between two runs: the number of parse workers, or the machine of the parser.
- **Measures.** "Parse time": from the upload of the first PDF to the moment the last page is parsed. "Seconds for a page": parse time divided by 46. "Time for one page": the time between two parsed pages of the same document (median and 90% value). 
- **Output.** Each run records the number of blocks, table blocks and words that the parser gave, to show that a faster run did the same work.

The script of a run: [`scripts/speed_run.sh`](parse-speed/scripts/speed_run.sh). The raw results: [`results.jsonl`](parse-speed/results.jsonl).

## Setup

- Rejea at commit `763d35e`, settings of `.env`.
- Parser software: PaddleOCR 3.7.0, PaddleX 3.7.2, model PaddleOCR-VL-1.6.
- Local machine: MacBook Pro, Apple M2 Pro, 16 GB. The vision model runs in the MLX server (mlx-vlm 0.8.0), which reads one block at a time.

## Results

### Local parser (Mac)

| Parse workers | Parse time | Seconds for a page | Time for one page (median / 90%) | Blocks / tables / words | Failed documents |
| --- | --- | --- | --- | --- | --- |
| 1 | 610 s | 13.3 | 13.0 s / 15.5 s | 881 / 6 / 20,959 | 0 |
| 2 | 597 s | 13.0 | 25.7 s / 29.0 s | 881 / 6 / 20,959 | 0 |
| 4 | 599 s | 13.0 | 51.7 s / 57.0 s | 881 / 6 / 20,959 | 0 |

Result for the local parser: more parse workers give no gain. The parse time stays at 600 seconds (13 seconds for a page), and the time for one page grows in step with the number of workers (13, 26, 52 seconds). The output is the same in the three runs. The point of diminishing returns is 1 worker. The cause is the parser on the Mac: its model server reads one block at a time, so pages that arrive together wait in a line.

### Parser on an AWS GPU machine

The machine: `ml.g6.2xlarge` of AWS SageMaker in `us-east-1` (1 NVIDIA L4 GPU with 23 GB, 8 vCPU, 32 GB), 1.222 US dollars for an hour. Rejea stayed on the Mac. Only the setting `REJEA_PARSER_URL` changed: it pointed to the machine through a Cloudflare tunnel. So each page picture went from the Mac to AWS over the internet.

On the machine, the vision model ran in the vLLM server, which reads many blocks at the same time. The parse API of PaddleX takes one request at a time, so 8 copies of it ran behind a small load balancer. The script: [`scripts/gpu_parser.sh`](parse-speed/scripts/gpu_parser.sh).

| Parse workers | Parse time | Seconds for a page | Time for one page (median / 90%) | Blocks / tables / words | Failed documents | Against the Mac (610 s) |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 180 s | 3.9 | 2.8 s / 4.1 s | 881 / 6 / 20,978 | 0 | 3.4 times faster |
| 2 | 101 s | 2.2 | 3.4 s / 4.3 s | 881 / 6 / 20,911 | 0 | 6.1 times faster |
| 4 | 72 s | 1.6 | 4.5 s / 5.3 s | 881 / 6 / 20,971 | 0 | 8.4 times faster |
| 8 | 59 s | 1.3 | 7.2 s / 7.6 s | 881 / 6 / 21,090 | 0 | 10.3 times faster |
| 1 (second run) | 171 s | 3.7 | 3.0 s / 3.9 s | 881 / 6 / 20,859 | 0 | 3.6 times faster |

The second run with 1 worker shows the spread between two equal runs: 180 and 171 seconds (5%).

### The file job and the index job (on the Mac, no parser)

Rejea has two more jobs for each document, and each has its own worker setting. Neither uses the parser, so both were measured on the Mac alone.

**The file job** downloads the file from the file store, makes its hash and checks for a duplicate. Setting: `REJEA_FILE_WORKERS`. Input: 100 PDFs (80 MB), selected at random with the seed 20261126: [`files-100.json`](parse-speed/files-100.json). All 100 were confirmed at the same moment, so that all jobs waited together. Script: [`scripts/file_run.sh`](parse-speed/scripts/file_run.sh).

| File workers | Time for the 100 documents | Work time of one job (median) |
| --- | --- | --- |
| 1 | 10.0 s | 0.004 s |
| 2 | 5.0 s | 0.004 s |
| 4 | 2.4 s | 0.006 s |
| 8 | 1.6 s (second run: 1.9 s) | 0.006 s |
| 10 | 1.2 s | 0.006 s |
| 16 | 0.8 s | 0.009 s |

**The index job** cuts the saved blocks of a document into chunks, gets a vector for each chunk from Cloudflare, and saves them. Setting: `REJEA_INDEX_WORKERS`. Input: a copy of a database with 17 parsed documents (292 pages, 676 chunks). The command `rejea reindex --all` put all 17 in the queue before the service started. Script: [`scripts/index_run.sh`](parse-speed/scripts/index_run.sh).

| Index workers | Time for the 17 documents | Time of one job (median) | Longest job | Failed documents |
| --- | --- | --- | --- | --- |
| 1 | 148 s | 1.4 s | 54 s | 0 |
| 2 | 91 s | 2.1 s | 44 s | 0 |
| 4 | 54 s | 2.7 s | 43 s | 0 |
| 8 | 43 s | 3.1 s | 40 s | 0 |

## What the results mean

- **On the Mac, parallel work gives nothing.** 1, 2 and 4 workers all need 600 seconds. The model server on the Mac reads one block at a time, so more pages only wait longer.
- **On the GPU machine, parallel work helps, and each step helps less.** From 1 to 2 workers the time drops by 79 seconds. From 2 to 4 it drops by 28 seconds. From 4 to 8 it drops by 13 seconds.
- **Each doubling gives less.** The parse time drops by 44% from 1 to 2 workers, by 28% from 2 to 4, and by 18% from 4 to 8. At the same time, the time for one page grows from 2.8 to 7.2 seconds. So 4 workers give most of the gain, and 8 give a little more.
- **The GPU machine is faster also with no parallel work.** With 1 worker it needs 3.9 seconds for a page, against 13.3 on the Mac.
- **The setting works as it is.** `REJEA_PARSE_WORKERS` and `REJEA_PARSER_URL` gave these numbers with no code change in Rejea.

- **The file job is not a bottleneck.** Its work needs 0.004 seconds for a document. The 10 seconds with 1 worker are wait time in the queue: the queue library (River 0.51.2) gives jobs to the workers one time each 100 ms, one job for each free worker. More workers remove that wait: the time still drops at 10 and at 16 workers. But the full gain from 1 to 16 is 9 seconds for 100 documents.
- **The index job gains from workers up to the size of the largest document.** The time drops by 39% from 1 to 2 workers, by 41% from 2 to 4, and by 20% from 4 to 8. One document is one job. At 8 workers the run needs 43 seconds, and its largest document alone needs 40. More workers cannot go below that.
- **Cloudflare refused no request at 8 index workers.** Each of the 17 jobs passed at its first attempt.

Calculated from these numbers, not measured: 1,750 pages need 6.3 hours on the Mac, and 37 minutes on this GPU machine with 8 workers (about 1 US dollar with the setup time).

## Limits

- 46 pages are a small run. The standard benchmarks use runs of 10 minutes or more for each size. Two equal runs differed by 5%.
- One kind of document (financial reports of 5 or 6 pages).
- A parse worker takes one document. With 8 documents, more than 8 workers cannot be tested. The machine also had 8 copies of the parse API, so 8 pages in work is its maximum.
- The test does not show which part limits the GPU machine at 8 workers: the GPU, its processor, or the upload of the page pictures from the Mac.
- The output is compared by counts only. Blocks and tables agree in all runs. The word count on the Mac is 20,959 in each run. On the GPU machine it changes from run to run, between 20,859 and 21,090 (0.6% around the Mac's count). The texts were not compared word by word.
- The three jobs were measured one at a time. No run had the file, parse and index workers all at 8 together.
- The index test has 17 documents. It does not show if Cloudflare refuses requests when 8 workers run for a long time.
- One GPU type. The official setup of the PaddleOCR team for high load (Triton) was not used, because it needs Docker on the machine.

## Details for a repeat of the test

- **Local runs.** `parser/start.sh` starts the parser. Then, for each size: `scripts/speed_run.sh "<label>" <parse workers> http://host.docker.internal:8112 <pdf dir> files.json scripts/upload.py results.jsonl`.
- **File job runs.** `scripts/file_run.sh "<label>" <file workers> <pdf dir> files-100.json results-file.jsonl`. Results: [`results-file.jsonl`](parse-speed/results-file.jsonl).
- **Index job runs.** `scripts/index_run.sh "<label>" <index workers> results-index.jsonl`. Results: [`results-index.jsonl`](parse-speed/results-index.jsonl).
- **AWS runs.** A SageMaker training job with the image `pytorch-training:2.6.0-gpu-py312-cu126-ubuntu22.04-sagemaker` runs `scripts/gpu_parser.sh` and stops itself after 1 hour at most (`MaxRuntimeInSeconds` 3600). The script prints the address of the tunnel. The parser URL of a run is `<address>/<secret>/`.
- **Software on the GPU machine.** vLLM 0.21.0 with `--max-num-batched-tokens 16384 --no-enable-prefix-caching --mm-processor-cache-gb 0 --gpu-memory-utilization 0.45` (the recipe of vLLM for this model), PaddleOCR 3.7.0, PaddleX 3.7.2, paddlepaddle-gpu 3.2.1. The pipeline settings are the default `PaddleOCR-VL-1.6.yaml` with the backend `vllm-server`.
- **Setup time of the GPU machine:** 11 minutes from its start to the first page.
- **Cost.** 0.82 US dollars: one job of 13 minutes that failed in its setup, and the job of 27 minutes that gave the results.
- **Events.** AWS had no free GPU machine for 3 minutes before the first start and for 20 minutes before the second. Three requests for an A10 GPU machine (`ml.g5.2xlarge`, in two regions) waited 10 to 18 minutes each and got none. The first job failed because the package `nginx` did not install on the image. The script now has its own load balancer in Python.
