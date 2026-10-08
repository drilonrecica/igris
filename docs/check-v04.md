# v0.4 gate: plan power

The v0.4 gate (V04-G) covers four things. v0.3 plans must still validate the same way. A real herdr run uses Verify profiles, Timeout, Context and task hooks. Slices, `reset` and `check --strict` are driven by hand. The full test suite must be green. It ran on Linux (Fedora 44) on 2026-10-08, from the owner's Claude Code session, which drove igris in its own herdr tab. All projects, plans and answers were synthetic scratch repos (`git init`).

Versions: igris built from `8969f1d` (`0.3.1-0.20261008080057-8969f1db9643`, the build that becomes v0.4.0), v0.3.0 built from `git archive v0.3.0`, herdr 0.9.1, Claude Code 2.1.294.

**Model and mode switch.** The first pass of checks 2, 3 and 5 mapped every rank to `haiku` and used `default_mode = "accept"`. The owner then asked for no haiku, `auto` mode and no herdr toasts. Every scratch config was changed to `[models]` → `sonnet`, `default_mode = "auto"` and `[notify.backend] enabled = false`. Checks 2–5 were then re-run from scratch in a fresh project, `p2`. Every result below comes from the sonnet/auto runs (the transcripts show `claude-sonnet-5-5`). The haiku pass gave the same results.

Scratch project setup (`p2`):

```toml
default_mode = "auto"
needs_input_after = "60s"
[models]   # every rank → "sonnet"
[run]
  verify = "echo default-profile-ran >> verify.log"   # profile "default"
  commit = "never"
[verify]
  fast = "echo fast-profile-ran >> verify.log; test -f a.txt"
  phasep = "echo phasep-profile-ran >> verify.log; test -f c.txt"
[phases.A]
  verify = "phasep"
[hooks]
  before_task = ["sh", "-c", "env | grep '^IGRIS_' | sort | sed 's/^/before /' >> hooks.log; test ! -f fail-hook || { echo hook refusing because fail-hook exists; exit 3; }"]
  after_task = ["sh", "-c", "env | grep '^IGRIS_' | sort | sed 's/^/after /' >> hooks.log"]
  timeout = "20s"
[notify.ntfy]   # https://ntfy.sh, random topic igris-gate04-<16 hex>
```

The plan has three phases. In phase A, A-01 (Verify `fast`, Context `docs/req.md`), A-02 (Verify `none`) and A-03 (Verify `—`) each create one file. Phase B has B-01, the hook-failure task. Phase C has C-01 (Timeout `30s`), which runs `sleep 50` first. `.claude/settings.local.json` puts the HEAD build first on `PATH`, so the sessions' `igris done` was the build under test (A-01 wrote `command -v igris` → `…/gate04/bin/igris`).

## Results

| # | Check | Result |
|---|---|---|
| 1 | v0.3 fixtures unchanged, same result as v0.3.0 | pass |
| 2 | Real phase with Verify profiles (`fast`, `none`, phase default) | pass |
| 3 | Timeout → overdue, Needs you, `task_overdue` on ntfy | pass |
| 4 | Context in the prompt; invalid paths rejected by `check` | pass |
| 5 | Task hooks: env, failing `before_task` → Needs you → retry | pass |
| 6 | `arise --only`, `--from/--until`, not-run report, `--dry-run` slices, validation | pass |
| 7 | `igris reset` without and with a running igris | pass |
| 8 | `check --strict` exit codes | pass |
| 9 | `make fmt lint test`, `go test -race ./...` | pass |

### 1. Fixtures

`igris check --plan <file>` ran on every `*.md` under a `testdata/` directory (46 plans: `cmd/igris`, `internal/plan` valid/invalid/lint/large, `internal/adapt`), from an empty directory, with both binaries. All 46 gave the same exit code and byte-identical output. `git diff v0.3.0 HEAD` changes no fixture plan except the new `lint/` ones, which v0.3.0 also validates (exit 0). The v0.3.0 `examples/` (plan and config from the tag) give `tasks.md: OK (2 phases, 8 tasks, 0 warnings)` with both. The v0.4 `examples/` and `examples/advanced/` pass with the new binary. v0.3.0 rejects their config (`unknown key(s) "verify", "hooks"`), as it should, because those keys are new in v0.4.

### 2. Verify profiles

