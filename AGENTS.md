# AGENTS.md — working rules for igris

These rules apply to every coding agent working in this repository (Claude Code reads them via `CLAUDE.md`).

## 1. Sources of truth
- `SPEC.md` is normative. If code and spec disagree, the spec wins until amended through a `P0`-style decision task in `tasks.md`.
- `tasks.md` is the implementation plan. Work only on the task you were given. Don't start adjacent tasks, even small ones.
- If the spec is ambiguous or seems wrong, stop and ask the owner. Don't silently pick an interpretation.

## 2. Task workflow
- Before changing anything: read this file, `SPEC.md` sections relevant to the task, and the task's row in `tasks.md`.
- Definition of done for any task: code + tests + `make fmt lint test` green + `SPEC.md`/`README.md` updated in the same change if behavior changed.
- **Status column:** when the task is run by igris itself, igris owns the Status column — don't edit it. When the owner runs a task manually (before igris can run itself, i.e. before M3 is done), update the task's Status and flip dependents to `ready` when all their deps are `done`/`skipped`.
- Commit only when the owner asks. Commit messages: `<TASK-ID>: <short summary>`.

## 3. Go conventions
- Go version pinned in `go.mod`. Module: `github.com/drilonrecica/igris`.
- Layout:
  - `cmd/igris/` — main, subcommand dispatch only
  - `internal/plan/` — parsing, validation, readiness, surgical writer
  - `internal/engine/` — scheduler, run loop, signals, verify, commit
  - `internal/backend/` — `Backend`/`Session` interfaces; `herdr/`, `fake/`
  - `internal/notify/` — backend toast, ntfy, Discord
  - `internal/state/` — `.igris/` state, lock, run log
  - `internal/config/` — `igris.toml`
  - `internal/tui/` — Bubble Tea UI
  - `internal/adapt/` — `igris adapt`
  - `internal/prompt/` — embedded templates
- Small concrete types, plain functions. Interfaces only at real seams (`Backend`, `Session`, notifier, clock, command runner).
- Every external process call goes through one injectable command runner with a timeout. Never build shell strings from plan text; pass argv.
- Errors: wrap with context (`fmt.Errorf("parse %s: %w", path, err)`); user-facing errors say what to do next.
- No global state; no `init()` side effects beyond flag/command registration.
- `context.Context` is threaded through everything that does I/O.

## 4. Dependencies
- Only dependencies approved in decision task P0-01 may be added. A new dependency needs a new decision task first.
- Prefer the standard library. No CGO.

## 5. Testing
- Table-driven unit tests next to the code. Golden files under `testdata/`; update them only with `-update` and review the diff.
- The plan writer must have byte-preservation tests for every change it can make.
- Engine behavior is tested end-to-end with the fake backend and a fake clock — no sleeps in tests.
- herdr backend tests use recorded JSON fixtures; never require a running herdr in `make test`.
- `go test -race ./...` must stay clean.
- No private plans (e.g. from the owner's other projects) in fixtures. Use synthetic ones.

## 6. Safety rules
- Igris must never: send plan text through a shell; read, print or store Claude credentials; set `ANTHROPIC_API_KEY`; log notification secrets; write the plan outside Status cells (except an owner-approved `adapt`).
- Skip-permissions mode must always require explicit per-run confirmation (SPEC §7.3). Don't add shortcuts around it.

## 7. Docs
- User-facing behavior changes update `README.md` in the same change.
- Keep `SPEC.md` and the CLI help text consistent.
