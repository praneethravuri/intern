# tether — build, test and release targets. CGO_ENABLED=0 throughout: the pure-Go
# SQLite driver makes both binaries static and cross-compilable with no C toolchain.

SHELL := /bin/sh

# Override for a release build: make build VERSION=v1.2.3
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

GO       ?= go
LDFLAGS  := -s -w -X main.version=$(VERSION)
GOFLAGS  := -trimpath -ldflags "$(LDFLAGS)"

BINS      := tether tetherd
DIST      := dist
COVERFILE := coverage.out

# Platforms for `make cross`, as GOOS/GOARCH pairs.
PLATFORMS := linux/amd64 linux/arm64 darwin/arm64 windows/amd64

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@echo "tether $(VERSION)"
	@echo
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[1m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build ./tether and ./tetherd with the version stamped in
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -o tether  ./cmd/tether
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -o tetherd ./cmd/tetherd

.PHONY: install
install: ## go install both binaries into GOBIN
	CGO_ENABLED=0 $(GO) install $(GOFLAGS) ./cmd/tether ./cmd/tetherd

.PHONY: test
test: ## Run the full test suite with the race detector
	$(GO) test -race -count=1 ./...

.PHONY: test-short
test-short: ## Run the fast tests only (-short, no race detector)
	$(GO) test -short -count=1 ./...

.PHONY: cover
cover: ## Write a coverage profile and print the total
	$(GO) test -race -count=1 -covermode=atomic -coverprofile=$(COVERFILE) ./...
	$(GO) tool cover -func=$(COVERFILE) | tail -n 1

.PHONY: lint
lint: ## Run golangci-lint using .golangci.yml
	golangci-lint run

.PHONY: fmt
fmt: ## Format every Go file in place
	gofmt -s -w .

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

.PHONY: cross
cross: ## Cross-compile both binaries for every supported platform into dist/
	@mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		for bin in $(BINS); do \
			echo "  $$os/$$arch  $$bin$$ext"; \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
				$(GO) build $(GOFLAGS) -o $(DIST)/$$bin-$$os-$$arch$$ext ./cmd/$$bin; \
		done; \
	done

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BINS) $(COVERFILE)
	rm -rf $(DIST)
	# Stray files from an interrupted build/gofmt or a crashed editor (see .gitignore).
	find . -name '*-go-tmp-umask' -delete
	find . -name '*.go.[0-9]*' -delete
	find . -name '.fuse_hidden*' -delete
