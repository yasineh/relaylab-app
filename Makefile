# RelayLab developer commands. Run `make help` for the list.

SHELL := /bin/bash
.DEFAULT_GOAL := help

SQLC_IMAGE     := sqlc/sqlc:1.27.0
TEST_DB_URL    ?= postgres://postgres:test-only-postgres@localhost:$${POSTGRES_TEST_HOST_PORT:-54321}/postgres?sslmode=disable
GITLEAKS_IMAGE := zricethezav/gitleaks:v8.21.2

-include .env
export

.PHONY: help
help: ## List available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: dev
dev: ## Start the full local stack with Docker Compose
	docker compose up --build

.PHONY: down
down: ## Stop the local stack (keeps the database volume)
	docker compose down

.PHONY: test
test: test-go test-web ## Run all tests

.PHONY: test-db
test-db: ## Start the isolated Postgres used by integration tests
	docker compose --profile test up -d --wait postgres-test

.PHONY: test-go
test-go: test-db ## Run Go unit and integration tests (isolated test Postgres)
	RELAYLAB_REQUIRE_DB=1 RELAYLAB_TEST_DATABASE_URL="$(TEST_DB_URL)" go test -race -count=1 ./...

.PHONY: test-web
test-web: ## Run web unit tests
	cd web && npm test -- --run

.PHONY: lint
lint: ## Run Go vet, format check, and web lint/typecheck
	go vet ./...
	@test -z "$$(gofmt -l $$(git ls-files '*.go'))" || (echo "gofmt needed on:"; gofmt -l $$(git ls-files '*.go'); exit 1)
	cd web && npm run lint && npm run typecheck

.PHONY: fmt
fmt: ## Format Go and web sources
	gofmt -w $$(git ls-files '*.go')
	cd web && npm run format

.PHONY: migrate
migrate: ## Apply database migrations with the migrator role
	go run ./cmd/api migrate

.PHONY: sqlc
sqlc: ## Generate type-safe Go code from SQL queries
	docker run --rm -v "$(CURDIR):/src" -w /src $(SQLC_IMAGE) generate

.PHONY: secrets-scan
secrets-scan: ## Scan the working tree for committed secrets
	docker run --rm -v "$(CURDIR):/repo" $(GITLEAKS_IMAGE) detect --source /repo --no-git --redact -v

.PHONY: build
build: ## Build Go binaries into ./bin (no embedded web app; Vite serves it in dev)
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker

.PHONY: build-embed
build-embed: ## Build the web app and an api binary that serves it (production layout)
	cd web && npm ci && npm run build
	rm -rf internal/webui/dist && cp -R web/dist internal/webui/dist
	go build -tags embedweb -o bin/api ./cmd/api
