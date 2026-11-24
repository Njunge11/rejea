# The commands of the project. They read .env when it exists.
-include .env
export

# The Go compiler links the tokenizers library of lib/ into the service
# (make tokenizer-install). The library is for macOS 15.5 and later.
export CGO_LDFLAGS := -L$(CURDIR)/lib
export MACOSX_DEPLOYMENT_TARGET := 15.5

.PHONY: up down db-up db-down migrate serve parser-install parser word-install tokenizer-install build test lint vet docker-check check

up: ## Start all of Rejea on this machine: the one command for a new clone.
	scripts/up

down: ## Stop all of Rejea. The data stays in its volumes.
	scripts/down

db-up: ## Start the database and the file store only, for a service that runs on the machine (make serve).
	docker compose up --detach --build --wait postgres storage

db-down: ## Stop the database and the file store. The data stays in its volumes.
	docker compose down

migrate: ## Bring the database schema up to date.
	go run ./cmd/rejea migrate

serve: ## Run the HTTP server.
	go run ./cmd/rejea serve

parser-install: ## Install the parser service (Apple Silicon only).
	parser/install.sh

parser: ## Run the parser service. The first start downloads 2.1 GB of models.
	parser/start.sh

word-install: ## Install the Word reader in bin/.
	scripts/install-word-reader

tokenizer-install: ## Install the tokenizers library in lib/ and the tokenizer of the embedding model in models/.
	scripts/install-tokenizer

build: ## Build the service: bin/rejea.
	go build -o bin/rejea ./cmd/rejea

test: ## Run all tests. The database must be up.
	go test -race ./...

lint: ## Check the format and the code.
	golangci-lint run

vet: ## Find suspect code.
	go vet ./...

docker-check: ## Check the Dockerfiles and the Compose file.
	docker build --check .
	docker build --check docker/postgres
	docker compose config --quiet

check: lint vet build test docker-check ## Run each check of the project. CI runs the same targets.
