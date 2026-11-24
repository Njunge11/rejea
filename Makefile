# The commands of the project. They read .env when it exists.
-include .env
export

# The Go compiler links the tokenizers library of lib/ into the service
# (make tokenizer-install). The library is for macOS 15.5 and later.
export CGO_LDFLAGS := -L$(CURDIR)/lib
export MACOSX_DEPLOYMENT_TARGET := 15.5

# need stops a target with a message when a program is not installed.
need = @command -v $(1) >/dev/null 2>&1 || { echo "$(1) is not installed. $(2)" >&2; exit 1; }
need-go = $(call need,go,Install Go 1.26: https://go.dev/dl/)
need-docker = $(call need,docker,Install Docker: https://docs.docker.com/get-started/get-docker/)

.PHONY: up down db-up db-down migrate serve parser-install parser word-install tokenizer-install build test lint vet docker-check check

up: ## Start all of Rejea on this machine: the one command for a new clone.
	@scripts/up

down: ## Stop all of Rejea. The data stays in its volumes.
	@scripts/down

db-up: ## Start the database and the file store only, for a service that runs on the machine (make serve).
	$(need-docker)
	docker compose up --detach --build --wait postgres storage

db-down: ## Stop the database and the file store. The data stays in its volumes.
	docker compose down

# The Go targets get the tokenizers library and the Word reader when they
# are not there.
lib/libtokenizers.a:
	scripts/install-tokenizer

bin/office-oxide:
	scripts/install-word-reader

migrate: lib/libtokenizers.a ## Bring the database schema up to date.
	$(need-go)
	go run ./cmd/rejea migrate

serve: lib/libtokenizers.a bin/office-oxide ## Run the HTTP server.
	$(need-go)
	go run ./cmd/rejea serve

parser-install: ## Install the parser service (Apple Silicon only).
	parser/install.sh

parser: ## Run the parser service. The first start downloads 2.1 GB of models.
	parser/start.sh

word-install: ## Install the Word reader in bin/.
	scripts/install-word-reader

tokenizer-install: ## Install the tokenizers library in lib/ and the tokenizer of the embedding model in models/.
	scripts/install-tokenizer

build: lib/libtokenizers.a ## Build the service: bin/rejea.
	$(need-go)
	go build -o bin/rejea ./cmd/rejea

test: lib/libtokenizers.a ## Run all tests. The database must be up.
	$(need-go)
	go test -race ./...

lint: ## Check the format and the code.
	$(call need,golangci-lint,Install it: https://golangci-lint.run/docs/welcome/install/)
	golangci-lint run

vet: lib/libtokenizers.a ## Find suspect code.
	$(need-go)
	go vet ./...

docker-check: ## Check the Dockerfiles and the Compose file.
	$(need-docker)
	docker build --check .
	docker build --check docker/postgres
	docker compose config --quiet

check: lint vet build test docker-check ## Run each check of the project. CI runs the same targets.
