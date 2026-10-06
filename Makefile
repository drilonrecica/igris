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

# Stub until M7-03 wires up GoReleaser (snapshot build into dist/, never publishes).
release-local:
	@echo "release-local is not implemented yet (see M7-03)"
