# Experiment: do the reranker and a knowledge graph make the search better?

Run on 2026-11-25 and 2026-11-26. All five runs are done.

## Result

| Run | Search | Reranker | Recall at 5 | All found at 5 |
| --- | --- | --- | --- | --- |
| 1 | Rejea: keyword + vector | no | 52.4% | 18.0% |
| 2 | Rejea: keyword + vector | yes | 64.0% | 29.5% |
| 3 | Vector only (HippoRAG code) | no | 55.1% | 21.0% |
| 4 | Graph (HippoRAG code) | no | 58.0% | 26.5% |
| 5 | Graph (HippoRAG code) | yes | 63.4% | 31.0% |

- **The reranker helps.** It adds 11.6 points to Rejea's search and 5.5 points to the graph search.
- **The graph helps a little when there is no reranker.** It adds 2.9 points to the vector search.
- **The graph adds nothing that the test can see when the reranker is on.** Graph + reranker (63.4%) and Rejea + reranker (64.0%) are the same within the margin.

## Question

Two components of the design must each find more correct passages than the search without them:

- the reranker (built, off by default);
- the knowledge graph (checklist items 39 to 42, not built).

## Why each component can help

- **Reranker.** The first search scores a question and a chunk that were prepared apart. A reranker reads the two together, so its score is more exact. It can only change the order of the chunks that the first search found.
- **Knowledge graph.** A search finds chunks that look like the question. In a question with two steps, the chunk of the second step does not look like the question. A graph connects chunks through the names in them, so it can reach that chunk.

## Test set

