# Rejea

A RAG (retrieval-augmented generation) engine for PDF and Word documents, with hybrid and graph retrieval. Each answer cites its source blocks, down to the bounding box on the PDF page.

## Design

- **Ingestion:** layout-aware OCR into blocks with bounding boxes, heading-based chunking, dense embeddings.
- **Metadata extraction:** an LLM classifier assigns a document type and extracts its typed properties (JSON Schema), for exact filter queries.
- **Hybrid retrieval:** BM25 and HNSW vector search, merged by reciprocal rank fusion, then a cross-encoder reranker.
- **Graph retrieval:** HippoRAG 2. Entity and triple extraction at ingestion, Personalized PageRank at query time.
- **Generation:** an LLM agent selects a retrieval tool per query and answers only from the retrieved chunks.
- **Stack:** Go, PostgreSQL 17 with pgvector and pg_textsearch, River for background jobs.

## Status

Built: the schema, the migrations, the job queues, upload URLs, the confirm of an upload, the file hash, the duplicate check, the failure rule, the unpack of archives (zip, tar, tar.gz), the job that fails old uploads, the status of one document and of the list of documents, the retry of a failed document, the parser service, the page images of a PDF, the blocks of a PDF, of a Word file and of a Markdown file, the chunker, the embeddings, the index step that saves the chunks of a document with their vectors, the command that makes the chunks again, the question file of the test set, the search endpoint, the reranker, the search report, the chat model port, and the endpoint for the pages of a document. Not built: the rest of ingestion, generation.

## Run

Requirements: Docker with Compose, and `make`. On a Mac with Apple Silicon also Python 3.12, for the parser.

```sh
git clone https://github.com/Njunge11/rejea.git
cd rejea
make up
```

`make up` needs no settings file. It first names each tool that is missing. Then it starts:

- the database, the file store and the service, each in a container ([compose.yaml](compose.yaml)). The migrations run before the service starts.
- the parser, on the Mac itself: Docker gives a container no GPU on a Mac. Its log is `parser/.run/parser.log`.

The service is at `http://localhost:8080`, and `GET /healthz` answers `ok`. `make down` stops all of it. The data stays in two Docker volumes.

The first `make up` downloads 0.8 GB of container images, and on a Mac 0.5 GB of Python packages (2.2 GB on the disk) and 2 GB of models for the parser. On an M2 Pro, a first run with empty caches took 18 minutes, most of it for the downloads. A later start takes 30 seconds.

`make up` first lists what the run installs, with the sizes. It then shows each of its 4 steps with the time, and one line that moves for each long download. In a pipe, or with `NO_COLOR` set, the output is plain text.

When a thing is missing or wrong, the message names it and gives the correction:

- `make up` examines the machine before it starts a thing: Docker, the ports (with the program that uses one), Python, the disk space, the values of `.env`. If a container does not start, it shows the end of the log of that container.
- The service stops when it starts, with the reason: a setting, the database, the schema, the file store, its keys, its bucket, the port. It only warns when the parser or the Word reader does not answer, or when the embedding model has no credentials: the file waits.

Linux has no parser at this time: a Word file and a Markdown file go through, and a PDF waits. Windows is not supported.

## Settings

Each setting has a default: [.env.example](.env.example) lists them. To change one, copy the file to `.env` and change the value. Compose and `make` read `.env`.

The ports of the machine are settings: `REJEA_PORT` (8080), `REJEA_DB_PORT` (5433), `REJEA_STORAGE_PORT` (8333) and `REJEA_PARSER_PORT` (8112). Each port listens on this machine only.

## Parser

The parser turns a page image into blocks: each with its box, its type and its text. It is PaddleOCR-VL-1.6, and it runs on Apple Silicon only: the vision model runs on the GPU through MLX.

On an M2 Pro with 16 GB, a page takes 3 to 40 seconds and the parser uses 3 GB of memory.

The service sends each page of a PDF to the parser. While the parser does not run, a PDF waits and the service tries again.

