# Experiment: do the reranker and a knowledge graph make the search better?

Run on 2026-11-25 and 2026-11-26.

## What was tested

Rejea's search gets a question and returns the pieces of text ("passages") that most probably hold the answer. The design has two components that must make the search find more of the correct passages. This experiment measures if they do.

- A **reranker** is a model that takes the best 50 passages of the search, reads each one together with the question, and puts them in a better order. Rejea has a reranker (from Cohere). It is off by default.
- A **knowledge graph** is a map of the names in the passages (persons, places, organizations) and of the passages that mention each name. With this map, a search can go from one passage to a second passage through a name that the two share. Rejea has no knowledge graph. Checklist items 39 to 42 are the plan to build one.

The graph is for questions that need more than one passage. Example: "Who is the mascot of the university related to Randy Conrads?" One passage names the university of Randy Conrads. A second passage gives the mascot of that university. The question does not name the university, so a search for the words of the question can miss the second passage.

## Method

The same questions go through the search with a component off and then on. Only that component changes between the two runs. Thus a difference in the score comes from that component.

**The questions.** The test set is MuSiQue, a public set for research: 11,656 short passages from Wikipedia, and questions that each need 2 to 4 of those passages. For each question, the makers of the set marked the passages that hold the answer. The experiment uses 200 questions, selected at random. The limit of 200 comes from the free Cohere key: 1,000 calls each month.

**The score.** For each question, the search returns its best 5 passages. The score of a question is the share of its marked passages that are in those 5. A question with 2 marked passages, of which 1 is in the 5, scores 50%. The score of a run is the mean of its 200 questions. A script calculates the score from the marks. No person and no model judges a result.

**Chance.** 200 questions are a sample, so a small difference between two runs can be chance. Each difference below has a range. The true difference is in that range with a probability of 95%. When the full range is above zero, the gain is not chance.

## How it was tested

There are five runs. Each run uses the same 200 questions and the same 11,656 passages.

- **Run 1** is Rejea's search as it is.
- **Run 2** is Rejea's search with the reranker.

Rejea has no knowledge graph, and to build one only for a test is too much work. So runs 3 to 5 use the public code of HippoRAG 2, the graph method that the checklist plans to copy. That code got the same passages, and the same model that Rejea uses to compare the meaning of texts.

- **Run 3** is the HippoRAG 2 code with its graph off. It is the base for run 4.
- **Run 4** is the HippoRAG 2 code with its graph on.
- **Run 5** is run 4 with the same reranker as run 2.

## Results

| Run | Search | Score |
| --- | --- | --- |
| 1 | Rejea | 52.4% |
| 2 | Rejea + reranker | 64.0% |
| 3 | HippoRAG 2 code, graph off | 55.1% |
| 4 | HippoRAG 2 code, graph on | 58.0% |
| 5 | HippoRAG 2 code, graph on + reranker | 63.4% |

- **The reranker on Rejea's search** (run 2 against run 1): 11.6 points more. The range is +8.3 to +15.0. The score went up for 66 questions and down for 13.
- **The graph** (run 4 against run 3): 2.9 points more. The range is +0.6 to +5.2. The score went up for 23 questions and down for 11.
- **The reranker on the graph search** (run 5 against run 4): 5.5 points more. The range is +2.4 to +8.6.
- **Graph + reranker against Rejea + reranker** (run 5 against run 2): 0.6 points less. The range is −2.6 to +1.5, which includes zero.

## What the results mean

- **The reranker helps, and the gain is large.** With it, Rejea finds 64 of each 100 correct passages in place of 52. The gain is not chance.
- **The graph helps a little when there is no reranker.** The gain is about 3 points. It is not chance, but it can be as small as 0.6 points.
- **The graph gives no gain that the test can see when the reranker is on.** The search with a graph and a reranker finds as much as Rejea's search with a reranker.

## Conclusion

Use the reranker. Do not build the knowledge graph now.

The graph adds little, and what it adds, the reranker adds also. The graph is also the more costly component. A reranker costs one model call for each search. The graph search costs one chat model call for each question (1.94 US dollars for these 200 questions). The graph build costs chat model calls for each passage, to read the names out of it.

Three limits apply to this conclusion:

- The passages are short paragraphs of Wikipedia. Long reports with tables are not tested.
- The graph is the HippoRAG 2 code with Rejea's small text model, not a graph that is built into Rejea. The authors of HippoRAG 2 report a gain of 5.0 points on this test set with a large model ([paper](https://arxiv.org/abs/2502.14802), table 3).
- 200 questions cannot show a gain below about 2 points.

## Details for a repeat of the test

**Files.** [`questions.json`](reranker-and-graph/questions.json) holds the 200 questions with their marked passages (random seed 20261125). [`runs/`](reranker-and-graph/runs/) holds the best 50 passages for each question of each run. [`scripts/`](reranker-and-graph/scripts/) holds the scripts.

**Test set.** The copy that the HippoRAG 2 authors publish: [osunlp/HippoRAG_2](https://huggingface.co/datasets/osunlp/HippoRAG_2), files `musique.json` and `musique_corpus.json`.

**Runs 1 and 2.**

- `scripts/load.py` puts each passage in a separate database as one document: the title as a heading, the text as a paragraph.
- `rejea reindex --all` prepares the passages for the search with Rejea's own code (commit `fd96f88`).
- `scripts/run_rejea.py` sends each question to `POST /search` with `"limit": 50` and `"rerank": false` or `true`.
- The text model is `@cf/qwen/qwen3-embedding-0.6b` (Cloudflare). The reranker is Cohere `rerank-v5.0-fast` on the best 50 passages.

**Runs 3 to 5.**

- The code is [OSU-NLP-Group/HippoRAG](https://github.com/OSU-NLP-Group/HippoRAG), commit `d5c8329`, run by `scripts/graph_run.py`.
- The names in the passages come from the file that the authors publish (`outputs/musique/openie_results_ner_gpt-4o-mini.json`). No model read names out of passages in this experiment.
- Run 3 is the function `retrieve_dpr`. Run 4 is the function `retrieve`.
- In run 4, a chat model selects the start points of the graph search for each question. Here it is Claude Sonnet 5.5, with the prompt that the authors made for Llama 3.3. For 18 of the 200 questions it selected no start point, and the code used its search without the graph.
- `scripts/rerank_run.py` gives the best 50 passages of run 4 to the reranker, with the same request as Rejea (run 5).
- `graph_run.py` changes the HippoRAG 2 code in three points. It adds the origin field that this version of the code demands in the file of names. It sets the token count of a text to 0, because Cloudflare gives none. It sets `force_index_from_scratch`, because the code refuses to build a graph when the text vectors are there already.

**Score.** `scripts/score.py` calculates the score, the range (10,000 bootstrap samples of the 200 differences) and a paired randomization test (10,000 rounds).

**More numbers.**

- The reranker can only reorder the best 50 passages. Those 50 hold 74.6% of the marked passages in runs 1 and 2, and 77.1% in runs 4 and 5.
- Run 3 scores 2.7 points above run 1 (range −0.2 to +5.5). The two are different searches, and the test cannot tell them apart.

**Events.**

- Cloudflare refused some requests with "input too big" when requests ran at the same time. The same texts got an answer when sent alone. 104 of the 11,656 passages needed a second run, and 5 questions of runs 1 and 2 were sent again.
- The graph build made 227,433 requests for text vectors. That used up the free daily allowance of Cloudflare (10,000 neurons).
- The first try of run 4 met a full disk, and 46 questions got no answer from the chat model. The second try has an answer for all 200 questions. Its results are the ones in this record.
