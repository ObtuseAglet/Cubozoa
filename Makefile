# Cubozoa developer tasks. Run `make help` for a summary.

BINARY      := cubozoa
PKG         := ./cmd/cubozoa
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -X main.version=$(VERSION)

.DEFAULT_GOAL := help

.PHONY: build
build: ## Build the single binary
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

.PHONY: run
run: ## Build and run the server
	go run $(PKG)

.PHONY: test
test: ## Run the test suite
	go test ./...

.PHONY: race
race: ## Run tests with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run tests and open a coverage summary
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: tidy
tidy: ## Tidy module dependencies
	go mod tidy

.PHONY: check
check: vet test ## Run vet + tests (the pre-commit gate)

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BINARY) coverage.out

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
