VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build install fmt lint test test-race fuzz release-local demo site

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

# Runs every Fuzz* target for FUZZTIME each, one after another (`make test`
# only replays their seeds). A failing input is saved under the package's
# testdata/fuzz/<Target>/; commit it with the fix as a regression seed.
FUZZTIME ?= 1m
fuzz:
	@for f in $$(grep -rl --include='*_test.go' '^func Fuzz' internal); do \
		for t in $$(sed -n 's/^func \(Fuzz[A-Za-z0-9_]*\)(.*/\1/p' $$f); do \
			echo "== $$t ($$(dirname $$f), $(FUZZTIME))"; \
			go test ./$$(dirname $$f) -run '^$$' -fuzz "^$$t\$$" -fuzztime $(FUZZTIME) || exit 1; \
		done; \
	done

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

# Builds the project page into _site/ (decisions.md P0-09); pages.yml deploys
# the same output. Preview: python3 -m http.server -d _site
site:
	sh site/build.sh
