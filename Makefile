VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build install fmt lint test test-race release-local demo

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

# Records the README demo GIF (docs/demo/README.md). Needs vhs, herdr and a
# logged-in claude; runs real sessions. Installs this build first: herdr panes
# and Claude's `igris done` find igris through your shell's PATH, not ours.
demo:
	@command -v vhs >/dev/null || { echo "vhs not found: https://github.com/charmbracelet/vhs#installation"; exit 1; }
	$(MAKE) install
	@# Drop HERDR_* so the recorded herdr doesn't refuse to nest when this runs in a herdr pane.
	env $$(env | sed -n 's/^\(HERDR_[A-Z_]*\)=.*/-u \1/p') vhs docs/demo/demo.tape
