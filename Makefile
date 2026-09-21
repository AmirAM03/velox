# Velox — High-performance V2Ray config engine
# https://github.com/AmirAM03/velox

BINARY_NAME := velox
MODULE := github.com/AmirAM03/velox
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -ldflags "-s -w -X $(MODULE)/internal/build.Version=$(VERSION) -X $(MODULE)/internal/build.Time=$(BUILD_TIME)"

# Go settings
GO := go
GOFLAGS := -trimpath
GOTEST := $(GO) test
GOFMT := gofmt

.PHONY: all build test lint clean fmt vet run help

all: build ## Default: build the binary

build: ## Build the velox binary
	$(GO) build $(GOFLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/velox/

run: build ## Build and run velox
	./bin/$(BINARY_NAME)

test: ## Run all tests
	$(GOTEST) -race -count=1 ./...

test-short: ## Run tests (skip slow/integration)
	$(GOTEST) -short -race -count=1 ./...

fuzz: ## Run fuzz tests on parsers (30s per target)
	$(GOTEST) -fuzz=. -fuzztime=30s ./internal/parser/...

lint: vet fmt-check ## Run all lint checks
	@echo "All lint checks passed."

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format all Go files
	$(GOFMT) -w -s .

fmt-check: ## Check if Go files are formatted
	@test -z "$$($(GOFMT) -l .)" || (echo "Files need formatting:" && $(GOFMT) -l . && exit 1)

clean: ## Remove build artifacts
	rm -rf bin/
	$(GO) clean -cache -testcache

deps: ## Download and tidy dependencies
	$(GO) mod download
	$(GO) mod tidy

coverage: ## Generate test coverage report
	$(GOTEST) -race -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'