```
10:14:04 A-01 started (sonnet → sonnet, mode auto) · Make a.txt
10:15:14 A-01 verifying (fast): echo fast-profile-ran >> verify.log; test -f a.txt
10:15:14 A-01 verify passed (fast)
10:15:21 A-02 started (sonnet → sonnet, mode auto) · Make b.txt
10:15:41 A-02 done · Created b.txt containing 'hi'          # Verify none: no verify step
10:16:01 A-03 verifying (phasep): echo phasep-profile-ran >> verify.log; test -f c.txt
10:16:04 phase A complete
```

`verify.log` contains `fast-profile-ran` and `phasep-profile-ran`, and nothing for A-02. Phases B and C have no `[phases.*]` entry, so they fell back to `default` (`verifying (default)`). The run log names each profile:

```
{"type":"verify_passed","task":"A-01",…,"detail":"profile fast"}
{"type":"verify_passed","task":"A-03",…,"detail":"profile phasep"}
{"type":"verify_passed","task":"C-01",…,"detail":"profile default"}
```

`arise A --dry-run` lists `verify fast`, `verify —` (A-02, `none`) and `verify phasep`. `status` shows the VERIFY/TIMEOUT/CONTEXT columns.

### 3. Timeout

```
10:17:17 C-01 session open
10:17:48 C-01 OVERDUE: running longer than its Timeout 30s; its session keeps running
10:17:48 C-01 NEEDS YOU: the task is running longer than its Timeout 30s; igris leaves its session running
10:18:28 C-01 verify passed (default)
10:18:31 C-01 done · Ran sleep 50 …, then created t.txt containing 'hi'
```

The run log has one `task_overdue` (`running longer than its Timeout 30s`) and one `notification` `task_overdue via ntfy`. The session was not stopped and finished normally. Read back with `curl https://ntfy.sh/<topic>/json?poll=1&since=all` (priority, title, message):

```
4 | igris · p2 | phase C · C-01 Slow task: needs you (running longer than its Timeout 30s)
```

It arrived once, at priority 4 (high). The other `phase_done` and `run_error` messages were priority 3 and contained no paths or output.

### 4. Context

The A-01 task prompt (from the session transcript) contains:

```
## Required reading

Read these paths of the project before changing anything:

- `docs/req.md`
```

Step 1 of "How to work" adds "the required reading above", and the session read the file. A-02 and A-03, which have no Context, have no such section. `check` on a one-task plan with each bad Context exited 1:

| Context cell | Message |
|---|---|
| `../secrets` | `Context "../secrets" leaves the project: paths must stay inside the project; use a repo-relative path` |
| `/etc/passwd` | `Context "/etc/passwd" is an absolute path; use a path relative to the project root` |
| `docs/missing.md` | `Context "docs/missing.md" does not exist; fix the path or remove it from the cell` |
| `docs/etclink/hosts` (symlink to `/etc`) | `… leads outside the project through a symlink; …` |
| `docs/../docs/ok.md` | `… leaves the project: …` (any `..` element is rejected) |
| ``docs/ok.md, `docs/` `` | `OK`, exit 0 |

### 5. Task hooks

`hooks.log` has a `before` and an `after` block for every agent task, for example:

```
before IGRIS_MODEL=sonnet / IGRIS_PHASE=A / IGRIS_RANK=sonnet / IGRIS_TASK_ID=A-01
after  IGRIS_MODEL=sonnet / IGRIS_PHASE=A / IGRIS_RANK=sonnet / IGRIS_RESULT=done / IGRIS_TASK_ID=A-01
```

With `fail-hook` present:

```
10:16:18 B-01 before_task hook failed: exit status 3
10:16:18   | hook refusing because fail-hook exists
10:16:18 B-01 NEEDS YOU: before_task hook failed: exit status 3; no session was opened
10:16:18 ? the before_task hook of B-01 failed: retry (runs the hook again), mark the task done, skip it, or stop
```

At that point B-01 was `in progress` and no tab was opened. The run log had an `error` entry (`before_task hook failed: exit status 3`, without the output), and `run_error` went to ntfy. Then `fail-hook` was removed and `retry` typed: `B-01 new session (fresh)`, verify passed, `phase B complete`. `hooks.log` has two `before` runs for B-01 and one `after` (`IGRIS_RESULT=done`).

### 6. Selection

Run on project `p3`, with user tasks only: M1-01, M1-02←M1-01, M1-03, M1-04←M1-02, then M2-01, M2-02←M2-01.

