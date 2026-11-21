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

Built: the schema, the migrations, the job queue. Not built: ingestion, retrieval, generation.

## Requirements

- Go 1.26
- Docker
- golangci-lint, for `make check`
- Lefthook, for the pre-commit hook

## Run

```sh
lefthook install   # the pre-commit hook runs the lint
cp .env.example .env
make db-up      # PostgreSQL 17 with pgvector and pg_textsearch, on port 5433
make migrate
make serve      # http://127.0.0.1:8080/healthz
```

## Check

```sh
make check      # lint, tests, Docker checks
```

## Commits

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org). CI checks each commit.

## Licence

MIT
