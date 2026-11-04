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
		"  benchmark  Run the Release benchmark on this checkout (Docker; never publishes)" \
		"  benchmark-smoke  Run the PR check's short smoke run of the Release benchmark (Docker)" \
		"  tidy       Tidy Go module dependencies" \
		"  check      Run formatting, vet, lint, and tests" \
		"  clean      Remove build artifacts" \
		"  release    Validate and build a snapshot release" \
		"  tag        Create and push a release tag: VERSION=vX.Y.Z[-rc.N] [BENCHMARK=publish] [DRY_RUN=1]"

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

.PHONY: benchmark
benchmark: ## Run the Release benchmark on this checkout; results in benchmark/results
	$(GO) -C benchmark run ./cmd/release-benchmark --checkout .. --results results

.PHONY: benchmark-smoke
benchmark-smoke: ## Run the smoke run the PR check runs: fails when a Contender is not in the Equivalent setup
	$(GO) -C benchmark run ./cmd/release-benchmark --checkout .. --results results --smoke

tidy: ## Tidy Go module dependencies
	$(GO) mod tidy

check: fmt-check vet lint test ## Run all verification checks

clean: ## Remove build artifacts
	$(RM) -r "$(BUILD_DIR)"

release-check:
	goreleaser check

release: release-check ## Build a snapshot release
	goreleaser release --snapshot --clean

# The Release benchmark publishes when the tag message has the line `benchmark: publish` (ADR 0009).
# git ls-remote --exit-code exits 2 when origin has no such tag.
.PHONY: tag
tag: ## Create and push a release tag: VERSION=vX.Y.Z[-rc.N] [BENCHMARK=publish] [DRY_RUN=1]
	@refuse() { printf 'make tag: %s\n' "$$1" >&2; exit 1; }; \
	printf '%s\n' "$$VERSION" | grep -Eqx 'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.[1-9][0-9]*)?' \
		|| refuse "VERSION must be vX.Y.Z or vX.Y.Z-rc.N, got '$$VERSION'"; \
	case "$$BENCHMARK" in ''|publish) ;; *) refuse "BENCHMARK must be publish or left out, got '$$BENCHMARK'" ;; esac; \
	case "$$DRY_RUN" in ''|1) ;; *) refuse "DRY_RUN must be 1 or left out, got '$$DRY_RUN'" ;; esac; \
	current_branch=$$(git symbolic-ref --quiet --short HEAD) || current_branch='a detached HEAD'; \
	[ "$$current_branch" = main ] || refuse "releases are tagged on main, not on $$current_branch"; \
	[ -z "$$(git status --porcelain)" ] \
		|| { git status --short >&2; refuse "the working tree is not clean: commit or remove the changes listed above"; }; \
	git fetch --quiet origin main || refuse "could not fetch main from origin"; \
	if [ "$$(git rev-parse main)" != "$$(git rev-parse origin/main)" ]; then \
		set -- $$(git rev-list --left-right --count main...origin/main); \
		refuse "local main is $$1 commit(s) ahead of origin/main and $$2 behind it: push or pull until they match"; \
	fi; \
	! git rev-parse --quiet --verify "refs/tags/$$VERSION" >/dev/null || refuse "the tag $$VERSION already exists locally"; \
	git ls-remote --exit-code --tags origin "refs/tags/$$VERSION" >/dev/null; \
	case $$? in \
		0) refuse "the tag $$VERSION already exists on origin" ;; \
		2) ;; \
		*) refuse "could not list the tags on origin" ;; \
	esac; \
	case "$$VERSION:$$BENCHMARK" in \
		*-rc.*:publish) refuse "BENCHMARK=publish needs a stable version: tagging a release candidate like $$VERSION starts no Release benchmark" ;; \
	esac; \
	tag_message=$$VERSION; \
	[ "$$BENCHMARK" != publish ] || tag_message=$$(printf '%s\n\n%s' "$$VERSION" 'benchmark: publish'); \
	if [ "$$DRY_RUN" = 1 ]; then \
		printf 'Every check passed. Without DRY_RUN=1, make tag would create the annotated tag %s\non %s\nwith this message, then push it to origin:\n\n' \
			"$$VERSION" "$$(git log -1 --format='%h %s' main)"; \
		printf '%s\n' "$$tag_message" | sed 's/^./    &/'; \
		printf '\nDRY_RUN=1: nothing was created or pushed.\n'; \
		exit 0; \
	fi; \
	git tag --annotate --message="$$tag_message" "$$VERSION" main || refuse "could not create the tag $$VERSION"; \
	git push origin "refs/tags/$$VERSION" \
		|| { git tag --delete "$$VERSION" >/dev/null; refuse "pushing $$VERSION to origin failed, so the local tag was deleted again: fix the cause and run make tag again"; }; \
	printf 'Pushed the annotated tag %s to origin, which starts the release.\n' "$$VERSION"
