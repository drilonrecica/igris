# Run log reference (`runs.jsonl`, schema v1)

Igris keeps a record of every run in `.igris/runs.jsonl` in the project root. `igris history` and `igris report` read it, and so can your own scripts. This page is the reference for the format. [SPEC §13](../SPEC.md#13-state-resume-and-locking) has the summary and is normative.

## The file

- Location: `<project root>/.igris/runs.jsonl`, mode 0600 (in `.igris/`, mode 0700).
- Append-only. Igris never rewrites or truncates it; delete it yourself to start over.
- One JSON object per line (JSON Lines), UTF-8, ending in `\n`. Each line is written with a single `write`, so lines don't interleave.
- After a crash the last line can be cut short. The next line igris appends starts with a newline of its own, so it is never glued to the cut one.
- Readers skip every line they can't read (not JSON, not an event, a field of the wrong type in a v0/v1 line, or longer than 1 MiB) and go on with the rest; `history` and `report` then add the note `N unreadable lines in runs.jsonl skipped`. A cut-off last line without a newline is skipped silently (igris may be writing it).
- Igris's readers open the log only if it is a regular file (a FIFO or device in its place is an error and never blocks) and read only its last 16 MiB, from the first whole line in them; `history` and `report` then add the note `runs.jsonl is over 16 MiB; only its newest runs are read`. This is a limit of igris's readers, not of the format.
- No secrets, file contents, diffs or command output ever go into it. Verify and hook lines say why something failed, never what it printed.

## Fields

Every line igris v0.5 or later writes has `"v":1`. `v`, `at` and `type` are always there. Every other field is left out when it is empty or zero, so a missing number means *unknown*, never 0.

| Field | Type | Content | On |
|---|---|---|---|
| `v` | integer | schema version, `1` | every line |
| `at` | string | RFC 3339 timestamp in UTC (`2026-10-08T09:15:00Z`, fractional seconds possible) | every line |
| `type` | string | the event type (below) | every line |
| `run` | string | the run ID, `YYYYMMDD-HHMMSS-xxxx`: the run's start in UTC plus 4 lowercase hex characters from a random source, e.g. `20261008-091500-3fa2` | every line a run (`igris arise`) writes, `run_started` and `run_stopped` included; absent on lines written outside a run (`igris reset` while no igris is running) |
| `task` | string | task ID | task events; absent on `run_started`, `run_stopped`, run-wide `needs_you`/`needs_you_clear` and most `error`s |
| `rank` | string | the task's rank (the plan's Model cell) | with `task` |
| `model` | string | the model the rank resolved to | with `task`, for agent tasks |
| `attempt` | integer | the agent task's attempt within this run: 1 for the session of `task_started` or the one `task_resumed` reattaches, +1 for each `task_retried`. A task reset and started again in the same run goes on counting (its new `task_started` has the next number, not 1) | every event of an agent task once it has an attempt; user tasks have none |
| `session` | string | the Claude Code session UUID (`claude --resume <uuid>`) | `task_started` and `task_retried` of agent tasks; `task_resumed` when `state.json` recorded one |
| `phase` | string | the task's phase ID | `task_started`, `task_resumed` |
| `title` | string | the task's title, markdown stripped (as notifications show it), cleaned of control characters, at most 80 characters | `task_started`, `task_resumed` |
| `owner` | string | `agent`, `user` or `agent + user` | `task_started`, `task_resumed` |
| `side` | string | the side session's ID, 6 lowercase hex characters (since 1.2). These lines have no `task`, and carry `run` only when the igris writing them has a run | `side_opened`, `side_closed`, `side_usage` |
| `profile` | string | the verify profile | `verify_passed`, `verify_failed` |
| `commit` | string | the full commit SHA (`git rev-parse HEAD` after the commit; left out, with a warning, if that fails); on `integrated` the run branch's new head (since 1.5) | `committed`, `integrated` |
| `branch` | string | the run's worktree branch, `igris/<run-id>`, in worktree mode (since 0.6); since 1.5 the task's branch, `igris/<run-id>-<ID>`, in a parallel phase, and the run branch on `integrated` | `run_started`; `task_started`, `task_retried`, `integrated` |
| `slot` | integer | the task's slot in a parallel phase, `0` to `max - 1` (SPEC §6.11; since 1.5). Absent outside a parallel phase | `task_started`, `task_resumed`, `task_retried` |
| `duration_ms` | integer | milliseconds. `task_done`/`task_skipped`: since this run's `task_started` or `task_resumed` of the task (wall time: watching, verify, questions, everything). `verify_*`: the verify command's run time. `run_stopped`: the run's length. Readers take a negative value or one over 30 days as unknown | those events |
| `reason` | string | why igris waits on you (below); on `integration_failed`, why the task didn't integrate: `conflict` (the rebase) or `verify` (on the integrated tree; since 1.5); on `resolver_opened`, the same: what the resolver session was opened for (since 1.8) | `needs_you`, `needs_you_clear`, `integration_failed`, `resolver_opened` |
| `plan_file` | string | where the plan is kept, `.igris/plans/<ID>-a<attempt>-<n>.md` (left out if it couldn't be saved; since 1.1) | `plan_presented`, `plan_approved`, `plan_rejected` |
| `plan_sha256` | string | the sha256 of the plan's text, in hex; the text itself and any feedback are never logged (since 1.1) | `plan_presented`, `plan_approved`, `plan_rejected` |
| `mode` | string | the mode the session switches to: `default`, `accept` or `auto` (since 1.1) | `plan_approved` |
| `usage` | object | the tokens the attempt's main thread recorded in its transcript: `{"input", "output", "cache_read", "cache_write", "messages"}`, integers, `messages` being the responses counted (since 1.1). Left out when the usage is unavailable | `usage`, `side_usage` |
| `sub` | object | the same for the attempt's subagents; left out when they recorded nothing (since 1.1) | `usage`, `side_usage` |
| `models` | array of strings | the model ids the attempt's main thread ran on, in the order first seen, e.g. `["claude-sonnet-5-5"]`; on `rank_mismatch`, those seen so far (since 1.1) | `usage`, `rank_mismatch`, `side_usage` |
| `sub_models` | array of strings | the model ids its subagents ran on (since 1.1) | `usage`, `side_usage` |
| `audit` | string | the rank audit's result (SPEC §6.9): `ok` (the main thread ran only on models its rank resolves to), `mismatch` (on another one), `unchecked` (the resolved `[models]` value is one igris can't check, such as `default` or `opusplan`) or `unavailable` (igris could not read the attempt's transcript) (since 1.1). A reader shows any other value as absent | `usage` |
| `resolver` | boolean | `true` on the lines of a resolver session (SPEC §6.11; since 1.8): its `attempt` is its task's, its `session` its own, so a reader adds its tokens to the task and counts no attempt for it. Absent (false) on every other line | `usage`, `rank_mismatch` |
| `detail` | string | free text, see each type | most events |

## Event types

| Type | Written when | `detail` |
|---|---|---|
| `run_started` | `igris arise` starts a run, after its checks | the scope: `phase A, B`, plus the selection of a sliced run, e.g. `phase M1; only M1-03, M1-05` |
| `run_stopped` | the run ends | `completed`, `stuck`, `stopped` or `error` |
| `task_started` | a task is marked in progress | `mode <mode>` for agent tasks, `user task` for user tasks |
| `task_resumed` | the run picks up a task an earlier run left in progress | empty, or `no record of an earlier session` when the plan says in progress but `state.json` has nothing |
| `task_retried` | the owner replaced the task's session (`r` in the TUI, `retry` on stdin) | `fresh` or `continue` |
| `task_done` | the task is accepted as done | the done note |
| `task_skipped` | the task is skipped | the reason |
| `task_overdue` | the attempt runs past the task's Timeout | `running longer than its Timeout <cell>` |
| `task_reset` | `igris reset` put the task back to ready/blocked | the status it had |
| `verify_passed` | the verify command passed | `profile <name>` |
| `verify_failed` | the verify command failed | `profile <name>: attempt N of M: <why>` (`exit status 2`, `timed out after 10m0s`) |
| `committed` | igris committed the task's changes | the commit subject |
| `needs_you` | igris starts waiting on you (marks Needs you, loses a session, holds the run) | the few words the notification says, e.g. `needs you (idle 5m0s without igris done)` |
| `needs_you_clear` | that wait ended while the task goes on: the agent works again, the session ran `igris done`, you answered, a retry | empty |
| `notification` | a notification was delivered, held for quiet hours, or sent as a digest | `<event> via <channel>` (`task_done via ntfy`); `<event> held for <channel> (quiet hours)`; `digest of N via <channel>` (no `task`). Failed deliveries are not logged here |
| `config_changed` | `igris.toml` was edited during the run; the run keeps the configuration it started with and the next `igris arise` asks before using the file (since 1.0) | `igris.toml changed during the run; igris keeps the configuration it started with` (no `task`) |
| `plan_presented` | a plan-mode session presented a plan for approval in igris (SPEC §6.3; since 1.1) | empty |
| `plan_approved` | you approved it in igris | empty |
| `plan_rejected` | you rejected it with feedback in igris; the session revises it and presents it again | empty |
| `usage` | an attempt of an agent task ended: after its `task_done` or `task_skipped`, before its `task_retried`, when its session is lost, or before `run_stopped` while it runs. Once per attempt that had a session; not with `[usage] enabled = false` (SPEC §6.9; since 1.1) | empty, or why the usage is `unavailable`: `no transcript reported by the session's hooks` or `the transcript could not be read` |
| `rank_mismatch` | the attempt's main thread ran on a model its rank doesn't resolve to; at most once per attempt, when the read that first sees it happens, so possibly right before the attempt's `usage` line. The session is left running. Not with `[usage] enabled = false` (SPEC §6.9; since 1.1) | `on <model id>, not its rank <rank>` (`<rank> → <model>` when they differ) |
| `side_opened` | a side session was opened (SPEC §6.10; since 1.2). No `task`; `side`, `rank` and `model` name it | empty |
| `side_closed` | a side session was closed or found gone; once, by the igris that claimed its record (since 1.2) | empty |
| `side_usage` | the ended side session's usage, read from its transcript as an attempt's is (SPEC §6.9), with `side`, `rank`, `model`, `usage`, `sub`, `models` and `sub_models`; never `attempt` or `audit`. Never an attempt, never audited. Not with `[usage] enabled = false` (since 1.2) | empty, or why the usage is unavailable (as for `usage`) |
| `integrated` | a task of a parallel phase is on the run branch: its commit was rebased onto it, verified there (when the rebase moved it) and fast-forwarded; written right before the task's `task_done` (SPEC §6.11; since 1.5). `commit` is the run branch's new head, `branch` the run branch | the commit subject |
| `integration_failed` | a task of a parallel phase didn't integrate: its rebase onto the run branch conflicted (`reason` `conflict`; igris aborted the rebase) or verify failed on the integrated tree (`reason` `verify`). What follows is in the detail: the task is run again from the start (`task_retried` `fresh`, once per task and run with `[parallel] on_conflict = "rerun"`), or it needs you (since 1.5) | the conflicting files, at most 20, comma-separated, or the verify profile and why (`profile fast: exit status 1`), or, since 1.7, why the fast-forward failed (`the run branch moved since the rebase; fast-forward failed`, `the fast-forward would overwrite uncommitted files in the project root: new.txt`, or `fast-forward failed: ` and git's first line of error; `reason` stays `conflict`), then `; then rerun`, `; then needs you` or, since 1.8, `; then resolve` (a resolver session follows, `resolver_opened`) |
| `resolver_opened` | a resolver session was opened for a task that didn't integrate (SPEC §6.11; since 1.8): with `[parallel] on_conflict = "resolve"`, or from the didn't-integrate question's **Resolve**. `reason` says what for: `conflict` (the rebase is left where it stopped) or `verify` (the integrated tree). `rank`, `model` and `session` are the resolver's (`[parallel] resolver_rank`, or the task's rank), `attempt` the task's: it is no attempt. Once per session opened for the resolver (a retry of its session logs it again) | the conflicting files, at most 20, comma-separated, or the verify profile |
| `resolver_done` | the resolver ran `igris done` and igris finished the rebase (or folded its fix into the task's commit) and found the branch in shape; the integrated tree is verified next (since 1.8) | the resolver's note |
| `resolver_failed` | the resolution failed: the resolver gave up (`igris skip`), left markers or a branch in another shape, or its resolution failed verify on the integrated tree. What follows is in the detail: the task is run again from the start (`task_retried` `fresh`, unless `on_conflict = "ask"` or the rerun was used), or it needs you (since 1.8) | why (`gave up: <reason>`, `markers left in a.go`, `the rebase stopped again`, `verify failed on the integrated tree (profile fast: exit status 2)`, `the owner skipped the task`), then `; then rerun`, `; then needs you` or `; then skipped` |
| `overlap` | two active tasks of a parallel phase both change one file; once per pair of tasks and file, from `git status` in their worktrees at each poll. Nothing is held. `task` is the task that changed it last (since 1.5) | `<ID> and <ID> both change <path>` |
| `error` | a task hook failed, or the run stopped with an error | the hook's reason (`before_task hook failed: exit status 1`) or the error |

### Needs-you reasons

| `reason` | igris waits because |
|---|---|
| `idle` | the agent settled for `needs_input_after` without `igris done` |
| `blocked` | the agent waits for a permission or an answer |
| `waiting` | the agent waited on background work (a background task or a scheduled wakeup) longer than `waiting_limit`; new in WAIT-01, older readers see `other` |
| `skip_request` | the session asks to skip its task; you confirm or decline |
| `session_lost` | the session is gone without `igris done` |
| `task_overdue` | the attempt runs past the task's Timeout |
| `verify_limit` | verify failed `verify_max_attempts` times in a row |
| `verify_not_sent` | a verify failure could not be sent to the session |
| `hook_failed` | the `before_task` hook failed and no session was opened |
| `commit` | the commit question under `commit = "ask"` |
| `reset_request` | a reset request waits for your confirmation (`task` is the task to reset, which may not be the current one) |
| `plan_changed` | the plan changed outside igris and the run holds (SPEC §5.4); run-wide, no `task` |
| `plan_approval` | a plan waits for your approval in igris (SPEC §6.3; since 1.1) |
| `rank_mismatch` | the session runs on a model its rank doesn't resolve to; igris leaves it running (SPEC §6.9; since 1.1) |
| `integration` | the task didn't integrate (a rebase conflict, verify failing on the integrated tree, or a failed fast-forward) and igris won't run it again by itself; its worktree is kept and you rerun it fresh, mark it done, skip it or stop, or (since 1.8) open a resolver session while none ran for the task in this run; with files in the way of the fast-forward, you move them and try again instead of a rerun (SPEC §6.11; since 1.5) |

A reader treats any other reason as `other`. The config-changed notice is not a wait (the run goes on with its snapshot) and is not logged as `needs_you`.

Needs-you time is the time igris waited on you. So:

- `idle` and `blocked` end when the agent works again or the session runs `igris done` (also when its verify then fails: a new idle stretch is a new wait).
- `task_overdue`, `rank_mismatch`, `verify_limit` and `verify_not_sent` end when the agent is seen working again: an overdue task, or one on another model, that keeps working waits on nobody. A mismatch first seen by the read at the attempt's end is logged and notified, but is no wait.
- `commit` in a parallel phase stays open while the commit question waits and after you decline it, until you answer again, retry, skip or stop (SPEC §6.11); `integration` ends when you rerun the task, mark it done or skip it.
- `plan_approval` ends when you approve or reject the plan, or when it is answered in the session's pane (then the session's own `blocked` wait may follow, as when you pick *Answer in the pane*).
- `plan_changed` starts when the run holds: the change is noticed between tasks, after the current task ended. It ends when you resume.
- A wait that is already open is not logged again: a second skip request from the session, the verify limit hit again, another plan edit while the run holds.

A wait starts at `needs_you` and ends at the first later event of the same run that is a `needs_you_clear` with the same task and reason, or for that task a `task_retried`, `task_done`, `task_skipped` or `task_reset`, or the run's `run_stopped` (its last line when it has none). A `needs_you_clear` ends every open wait of its task and reason, so logs where igris v0.5.0 before this rule logged one wait twice still add up.

## Runs

A run is the lines with one `run` ID. Lines without `run` (v0 lines) form runs the v0 way: from a `run_started` to its `run_stopped`, or to the next `run_started` when it has none (igris died or was killed: `interrupted`). Lines without `run` outside such a run belong to no run.

## Compatibility

- A line without `v` is **v0**: igris v0.2–v0.4 wrote `at`, `type`, `task`, `rank`, `model` and `detail` only (shape below). Readers accept v0 and v1 lines mixed in one file.
- Readers ignore unknown types and unknown fields.
- A line with a larger `v` is read best effort: the fields known here, a field whose type changed left out, the line kept; `history` and `report` then add the note `runs.jsonl has lines from a newer igris (vN); some details may be missing`.
- Within v1, fields and types are only ever added. None is removed or renamed, and none changes its meaning. Anything else is a new version.
- `run` and `session` are shape-checked when read (`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`, and a lowercase UUID). A value that fails counts as absent: a session UUID ends up in a `claude --resume` command.

## Examples

One line per type, as igris writes them:

```json
{"v":1,"at":"2026-10-08T09:15:00Z","type":"run_started","run":"20261008-091500-3fa2","detail":"phase M1; only M1-03, M1-05"}
{"v":1,"at":"2026-10-08T09:15:01Z","type":"task_started","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":1,"session":"0f6c2a3e-5b1d-4c7e-9a8f-1d2e3f4a5b6c","phase":"M1","title":"Config loader","owner":"agent","detail":"mode auto"}
{"v":1,"at":"2026-10-08T09:25:00Z","type":"needs_you","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":1,"reason":"idle","detail":"needs you (idle 5m0s without igris done)"}
{"v":1,"at":"2026-10-08T09:27:10Z","type":"needs_you_clear","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":1,"reason":"idle"}
{"v":1,"at":"2026-10-08T09:27:20Z","type":"plan_presented","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":1,"plan_file":".igris/plans/M1-03-a1-1.md","plan_sha256":"9f2c1d0b8e7a6f5e4d3c2b1a09f8e7d6c5b4a3928170f6e5d4c3b2a1908f7e6d"}
{"v":1,"at":"2026-10-08T09:27:40Z","type":"plan_approved","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":1,"plan_file":".igris/plans/M1-03-a1-1.md","plan_sha256":"9f2c1d0b8e7a6f5e4d3c2b1a09f8e7d6c5b4a3928170f6e5d4c3b2a1908f7e6d","mode":"auto"}
{"v":1,"at":"2026-10-08T09:28:00Z","type":"verify_failed","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":1,"profile":"fast","duration_ms":12450,"detail":"profile fast: attempt 1 of 3: exit status 2"}
{"v":1,"at":"2026-10-08T09:28:30Z","type":"task_retried","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"session":"7d1e9b20-3c4a-4f5e-8b6d-0a1b2c3d4e5f","detail":"fresh"}
{"v":1,"at":"2026-10-08T09:29:00Z","type":"task_overdue","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"detail":"running longer than its Timeout 10m"}
{"v":1,"at":"2026-10-08T09:29:00Z","type":"notification","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"detail":"task_overdue via ntfy"}
{"v":1,"at":"2026-10-08T09:29:01Z","type":"verify_passed","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"profile":"fast","duration_ms":11020,"detail":"profile fast"}
{"v":1,"at":"2026-10-08T09:29:03Z","type":"committed","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"commit":"3fa29c1e0b6d4a8f9c2e7b1d5a0f6e3c8b9d2a4f","detail":"M1-03: Config loader"}
{"v":1,"at":"2026-10-08T09:29:03Z","type":"task_done","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"duration_ms":842000,"detail":"tests added"}
{"v":1,"at":"2026-10-08T09:29:04Z","type":"usage","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"session":"7d1e9b20-3c4a-4f5e-8b6d-0a1b2c3d4e5f","usage":{"input":412,"output":41210,"cache_read":2104577,"cache_write":187340,"messages":96},"sub":{"input":20,"output":3100,"cache_read":0,"cache_write":8000,"messages":4},"models":["claude-sonnet-5-5"],"sub_models":["claude-haiku-5-5"],"audit":"ok"}
{"v":1,"at":"2026-10-08T09:30:00Z","type":"task_resumed","run":"20261008-093000-c01d","task":"M1-05","rank":"opus","model":"opus","attempt":1,"session":"9aa07c5e-d5cc-43b5-812b-1a20c5336f67","phase":"M1","title":"Docs","owner":"agent"}
{"v":1,"at":"2026-10-08T09:33:40Z","type":"task_skipped","run":"20261008-093000-c01d","task":"M1-05","rank":"opus","model":"opus","attempt":1,"duration_ms":220000,"detail":"covered by M1-03"}
{"v":1,"at":"2026-10-08T09:33:41Z","type":"usage","run":"20261008-093000-c01d","task":"M1-05","rank":"opus","model":"opus","attempt":1,"session":"9aa07c5e-d5cc-43b5-812b-1a20c5336f67","audit":"unavailable","detail":"the transcript could not be read"}
{"v":1,"at":"2026-10-08T09:34:00Z","type":"error","run":"20261008-093000-c01d","task":"M1-06","rank":"sonnet","model":"sonnet","attempt":1,"detail":"before_task hook failed: exit status 1"}
{"v":1,"at":"2026-10-08T09:40:00Z","type":"run_stopped","run":"20261008-093000-c01d","duration_ms":600000,"detail":"stopped"}
{"v":1,"at":"2026-10-08T18:00:00Z","type":"task_reset","task":"M1-03","rank":"sonnet","detail":"done"}
```

## v0 (igris v0.2–v0.4)

For reference, the shape before v1: no `v`, no `run`, and only these fields.

```json
{"at":"2026-10-01T09:00:00Z","type":"run_started","detail":"phase A"}
{"at":"2026-10-01T09:00:01Z","type":"task_started","task":"A-1","rank":"sonnet","model":"sonnet"}
{"at":"2026-10-01T09:04:05Z","type":"task_done","task":"A-1","rank":"sonnet","model":"sonnet","detail":"added the thing"}
{"at":"2026-10-01T09:05:11Z","type":"run_stopped","detail":"completed"}
```
