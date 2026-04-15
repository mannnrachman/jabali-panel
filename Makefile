.PHONY: help build run test test-short test-coverage coverage-check lint fmt vet tidy clean

GO       := go
API_PKG  := ./panel-api/...
BIN      := ./bin/jabali
COVER    := coverage.out
MIN_COV  := 80

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Compile the server binary
	mkdir -p bin
	$(GO) build -o $(BIN) ./panel-api/cmd/server

run: ## Run the server (dev)
	$(GO) run ./panel-api/cmd/server

test: ## Run all Go tests
	$(GO) test -race -count=1 $(API_PKG)

test-short: ## Run only fast unit tests (skip integration)
	$(GO) test -race -count=1 -short $(API_PKG)

test-coverage: ## Run tests with coverage (internal packages only)
	$(GO) test -race -count=1 -coverprofile=$(COVER) -covermode=atomic -coverpkg=./panel-api/internal/... ./panel-api/internal/...
	$(GO) tool cover -func=$(COVER) | tail -n 1

test-integration: ## Run integration tests (requires JABALI_TEST_DATABASE_URL + real MariaDB)
	$(GO) test -race -count=1 -tags=integration -coverprofile=$(COVER) -covermode=atomic -coverpkg=./panel-api/internal/... ./panel-api/internal/...
	$(GO) tool cover -func=$(COVER) | tail -n 1

coverage-check: test-coverage ## Fail if coverage below MIN_COV
	@pct=$$($(GO) tool cover -func=$(COVER) | awk '/total:/ {gsub("%","",$$3); print $$3}'); \
	awk -v p="$$pct" -v m="$(MIN_COV)" 'BEGIN { if (p+0 < m+0) { printf "coverage %s%% below %s%%\n", p, m; exit 1 } else { printf "coverage %s%% OK\n", p } }'

lint: ## Run golangci-lint
	golangci-lint run $(API_PKG)

fmt: ## Format all Go code
	$(GO) fmt $(API_PKG)

vet: ## Run go vet
	$(GO) vet $(API_PKG)

tidy: ## Tidy module deps
	$(GO) mod tidy

clean: ## Remove build artefacts
	rm -rf bin $(COVER)
