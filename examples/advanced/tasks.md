# Advanced example plan

The example plan of `../tasks.md` with the optional `Verify`, `Timeout` and `Context` columns (SPEC §3.2). Try it:

```sh
cd examples/advanced
igris check --strict
igris status
igris arise P1 --dry-run
```

- **Verify** names a profile from `igris.toml` (`fast`, `full`, `default`) or `none`; an empty cell (`—`) falls back to the phase's `[phases.<id>]` profile, then to `default`.
- **Timeout** is a Go duration after which a running task is reported overdue (a `task_overdue` notification); igris never stops the session for it.
- **Context** lists repo-relative files or directories the session must read first. They must exist, so `docs/cli.md` is part of this example.

## P1 — Hello CLI

| ID | Task | Deps | Status | Model | Owner | Verify | Timeout | Context |
|---|---|---|---|---|---|---|---|---|
| P1-01 | **Project skeleton** — module, `main.go`, a Makefile with `build` and `test` | — | ready | sonnet | agent | — | 30m | — |
| P1-02 | **Flag parsing** — `--name` and `--shout` flags with tests | P1-01 | blocked | sonnet | agent | fast | 45m | docs/cli.md |
| P1-03 | **Greeting design** — decide how greetings are chosen and localized; write the decision to `docs/greeting.md` | P1-01 | blocked | opus | agent | none | 1h | docs/ |
| P1-G | **P1 gate** — the owner runs the binary and approves the output | P1-01…P1-03 | blocked | sonnet | agent + user | — | — | — |

## P2 — Polish

| ID | Task | Deps | Status | Model | Owner | Verify | Timeout | Context |
|---|---|---|---|---|---|---|---|---|
| P2-01 | **Greeting engine** — implement the design from P1-03 | P1-G | blocked | opus | agent | — | 1h30m | `docs/cli.md`, docs/ |
| P2-02 | **README** — usage, flags, examples | P2-01 | blocked | sonnet | agent | fast | 20m | docs/cli.md |