For a parser on another machine, such as a GPU machine in the cloud, set `REJEA_PARSER_URL` in `.env` to its address. `make up` then starts no parser on this machine. With no value, the service uses the parser of this machine. The experiment [parse speed](docs/experiments/parse-speed.md) used a GPU machine in AWS in this way.

## Word reader

The service reads `.docx` and `.doc` files with the command line program of [office_oxide](https://github.com/yfedoseev/office_oxide), one process for each file. The image of the service has the program.

## Markdown reader

The service reads `.md` and `.markdown` files with [goldmark](https://github.com/yuin/goldmark), the Markdown library of Hugo. It needs no parser and no other program: a Markdown file goes from the upload to the chunks in milliseconds.

| In the file | Block |
| --- | --- |
| A heading (`#` to `######`, or a line with `===` or `---` below it) | A heading with its level, 1 to 6. The headings make the heading path of each chunk. |
| A paragraph, a quote, a code block | A paragraph |
| A list, also with tasks (`- [x]`) | One list, with one line for each item |
| A table, in Markdown or in HTML | A table |
| A picture | Its description (the alt text) |
| A footnote | A footnote. Its number in the text is in brackets: `[1]`. |

- The Markdown is [CommonMark](https://commonmark.org) with the additions of GitHub (tables, struck text, task lists) and footnotes.
- The reader leaves out the metadata block at the start of a file (YAML between two lines of `---`), the address of a link, a comment, and a script.
- The file must be text in UTF-8, with `REJEA_MARKDOWN_MAX_BYTES` bytes or less (16 MiB by default). A file with a fault fails at once with the reason.
- A Markdown file has no pages: a search result gives its heading path, and `null` for its pages.

The method is that of [Unstructured](https://github.com/Unstructured-IO/unstructured/blob/main/unstructured/partition/md.py), which [Docling](https://github.com/docling-project/docling/blob/main/docling/backend/md_backend.py) also uses for a file with HTML in it: the library turns the Markdown into HTML, and one HTML reader makes the blocks.

## Chunks

The chunker cuts the blocks of a document into chunks at its headings. Each chunk starts with its heading path, and has `REJEA_CHUNK_MAX_TOKENS` tokens or less (1,024 by default). A table that is larger is split at its rows, and each part starts with the header rows.

The default comes from the [search report](#search-report): with the reranker, the search finds the same from 1,024 to 4,096 tokens, and a chunk of 1,024 tokens is on fewer pages, so a result names its source more exactly.

The limit is in tokens of the embedding model, Qwen3-Embedding-0.6B. The service counts them with the [tokenizers](https://github.com/huggingface/tokenizers) library of Hugging Face, through [its Go binding](https://github.com/daulet/tokenizers). The image of the service has the library and the tokenizer of the model.

## Index

After the parse, a document has the status `indexing`. The index worker cuts its blocks into chunks, gets the vector of each chunk from the embedding model, and saves the chunks in one transaction. The document then has the status `indexed`. Each chunk has its heading path, its text, its pages, its blocks and its vector.

A fault that a new attempt can correct (the provider gives no answer) makes the job run again after a wait. After `REJEA_JOB_MAX_ATTEMPTS` attempts, the document has the status `failed` and the reason. `POST /documents/{id}/retry` starts a failed document again: it skips the parse that is complete. A document with no text fails at once.

`REJEA_INDEX_WORKERS` is the number of documents in the index work at the same time.

### New chunks for the documents in the database

A document gets its chunks and its vectors one time, with the chunk size and the embedding model of that day. After a change of `REJEA_CHUNK_MAX_TOKENS` or of the model, the documents in the database still have the old chunks. Vectors from two models cannot be compared, so after a change of the model the search cannot find the old documents correctly.

`make reindex` gives each parsed document new chunks and new vectors. It uses the blocks that the parse saved: the parser does not run.

```sh
# 1. Change the setting in .env, then start the service again with it.
make up
# 2. Put each parsed document in the index queue again.
make reindex
# One document:
make reindex DOC=01a20e0e-81f5-7043-922b-4ed3749f5436
```

The command adds the jobs, and the service does the work. A document has the status `indexing` until its new chunks are saved, and it keeps its old chunks until then. The command refuses a document with no complete parse, and it says why. Without Docker, the command is `go run ./cmd/rejea reindex --all`.

A model with a different number of dimensions also needs a migration of the column `chunks.embedding`.

## Embeddings

The embedding model turns each chunk and each search question into a vector of 1,024 numbers. The model is Qwen3-Embedding-0.6B on [Cloudflare Workers AI](https://developers.cloudflare.com/workers-ai/). A Cloudflare account is free, and it gives 10,000 neurons each day: about 9 million tokens of this model.

1. Make an account at [dash.cloudflare.com](https://dash.cloudflare.com/sign-up).
2. In the dashboard, open **AI**, then **Workers AI**, then **REST API**.
3. Make a Workers AI API token there, and copy the token and the account ID.
4. Put the two values in `.env`, then run `make up`:

```sh
REJEA_EMBED_API_KEY=...     # the token
CLOUDFLARE_ACCOUNT_ID=...
```

Each model of the service (the embedding model, the reranker, the chat model) has three settings of the same form: `REJEA_EMBED_PROVIDER`, `REJEA_EMBED_MODEL` and `REJEA_EMBED_API_KEY` for this one. To change the provider of a model, change these three values.

When the service starts, it sends one small request to the model. With no values, it starts and warns: a document is parsed, and its index work fails with the correction as its reason. With a wrong value (the token, the account ID, the name of the model, the number of dimensions), it stops and names the setting to correct.

The code for a provider is an adapter behind one interface ([internal/embed](internal/embed/embed.go)). The registry in [internal/models](internal/models/models.go) has the name of each provider, and `REJEA_EMBED_PROVIDER` selects one. The new attempts, the time limit and the usage log are the same code for each provider. The usage log is one line for each request: the model, the number of texts, the tokens that the provider counted, the time.

## Search

`POST /search` gives the chunks that agree best with a question, each with its document and its pages.

```sh
curl -s http://localhost:8080/search \
  -H 'Content-Type: application/json' \
  -d '{"query": "What was the overall inflation rate in April 2011?", "limit": 1}'
```

```json
{
  "results": [
    {
      "chunk_id": "01a20e3d-05ab-78aa-87b3-c63c6b8628dc",
      "document": { "id": "01a20e10-5d4b-71d4-80a6-306d539111bc", "filename": "cpi-a.pdf", "title": null },
      "page_start": 1,
      "page_end": 3,
      "heading_path": ["CPI AND INFLATION RATES FOR THE MONTH OF APRIL 2011"],
      "text": "CPI AND INFLATION RATES FOR THE MONTH OF APRIL 2011\n\nKenya National Bureau of Statistics hereby ...",
      "block_ids": ["01a20e11-0e87-7256-bd54-63ee78dc598d", "01a20e11-0e87-725b-84c5-d9714f94e008"],
      "score": 0.03279,
      "rerank_score": null,
      "keyword_rank": 1,
      "vector_rank": 1
    }
  ]
}
```

The method is hybrid search. The service runs two searches for one question and merges their lists.

- **Keyword search** (BM25, with `pg_textsearch`) finds the exact words and numbers of the question.
- **Vector search** (with `pgvector`) finds text with the same meaning in other words.
- **Reciprocal rank fusion** merges the two lists: the score of a chunk is the sum of `1 / (60 + rank)` for each list that has it. A chunk that is high in the two lists comes first.

The request:

| Field | Value |
| --- | --- |
| `query` | The question. Necessary. |
| `limit` | The largest number of results. Default 10, largest 50. |
| `filters.document_ids` | Search in these documents only. |
| `filters.metadata` | Search in the documents whose metadata has each name and value of this object, for example `{"department": "finance"}`. |
| `rerank` | `true` or `false`: use the reranker for this search. Default: the setting `REJEA_RERANK`. |

A result has no pages (`null`) for a Word file and for a Markdown file. `keyword_rank` or `vector_rank` is `null` when that search did not find the chunk. `score` puts the results in order: it does not tell how good a result is. The vector search always gives its nearest chunks, so a question with no answer in the documents also gets results.

The search reads the documents with the status `indexed`, and those in the index work again (`make reindex`), which keep their old chunks until the new chunks are saved. It needs the embedding model: with no model, the answer is 503 and the log gives the correction.

### Reranker

The reranker is an optional second stage of the search (retrieve, then rerank). A reranker model reads the question together with one chunk and gives a score from 0 to 1: how well the chunk answers the question. The search gives the model the best 50 chunks of the merge and puts them in the order of that score. A result then has the score in `rerank_score`.

The reranker is off by default. `REJEA_RERANK=true` turns it on for each search, and the field `rerank` of a request turns it on or off for that search.

There are three providers: Cohere, Cloudflare, and Amazon Bedrock (it has the Cohere models of AWS). The numbers are for the 42 questions of the test set that have an answer, measured with chunks of 4,096 tokens at most (17 documents, 330 chunks):

| Reranker | It reads | First result correct | Correct in the first 5 | Median time of a search |
| --- | --- | --- | --- | --- |
| None | | 36 | 41 | 0.5 s |
| Cohere `rerank-v5.0-fast` | 32,768 tokens | 40 | 42 | 2.0 s |
| Cohere `rerank-v5.0-pro` | 32,768 tokens | 40 | 42 | not measured in the service |
| Cloudflare `bge-reranker-base` | 512 tokens | 33 | 39 | 2.1 s |

Thus Cohere Rerank 5 makes the search better, and the model of Cloudflare makes it worse: that model is from 2023 and reads only the start of a long chunk.

These questions have one fact each. On 200 questions of 2 to 4 steps over 11,656 passages, the Cohere reranker raises the share of correct passages in the first 5 results from 52.4% to 64.0%: see [the experiment](docs/experiments/reranker-and-graph.md). On 400 questions over 219 real finance and legal PDFs (3,121 pages), Cohere Rerank 3.5 on Amazon Bedrock raises the share of correct pages in the first 5 results from 80.3% to 83.2%.

To use Cohere:

1. Make an API key at [dashboard.cohere.com/api-keys](https://dashboard.cohere.com/api-keys). A trial key is free, with 1,000 calls each month and 10 calls each minute.
2. Put these lines in `.env`, then run `make up`:

```sh
REJEA_RERANK_PROVIDER=cohere
REJEA_RERANK_API_KEY=...
REJEA_RERANK=true
```

The model is `rerank-v5.0-fast`. `REJEA_RERANK_MODEL=rerank-v5.0-pro` selects the larger model. Cohere bills for the tokens: 0.02 US dollars for one million tokens of the fast model and 0.05 for the pro model. One search with 50 chunks was 80,000 tokens on average in the measurement.

When the service starts, it sends one small request to the model. With a wrong key or a wrong name of the model, it stops and names the setting to correct. When the model gives no answer to a search, or the account is at its rate limit, the answer is 503 and tells the client to try again or to send `"rerank": false`.

To use Cohere Rerank 3.5 on Amazon Bedrock (an AWS account, with no Cohere account):

1. Make an Amazon Bedrock API key in the Bedrock console of your AWS account, in "API keys". A short-term key is valid for 12 hours at most, and only in its AWS Region. A long-term key is valid until the date that you give.
2. Put these lines in `.env`, then run `make up`:

```sh
REJEA_RERANK_PROVIDER=bedrock
REJEA_RERANK_API_KEY=...
REJEA_RERANK=true
```

The model is `cohere.rerank-v3-5:0` in the AWS Region `us-east-1`. For a different Region, change the address: `REJEA_BEDROCK_API_URL=https://bedrock-runtime.us-west-2.amazonaws.com`. The model reads 4,096 tokens of the question and one chunk together, so keep `REJEA_CHUNK_MAX_TOKENS` below that. AWS bills for each search (0.002 US dollars for a search with 100 chunks or fewer), and the log has no number of tokens.

Measured on 2026-11-26 with the 42 questions and chunks of 1,024 tokens (13 documents, 662 chunks):

| Reranker | First result correct | Correct in the first 5 | Median time of a search |
| --- | --- | --- | --- |
| None | 36 | 39 | 0.5 s |
| Cohere `rerank-v5.0-fast` | 39 | 42 | 1.3 s |
| Bedrock `cohere.rerank-v3-5:0` | 38 | 42 | 2.3 s |

One search with the Bedrock model is about 1 second slower. But a trial key of Cohere permits 10 searches each minute, and the AWS account of the measurement permits 250: 42 searches, 8 at the same time, were complete in 11 seconds.

To use the model of Cloudflare, give `REJEA_RERANK_API_KEY` the token of `REJEA_EMBED_API_KEY`: the provider `cloudflare` uses `CLOUDFLARE_ACCOUNT_ID` also.

The code for a provider is an adapter behind one interface ([internal/rerank](internal/rerank/rerank.go)), as for the embedding model. `REJEA_RERANK_PROVIDER` selects the provider.

## Pages of a document

`GET /documents/{id}/pages` gives the pages of a document that have an image, each with its size and a URL that reads the image. A viewer lays out the pages from the sizes, and loads an image when its page comes into view.

```sh
curl -s "http://localhost:8080/documents/<id>/pages?limit=2"
```

```json
{
  "pages": [
    { "number": 1, "width": 1785, "height": 2526, "image_url": "http://localhost:8333/rejea/documents/<id>/pages/1.png?X-Amz-..." },
    { "number": 2, "width": 1785, "height": 2526, "image_url": "http://localhost:8333/rejea/documents/<id>/pages/2.png?X-Amz-..." }
  ],
  "has_more": true,
  "expires_at": "2026-11-25T13:44:18Z"
}
```

| Parameter | Use |
| --- | --- |
| `limit` | The number of pages in the list. Default: `REJEA_PAGES_LIMIT` (100). Largest: `REJEA_MAX_PAGES_LIMIT` (1,000). |
| `starting_after` | The number of the last page of the list before. The list starts at the next page. |

`has_more` tells that pages come after the list. An image URL needs no credentials, and it stops at `expires_at` (`REJEA_VIEW_URL_LIFE`, 1 hour): the viewer then asks for the list again. A Word file, and a PDF before its parse, has an empty list.

With the store of `make up`, an image URL is a presigned URL of the bucket. For a bucket in AWS S3 behind a CloudFront distribution, it is a signed CloudFront URL:

1. Make an RSA key pair of 2,048 bits: `openssl genrsa -out private_key.pem 2048`, then `openssl rsa -pubout -in private_key.pem -out public_key.pem`.
2. In CloudFront, add the public key ("Key management", "Public keys"), put it in a key group, and make that group a trusted key group of the behaviour of the distribution. The origin is the bucket, with origin access control.
3. Put these lines in `.env`, and run the service on the machine itself (see "Develop"):

```sh
REJEA_CLOUDFRONT_DOMAIN=d111111abcdef8.cloudfront.net
REJEA_CLOUDFRONT_KEY_PAIR_ID=K2JCJMDEHXQW5F
REJEA_CLOUDFRONT_PRIVATE_KEY_FILE=/path/to/private_key.pem
```

The URLs of one list have one signature: a [custom policy](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-signed-urls.html) for the folder of the page images of the document, which the CloudFront documentation gives for more than one file. The signature uses SHA-256. It permits a read of the page images of that document only: CloudFront refuses the same signature for a different file.

When the service starts, it sends one signed request to the distribution. With a key pair ID that CloudFront does not know, a private key that is not the key of that ID, or a domain with no distribution, it stops and names the setting to correct.

## Chat model

The chat model reads instructions and a question, and writes an answer: text, or JSON in the form of a schema. No part of the service asks it a question yet: the classifier of document types and the knowledge graph are its first users.

The provider is Anthropic, and the default model is Claude Sonnet 5.5 (`claude-sonnet-5-5`).

1. Make an API key at [platform.claude.com/settings/keys](https://platform.claude.com/settings/keys).
2. Put these lines in `.env`, then run `make up`:

```sh
REJEA_CHAT_PROVIDER=anthropic
REJEA_CHAT_API_KEY=...
```

`REJEA_CHAT_MODEL` selects a different model, for example `claude-opus-5-5` or `claude-haiku-5-5`. Anthropic bills for the tokens of each answer: the log has one line for each answer, with the model and the tokens.

When the service starts, it asks Anthropic for the model, which costs nothing. With no key, it starts with no chat model. With a wrong key or a wrong name of the model, it stops and names the setting to correct.

A safety classifier of a Claude model can decline a request. The request asks for the [refusal fallback](https://platform.claude.com/docs/en/build-with-claude/refusals-and-fallback) of the API: Anthropic then gives the same request to a different Claude model, and bills at the price of that model. The log line of the answer names the model that wrote it.

The code for a provider is an adapter behind one interface ([internal/chat](internal/chat/chat.go)), as for the other models. The adapter uses the [Go SDK of Anthropic](https://github.com/anthropics/anthropic-sdk-go). `REJEA_CHAT_PROVIDER` selects the provider.

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

## File storage

The files are in a store with the API of S3.

`make up` starts [SeaweedFS](https://github.com/seaweedfs/seaweedfs) as this store, in a container with the bucket `rejea`. Its two keys are the settings `REJEA_STORAGE_ACCESS_KEY` and `REJEA_STORAGE_SECRET_KEY`. The defaults are for a store that only this machine reaches: change them before another machine can reach it.

For a bucket in AWS S3:

- Set `REJEA_S3_BUCKET` and `REJEA_S3_REGION` in `.env`, and remove `REJEA_S3_ENDPOINT`, `REJEA_S3_PUBLIC_ENDPOINT` and the two AWS keys.
- Run the service on the machine itself (see "Develop"). The containers of `make up` use the store of `compose.yaml`.
- The service gets its AWS credentials from the default sources of the AWS SDK. Give it an IAM role, not the access key of a user.
- The role needs five actions, and no more:
  - `s3:GetObject` and `s3:PutObject` on the objects of the bucket.
  - `s3:DeleteObject` and `s3:AbortMultipartUpload` on the objects of the bucket. The service sends each file from an archive to the bucket, a large file in parts. When the work on an archive fails, the service removes what it sent.
  - `s3:ListBucket` on the bucket. With it, S3 answers 404 for a file that is not there. Without it, S3 answers 403, and the service cannot tell a missing file from a refused request.
- Give the bucket a lifecycle rule that removes incomplete multipart uploads. If the service stops in the middle of an upload in parts, the parts stay in the bucket until this rule removes them.
- On a developer machine, make an AWS profile that assumes the role, and name it in `AWS_PROFILE`:

```ini
[profile rejea]
role_arn = arn:aws:iam::<account id>:role/<role name>
source_profile = <a profile that can assume the role>
region = <the region of the bucket>
duration_seconds = 43200
```

S3 refuses an upload URL when the credentials that signed it are expired. Thus the session (`duration_seconds`) must be longer than `REJEA_UPLOAD_URL_LIFE`. The service does not start if it is not.

## Develop

To change the code, run the service on the machine itself. This needs Go 1.26, a C compiler (the Xcode command line tools, or gcc), golangci-lint and Lefthook.

```sh
lefthook install         # hooks: lint before a commit, check of the commit message
cp .env.example .env
make tokenizer-install   # the tokenizers library in lib/ and the tokenizer of the embedding model in models/, 50 MB
make word-install        # the Word reader in bin/, 4 MB
make db-up               # the database and the file store, in containers
make migrate
make serve               # http://127.0.0.1:8080/healthz
make parser              # the parser, in a second terminal
make check               # lint, vet, build, tests, Docker checks, the question file
```

Use `make build`, `make serve` and `make test`, not the bare `go` commands: they tell the Go compiler where the tokenizers library is.

## Commits

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org). A Git hook checks the message before the commit, and CI checks each pushed commit with the same script.

## Licence

MIT
