# Experiment: do a reranker and a knowledge graph make the search better?

Rejea answers a question in two steps. First its **search** finds the pieces of the documents that most probably hold the answer. Then a chat model writes the answer from those pieces. If the search misses the correct piece, the answer is wrong. This experiment measures two components that can make the search find more correct pieces: a **reranker** and a **knowledge graph**.

## Status

| Part | Documents | Questions | Status |
| --- | --- | --- | --- |
| [Experiment 1](#experiment-1-wikipedia-paragraphs) | 11,656 short paragraphs of Wikipedia | 200 | Complete (2026-11-25) |
| [Experiment 2](#experiment-2-real-pdf-documents) | 219 real finance and legal PDFs, 3,121 pages | 400 | Complete (2026-11-26) |

Experiment 1 is not enough for a decision. Its documents are short paragraphs of plain text. The documents that Rejea is made for are long PDF reports with tables and pictures. Experiment 2 repeats the test on such documents. The two experiments give the same answer.

## Answers

| Question | Answer | Proof |
| --- | --- | --- |
| Does the reranker make the search better? | Yes, in each test. | Real PDFs: 2.9 points more (range +0.6 to +5.3). Wikipedia paragraphs: 11.6 points more. Rejea's own test set: the first result is correct for 38 or 39 of 42 questions in place of 36. |
| Does the knowledge graph make the search better? | Not above what Rejea has. | The graph makes a plain vector search better: 3.9 points on real PDFs and 2.9 on Wikipedia. But Rejea's hybrid search without a graph finds more than the graph search (80.3% against 77.1% on real PDFs). With the reranker on, the test sees no difference: 83.2% without the graph and 82.7% with it. |
| Which reranker provider? | Amazon Bedrock for a run of many searches. | 32 times faster than a free Cohere key for 42 searches, with almost the same quality. See [reranker providers](#finding-reranker-providers). |
| How fast can the graph be built? | 0.33 seconds for a chunk. The limit is the rate limit of the chat model account. | See [graph build speed](#finding-graph-build-speed). |

**Conclusion.** Use the reranker. Do not build the knowledge graph now. See [the conclusion](#conclusion) for the reasons and the limits.

## Terms

| Term | Meaning in plain words |
| --- | --- |
| Chunk | A piece of a document that the search can return: some paragraphs or one table, with its page numbers. Rejea cuts each document into chunks. |
| Embedding model | A model that changes a text into a list of numbers (a **vector**). Two texts with a near meaning get near vectors. |
| Vector search | A search that compares the vector of the question with the vector of each chunk. It finds chunks with the same meaning, also when the words differ. |
| Keyword search | A search that looks for the words of the question in the chunks. |
| Hybrid search | Rejea's search: a keyword search and a vector search, merged into one list. |
| Reranker | A model that reads the question together with one chunk and gives a score from 0 to 1: how well the chunk answers the question. The search gives it the best 50 chunks and puts them in the order of that score. |
| Knowledge graph | A set of facts from the documents, connected by the names that the facts share. See [how the graph works](#how-a-knowledge-graph-search-works). |
| Triple | One fact in three parts: (name, relation, name). Example: (Acme Ltd, notice period, 90 days). |
| Named entity recognition (NER) | The step where a chat model lists the names in a chunk: persons, organizations, places, amounts. |
| Chat model | A large language model that reads an instruction and a text and writes an answer. Here it reads names and facts out of chunks. |
| Parser | The software that reads a PDF page as a picture and gives its text, its tables and its layout. |
| Rate limit | The largest amount of work that a provider accepts from one account in one minute. |
| Requests at the same time | How many requests are in work at one moment (also: concurrency). More of them make a run faster, until a limit of the provider is reached. |
| Score | The share of the correct pieces that the search returns in its first 5 results. Each experiment gives its exact rule. |
| Range | The values between which the true difference is with a probability of 95% (a confidence interval). If the full range is above zero, the gain is not chance. |

## The components

| Component | What it is in this experiment | Where it runs |
| --- | --- | --- |
| Parser | PaddleOCR-VL-1.6 | A GPU machine of AWS. See [the parse speed experiment](parse-speed.md). |
| Embedding model | `@cf/qwen/qwen3-embedding-0.6b` | Cloudflare Workers AI |
| Search | Rejea's hybrid search (`POST /search`) | The Rejea service, on a Mac |
| Reranker | Cohere `rerank-v5.0-fast` in experiment 1. Cohere Rerank 3.5 (`cohere.rerank-v3-5:0`) in experiment 2. | The Cohere API. Amazon Bedrock. |
| Knowledge graph | The public code of [HippoRAG 2](https://github.com/OSU-NLP-Group/HippoRAG) (Ohio State University), commit `d5c8329`. Rejea has no knowledge graph. | A Mac |
| Chat model of the graph build | Claude Haiku 5.5, effort `low` | The Anthropic API |
| Chat model of the graph search | Claude Sonnet 5.5 | The Anthropic API |

### How a knowledge graph search works

Two documents:

- A memo: "We replace our cleaning vendor, Acme Limited, in June."
- A contract: "Acme Ltd must get 90 days of notice before the agreement ends."

The question: "How much notice must we give the cleaning vendor that we replace in June?" The answer is in the contract. But the question has no word of the contract in it: no "Acme", no "90 days". A hybrid search finds the memo and can miss the contract.

| Step | When | Who does it | Result for the example |
| --- | --- | --- | --- |
| 1. Read the facts | Build (one time) | The chat model | (cleaning vendor, is, Acme Limited) from the memo. (Acme Ltd, notice period, 90 days) from the contract. |
| 2. Join equal names | Build (one time) | The embedding model | "Acme Limited" and "Acme Ltd" have near vectors, so the graph gets a line between them. |
| 3. Find the start | Search | The embedding model | The fact nearest in meaning to the question is the fact of the memo. |
| 4. Walk the graph | Search | The graph code | cleaning vendor → Acme Limited → Acme Ltd → the contract chunk. |

```text
cleaning vendor ──is──> Acme Limited ══same as══ Acme Ltd ──notice period──> 90 days
            (from the memo)                         (from the contract)
```

So the chat model makes the facts of the graph, and the embedding model connects the graph: it joins names that mean the same thing, and it finds where a question enters the graph. Source: the [HippoRAG 2 paper](https://arxiv.org/abs/2502.14802).

## Method of the two experiments

The same questions go through the search with a component off and then on. Only that component changes between two runs. Thus a difference in the score comes from that component.

| Run | Search | It is compared with | The comparison shows |
| --- | --- | --- | --- |
| 1 | Rejea's search | | The base |
| 2 | Rejea's search + reranker | Run 1 | What the reranker adds |
| 3 | HippoRAG 2 code, graph off (its vector search) | | The base for the graph |
| 4 | HippoRAG 2 code, graph on | Run 3 | What the graph adds |
| 5 | HippoRAG 2 code, graph on + reranker | Run 4 and run 2 | What the graph adds when the reranker is on |

Run 3 is necessary because the HippoRAG 2 code is a different search from Rejea's. "Run 4 against run 1" mixes two changes. "Run 4 against run 3" changes one thing only: the graph.

A script calculates each score from the marks of the test set. No person and no model judges a result.

**Chance.** The questions are a sample, so a small difference between two runs can be chance. Each difference has a range, calculated from the differences of the single questions (10,000 bootstrap samples).

## Experiment 1: Wikipedia paragraphs

Run on 2026-11-25 and 2026-11-26.

| | |
| --- | --- |
| Test set | [MuSiQue](https://huggingface.co/datasets/osunlp/HippoRAG_2), a public set for research |
| Documents | 11,656 short paragraphs of Wikipedia |
| Questions | 200, selected at random. Each question needs 2 to 4 paragraphs. |
| Correct pieces | The makers of the set marked the paragraphs that hold the answer of each question. |
| Score of a question | The share of its marked paragraphs that are in the first 5 results |
| Reranker | Cohere `rerank-v5.0-fast` on the best 50 paragraphs |

Example of a question: "Who is the mascot of the university related to Randy Conrads?" One paragraph names the university of Randy Conrads. A second paragraph gives the mascot of that university.

### Results

| Run | Search | Score |
| --- | --- | --- |
| 1 | Rejea | 52.4% |
| 2 | Rejea + reranker | 64.0% |
| 3 | HippoRAG 2 code, graph off | 55.1% |
| 4 | HippoRAG 2 code, graph on | 58.0% |
| 5 | HippoRAG 2 code, graph on + reranker | 63.4% |

| Comparison | Difference | Range | Meaning |
| --- | --- | --- | --- |
| Reranker on Rejea's search (run 2 against run 1) | +11.6 points | +8.3 to +15.0 | The reranker helps, and the gain is not chance. |
| Graph (run 4 against run 3) | +2.9 points | +0.6 to +5.2 | The graph helps a little when there is no reranker. |
| Reranker on the graph search (run 5 against run 4) | +5.5 points | +2.4 to +8.6 | The reranker helps the graph search also. |
| Graph + reranker against Rejea + reranker (run 5 against run 2) | −0.6 points | −2.6 to +1.5 | No difference that the test can see. |

### Limits

- The documents are short paragraphs of plain text. Long reports with tables are not tested. This is why experiment 2 exists.
- The names in the paragraphs came from a file that the HippoRAG 2 authors publish. No chat model read names out of text in this experiment.
- The graph used Rejea's small embedding model. The HippoRAG 2 authors report a gain of 5.0 points on this test set with a large model ([paper](https://arxiv.org/abs/2502.14802), table 3).
- 200 questions cannot show a gain below about 3 points.

## Experiment 2: real PDF documents

| | |
| --- | --- |
| Test set | [UniDoc-Bench](https://huggingface.co/datasets/Salesforce/UniDoc-Bench) of Salesforce ([paper](https://arxiv.org/abs/2510.03663)): real PDF documents of 8 domains, with 200 questions for each domain |
| Domains in this experiment | Finance and legal |
| Documents | 219 PDFs (114 finance, 107 legal, of which 2 are in the two domains), 3,121 pages. The longest has 30 pages. |
| Questions | 400: 200 finance and 200 legal |
| Kinds of answer | 100 questions each: the answer is in text only, in a table, in a picture only, in a picture and text |
| Correct pieces | The makers of the set marked the pages that hold the answer of each question: 2.2 pages on average |
| Score of a question | The share of its marked pages that the first 5 results cover. A result covers a page when it is a chunk of that document and the page is in its page range. |
| Reranker | Cohere Rerank 3.5 on Amazon Bedrock, on the best 50 chunks |

Each PDF goes through Rejea's own code: upload, parse, chunks, vectors. Thus the test includes the faults of the parser and of the chunks, as in real use.

The licence of the test set is CC BY-NC 4.0, so its questions and its PDFs are not in this repository. The scripts read them from the dataset.

### How small a gain can the test see?

A test with more questions can see a smaller gain. The numbers below are the smallest true gain that the test finds 4 times out of 5 (a power of 80%). They use the spread of the question scores of experiment 1 and the formula of Anthropic's paper ["Adding Error Bars to Evals"](https://arxiv.org/abs/2411.00640).

| Questions | Smallest gain of the reranker that the test can see | Smallest gain of the graph |
| --- | --- | --- |
| 200 | 4.9 points | 3.3 points |
| 400 (this experiment) | 3.4 points | 2.4 points |
| 1,000 (what the paper advises) | 2.2 points | 1.5 points |

400 questions is the limit of cost for this experiment. A gain smaller than these numbers stays hidden.

### Test run

Before the full run, the full procedure ran on a small part, to find faults when they are cheap: 4 finance PDFs, 25 pages, 11 questions (2026-11-26).

| Step | Result |
| --- | --- |
| Parse on the AWS GPU machine | 25 pages in 82 seconds, 19 tables, no page failed |
| Chunks | 70 |
| Graph build | 996 names, 1,211 facts, 1,663 lines between equal names |
| Graph search | All 11 questions got an answer from the graph search |

| Run | Score (11 questions) |
| --- | --- |
| 1. Rejea | 87.9% |
| 2. Rejea + reranker | 87.9% |
| 3. HippoRAG 2 code, graph off | 83.3% |
| 4. HippoRAG 2 code, graph on | 87.9% |
| 5. HippoRAG 2 code, graph on + reranker | 87.9% |

These scores prove only that each step works. With 4 documents, almost each search finds the correct page. They are not a result.

### The full run

Run on 2026-11-26, from 20:18 to 22:39 (2 hours 21 minutes).

| Step | Time | Result |
| --- | --- | --- |
| Wait for a free GPU machine at AWS | 18 minutes (not billed) | `ml.g5.2xlarge` (NVIDIA A10G), 1.515 US dollars for an hour |
| Setup of the parser machine | 11 minutes | |
| Parse and index | 53 minutes | All 219 documents and 3,121 pages, none failed. 37,229 blocks, of which 946 tables. 5,872 chunks. 7 of the 53 minutes were a stop of the Rejea service: see [the fault](#fault-that-the-full-run-found). |
| Run 1 and run 2 | 68 seconds and 125 seconds | 400 searches each, 8 at the same time |
| Graph build | 32 minutes of work, in 4 starts | 57,705 names, 90,337 facts, 668,432 lines between equal names |
| Run 3, run 4, run 5 | 13 seconds, 10.5 minutes, 83 seconds | In run 4, the chat model found a start point in the graph for 386 of the 400 questions. For the other 14, the code used its vector search. |

| Cost | US dollars |
| --- | --- |
| Parser machine: 4,178 seconds | 1.76 |
| Reranker: 801 searches on Amazon Bedrock | 1.60 |
| Graph build: 11,594 requests to Claude Haiku 5.5 (12.1 million input tokens, 9.9 million output tokens) | 6.18 |
| Graph search: 400 requests to Claude Sonnet 5.5 | 4.09 |
| Total | 13.63 |

### Results

The score is the share of the marked answer pages that the first 5 results cover.

| Run | Search | Score | Finance | Legal | Text only | Table | Picture only | Picture and text |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | Rejea | 80.3% | 76.8% | 83.8% | 82% | 73% | 85% | 81% |
| 2 | Rejea + reranker | **83.2%** | 80.6% | 85.8% | 82% | 79% | 91% | 81% |
| 3 | HippoRAG 2 code, graph off | 73.3% | 70.1% | 76.4% | 77% | 69% | 73% | 75% |
| 4 | HippoRAG 2 code, graph on | 77.1% | 76.2% | 78.1% | 78% | 73% | 80% | 77% |
| 5 | HippoRAG 2 code, graph on + reranker | 82.7% | 80.1% | 85.2% | 81% | 79% | 88% | 82% |

| Comparison | Difference | Range | Questions up, down | Meaning |
| --- | --- | --- | --- | --- |
| Reranker on Rejea's search (run 2 against run 1) | +2.9 points | +0.6 to +5.3 | 44, 28 | The reranker helps, and the gain is not chance. |
| Graph (run 4 against run 3) | +3.9 points | +1.0 to +6.8 | 57, 41 | The graph makes a plain vector search better, and the gain is not chance. |
| Graph search against Rejea's search (run 4 against run 1) | −3.2 points | −6.0 to −0.4 | 37, 59 | Rejea's hybrid search without a graph finds more than the graph search. |
| Reranker on the graph search (run 5 against run 4) | +5.5 points | +2.7 to +8.4 | 73, 31 | The reranker helps the graph search also. |
| Graph + reranker against Rejea + reranker (run 5 against run 2) | −0.5 points | −1.9 to +0.7 | 6, 7 | No difference that the test can see. The score differs for only 13 of the 400 questions. |

The same scores with more results:

| Run | First 5 results | First 10 | First 50 |
| --- | --- | --- | --- |
| 1. Rejea | 80.3% | 89.3% | 97.4% |
| 2. Rejea + reranker | 83.2% | 91.3% | 97.4% |
| 3. Graph off | 73.3% | 83.0% | 94.9% |
| 4. Graph on | 77.1% | 87.9% | 96.1% |
| 5. Graph on + reranker | 82.7% | 89.6% | 96.1% |

The reranker only puts the best 50 chunks in a new order. Those 50 already hold 97.4% of the marked pages in Rejea's search, so almost all that is left to gain is the order.

### What the results mean

- **The reranker helps on real PDFs.** The gain is smaller than on the Wikipedia paragraphs (2.9 points against 11.6), because Rejea's search alone is already good on these documents. The gain is in the questions with the answer in a table (73% to 79%) and in a picture (85% to 91%).
- **The graph helps a plain vector search**, by 3.9 points. That agrees with the claim of its authors.
- **Rejea's search does not need the graph to get that gain.** Its hybrid search (keywords and vectors) scores 80.3%, above the graph search (77.1%).
- **With the reranker on, the graph adds nothing that the test can see.** The two searches then have the same score for 387 of the 400 questions.

### Conclusion

Use the reranker. Do not build the knowledge graph now.

| | Reranker | Knowledge graph |
| --- | --- | --- |
| Gain on real PDFs | +2.9 points on Rejea's search | None above Rejea's search with the reranker |
| Gain on Wikipedia paragraphs | +11.6 points | None above Rejea's search with the reranker |
| Cost for each search | 0.002 US dollars | 0.010 US dollars (one request to a chat model) |
| Cost for each document | None | 0.028 US dollars and 9 seconds (measured: 6.18 US dollars and 32 minutes for 219 documents) |
| Work to add it to Rejea | Done: it is a setting | A new part of the service: a chat model step in the ingest, a graph store, a graph search |

### Limits

- **No question needs two documents.** For each of the 400 questions, all marked pages are in one document. A graph is made to connect facts of different documents. Experiment 1 tests that case (each question needs 2 to 4 paragraphs), but on plain text only. A test with real PDFs and questions over two or more documents does not exist in this record.
- **The graph is not a graph that is built into Rejea.** It is the HippoRAG 2 code with Rejea's small embedding model and Claude Haiku 5.5 at effort `low`. For 64 of the 5,789 chunks (0.86% of all words), the code could not read the answer of the chat model, so the graph has no names or no facts for them. 19 of those chunks have 10 words or fewer.
- **The reranker is Cohere Rerank 3.5.** The newer `rerank-v5.0-fast` was not used here, because its free key permits only 10 requests each minute.
- **400 questions.** The test cannot see a gain below about 2.4 to 3.4 points.
- **The score counts pages, not answers.** It tells if the search found the correct page. It does not tell if a chat model then writes the correct answer.
- 37 of the 3,121 pages have no chunk, and 2 of the 894 marked pages are among them. No search can find those 2 pages.

### Fault that the full run found

The Rejea service used 4 MB more memory for each parsed page, and Docker stopped it after 1,632 pages. The cause: the PDF reader started the form of each PDF and never ended it, and the PDF library keeps each started form, with all the memory of its PDF, in a list. The correction is one call in the close function of the PDF reader (commit `1572059`). With it, the memory of the service stays near 450 MB during the parse. The stop cost 7 minutes, and no parsed page was lost.

## Finding: reranker providers

The reranker of experiment 1 came from the Cohere API with a free key. Such a key permits 10 requests each minute, so 800 searches need 80 minutes. Amazon Bedrock sells Cohere Rerank 3.5 with a limit of 250 requests each minute for the account of this experiment. Rejea got a provider for it (`REJEA_RERANK_PROVIDER=bedrock`).

Measured on 2026-11-26 with the 42 questions of Rejea's own test set (13 documents, 662 chunks), through Rejea's search:

| | Cohere API, `rerank-v5.0-fast` | Amazon Bedrock, `cohere.rerank-v3-5:0` |
| --- | --- | --- |
| First result correct (without a reranker: 36) | 39 of 42 | 38 of 42 |
| Correct in the first 5 (without a reranker: 39) | 42 of 42 | 42 of 42 |
| Time of the reranker step of one search (median) | 0.8 s | 1.8 s |
| 42 searches, one after the other | 357 s (a pause of 7 s for the limit) | 75 s |
| 42 searches, 8 at the same time | Not possible with the limit | 11 s |
| Price | Free key: 1,000 requests each month | 0.002 US dollars for a search |

| Finding | Meaning |
| --- | --- |
| One search is 1 second slower on Bedrock. | The model on Bedrock is not faster. |
| A run of 42 searches is 32 times faster on Bedrock (357 s against 11 s). | The gain comes from the rate limit, not from the model. A paid Cohere key has a higher limit. |
| The quality is almost the same. | A difference of 1 question of 42 can be chance. The model on Bedrock is one generation older. |

## Finding: graph build speed

The graph build is the longest step after the parse. For each chunk, the chat model makes 2 requests (the names, then the facts), and the embedding model gets about 45 texts (the chunk, its names, its facts).

### Requests at the same time

The HippoRAG 2 code sends its vector requests one after the other. A request waits on the network for almost all of its time, so many requests can wait at the same time. The standard tool of Python for this is a thread pool ([documentation](https://docs.python.org/3/library/concurrent.futures.html)).

Measured on the 70 chunks of the test run:

| Change | Time of the vector step |
| --- | --- |
| 1 request at a time | 141 s |
| 4 at the same time | 39 s |
| 8 at the same time | 26 s |
| 16 at the same time | 18 s |

The full build of the 70 chunks went from 230 seconds to 68 seconds with 32 chat requests and 16 vector requests at the same time.

### Less thinking of the chat model

Claude Haiku 5.5 thinks before it answers, and the thinking counts as output tokens. The effort setting controls it ([Anthropic's effort page](https://platform.claude.com/docs/en/build-with-claude/effort)). Measured on the 70 chunks:

| Setting | Chat step | Output tokens | Facts found | Usable |
| --- | --- | --- | --- | --- |
| Default (effort `medium`) | 41 s | 128,023 | 1,211 | Yes |
| Effort `low` | 33 s | 118,677 | 1,196 | Yes: the experiment uses this |
| Thinking off | Not measured | 51,182 | The build stopped | No: the model answered in the wrong form for chunks with 19% of all words |

### The limit, measured on a larger set

A test on 70 chunks is too short to show the steady speed: with 32 requests at the same time, each step is only 2 or 3 rounds of requests. So the build ran again on 636 chunks of 17 documents (14 public reports and 3 test files). A speed counts as steady when 3 measurements in sequence are within 10%, the rule of [NVIDIA's Perf Analyzer](https://github.com/triton-inference-server/perf_analyzer/blob/main/docs/cli.md).

| Step | Setting | Result |
| --- | --- | --- |
| Chat: names | 40 requests at the same time | 636 requests in 60 s |
| Chat: facts | 40 requests at the same time | Steady at 226 requests each minute. 430,000 output tokens each minute. The provider refused 33 requests. |
| Chat step as a whole | 40 requests at the same time | 636 chunks in 219 s: 174 chunks each minute |
| Vectors | 16 requests at the same time | 96 s |
| Vectors | 32 requests at the same time | 73 s |
| Vectors | 64 requests at the same time | 75 s, and the provider refused 5 requests |
| Full build | | About 297 s: 0.47 s for a chunk |

| Finding | Meaning |
| --- | --- |
| The chat step is at the rate limit of the account. | The Anthropic account of this experiment can write 400,000 output tokens each minute (read from the response headers). One chunk needs about 2,200. More requests at the same time give refused requests, not more speed. |
| The vector step stops to gain at 32 requests at the same time. | Above that number, each request of Cloudflare gets slower. |
| A refused request must be sent again. | In the first run, 33 refused requests stopped the build. The script now waits and sends a refused request again. |
| A small test can mislead. | The 70 chunks gave 0.97 s for a chunk. The 636 chunks gave 0.47 s. |

### The graph build of the full run

The build of the full run (5,872 chunks, of which 5,789 with a different text) confirms the limit:

| Step | Setting | Result |
| --- | --- | --- |
| Chat: names | 36 requests at the same time | 5,789 requests in 387 s: 897 requests each minute |
| Chat: facts | 36 requests at the same time | 5,786 requests in 1,217 s. Steady at 400,000 output tokens each minute, which is the limit of the account. |
| Chat step as a whole | | 26.7 minutes: 217 chunks each minute |
| Vectors | 32 requests at the same time | 148,042 texts in 303 s: 916 requests each minute |
| Full build | | 32 minutes: 0.33 s for a chunk |

| Event | What the script does now |
| --- | --- |
| The provider refused 2 requests with "Number of concurrent requests ... has exceeded your organization's limit". This is a second limit of the account, on the requests at the same time. | A new start of the build sends only the requests that have no saved answer. |
| For 50 chunks, the answer had the demanded JSON in a code fence, with a sentence after it. The HippoRAG 2 code cannot read such an answer. | The script gives the text of the fence to the same read function of the code. |
| For 64 chunks, the answer cannot be read (plain text, or cut at the output limit, or a chunk with almost no words). | The chunk gets no names or no facts. The build stops if such chunks hold more than 1% of all words: here 0.86%. |

## Details for a repeat of the tests

### Experiment 1

**Files.** [`questions.json`](reranker-and-graph/questions.json) holds the 200 questions with their marked paragraphs (random seed 20261125). [`runs/`](reranker-and-graph/runs/) holds the best 50 paragraphs for each question of each run. [`scripts/`](reranker-and-graph/scripts/) holds the scripts.

**Test set.** The copy that the HippoRAG 2 authors publish: [osunlp/HippoRAG_2](https://huggingface.co/datasets/osunlp/HippoRAG_2), files `musique.json` and `musique_corpus.json`.

**Runs 1 and 2.**

- `scripts/load.py` puts each paragraph in a separate database as one document: the title as a heading, the text as a paragraph.
- `rejea reindex --all` prepares the paragraphs for the search with Rejea's own code (commit `fd96f88`).
- `scripts/run_rejea.py` sends each question to `POST /search` with `"limit": 50` and `"rerank": false` or `true`.

**Runs 3 to 5.**

- The code is [OSU-NLP-Group/HippoRAG](https://github.com/OSU-NLP-Group/HippoRAG), commit `d5c8329`, run by `scripts/graph_run.py`.
- The names in the paragraphs come from the file that the authors publish (`outputs/musique/openie_results_ner_gpt-4o-mini.json`).
- Run 3 is the function `retrieve_dpr`. Run 4 is the function `retrieve`.
- In run 4, a chat model selects the start points of the graph search for each question. Here it is Claude Sonnet 5.5, with the prompt that the authors made for Llama 3.3. For 18 of the 200 questions it selected no start point, and the code used its search without the graph.
- `scripts/rerank_run.py` gives the best 50 paragraphs of run 4 to the reranker, with the same request as Rejea (run 5).
- `graph_run.py` changes the HippoRAG 2 code in three points. It adds the origin field that this version of the code demands in the file of names. It sets the token count of a text to 0, because Cloudflare gives none. It sets `force_index_from_scratch`, because the code refuses to build a graph when the text vectors are there already.

**Score.** `scripts/score.py` calculates the score, the range (10,000 bootstrap samples of the 200 differences) and a paired randomization test (10,000 rounds).

**More numbers.**

- The reranker can only reorder the best 50 paragraphs. Those 50 hold 74.6% of the marked paragraphs in runs 1 and 2, and 77.1% in runs 4 and 5.
- Run 3 scores 2.7 points above run 1 (range −0.2 to +5.5). The two are different searches, and the test cannot tell them apart.

**Events.**

- Cloudflare refused some requests with "input too big" when requests ran at the same time. The same texts got an answer when sent alone. 104 of the 11,656 paragraphs needed a second run, and 5 questions of runs 1 and 2 were sent again.
- The graph build made 227,433 requests for text vectors. That used up the free daily allowance of Cloudflare (10,000 neurons).
- The first try of run 4 met a full disk, and 46 questions got no answer from the chat model. The second try has an answer for all 200 questions. Its results are the ones in this record.

### Experiment 2 and the two findings

The scripts are in [`real-documents/scripts/`](reranker-and-graph/real-documents/scripts/), and the saved results are in [`real-documents/results/`](reranker-and-graph/real-documents/results/).

| Script | Use |
| --- | --- |
| `make_inputs.py` | Makes the question file, the document list and the PDF folder from the rows of UniDoc-Bench. |
| `parse_run.sh` | Starts a Rejea test service on a separate database with 16 file workers, 8 parse workers and 8 index workers, sends the PDFs, and waits until each one has its chunks. |
| `search_run.py` | Sends each question to `POST /search` with `"limit": 50`, without or with the reranker, some questions at the same time. |
| `graph_run.py` | Builds the graph from Rejea's chunks with the HippoRAG 2 code, and runs its vector search and its graph search. Its settings give the models, the effort, and the number of requests at the same time. |
| `rerank_run.py` | Gives the best 50 chunks of the graph search to the reranker on Amazon Bedrock, with the same request as Rejea. |
| `score_pages.py` | Calculates the score by page and the range of a difference. |
| `build_rate.py` | Reads the time log of a graph build and gives the steady rate of each step. |
| `effort_probe.py` | Sends one chunk with three thinking settings and prints the output tokens of each. |

| Result file | Content |
| --- | --- |
| `results/runs/` | The best 50 results of each question for each of the 5 runs of experiment 2, each as document, first page, last page. `score_pages.py` gives the scores of this record from these files and the question file. |
| `results/full-run-chunks-not-read.json` | The 64 chunks of the full run with an answer that the code cannot read: the step, the number of words, the error |
| `results/time-logs/full-run-chat.jsonl`, `full-run-vectors.jsonl` | Start and end of each chat request and each vector request of the graph build of the full run |
| `results/search-report-cohere.json`, `results/search-report-bedrock.json` | The two search reports of the reranker comparison (`rejea search-report`) |
| `results/time-logs/chat-40-calls.jsonl` | Start, end and output tokens of each chat request of the build on 636 chunks |
| `results/time-logs/vectors-16-requests.jsonl`, `vectors-32-requests.jsonl`, `vectors-64-requests.jsonl` | Start and end of each vector request, for 16, 32 and 64 requests at the same time |

The parser machine is the one of [the parse speed experiment](parse-speed.md): `ml.g6.2xlarge` or `ml.g5.2xlarge` of AWS SageMaker, with the script [`gpu_parser.sh`](parse-speed/scripts/gpu_parser.sh).
