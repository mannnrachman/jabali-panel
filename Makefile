.PHONY: help build run test test-short test-coverage coverage-check lint fmt vet tidy clean

GO         := go
API_PKG    := ./panel-api/...
AGENT_PKG  := ./panel-agent/...
WIRE_PKG   := ./agentwire/...
ALL_PKG    := $(API_PKG) $(AGENT_PKG) $(WIRE_PKG)
BIN        := ./bin/jabali
AGENT_BIN  := ./bin/jabali-agent
COVER      := coverage.out
MIN_COV    := 80

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Compile both binaries (panel + agent)
	mkdir -p bin
	$(GO) build -o $(BIN) ./panel-api/cmd/server
	$(GO) build -o $(AGENT_BIN) ./panel-agent/cmd/jabali-agent

run: ## Run the panel server (dev)
	$(GO) run ./panel-api/cmd/server

test: ## Run all Go tests across the workspace
	$(GO) test -race -count=1 $(ALL_PKG)

test-short: ## Run only fast unit tests (skip integration)
	$(GO) test -race -count=1 -short $(ALL_PKG)

test-coverage: ## Run tests with coverage (internal packages only)
	$(GO) test -race -count=1 -coverprofile=$(COVER) -covermode=atomic -coverpkg=./panel-api/internal/... ./panel-api/internal/...
	$(GO) tool cover -func=$(COVER) | tail -n 1

test-integration: ## Run integration tests (requires JABALI_TEST_DATABASE_URL + real MariaDB)
	$(GO) test -race -count=1 -tags=integration -coverprofile=$(COVER) -covermode=atomic -coverpkg=./panel-api/internal/... ./panel-api/internal/...
	$(GO) tool cover -func=$(COVER) | tail -n 1

coverage-check: ## Fail if combined (unit+integration) coverage below MIN_COV
	@if [ -z "$$JABALI_TEST_DATABASE_URL" ]; then \
		echo "coverage-check requires JABALI_TEST_DATABASE_URL (real MariaDB)"; \
		echo "  set it, or run 'make test-coverage' for unit-only (no gate)"; \
		exit 1; \
	fi
	@$(MAKE) --no-print-directory test-integration
	@pct=$$($(GO) tool cover -func=$(COVER) | awk '/total:/ {gsub("%","",$$3); print $$3}'); \
	awk -v p="$$pct" -v m="$(MIN_COV)" 'BEGIN { if (p+0 < m+0) { printf "coverage %s%% below %s%%\n", p, m; exit 1 } else { printf "coverage %s%% OK\n", p } }'

lint: ## Run golangci-lint across the workspace
	golangci-lint run $(ALL_PKG)

fmt: ## Format all Go code
	$(GO) fmt $(ALL_PKG)

vet: ## Run go vet
	$(GO) vet $(ALL_PKG)

tidy: ## Tidy module deps
	$(GO) mod tidy

clean: ## Remove build artefacts
	rm -rf bin $(COVER)
