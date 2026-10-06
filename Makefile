VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build install fmt lint test test-race release-local

build:
	go build -ldflags "$(LDFLAGS)" -o bin/igris ./cmd/igris

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/igris

fmt:
	gofmt -w .

lint:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }
	go vet ./...
	golangci-lint run

test:
	go test ./...

test-race:
	go test -race ./...

# Snapshot build of all release targets into dist/ (SPEC §18). Needs goreleaser;
# never publishes: the owner uploads dist/ to a GitHub Release by hand.
release-local:
	@command -v goreleaser >/dev/null || { echo "goreleaser not found: https://goreleaser.com/install/"; exit 1; }
	goreleaser check
	goreleaser release --snapshot --clean --skip=publish
