# v1.5 gate

The v1.5 gate (V15-G) checks parallel phases end to end, in nested runs with real Claude Code sessions: a phase with `max = 2` including a forced conflict resolved by the rerun, a `Group` pair serialized, a gate task running alone, an overlap warning, and resume with two active tasks after a stop. Then it runs the full suite, race, fuzz and govulncheck.

It ran unattended on Linux (Fedora 44) on 2026-10-22, from the V15-G task session (owner's decision 2026-10-21). The build was this tree (`master` at `ea09686`, then with the fixes below), built with `-X main.version=1.5.0` into a scratch `GOBIN` put first in `PATH`. Every project was a synthetic scratch git repo under a `mktemp -d` directory, with tasks on sonnet that write a file, sometimes `sleep`, then run `igris done`. Each had `commit = "auto"` and `verify = "test -n \"$IGRIS_SLOT\" && echo slot=$IGRIS_SLOT"`, so every verify also proved the slot was set. The nested runs used a private tmux server (`tmux -L igris-gate-v15`, `HERDR_*` unset, `SHELL=/bin/bash`, windows started as `bash --noprofile --norc`). Mode was `auto` throughout, never `yolo` or `plan:yolo`. The gate played the owner with `tmux send-keys`: Claude Code's folder-trust question in session windows, and the sign-off the gate tasks asked for in their panes. `igris stop` ran from outside the view. Processes were addressed by exact PID. Afterwards the tmux server and its socket file were removed, along with `/tmp/tmux-1000`, which the gate had created. The scratch tree was removed too. No igris, Claude Code or test process of the gate was left behind; the owner's own two igris runs were not touched.

**Versions:** Claude Code 2.1.308, tmux 3.7c, Go 1.27.3.

## A phase with max = 2: conflict, rerun, overlap, Group, gate

Project p1, phase `G1` marked parallel, `max = 2`. `shared.txt` (`base`) is committed. `G1-01` appends `alpha` to it and `G1-02` appends `beta`; each then sleeps 40 s, so both changes exist at once and both rebases touch the same line. `G1-03` and `G1-04` are in `Group db`. `G1-G` is an `agent + user` gate depending on all four.

| Step | Result |
|---|---|
| `igris arise G1 --mode auto` | `G1-01` and `G1-02` started in slots 0 and 1, each in `.igris/worktrees/<run-id>-<ID>` on `igris/<run-id>-<ID>`. The header showed `2 active` and the view one card per task |
| Both appended | `overlap: G1-01 and G1-02 both change shared.txt`, in the feed and as an `overlap` event, once |
| `G1-01` done | verify passed (`slot=0`), committed, `integrated onto main`, `done` |
| `G1-02` done | committed, then `G1-02 didn't integrate: the rebase onto the run branch conflicts in shared.txt; igris runs the task again from the start` with `\| shared.txt`. The run log shows `integration_failed` (`reason: conflict`) and `task_retried` (attempt 2, fresh session, same slot) |
| Meanwhile | `G1-03` took the slot `G1-01` freed. `G1-04` (also `db`) started only after `G1-03` was integrated |
| `G1-02` rerun | on the new base it appended `beta` after `alpha`. Verify passed, then `verify passed (default): integrated tree`, then integrated and `done` |
| `G1-G` | started when the four were done. Its prompt says to wait for the owner's sign-off, so the agent did, said so after igris's nudge, and became **NEEDS YOU** after 30 s with a notification. The owner typed `approved` in its pane: `done`, integrated, `phase G1 complete`, `run stopped: completed` |

The result: linear history `init`, `G1-01`, `G1-03`, `G1-04`, `G1-02`, `G1-G`, one commit per task in completion order. `shared.txt` read `base / alpha / beta`. No worktree or task branch was left, every Status was `done`, and `state.json` was version 4.

In this phase both slots were full whenever `G1-04` waited, so the cap alone could explain the `Group` pair. The next project isolates both `Group` and the gate's exclusivity.

## Group, a gate alone, stop and resume with two active

Project p2, phase `R`, run with `--parallel 3`. File order:

- `R-01` (`db`) and `R-03` sleep 150 s.
- `R-02` (`db`) does not sleep.
- `R-G` is an `agent + user` task with **no deps**.
- `R-04` has no deps.

| Step | Result |
|---|---|
| `igris arise R --mode auto --parallel 3` | Only `R-01` and `R-03` started, with a third slot free. `R-02` was held by its group. `R-G` was held because others were active, and it held `R-04` behind it (§5.1 rule 3) |
| `state.json` | version 4, `active` listing both tasks with session, worktree, branch and slot 0/1. `current` was `R-01` |
| `igris stop` (both active) | `the run stopped; its sessions stay open`, exit 0. Both sessions and both worktrees stayed, and serve exited |
| `igris arise R --mode auto --parallel 3` again | `R-01 resumed: reattached to its session`, `R-03 resumed: …`, same slots. The run went on |
| Both ended | `R-02` started (see the first finding below for when) |
| `R-G` | started only when nothing was active, and ran alone for about 2 minutes: three slots, `R-04` ready and unblocked, nothing started beside it. After the owner's `approved` it integrated, then `R-04` started |

The result: `R-01`, `R-03`, `R-02`, `R-G`, `R-04`, one commit each, no worktrees or branches left.

## The suite

- `go test -race -count=1 ./...`: every package ok.
- `make fmt lint test`: 0 issues, every package ok, including the compatibility suite.
- `go test -race -count=10 -run 'TestParallel|TestResume' ./internal/engine/`: ok.
- `make fuzz FUZZTIME=30s`: the first pass failed in `FuzzParseImport` (second finding below). After the fix, all eleven targets ran 30 s each with no failure (`FuzzParse` config and plan, `FuzzSplitRow`, `FuzzLoadIncludes`, `FuzzSplice`, `FuzzSpliceGenerated`, `FuzzExtend`, `FuzzLoadRun`, `FuzzParseSignal`, `FuzzParseAgentState`, `FuzzParseImport`).
- govulncheck: `Your code is affected by 0 vulnerabilities`. One module finding is not reachable: GO-2026-5024 in `golang.org/x/sys` (v0.36.0, Windows only, not called), as at the V13 and V14 gates.

Every test run went through a `systemd-run --user --scope` fence (MemoryMax 6G, TasksMax 400), one at a time, with `pgrep` before and after. No `.test` process was left.

## Findings

Fixed (with a regression test):

- **A resumed run didn't fill a freed slot until every reattached task had ended.** SPEC §5.1 starts candidates on every iteration while fewer than `max` are active. The engine re-reads the plan with tasks active only for a phase it already knows is parallel. It learns that in `schedule`, which a resumed run never reached while a reattached task was active, because resume reattaches before the first poll. Seen live in p2: `R-01` was integrated at 13:44:00 with `R-02` free to start, but `R-02` started at 13:44:08, after `R-03` ended at 13:44:06. With two long tasks of different lengths, the slot stays empty for the whole difference. V15-03 had reported it as a known gap. **Fixed:** `wantsPlan` also reads the plan for a phase not yet decided when an isolated task (one with a slot or a worktree, which only a parallel phase gives) is active, so the first poll decides the phase. A sequential resume is unchanged. Test: `TestParallelResumeFillsAFreedSlot` (internal/engine): stop with `a` and `b` active, resume, `a` ends, `c` must open while `b` runs. It failed before the fix. Checked live on the fixed build in p3: `F-01` (70 s) and `F-02` (200 s) were stopped and resumed. `F-01` was integrated at 13:49:10, and `F-03` started at 13:49:12 in slot 0 while `F-02` slept until 13:51:18.
- **`igris draft --import` turned a CSV row whose key is only control characters and spaces into an item with an empty key** (found by `FuzzParseImport`, input `"\x00 \x00"`). The key was trimmed *before* `textsafe.Line` removed the control characters, so `" "` passed the empty-key check, and `newItem` trimmed it to `""`. Titles, statuses, labels and header names had the same order. This is in 1.4.0. **Fixed:** one `field` helper cleans, then trims, then clips, and all six sites use it. The row is now refused with `record 2 has no ID`. Tests: the fuzz input kept as a seed (`internal/draft/testdata/fuzz/FuzzParseImport/f09228bee30cf8f5`) and a `TestParseImportErrors` case. Both failed before the fix. In CHANGELOG 1.5.0 under Fixed.

Needs a decision: none.

Observations (no change):

- **Session starts wait for each other while Claude Code asks for folder trust.** `startAll` opens sessions one after another, and the tmux backend's `OpenSession` waits up to 15 s for the first Claude Code hook. A session at the trust question sends none, so in p1 `G1-02` started 15.5 s after `G1-01`. Claude Code keys trust to the repository: after the first answer, no later worktree of the same repo asked. A project the owner already trusts should not see this. Opening sessions concurrently would remove the wait but is a design change, not a defect.
- **The plan's Status cells stay uncommitted in the project root** after a parallel phase (`M tasks.md`), because commits are made in worktrees, whose plan copy is never written. This is by design (§6.11) and the same as worktree mode (§6.8).
- **Claude Code refuses a foreground `sleep 40`** and runs it in the background. igris then showed `waits on background work; Claude Code resumes it when that is done` and treated it correctly.
- **A gate task waits for the owner in its pane**, as the prompt tells it. igris nudged it once, then raised Needs you after `needs_input_after` (30 s) with a notification. The owner answers in the pane (Open session).
- **Right after a resume, the cards show elapsed time from the reattach** (`mode auto · 6s`) and `tokens 0 in · 0 out` until the next usage read, as at the V14 gate.
- **Gate-only pitfalls.** A `tmux new-session` started from the gate's own shell took that shell's `PATH`, so its first `arise` was the owner's older igris (`flag provided but not defined: -parallel`). The pane exported the gate's `PATH` and ran again. The gate's helper for answering folder trust once also matched the igris view itself, whose card tail showed the question, and pressed Down and Enter there. That moved focus to Open session, opened nothing (no tmux client was attached), and changed nothing in the run. The helper was then limited to session windows.
- Answering folder trust records the scratch paths in Claude Code's own config, and the sessions left transcripts under `~/.claude/projects/`, as at earlier gates. The gate didn't edit the owner's Claude configuration to remove them.

## Owner checks left

Only these need the owner before v1.5.0 ships (with V15-R):

1. **Look at the multi-task view on your own terminal.** Run a small phase with `<!-- igris:parallel -->` and `max = 2` in a scratch or real project, and check that the two cards read well at your usual width. Narrow the terminal (or attach from the phone over ssh) to see the switcher, and use `[` and `]` to change task. This gate saw the wide cards only in a 200-column tmux capture, and judging the layout needs your eyes.
2. **Optional, once on herdr.** This gate ran on tmux only, by the unattended-run rules. Parallel doesn't depend on the backend, but one `igris arise <parallel phase>` in a herdr tab confirms two task tabs side by side, and that Open session focuses the right one from each card.
