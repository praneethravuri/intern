# intern — build, test and release targets. CGO_ENABLED=0 throughout: the pure-Go
# SQLite driver makes the binary static and cross-compilable with no C toolchain.

SHELL := /bin/sh

# Override for a release build: make build VERSION=v1.2.3
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

GO       ?= go
LDFLAGS  := -s -w -X main.version=$(VERSION)
GOFLAGS  := -trimpath -ldflags "$(LDFLAGS)"

BIN       := intern
DIST      := dist
COVERFILE := coverage.out

# Platforms for `make cross`, as GOOS/GOARCH pairs.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@echo "intern $(VERSION)"
	@echo
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[1m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build ./intern with the version stamped in
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -o $(BIN) ./cmd/$(BIN)

.PHONY: install
install: ## go install intern into GOBIN
	CGO_ENABLED=0 $(GO) install $(GOFLAGS) ./cmd/$(BIN)

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
cross: ## Cross-compile intern for every supported platform into dist/
	@mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "  $$os/$$arch  $(BIN)"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			$(GO) build $(GOFLAGS) -o $(DIST)/$(BIN)-$$os-$$arch ./cmd/$(BIN); \
	done

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BIN) $(COVERFILE)
	rm -rf $(DIST)
	# Stray files from an interrupted build/gofmt or a crashed editor (see .gitignore).
	find . -name '*-go-tmp-umask' -delete
	find . -name '*.go.[0-9]*' -delete
	find . -name '.fuse_hidden*' -delete
