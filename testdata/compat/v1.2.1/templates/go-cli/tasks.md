<!-- igris:format 1 -->
# Plan — Go command-line tool

A starter plan for a Go CLI: module and Makefile, the commands, release builds. Written by `igris init --template go-cli`; edit it freely, only the task tables matter to igris (SPEC §3).

**Ranks are suggestions.** `sonnet` is set for well-specified work and `opus` for decisions, the core design and reviews. Change the Model column to what you want to spend before the first run, then run `igris check`. Also set `[run] verify` in `igris.toml` to the project's checks (for example `go test ./...`).

**Start here.** `M0-01` is yours: describe what the tool is in `SPEC.md`. Every other task waits for it, so a run starts only once you have said what to build.

## M0 — Foundation

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M0-01 | **Write the spec** — say what the tool is, its commands and flags, and what it must never do, in `SPEC.md` | — | ready | — | user | — |
| M0-02 | **Decide layout and dependencies** — package layout under `cmd/` and `internal/`, the flag library or the standard library, the test approach; write the decision to `docs/decisions.md` for the owner to approve | M0-01 | blocked | opus | agent + user | plan |
| M0-03 | **Module, Makefile and CI** — `go.mod`, a `main` that prints its version, a Makefile with `fmt`, `lint`, `test` and `build`, and a CI workflow running them | M0-02 | blocked | sonnet | agent | — |
| M0-G | **M0 gate** — run `make fmt lint test build` and approve the layout and the dependency list | M0-01…M0-03 | blocked | sonnet | agent + user | — |

## M1 — Core

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M1-01 | **Commands and flags** — the command dispatch and every flag from `SPEC.md`, with usage text, exit codes and table-driven tests | M0-G | blocked | sonnet | agent | — |
| M1-02 | **Core package** — the logic behind the commands in `internal/`, small concrete types, errors wrapped with context, unit tests | M0-G | blocked | opus | agent | — |
| M1-03 | **Configuration** — read the tool's config file and environment, with defaults and clear errors | M1-02 | blocked | sonnet | agent | — |
| M1-04 | **Wiring and golden tests** — connect the commands to the core and the config; end-to-end tests of the built binary against golden output files | M1-01, M1-03 | blocked | sonnet | agent | — |
| M1-G | **M1 gate** — try every command by hand and approve the behavior and the output | M1-01…M1-04 | blocked | sonnet | agent + user | — |

## M2 — Release

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M2-01 | **README and help** — install, usage and examples in `README.md`; help text that matches `SPEC.md` | M1-G | blocked | sonnet | agent | — |
| M2-02 | **Release builds** — cross-compiled binaries with checksums and version info, built by a workflow on a tag | M1-G | blocked | sonnet | agent | — |
| M2-03 | **Security review** — read the finished tool for unsafe file, process and input handling; fix what is clear and list the rest | M2-01, M2-02 | blocked | opus | agent | — |
| M2-G | **M2 gate** — run the release build on a clean machine and approve shipping | M2-01…M2-03 | blocked | sonnet | agent + user | — |
