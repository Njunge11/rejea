# Search quality

How the search of Rejea is measured, and the results. The README has the summary.

## Reranker

The numbers are for the 42 questions of the test set that have an answer.

Measured with chunks of 4,096 tokens at most (17 documents, 330 chunks):

| Reranker | It reads | First result correct | Correct in the first 5 | Median time of a search |
| --- | --- | --- | --- | --- |
| None | | 36 | 41 | 0.5 s |
| Cohere `rerank-v5.0-fast` | 32,768 tokens | 40 | 42 | 2.0 s |
| Cohere `rerank-v5.0-pro` | 32,768 tokens | 40 | 42 | not measured in the service |
| Cloudflare `bge-reranker-base` | 512 tokens | 33 | 39 | 2.1 s |

Thus Cohere Rerank 5 makes the search better, and the model of Cloudflare makes it worse: that model is from 2023 and reads only the start of a long chunk.

These questions have one fact each. On 200 questions of 2 to 4 steps over 11,656 passages, the Cohere reranker raises the share of correct passages in the first 5 results from 52.4% to 64.0%: see [the experiment](experiments/reranker-and-graph.md). On 400 questions over 219 real finance and legal PDFs (3,121 pages), Cohere Rerank 3.5 on Amazon Bedrock raises the share of correct pages in the first 5 results from 80.3% to 83.2%.

Measured on 2026-11-26 with chunks of 1,024 tokens (13 documents, 662 chunks):

| Reranker | First result correct | Correct in the first 5 | Median time of a search |
| --- | --- | --- | --- |
| None | 36 | 39 | 0.5 s |
| Cohere `rerank-v5.0-fast` | 39 | 42 | 1.3 s |
| Bedrock `cohere.rerank-v3-5:0` | 38 | 42 | 2.3 s |

One search with the Bedrock model is about 1 second slower. But a trial key of Cohere permits 10 searches each minute, and the AWS account of the measurement permits 250: 42 searches, 8 at the same time, were complete in 11 seconds.

## Test set

`testset/questions.json` has the questions that measure the search and the answers of the service. Each later change is measured with the same questions.

- **Documents:** 13 public reports of the Kenya National Bureau of Statistics, with one or more reports of each of its 10 report types. Each document has its URL and its SHA-256 hash, so that you can get the same file.
- **Questions:** 48, of 4 types.

| Type | The question | Number |
| --- | --- | --- |
| `fact` | One passage answers it. | 23 |
| `reasoning` | It needs two or more passages together. | 12 |
| `summary` | It needs passages of two or more documents. | 7 |
| `no_answer` | The documents cannot answer it. The correct result is no answer. | 6 |

One question:

```json
{
  "id": "fact-cpi-2011-inflation",
  "type": "fact",
  "question": "What was the overall inflation rate in Kenya in April 2011?",
  "answer": "12.05 per cent.",
  "sources": [{ "document": "cpi-2011-04", "pages": [1] }],
  "evidence": "p.1: \"The overall inflation rate stood at 12.05 per cent in April 2011\""
}
```

`sources` gives each page that has the answer or a part of it. The first page of a document is 1. `evidence` gives the words of the page. A question of the type `no_answer` has no `answer` and no `sources`, and its `evidence` tells why the documents have no answer.

```sh
make testset-check
```

The command loads the file and examines it. It names each fault with its question and its correction.

## Search report

```sh
make search-report
```

The command measures the search with the questions of the test set. The service of `make up` runs each question through its search, with its own settings, without the reranker and with it. The command writes the report to `testset/reports/last.json`, prints it, and prints its differences from the saved report, `testset/report.json`: the benchmark. Run it after each change to the search, to see if the change made the search better or worse.

| Option | Use |
| --- | --- |
| `SAVE=1` | Save this report as the new benchmark. Commit the file. |
| `RERANK=off` or `RERANK=on` | Measure only without the reranker, or only with it. |
| `PAUSE=7s` | Wait before each search with the reranker. A trial key of Cohere permits 10 requests each minute. |

The measures are those of [trec_eval](https://github.com/usnistgov/trec_eval), the tool of NIST for search measures. A result is on a correct page when it is a chunk of a source document of the question and its pages have a source page.

- **Success at K:** a correct page is in the first K results. The report prints it as a number of questions.
- **Recall at 10:** the part of the correct pages that the first 10 results have.
- **MRR:** 1 / the position of the first result on a correct page, as the mean of the questions.

The 6 questions of the type `no_answer` have no correct page, so they are in no measure of the search.

The saved report, with chunks of 1,024 tokens and Cohere `rerank-v5.0-fast`:

```text
  A correct page is       in first 1  in first 3  in first 5  in first 10  recall at 10  MRR     median time
  without the reranker    37 of 42    38 of 42    40 of 42    41 of 42    0.968         0.904   521 ms
  with the reranker       39 of 42    42 of 42    42 of 42    42 of 42    0.986         0.964   1435 ms
```

Two runs with the same settings can differ by 1 question: a change is real only when it is larger than that.

The chunk size was selected with this command. `testset/reports/` has the report of each size, with the 13 documents of the test set and Cohere `rerank-v5.0-fast`:

| Chunk size | Chunks | Reranker | First result correct | Correct in the first 3 | In the first 5 | In the first 10 | Median time |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 4,096 | 320 | none | 36 of 42 | 41 | 41 | 42 | 0.8 s |
| 4,096 | 320 | Cohere | 40 of 42 | 41 | 42 | 42 | 2.2 s |
| 2,048 | 420 | none | 34 of 42 | 39 | 39 | 42 | 0.6 s |
| 2,048 | 420 | Cohere | 40 of 42 | 42 | 42 | 42 | 1.8 s |
| 1,024 | 662 | none | 37 of 42 | 39 | 40 | 41 | 0.5 s |
| 1,024 | 662 | Cohere | 39 of 42 | 42 | 42 | 42 | 1.2 s |
| 512 | 1,439 | none | 34 of 42 | 38 | 39 | 40 | 0.6 s |
| 512 | 1,439 | Cohere | 40 of 42 | 41 | 41 | 41 | 1.4 s |

What the numbers show:

- With the reranker, the sizes from 1,024 to 4,096 give the same result: the difference is 1 question of 42.
- Without the reranker, 4,096 has a correct page in the first 3 results for 2 or 3 questions more than 1,024, and in the first 10 for 1 question more. A part of this comes from the measure: a larger chunk is on more pages, so it is on a correct page more easily.
- At 512 tokens, one question has no correct page in the first 10 results, with the reranker and without it.

The default is 1,024. A chunk is then on 1.25 pages on average (1.56 at 4,096), so a result names its source more exactly, and a search with the reranker takes two thirds of the time. For a service without a reranker, 4,096 finds a little more: `REJEA_CHUNK_MAX_TOKENS` and `make reindex` change the size.
