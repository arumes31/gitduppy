# GitDuppy Makefile
#
# POSIX-make compatible: targets shell out only to basic sh, so this works with
# GNU make on Linux/macOS and with make under Windows git-bash. All recipes use
# forward-slash paths and plain `go`/`gofmt`/`golangci-lint`/`docker` invocations.

# Version metadata injected into the binary. Override on the command line, e.g.
#   make build VERSION=v1.2.3
# The defaults mirror what the Docker/release builds inject into main.Version /
# main.BuildTime (see Dockerfile: -X main.Version / -X main.BuildTime).
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

BINARY  := gitduppy
BIN_DIR := bin
CMD     := ./cmd/server

# -trimpath strips local paths; -s -w drop the symbol/debug tables. Matches the
# Dockerfile's build flags so `make build` and the container binary are identical.
GO_BUILD_FLAGS := -trimpath
LDFLAGS        := -s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)

# Formatting/vetting is scoped to the first-party source trees so it never walks
# mirrored repositories under data/ (which contain unrelated third-party .go files).
FMT_PATHS := cmd internal pkg

.DEFAULT_GOAL := help

.PHONY: help build test race cover lint vet fmt fmt-fix run docker tidy clean

help: ## Show this help
	@echo "GitDuppy make targets:"
	@echo "  build    - go build ./... and a versioned server binary into $(BIN_DIR)/"
	@echo "  test     - go test ./..."
	@echo "  race     - go test -race ./..."
	@echo "  cover    - coverage profile + per-func summary"
	@echo "  lint     - golangci-lint run"
	@echo "  vet      - go vet ./..."
	@echo "  fmt      - check gofmt (fails if any file needs formatting)"
	@echo "  fmt-fix  - apply gofmt -w to $(FMT_PATHS)"
	@echo "  run      - go run $(CMD)"
	@echo "  docker   - docker build the container image"
	@echo "  tidy     - go mod tidy"
	@echo "  clean    - remove build artifacts"
	@echo ""
	@echo "Note: database migrations are embedded goose SQL files applied"
	@echo "automatically at boot (RunSQLMigrations); there is no migrate target."

build: ## Build all packages and the versioned server binary
	go build ./...
	go build $(GO_BUILD_FLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

test: ## Run the test suite
	go test ./...

race: ## Run the test suite under the race detector
	go test -race ./...

cover: ## Produce a coverage profile and print a per-function summary
	go test -covermode=atomic -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint: ## Run golangci-lint with the repository config
	golangci-lint run --config .golangci.yml --timeout 5m

vet: ## Run go vet
	go vet ./...

fmt: ## Check formatting; fails (listing offenders) if any file needs gofmt
	@out=`gofmt -l $(FMT_PATHS)`; \
	if [ -n "$$out" ]; then \
		echo "The following files are not gofmt-clean:"; \
		echo "$$out"; \
		exit 1; \
	fi; \
	echo "gofmt: all files formatted"

fmt-fix: ## Apply gofmt -w to the first-party source trees
	gofmt -w $(FMT_PATHS)

run: ## Run the server from source
	go run $(CMD)

docker: ## Build the Docker image with version metadata baked in
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(BINARY):$(VERSION) .

tidy: ## Tidy go.mod/go.sum
	go mod tidy

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) coverage.out
