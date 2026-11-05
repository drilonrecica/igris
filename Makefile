VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build install fmt lint test test-race vscode-deps lint-vscode test-vscode-unit test-vscode vsix vsix-check vsix-publish config-ref fuzz release-local sign sign-check demo demo-screens site

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
	$(MAKE) lint-vscode

# -timeout: a test that hangs, or a process it started, fails instead of
# running on (AGENTS.md §5: tests never start the real igris).
test:
	go test -timeout 120s ./...
	$(MAKE) test-vscode-unit

test-race:
	go test -race -timeout 120s ./...

# Regenerates docs/config.md from the config struct and its defaults; the
# drift test in internal/config fails while it is stale.
config-ref:
	go test -timeout 120s ./internal/config -run TestReferenceUpToDate -update-reference

# Runs every Fuzz* target for FUZZTIME each, one after another (`make test`
# only replays their seeds). A failing input is saved under the package's
# testdata/fuzz/<Target>/; commit it with the fix as a regression seed. CI
# runs it with FUZZTIME=30s on every push (decisions.md V06-P1).
FUZZTIME ?= 1m
fuzz:
	@for f in $$(grep -rl --include='*_test.go' '^func Fuzz' internal); do \
		for t in $$(sed -n 's/^func \(Fuzz[A-Za-z0-9_]*\)(.*/\1/p' $$f); do \
			echo "== $$t ($$(dirname $$f), $(FUZZTIME))"; \
			go test ./$$(dirname $$f) -run '^$$' -fuzz "^$$t\$$" -fuzztime $(FUZZTIME) || exit 1; \
		done; \
	done

# Snapshot build of all release targets into dist/ (SPEC §18), with an SBOM per
# archive. Needs goreleaser and syft; never publishes: the owner uploads dist/
# to a GitHub Release by hand.
release-local:
	@command -v goreleaser >/dev/null || { echo "goreleaser not found: https://goreleaser.com/install/"; exit 1; }
	@command -v syft >/dev/null || { echo "syft not found (makes the SBOMs): https://github.com/anchore/syft#installation"; exit 1; }
	goreleaser check
	goreleaser release --snapshot --clean --skip=publish
	go run ./tools/genformula -dist dist

# The release itself (SPEC §18, decisions.md V06-P1), run by the owner: signs
# tag vVERSION with the release key, builds dist/ from it and signs
# dist/checksums.txt. Pushes nothing. sign-check only proves the key, its
# passphrase and allowed_signers match. KEY= overrides ~/.ssh/igris-release.
sign:
	@test -n "$(filter command line,$(origin VERSION))" || { echo "usage: make sign VERSION=X.Y.Z"; exit 1; }
	sh tools/sign.sh $(VERSION)

sign-check:
	sh tools/sign.sh --check

# Captures what release TAG wrote into testdata/compat/TAG for the
# compatibility suite (SPEC §17, §19); run after tagging, then commit.
compat:
	@test -n "$(TAG)" || { echo "usage: make compat TAG=vX.Y.Z"; exit 1; }
	sh tools/compat.sh $(TAG)

# The environment the demo tapes run in: -u for each HERDR_* and CLAUDE* variable.
DEMO_ENV = $$(env | sed -En 's/^((HERDR|CLAUDE)[A-Z_]*)=.*/-u \1/p')

# Records the README demo GIF (docs/demo/README.md). Needs vhs, herdr and a
# logged-in claude; runs real sessions. Installs this build first: herdr panes
# and Claude's `igris done` find igris through your shell's PATH, not ours.
demo:
	@command -v vhs >/dev/null || { echo "vhs not found: https://github.com/charmbracelet/vhs#installation"; exit 1; }
	$(MAKE) install
	@# Drop HERDR_* so the recorded herdr doesn't refuse to nest when this runs in a herdr pane,
	@# and CLAUDE* so its sessions aren't child sessions of a Claude Code this runs in (they
	@# would write no transcript of their own, and the run's tokens would be unavailable).
	env $(DEMO_ENV) vhs docs/demo/demo.tape

# Takes the README's home screenshots (docs/demo/README.md). Needs vhs and
# tmux; starts no Claude Code session. Home runs in a private tmux server.
demo-screens: install
	@command -v vhs >/dev/null || { echo "vhs not found: https://github.com/charmbracelet/vhs#installation"; exit 1; }
	env $(DEMO_ENV) -u TMUX vhs docs/demo/home.tape
	env $(DEMO_ENV) -u TMUX vhs docs/demo/home-narrow.tape

# Builds the project page into _site/ (decisions.md P0-09); pages.yml deploys
# the same output. Preview: python3 -m http.server -d _site
site:
	sh site/build.sh

# The VS Code extension (editors/vscode, SPEC §15.9, decisions.md V19-P1).
# Needs Node and npm; dependencies come from package-lock.json with `npm ci`.
# No formatter is approved for TypeScript, so `make fmt` leaves it alone.
VSCODE := editors/vscode

$(VSCODE)/node_modules/.package-lock.json: $(VSCODE)/package-lock.json $(VSCODE)/package.json
	cd $(VSCODE) && npm ci

vscode-deps: $(VSCODE)/node_modules/.package-lock.json

# tsc --noEmit over the extension and its tests (part of `make lint`).
lint-vscode: vscode-deps
	cd $(VSCODE) && npx --no-install tsc --noEmit

# The extension's unit tests, with no VS Code and a fake `igris serve` socket
# (part of `make test`).
test-vscode-unit: vscode-deps
	cd $(VSCODE) && npm test

# The tests in a real VS Code (@vscode/test-electron, downloaded once into
# $(VSCODE)/.vscode-test). Needs a display: on a headless Linux box run it as
# `xvfb-run -a make test-vscode`, as CI does.
test-vscode: vscode-deps
	cd $(VSCODE) && npm run test:vscode

# Builds bin/igris-X.Y.Z.vsix, byte for byte the same from the same commit.
# Never publishes (the owner does, make vsix-publish).
vsix: vscode-deps
	sh $(VSCODE)/scripts/vsix.sh $(CURDIR)/bin

# Rebuilds the .vsix from tag TAG and compares its checksum with the one CI
# attached to the release.
vsix-check:
	@test -n "$(TAG)" || { echo "usage: make vsix-check TAG=vX.Y.Z"; exit 1; }
	sh tools/vsix-check.sh $(TAG)

# The owner's step (decisions.md V19-P1): vsix-check first, then vsce and ovsx
# publish the .vsix it checked. VSCE_PAT and OVSX_PAT come from the environment
# only (never a file in the repo, never echoed), and no npm step runs while
# they are set: no vscode-deps prerequisite here (the script says to run it
# when vsce or ovsx is missing). DRY=1 prints the two commands with the tokens
# masked and publishes nothing.
vsix-publish:
	@test -n "$(TAG)" || { echo "usage: make vsix-publish TAG=vX.Y.Z [DRY=1]"; exit 1; }
	@sh tools/vsix-publish.sh $(TAG) $(if $(DRY),dry)
