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

Built: the schema, the migrations, the job queues, upload URLs, the confirm of an upload, the file hash, the duplicate check, the failure rule, and the unpack of archives (zip, tar, tar.gz). Not built: the rest of ingestion, retrieval, generation.

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
