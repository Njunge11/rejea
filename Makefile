# The commands of the project. They read .env when it exists.
-include .env
export

.PHONY: db-up db-down migrate serve test lint vet docker-check check

db-up: ## Build and start the database, and wait until it is healthy.
	docker compose up -d --build --wait

db-down: ## Stop the database. The data stays in its volume.
	docker compose down

migrate: ## Bring the database schema up to date.
	go run ./cmd/rejea migrate

serve: ## Run the HTTP server.
	go run ./cmd/rejea serve

test: ## Run all tests. The database must be up.
	go test -race ./...

lint: ## Check the format and the code.
	golangci-lint run

vet: ## Find suspect code.
	go vet ./...

docker-check: ## Check the Dockerfile and the Compose file.
	docker build --check docker/postgres
	docker compose config --quiet

check: lint vet test docker-check ## Run each check of the project. CI runs the same targets.