```
$ igris arise --only M1-02,M1-03 --no-tui
10:19:12 run started: phase M1; only M1-02, M1-03
10:19:12 M1-03 YOUR TURN: **Three** — owner step
10:19:32 M1-03 done · gate ok
10:19:32 not run: M1-02 waits on M1-01 (ready, phase M1)
10:19:32 run stopped: completed
not run: M1-02 waits on M1-01 (ready, phase M1)
```

`state.json` recorded `"selection": {"only": ["M1-02","M1-03"]}`. M1-02 stayed `blocked`, and the run log has no `phase_done` or `phase_stuck`.

```
$ igris arise --from M1-03 --until M2-01 --no-tui
10:19:39 run started: phase M1, M2; from M1-03 until M2-01
10:19:39 not run: M1-04 waits on M1-02 (blocked, phase M1)
10:19:39 phase M2 started
10:19:40 M2-01 done
10:19:40 run stopped: completed
```

M2-02 was outside the slice and went to `ready` through readiness sync only, with no `phase_done` for M2. The dry runs (`--only …`, `--from M1-03 --until M2-01`, `--from M2-01`, `M1 --until M1-03`) list the slice and the same `not run:` lines.

Validation:

| Command | Result |
|---|---|
| `--only M1-02 --from M1-01` | exit 2, `--only can't be combined with --from or --until` |
| `--only ''`, `--only M1-03,,M1-02`, `--only 'M1-03;rm'`, `--from .bad` | exit 2, usage error |
| `--only M9-01` | exit 1, `is not a task in …/tasks.md` |
| `M1 --until M2-01` | exit 1, `--until M2-01 is in phase M2, outside the run's phase M1; widen --through or drop it` |
| `--from M1-04 --until M1-02` | exit 1, `--from M1-04 comes after --until M1-02 in the plan; swap them` |

### 7. Reset

With no igris running (no lock):

```
$ igris reset M2-01            → exit 1  igris reset: M2-01 is done; pass --force to reset it
$ igris reset M2-01 --force    → M2-01: done → ready / M2-02: ready → blocked
$ igris reset M1-01            → M1-01 is ready: nothing to reset (exit 0, nothing written)
$ igris reset M1-01            (M1-01 and its dependent M1-02 both in progress)
M1-01: in progress → ready
M1-02 depends on M1-01 and is in progress; reset leaves it as it is
```

`git diff` shows only the two Status cells changed, and the run log has `task_reset`.

With igris running, on the current user task (`p3`, M1-03 waiting for the owner):

```
$ igris reset M1-03            → M1-03: reset sent to the running igris
10:19:20 reset M1-03: in progress → ready
10:19:20 pause after task: on (type `pause` again to turn it off)
10:19:20 paused before M1-03; type `pause` to continue
```

Typing `pause` restarted M1-03.

With igris running, on the current agent task (`p2`, `arise --only A-03`, session open):

```
10:20:07 A-03 session open
10:20:10 reset A-03: in progress → ready
10:20:10 paused before A-03; type `pause` to continue
```

The `A-03 · sonnet` tab closed right away, `state.json` has no current task, and the plan says `ready`.

### 8. `check --strict`

| Fixture (`internal/plan/testdata/lint/`) | `check --strict` | plain `check` |
|---|---|---|
| `clean.md` | 0 | 0 |
| `title.md`, `long.md`, `owner-step.md`, `gate.md`, `yolo.md`, `fable.md` | 1 (one `warning: file:line: …` each, summary `1 warning(s) under --strict; …`) | 0 |

`examples/advanced`: `check --strict` exits 0. Run inside the repo, `clean.md` gets the machine warning `igris.toml found in …` and still exits 0. `--json` gives `"strict": true` and `"lint": "yolo"`.

### 9. Test suite

On HEAD `8969f1d`, `make fmt lint test` passed (exit 0, tree unchanged). `go test -race -count=1 ./...` passed: 20 packages ok, 2 with no test files.

## Observations (not blocking)

- The dry run prints `verify —` both for a task whose Verify is `none` and for a task with no verification at all, where "none" would be clearer.
- `docs/../docs/ok.md` is rejected with the "leaves the project" message, although the path stays inside the project. The rule (no `..` element) is as decided in V04-P1; only the wording is loose.
- In the earlier haiku/`accept` pass, herdr reported A-01 and A-03 as `blocked` for about a minute while the machine's global Claude Code PreToolUse hooks ran. This raised **Needs you** after `needs_input_after`, and it cleared without any action. In the sonnet/`auto` re-run no Needs you came up.

No fixes were needed. Nothing blocks v0.4.0.
