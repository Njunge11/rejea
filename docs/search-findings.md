# Search findings

> **These tests are too small for their conclusions.** Test 1 has 6 questions, and test 2 has 10 questions of each kind. A score of 10 questions has a margin of ±31 points. [The experiment with 200 questions](experiments/reranker-and-graph.md) replaces the conclusions of this page on the reranker, on two-step questions and on the knowledge graph. In that experiment the reranker makes two-step questions better, which is the opposite of test 1. What stays of use here: the 30 questions with their fact lists, and the result that 50 chunks find more than 10 for a whole-collection question.

Two tests of the search, run on 2026-11-25. Each one gives what was tested, the result, the cause, what works, and the limits of the test.

The collection for both tests: 14 public KNBS reports and 3 Word test files with filler text (17 documents, 676 chunks of 1,024 tokens at most). The data of the tests is in [`search-findings/`](search-findings/).

## 1. The reranker on two-step questions

A two-step question needs two facts from two reports, and it does not name the thing that connects them. Example: "What was the overall poverty rate in 2019 of the county that leads in manufacturing activities?" One report says which county leads. The other report gives the poverty rate of that county.

**Test.** Six two-step questions, written by hand from the Kenya Poverty Report 2019 and the Kenya Gross County Product 2021 report. A question passes when the two chunks with the two facts are in the first 10 results. Data: [`two-step-reranker.json`](search-findings/two-step-reranker.json).

**Result.**

| Search | Questions with both facts in the first 10 |
| --- | --- |
| Without the reranker | 6 of 6 |
| With Cohere `rerank-v5.0-fast` | 5 of 6 |

**The failure.** For the example question above, the chunk "Nairobi City County is leading in manufacturing" was at place 1 of the keyword search and at place 6 of the vector search. The reranker gave it a score of 0.189 and moved it to place 26 of 50. The first 10 results were all chunks of the poverty report, with scores from 0.39 to 0.31.

**Cause.** The reranker reads the question with one chunk at a time and scores how well that chunk answers the question. The question asks for a poverty rate, so each poverty chunk gets a higher score than the chunk that only names the county. That chunk is necessary, but it does not answer the question by itself. This cause agrees with the scores. No other test confirms it.

**What works.** Keep the reranker off (the default) for a question that needs facts from two places. For a question with one fact, the reranker makes the search better: see the table in "Reranker" of the README.

**Limits.** Six questions. Their writer had the chunks in view, so the questions can be too easy. One reranker model.

## 2. Is a knowledge graph necessary?

**The claim.** Microsoft's GraphRAG work says that a search without a graph fails on two kinds of question: two-step questions, and questions about the whole collection (example: "How do economic output and poverty levels differ across Kenya's counties?").

**Test.** Microsoft's test tool [BenchmarkQED](https://github.com/microsoft/benchmark-qed) 0.4.0 made 10 questions of each kind from the 14 reports, with Claude Sonnet 5.5. For each question it also made the list of facts that a correct answer must hold. Each question went to `POST /search` with no reranker. A judge model then checked each fact against the returned chunks. Data: [`questions.json`](search-findings/questions.json).

**Result with 10 chunks.**

| Kind of question | Facts | Passed by the judge of the tool | Facts that are in the chunks (count by hand) |
| --- | --- | --- | --- |
| Simple (control) | 34 | 30 | about 33 |
| Two-step | 53 | 41 | about 50 |
| Whole collection | 106 | 56 | about 69 |

The judge of the tool expects a written answer. It fails a fact when the numbers are in the chunks but no sentence states the conclusion. The count by hand comes from the notes of that judge for each failed fact.

**Second run for the whole-collection questions.** A judge with one rule graded each fact: do the chunks hold the information (1), a part of it (0.5), or none of it (0)? Each fact was graded two times. Data: [`whole-collection.json`](search-findings/whole-collection.json).

| Search | Facts found (of 106) | Facts fully absent |
| --- | --- | --- |
| One search, 10 chunks | 61% | 31 |
| One search, 18 chunks on average | 73% | 16 |
| A model cuts the question into 1 to 3 searches, 18 chunks on average | 71% | 21 |
| One search, 50 chunks | 90% | 4 |

**What the tests show.**

- Two-step questions do not need a graph on these reports. The search finds their facts as well as the facts of simple questions.
- Whole-collection questions fail with 10 chunks. The cause is the number of chunks: a topic that is spread over many pages does not fit in 10 chunks.
- More chunks correct most of it. With `"limit": 50` in the request, the search finds 90% of the facts.
- A cut of the question into small searches gives no gain over one search with the same number of chunks.

**Limits.**

- Ten questions of each kind.
- The collection is small: 50 chunks are 7% of all chunks. In a large collection, 50 chunks find a smaller part.
- No graph system was run, so the tests do not show what a graph adds above the 90%.
- The judge is a model.
- Most two-step questions of the tool name the two things that they compare. Such questions are easy for a search.

## To repeat the tests

The questions and the fact lists are saved in this folder, because their generation is the costly step: the tool made more than 1,100 model calls (4.7 million input tokens, 0.5 million output tokens). The first judge run used 1.3 million input tokens. The second run with its four searches cost 2.98 US dollars at 2 and 10 US dollars for one million input and output tokens.

Faults of BenchmarkQED 0.4.0 that the run met, with the correction of each:

- The embedding request of the tool holds 32 texts, which is too large for the Cloudflare model. Correction: `EMBEDDING_BATCH_SIZE = 16` in `benchmark_qed/config/defaults.py`.
- The tool has no Anthropic provider. Correction: add `"anthropic.chat": "anthropic"` to `_PROVIDER_TO_MODEL_PROVIDER` in `benchmark_qed/config/llm_config.py`, set `litellm.modify_params = True`, and give `call_args: {}` in the settings.
- `autoe assertion-scores` with one method stops with `KeyError`. Correction: use the settings form with `rag_methods`.
- The documentation names the path field `{rag_method}`. The code reads `{generated_rag}`.

The instruction of the judge in the second run:

```text
You are an impartial judge of retrieved evidence. You get a question, a set of retrieved text chunks, and a numbered list of assertions about what a correct answer must contain.
For each assertion, decide if the chunks contain the information that a writer needs to satisfy it. The chunks are raw source text and tables: they do not need to state a conclusion in words. Information counts as present when it is stated in the chunks, or when it follows from the chunks by a direct comparison or simple arithmetic on values that are in the chunks.
Grades:
- 1: the chunks contain all the information needed for the assertion.
- 0.5: the chunks contain a part of the information, and a part is missing.
- 0: the chunks do not contain the information.
```

The instruction of the model that cuts a question:

```text
You plan searches over a collection of documents. You do not know the documents.
Cut the question of the user into 1 to 3 short search queries. Each query covers one subject of the question. Use the words of the question. Do not add facts.
```
