MODULE := github.com/nguyenngocanh94/mate
GO ?= go
GOFMT ?= gofmt
BIN ?= bin/mate

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X $(MODULE)/internal/config.Version=$(VERSION) \
	-X $(MODULE)/internal/config.Commit=$(COMMIT) \
	-X $(MODULE)/internal/config.BuildDate=$(BUILD_DATE)

# dash rejects `set -o pipefail`; keep bash for the test pipe.
SHELL := /bin/bash

.PHONY: all build test test-race vet fmt check-fmt check clean

all: check build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/mate

# gotestreport prints every skip and fails on any skip not declared in
# scripts/gotestreport/expected-skips.txt. Live Herdr/harness tests run
# only with MATE_LIVE=1.
test:
	set -o pipefail; $(GO) test -json ./... | $(GO) run ./scripts/gotestreport

test-race:
	set -o pipefail; $(GO) test -race -json ./... | $(GO) run ./scripts/gotestreport

vet:
	$(GO) vet ./...

fmt:
	$(GOFMT) -w cmd internal scripts assets

check-fmt:
	@unformatted=$$($(GOFMT) -l cmd internal scripts assets); \
	if [ -n "$$unformatted" ]; then \
		echo "files not formatted with gofmt:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

check: check-fmt vet test

clean:
	rm -rf bin coverage.out coverage.html skip-report.md skip-report-race.md
