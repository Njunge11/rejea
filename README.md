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

Built: the schema, the migrations, the job queues, upload URLs, the confirm of an upload, the file hash, the duplicate check, the failure rule, the unpack of archives (zip, tar, tar.gz), the job that fails old uploads, the status of one document and of the list of documents, the retry of a failed document, the parser service, the page images of a PDF, the blocks of a PDF and of a Word file, and the chunker. Not built: the rest of ingestion, retrieval, generation.

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

When a thing is missing or wrong, the message names it and gives the correction:

- `make up` examines the machine before it starts a thing: Docker, the ports (with the program that uses one), Python, the disk space, the values of `.env`. If a container does not start, it shows the end of the log of that container.
- The service stops when it starts, with the reason: a setting, the database, the schema, the file store, its keys, its bucket, the port. It only warns when the parser or the Word reader does not answer: the file waits.

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
