# Run log reference (`runs.jsonl`, schema v1)

Igris keeps a record of every run in `.igris/runs.jsonl` in the project root. `igris history` and `igris report` read it, and so can your own scripts. This page is the reference for the format. [SPEC §13](../SPEC.md#13-state-resume-and-locking) has the summary and is normative.

## The file

- Location: `<project root>/.igris/runs.jsonl`, mode 0600 (in `.igris/`, mode 0700).
- Append-only. Igris never rewrites or truncates it; delete it yourself to start over.
- One JSON object per line (JSON Lines), UTF-8, ending in `\n`. Each line is written with a single `write`, so lines don't interleave.
- After a crash the last line can be cut short. Readers skip a malformed last line.
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
| `attempt` | integer | the agent task's attempt within this run: 1 for the session of `task_started` or the one `task_resumed` reattaches, +1 for each `task_retried` | every event of an agent task once it has an attempt; user tasks have none |
| `session` | string | the Claude Code session UUID (`claude --resume <uuid>`) | `task_started` and `task_retried` of agent tasks; `task_resumed` when `state.json` recorded one |
| `phase` | string | the task's phase ID | `task_started`, `task_resumed` |
| `title` | string | the task's title, markdown stripped (as notifications show it), cleaned of control characters, at most 80 characters | `task_started`, `task_resumed` |
| `owner` | string | `agent`, `user` or `agent + user` | `task_started`, `task_resumed` |
| `profile` | string | the verify profile | `verify_passed`, `verify_failed` |
| `commit` | string | the full commit SHA (`git rev-parse HEAD` after the commit; left out, with a warning, if that fails) | `committed` |
| `duration_ms` | integer | milliseconds. `task_done`/`task_skipped`: since this run's `task_started` or `task_resumed` of the task (wall time: watching, verify, questions, everything). `verify_*`: the verify command's run time. `run_stopped`: the run's length | those events |
| `reason` | string | why igris waits on you (below) | `needs_you`, `needs_you_clear` |
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
| `needs_you` | igris marks Needs you or loses a session | the few words the notification says, e.g. `needs you (idle 5m0s without igris done)` |
| `needs_you_clear` | that wait ended while the task goes on: the agent works again, you answered, a retry | empty |
| `notification` | a notification was delivered, held for quiet hours, or sent as a digest | `<event> via <channel>` (`task_done via ntfy`); `<event> held for <channel> (quiet hours)`; `digest of N via <channel>` (no `task`). Failed deliveries are not logged here |
| `error` | a task hook failed, or the run stopped with an error | the hook's reason (`before_task hook failed: exit status 1`) or the error |

### Needs-you reasons

| `reason` | igris waits because |
|---|---|
| `idle` | the agent settled for `needs_input_after` without `igris done` |
| `blocked` | the agent waits for a permission or an answer |
| `skip_request` | the session asks to skip its task; you confirm or decline |
| `session_lost` | the session is gone without `igris done` |
| `task_overdue` | the attempt runs past the task's Timeout |
| `verify_limit` | verify failed `verify_max_attempts` times in a row |
| `verify_not_sent` | a verify failure could not be sent to the session |
| `hook_failed` | the `before_task` hook failed and no session was opened |
| `commit` | the commit question under `commit = "ask"` |
| `reset_request` | a reset request waits for your confirmation (`task` is the task to reset, which may not be the current one) |
| `plan_changed` | the plan changed outside igris and the run holds (SPEC §5.4); run-wide, no `task` |

A reader treats any other reason as `other`. The config-changed notice is not a wait (the run goes on with its snapshot) and is not logged as `needs_you`.

A wait starts at `needs_you` and ends at the first later event of the same run that is a `needs_you_clear` with the same task and reason, or for that task a `task_retried`, `task_done`, `task_skipped` or `task_reset`, or the run's `run_stopped` (its last line when it has none).

## Runs

A run is the lines with one `run` ID. Lines without `run` (v0 lines) form runs the v0 way: from a `run_started` to its `run_stopped`, or to the next `run_started` when it has none (igris died or was killed: `interrupted`). Lines without `run` outside such a run belong to no run.

## Compatibility

- A line without `v` is **v0**: igris v0.2–v0.4 wrote `at`, `type`, `task`, `rank`, `model` and `detail` only (shape below). Readers accept v0 and v1 lines mixed in one file.
- Readers ignore unknown types and unknown fields.
- A line with a larger `v` is read best effort (the fields known here); `history` and `report` then add the note `runs.jsonl has lines from a newer igris (vN); some details may be missing`.
- Within v1, fields and types are only ever added. None is removed or renamed, and none changes its meaning. Anything else is a new version.
- `run` and `session` are shape-checked when read (`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`, and a lowercase UUID). A value that fails counts as absent: a session UUID ends up in a `claude --resume` command.

## Examples

One line per type, as igris writes them:

```json
{"v":1,"at":"2026-10-08T09:15:00Z","type":"run_started","run":"20261008-091500-3fa2","detail":"phase M1; only M1-03, M1-05"}
{"v":1,"at":"2026-10-08T09:15:01Z","type":"task_started","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":1,"session":"0f6c2a3e-5b1d-4c7e-9a8f-1d2e3f4a5b6c","phase":"M1","title":"Config loader","owner":"agent","detail":"mode auto"}
{"v":1,"at":"2026-10-08T09:25:00Z","type":"needs_you","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":1,"reason":"idle","detail":"needs you (idle 5m0s without igris done)"}
{"v":1,"at":"2026-10-08T09:27:10Z","type":"needs_you_clear","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":1,"reason":"idle"}
{"v":1,"at":"2026-10-08T09:28:00Z","type":"verify_failed","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":1,"profile":"fast","duration_ms":12450,"detail":"profile fast: attempt 1 of 3: exit status 2"}
{"v":1,"at":"2026-10-08T09:28:30Z","type":"task_retried","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"session":"7d1e9b20-3c4a-4f5e-8b6d-0a1b2c3d4e5f","detail":"fresh"}
{"v":1,"at":"2026-10-08T09:29:00Z","type":"task_overdue","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"detail":"running longer than its Timeout 10m"}
{"v":1,"at":"2026-10-08T09:29:00Z","type":"notification","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"detail":"task_overdue via ntfy"}
{"v":1,"at":"2026-10-08T09:29:01Z","type":"verify_passed","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"profile":"fast","duration_ms":11020,"detail":"profile fast"}
{"v":1,"at":"2026-10-08T09:29:03Z","type":"committed","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"commit":"3fa29c1e0b6d4a8f9c2e7b1d5a0f6e3c8b9d2a4f","detail":"M1-03: Config loader"}
{"v":1,"at":"2026-10-08T09:29:03Z","type":"task_done","run":"20261008-091500-3fa2","task":"M1-03","rank":"sonnet","model":"sonnet","attempt":2,"duration_ms":842000,"detail":"tests added"}
{"v":1,"at":"2026-10-08T09:30:00Z","type":"task_resumed","run":"20261008-093000-c01d","task":"M1-05","rank":"opus","model":"opus","attempt":1,"session":"9aa07c5e-d5cc-43b5-812b-1a20c5336f67","phase":"M1","title":"Docs","owner":"agent"}
{"v":1,"at":"2026-10-08T09:33:40Z","type":"task_skipped","run":"20261008-093000-c01d","task":"M1-05","rank":"opus","model":"opus","attempt":1,"duration_ms":220000,"detail":"covered by M1-03"}
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
