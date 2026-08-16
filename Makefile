GO ?= go
BINARY_NAME ?= divisor
BUILD_DIR ?= bin
GOOS := $(shell $(GO) env GOOS)

ifeq ($(GOOS),windows)
BINARY_EXT := .exe
endif

BINARY := $(BUILD_DIR)/$(BINARY_NAME)$(BINARY_EXT)

.DEFAULT_GOAL := help

.PHONY: help build run install generate fmt fmt-check vet lint test tidy check clean release release-check

help: ## Show available targets
	@printf "%s\n" \
		"Usage: make <target>" \
		"" \
		"Targets:" \
		"  help       Show available targets" \
		"  build      Build the application into $(BINARY)" \
		"  run        Run the application" \
		"  install    Install the application" \
		"  generate   Run Go code generation" \
		"  fmt        Format Go source files" \
		"  fmt-check  Check Go source formatting" \
		"  vet        Run go vet" \
		"  lint       Run golangci-lint" \
		"  test       Run all tests verbosely" \
		"  tidy       Tidy Go module dependencies" \
		"  check      Run formatting, vet, lint, and tests" \
		"  clean      Remove build artifacts" \
		"  release    Validate and build a snapshot release"

build: ## Build the application
	@mkdir -p "$(BUILD_DIR)"
	$(GO) build -o "$(BINARY)" .

run: ## Run the application
	$(GO) run .

install: ## Install the application
	$(GO) install .

generate: ## Run Go code generation
	$(GO) generate ./...

fmt: ## Format Go source files
	$(GO) fmt ./...

fmt-check: ## Check Go source formatting
	@test -z "$$(gofmt -l .)" || (echo "Unformatted Go files found; run 'make fmt'." && gofmt -l . && exit 1)

vet: ## Run go vet
	$(GO) vet ./...

lint: ## Run golangci-lint
	golangci-lint run --timeout=3m

test: ## Run all tests verbosely
	$(GO) test -v ./...

tidy: ## Tidy Go module dependencies
	$(GO) mod tidy

check: fmt-check vet lint test ## Run all verification checks

clean: ## Remove build artifacts
	$(RM) -r "$(BUILD_DIR)"

release-check:
	goreleaser check

release: release-check ## Build a snapshot release
	goreleaser release --snapshot --clean
