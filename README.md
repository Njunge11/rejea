# Rejea

Rejea answers questions from an organization's PDF and Word files, with page citations.

In development. The database, the migrations and the job queue exist. Upload, search and answers do not.

## Requirements

- Go 1.26
- Docker
- golangci-lint, for `make check`

## Run

```sh
cp .env.example .env
make db-up      # PostgreSQL 17 with pgvector and pg_textsearch, on port 5433
make migrate
make serve      # http://127.0.0.1:8080/healthz
```

## Check

```sh
make check      # lint, tests, Docker checks
```
