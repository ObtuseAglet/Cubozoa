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
	rm -rf dist

# Cross-compilation targets for releases. CGO is disabled so every artifact is a
# fully static, dependency-free binary.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: dist
dist: ## Cross-compile static release binaries into dist/
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out="dist/$(BINARY)_$(VERSION)_$${os}_$${arch}$$ext"; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o "$$out" $(PKG) || exit 1; \
	done
	@echo "done -> dist/"

.PHONY: docker
docker: ## Build the container image (tag: cubozoa:$(VERSION))
	docker build -t cubozoa:$(VERSION) --build-arg VERSION=$(VERSION) .

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
