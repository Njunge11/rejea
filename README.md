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

Built: the schema, the migrations, the job queues, upload URLs, the confirm of an upload, the file hash, the duplicate check, the failure rule, the unpack of archives (zip, tar, tar.gz), the job that fails old uploads, the status of one document and of the list of documents, the retry of a failed document, the parser service, the page images of a PDF, the blocks of a PDF and of a Word file, the chunker, the embeddings, the index step that saves the chunks of a document with their vectors, and the command that makes the chunks again. Not built: the rest of ingestion, retrieval, generation.

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

Linux has no parser at this time: a Word file goes through, and a PDF waits. Windows is not supported.

## Settings

Each setting has a default: [.env.example](.env.example) lists them. To change one, copy the file to `.env` and change the value. Compose and `make` read `.env`.

The ports of the machine are settings: `REJEA_PORT` (8080), `REJEA_DB_PORT` (5433), `REJEA_STORAGE_PORT` (8333) and `REJEA_PARSER_PORT` (8112). Each port listens on this machine only.

## Parser

The parser turns a page image into blocks: each with its box, its type and its text. It is PaddleOCR-VL-1.6, and it runs on Apple Silicon only: the vision model runs on the GPU through MLX.

On an M2 Pro with 16 GB, a page takes 3 to 40 seconds and the parser uses 3 GB of memory.

The service sends each page of a PDF to the parser. While the parser does not run, a PDF waits and the service tries again.

## Word reader

The service reads `.docx` and `.doc` files with the command line program of [office_oxide](https://github.com/yfedoseev/office_oxide), one process for each file. The image of the service has the program.

## Chunks

The chunker cuts the blocks of a document into chunks at its headings. Each chunk starts with its heading path, and has `REJEA_CHUNK_MAX_TOKENS` tokens or less. A table that is larger is split at its rows, and each part starts with the header rows.

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
CLOUDFLARE_ACCOUNT_ID=...
CLOUDFLARE_API_TOKEN=...
```

When the service starts, it sends one small request to the model. With no values, it starts and warns: a document is parsed, and its index work fails with the correction as its reason. With a wrong value (the token, the account ID, the name of the model, the number of dimensions), it stops and names the setting to correct.

The code for a provider is an adapter behind one interface ([internal/embed](internal/embed/embed.go)). The registry in [internal/models](internal/models/models.go) has the name of each provider, and `REJEA_EMBED_PROVIDER` selects one. The new attempts, the time limit and the usage log are the same code for each provider. The usage log is one line for each request: the model, the number of texts, the tokens that the provider counted, the time.

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
make check               # lint, vet, build, tests, Docker checks
```

Use `make build`, `make serve` and `make test`, not the bare `go` commands: they tell the Go compiler where the tokenizers library is.

## Commits

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org). A Git hook checks the message before the commit, and CI checks each pushed commit with the same script.

## Licence

MIT
