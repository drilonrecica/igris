# Usage and plan approval

Two things igris added in 1.1: approving a plan-mode session's plan without leaving igris, and seeing the tokens each run used and which model each task ran on.

## Approving plans in igris

A task in `plan` mode (or `plan:accept`, `plan:auto`, `plan:default`, `plan:yolo`) makes Claude plan before it edits anything. When the session presents its plan, igris shows it in a dialog and marks the task **needs you**. You choose:

- **Approve.** The session continues in the mode the task's Mode names: `plan:accept` continues in `accept`, and so on. For plain `plan` it is `[plan_mode] then`, `auto` unless you set `default` or `accept`.
- **Reject with feedback.** Claude revises the plan and presents it again.
- **Answer in the pane.** Opens the session, where Claude Code's own plan dialog is still waiting. Whichever dialog you answer first wins.

If you don't answer within `[plan_mode] approve_timeout` (30 minutes by default; `"0"` approves in the pane only), or igris isn't running, the plan waits in the pane as it always did. Nothing is lost. The dialog is only in the TUI, not with `--no-tui`.

igris keeps every plan presented under `.igris/plans/`, logs it (the file and a hash, never the text) and adds the approved plan to the task's commit message. `plan:yolo` skips permissions after approval, so it needs the same typed confirmation as `yolo`, every run; see [modes](modes.md#skip-permissions).

## Token usage

While a task runs, igris reads the token counts and model ids from the session's transcript (and its subagents' files), about every 10 seconds and once more when the attempt ends. It never reads the messages. The current task's card shows the counter, `tokens 2.3M in · 41k out · claude-sonnet-5-5`; `in` is input plus cache read and cache write. Each attempt's totals go into the run log as a `usage` line.

These are tokens, not money: igris never prices them. `[usage] enabled = false` turns the reading off.

```sh
igris usage                # the last run
igris usage 20261001-0900  # a run, as for igris report
igris usage --since 7d     # every run in the last week (7d, 36h or 2026-10-01)
igris usage --json         # the same, for scripts
```

The report has totals, a table by task, by model and by rank, the top five tasks by tokens, the rank audit and the subscription quota. It reads the run log, so it works after the run is over. It exits 1 when there is no run to report.

## The rank audit

Every task names a model rank, and igris starts the session with the model that rank resolves to. From the same transcript reads, igris checks that the session's main thread runs on it. If Claude answers on another model (you typed `/model` in the pane, a setting slipped through), the task is marked **needs you** with `RANK MISMATCH` on its card, you get a `rank_mismatch` notification and the run log gets a line, once per attempt. igris doesn't stop or change the session: you decide. Subagents may use other models and aren't checked.

A `[models]` value igris can't check (`default`, `opusplan`, a custom name) is logged as `unchecked`. Each `usage` line carries `ok`, `mismatch`, `unchecked` or `unavailable`; `igris usage` and `igris report` show the result.

## Subscription quota

Claude Code gives your 5-hour and 7-day limits only to a status line. So the quota is opt-in:

```toml
[usage]
statusline = true
```

igris's own status line then runs in task panes (it replaces yours there, which is why it is off by default), shows `ID · model · ctx 5% · 5h 4% · 7d 52%` and records the two limits in `.igris/quota.json`. The TUI header shows `5h 4% · 7d 52%`, amber from 80% and red from 95%, and `igris usage` prints the last quota with its time. With an API-key login, or before Claude Code's first response, it says `unavailable`. igris never reads your credentials or calls an API for it.

## What a session could fake

A session can write `.igris/` like everything else in the project, so it could forge its transcript, `quota.json` or an approval of its own plan. It can never choose the mode a plan switches to. igris reads each file bounded and shape-checked. See the [threat model](../../docs/threat-model.md).

## Planning for parallel work

`igris phases` and `igris arise --dry-run` also show, per phase, the **critical path** (the longest chain of dependent tasks), the **width** (the most tasks that depend on none of each other) and the sets of tasks that could run together. It is read-only. igris still runs one task at a time; this shows how much a plan would allow.