MuSiQue, in the copy that the HippoRAG 2 authors publish ([osunlp/HippoRAG_2](https://huggingface.co/datasets/osunlp/HippoRAG_2), files `musique.json` and `musique_corpus.json`).

- 11,656 passages of Wikipedia, 69 words each at the median. All are in the index for each run.
- 1,000 questions of 2 to 4 steps. Each question names the passages that hold its answer.
- The runs use 200 questions, selected at random with the seed 20261125: [`questions.json`](reranker-and-graph/questions.json). They name 531 passages: 2 for 106 questions, 3 for 57, 4 for 37.
- The limit of 200 comes from the free Cohere key: 10 calls each minute and 1,000 each month. Runs 2 and 5 use 200 calls each.

## Setup

All runs use the same 200 questions, the same passages and the same embedding model (`@cf/qwen/qwen3-embedding-0.6b`). The reranker is Cohere `rerank-v5.0-fast` on the best 50 passages of a search.

**Runs 1 and 2: Rejea.**

- A second database, `rejea_bench`, holds the passages. The documents of the main database are not in it.
- Each passage is one document with two blocks: the title as a heading, the text as a paragraph ([`scripts/load.py`](reranker-and-graph/scripts/load.py)).
- `rejea reindex --all` made the chunks and the embeddings with Rejea's own code: 11,656 documents, 11,656 chunks.
- A second Rejea service (commit `fd96f88`, the settings of `.env`) reads that database.
- Each question goes to `POST /search` with `"limit": 50` and `"rerank": false` or `true` ([`scripts/run_rejea.py`](reranker-and-graph/scripts/run_rejea.py)).

**Runs 3 to 5: the code of the HippoRAG 2 authors.** Rejea has no graph, so these runs use [OSU-NLP-Group/HippoRAG](https://github.com/OSU-NLP-Group/HippoRAG), commit `d5c8329` ([`scripts/graph_run.py`](reranker-and-graph/scripts/graph_run.py)).

- The names and the facts of each passage come from the file that the authors publish (`outputs/musique/openie_results_ner_gpt-4o-mini.json`): 101,544 names and 125,889 facts, extracted with gpt-4o-mini. No model extracted anything in this experiment.
- The graph has 113,200 points: 11,656 passages and 101,544 names.
- Run 3 is the control for run 4: the same code and the same vectors, with the graph off (`retrieve_dpr`).
- Run 4 is the graph search (`retrieve`). For each question, a chat model selects the facts that start the search. Here that model is Claude Sonnet 5.5, with the prompt that the authors made for Llama 3.3. When the model selects no fact, the code uses its vector search: 18 of 200 questions.
- Run 5 gives the best 50 passages of run 4 to the reranker, with the same request as Rejea ([`scripts/rerank_run.py`](reranker-and-graph/scripts/rerank_run.py)).

Three changes to the HippoRAG code were necessary, all in `graph_run.py`:

- The published file of names and facts has no origin field, and this version of the code demands one. The script adds the field.
- Cloudflare gives no token count with an embedding, and the code demands one. The script sends the same request and sets the count to 0.
- The code refuses to build a graph when embeddings exist and no graph exists. The script sets `force_index_from_scratch`, as the message of the code says.

## Measure

For one question, the **share found** is: named passages in the first 5 results, divided by named passages.

Example: "Who is the mascot of the university related to Randy Conrads?" names two passages, "Randy Conrads" and "Benny Beaver". Both are in the first 5 results of run 1, so the share found is 2 of 2 = 100%. With one of the two, it is 50%.

- **Recall at 5** of a run: the mean of the share found over the 200 questions.
- **All found at 5** of a run: the share of the questions with a share found of 100%.

No model judges a result. [`scripts/score.py`](reranker-and-graph/scripts/score.py) compares the saved results with the named passages. The files in [`runs/`](reranker-and-graph/runs/) hold the best 50 passages for each question of each run.

## Counts

| Run | Named passages in the first 5 (of 531) | Questions with all found (of 200) | Recall in the first 50 |
| --- | --- | --- | --- |
| 1 | 259 | 36 | 74.6% |
| 2 | 321 | 59 | 74.6% |
| 3 | 273 | 42 | 76.0% |
| 4 | 286 | 53 | 77.1% |
| 5 | 317 | 62 | 77.1% |

Recall at 5 is a mean over questions, so it is not the first column divided by 531: a question with 2 named passages has the same weight as a question with 4.

The reranker reads the best 50 passages, so it cannot go above the recall in the first 50.

## Comparisons

Each comparison is on the same 200 questions. The interval comes from 10,000 bootstrap samples of the 200 differences. The p-value comes from a paired randomization test with 10,000 rounds.

| Gain of | Comparison | Recall at 5 | 95% interval | p | Better / worse (questions) |
| --- | --- | --- | --- | --- | --- |
| Reranker on Rejea's search | run 2 − run 1 | +11.6 | +8.3 to +15.0 | < 0.0001 | 66 / 13 |
| Graph | run 4 − run 3 | +2.9 | +0.6 to +5.2 | 0.018 | 23 / 11 |
| Reranker on the graph search | run 5 − run 4 | +5.5 | +2.4 to +8.6 | 0.001 | 45 / 16 |
| Graph + reranker against Rejea + reranker | run 5 − run 2 | −0.6 | −2.6 to +1.5 | 0.61 | 14 / 16 |

Recall at 5 by the number of steps of a question:

| Steps | Questions | Run 1 | Run 2 | Run 3 | Run 4 | Run 5 |
| --- | --- | --- | --- | --- | --- | --- |
| 2 | 106 | 62.3% | 73.6% | 65.1% | 69.8% | 73.6% |
| 3 | 57 | 49.7% | 61.4% | 52.0% | 52.0% | 60.2% |
| 4 | 37 | 28.4% | 40.5% | 31.1% | 33.1% | 39.2% |

## What the results mean

- **Reranker.** The gain is large and is not chance. It holds for questions of 2, 3 and 4 steps.
- **Graph without a reranker.** The gain is small and is not chance at the 95% level. It comes from the questions of 2 steps. The graph also brings few new passages into the best 50: 77.1% against 76.0%.
- **Graph with a reranker.** The test sees no gain. The interval says that a gain of more than 1.5 points is not probable.

## Limits

- 200 questions of one test set. A gain below about 2 points is not visible.
- The passages are short paragraphs of Wikipedia. Long reports with tables are not tested.
- The test measures which passages the search finds. It does not measure an answer.
- The graph is not Rejea's. Runs 2 and 5 have different first searches, so their comparison is not a pure test of the graph. The pure test of the graph is run 4 against run 3.
- The graph run differs from the published one in three points: the embedding model is small (0.6 billion parameters), a question gets no instruction text before its embedding, and the chat model is not the one that the prompt was made for. The authors report +5.0 points for the graph on the 1,000 questions with a large embedding model (69.7% to 74.7%, recall at 5, [paper](https://arxiv.org/abs/2502.14802), table 3).
- One reranker model.

## Cost

- Reranker: one Cohere call for each search. 400 calls in this experiment, on the free key.
- Graph search: one chat model call for each question. 200 questions cost 1.94 US dollars: 877,811 input tokens and 17,955 output tokens, at 2 and 10 US dollars for one million.
- Graph build: one embedding for each name and each fact. For 11,656 passages that is 227,433 embeddings, which used up the free daily allowance of Cloudflare (10,000 neurons). The extraction of the names and the facts needs model calls for each passage, which this experiment did not make.

## Events in the runs

- Cloudflare refused some embedding requests with "input too big" when requests ran at the same time. The same texts got an answer when sent alone. In Rejea's index work, 104 of 11,656 documents failed for this cause and got their chunk on a second run. In run 1, four questions were sent again. In run 2, one question was sent again.
- On 2026-11-25 at 18:15 UTC, the embeddings for the graph used up the free daily allowance of Cloudflare. Runs 3 to 5 ran on the next day.
- The first try of run 4 met a full disk: 46 questions got no answer from the chat model and used the vector search. The second try has an answer for all 200 questions, and its results are the ones in this record.
