# Contributing to igris

Thanks for helping. igris is small and opinionated, so please read this before you open a pull request.

## Before you start

- [`SPEC.md`](SPEC.md) is the source of truth. If the code and the spec disagree, the spec wins. A change in behavior updates `SPEC.md` and `README.md` in the same change.
- For anything bigger than a bug fix, open an issue first and describe the problem. Features that bend one of the rules below need a decision before any code is written.
- Bugs: use the bug report template and include `igris version`, `claude --version`, `herdr --version`, your OS and your terminal.
- Security problems: don't open a public issue; see [`SECURITY.md`](SECURITY.md).

## Development

You need the Go version in `go.mod` and [golangci-lint](https://golangci-lint.run/) v2.

```sh
make build        # bin/igris
make fmt lint test
make test-race    # go test -race ./...
```

`make test` never needs herdr or Claude Code: the herdr backend replays recorded JSON fixtures, and the engine runs against a fake backend and a fake clock. Please keep it that way — no sleeps in tests, no network, no real sessions.

A change is done when it has code, table-driven tests next to it, `make fmt lint test` and `go test -race ./...` pass, and `SPEC.md`/`README.md` are updated if behavior changed. Golden files under `testdata/` are updated only with `-update`, and the diff is reviewed.

## Workflow

- igris is developed trunk-based on `master`. Keep each change small and green so `master` stays releasable. Pull requests are welcome; they're merged onto `master` as small commits.
- Commit messages: a short imperative summary (`plan: keep CRLF in escaped pipes`).
- **Dependencies:** prefer the standard library. A new Go module needs an approved decision first (see [`docs/decisions.md`](docs/decisions.md), P0-01). No CGO.
- **Fixtures:** only synthetic plans. Never commit a plan, log or note from a private project.

## Rules igris never breaks

A change that touches one of these needs an explicit decision, not just a pull request:

- Plan text never goes through a shell; external commands get argv through the injectable runner, with a timeout.
- igris never reads, prints or stores Claude credentials, and never sets `ANTHROPIC_API_KEY`.
- The plan is written only in Status cells (except an owner-approved `igris adapt`).
- Skip-permissions mode always needs a typed confirmation for every run.
- The model comes from the plan, never from igris's own judgment.
- Notification secrets are never logged.

## License

By contributing you agree that your contribution is licensed under the [MIT License](LICENSE).
