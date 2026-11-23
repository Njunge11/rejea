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

Built: the schema, the migrations, the job queues, upload URLs, the confirm of an upload, the file hash, the duplicate check, the failure rule, the unpack of archives (zip, tar, tar.gz), the job that fails old uploads, the status of one document and of the list of documents, the retry of a failed document, and the parser service. Not built: the rest of ingestion, retrieval, generation.

## Requirements

- Go 1.26
- Docker
- golangci-lint, for `make check`
- Lefthook, for the Git hooks

## Run

```sh
lefthook install   # hooks: lint before a commit, check of the commit message
cp .env.example .env
make db-up      # PostgreSQL 17 with pgvector and pg_textsearch, on port 5433
make migrate
make serve      # http://127.0.0.1:8080/healthz
```

## Parser

The parser turns a page image into blocks: each with its box, its type and its text. It is PaddleOCR-VL-1.6, and it runs on Apple Silicon only: the vision model runs on the GPU through MLX.

```sh
make parser-install   # two Python 3.12 environments, 2.2 GB
make parser           # http://127.0.0.1:8112/layout-parsing; the first start downloads 2.1 GB of models
```

On an M2 Pro with 16 GB, a page takes 12 to 40 seconds and the service uses 3 GB of memory.

## File storage

The files are in an S3 bucket. `make serve` needs it. `make migrate` and `make test` do not.

- Set `REJEA_S3_BUCKET` and `REJEA_S3_REGION` in `.env`.
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

## Check

```sh
make check      # lint, tests, Docker checks
```

## Commits

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org). A Git hook checks the message before the commit, and CI checks each pushed commit with the same script.

## Licence

MIT
