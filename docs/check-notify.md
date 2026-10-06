# Real-channel check: ntfy and Discord (M5-06)

`make test` checks notification messages against a fake HTTP server. This check receives every event type for real, on the phone through ntfy and in Discord, and checks wording, priority and that nothing secret or private leaks (SPEC §10).

## Setup

In a scratch project (e.g. the smoke project from `docs/smoke-herdr.md`), add to `igris.toml`:

```toml
[notify.backend]
enabled = true

[notify.ntfy]
server = "https://ntfy.sh"          # or your own server
topic = "igris-check-<random>"      # subscribe to it in the ntfy app
token = ""                          # or "env:NTFY_TOKEN"
events = ["needs_input", "session_lost", "verify_failed_limit", "task_done", "phase_done", "phase_stuck", "run_error"]

[notify.discord]
webhook_url = "env:IGRIS_DISCORD_WEBHOOK"
events = ["needs_input", "session_lost", "verify_failed_limit", "task_done", "phase_done", "phase_stuck", "run_error"]
```

`task_done` isn't on by default; it's listed here so every event gets checked. Export the webhook: `export IGRIS_DISCORD_WEBHOOK=…` (don't put it in the file).

## 1. Samples

```sh
igris notify test
```

- [ ] It prints `ok` for each of the 7 events on ntfy and on Discord (and the herdr toast, if reachable), and the output never shows the webhook URL or token
- [ ] The phone shows 7 ntfy notifications; `needs_input` and `session_lost` arrive with high priority (they make a sound / show as urgent)
- [ ] Discord shows 7 short messages, no embeds
- [ ] Each message names the project, phase, task and event in words you'd understand on a lock screen
- [ ] `igris notify test --event phase_done` sends only that one

## 2. Real events

Run the smoke plan (`igris arise S0`, TUI or `--no-tui`). Trigger each event for real:

| Event | How to trigger it | Got it on ntfy | Got it on Discord |
|---|---|---|---|
| `task_done` | S0-01 finishes | [ ] | [ ] |
| `needs_input` | leave S0-03 idle past `needs_input_after` (20 s in the smoke config) | [ ] | [ ] |
| `session_lost` | close S0-03's Claude tab by hand | [ ] | [ ] |
| `verify_failed_limit` | set `verify = "false"` and `verify_max_attempts = 1`, reset S0-01 to `ready`, run again | [ ] | [ ] |
| `phase_done` | let the phase finish | [ ] | [ ] |
| `phase_stuck` | add a task S0-04 that depends on a `ready` user task in another phase, then `igris arise S0` | [ ] | [ ] |
| `run_error` | only from `notify test` above (a real one needs a broken environment) | — | — |

For every received message:

- [ ] No file contents, diffs, verify output, notes text or paths beyond the project name
- [ ] The wording matches what happened (e.g. "needs you" really means a session is waiting)
- [ ] Nothing arrives twice for the same event

## 3. Failure handling

- [ ] Set an unreachable ntfy server (`server = "https://ntfy.invalid"`) and run `igris notify test`: it prints `FAILED: …` for ntfy (never the token), exits 1, and Discord still says `ok`
- [ ] During a run with the broken server, igris shows the delivery failure in the TUI/log and keeps running
- [ ] `.igris/runs.jsonl` records the notifications without the webhook URL or token: `grep -c discord.com .igris/runs.jsonl` prints 0

## Report

One line per finding: event, channel, what you saw, what you expected. Screenshots of the phone notifications help (crop out the topic).

## Result

Date `____`, ntfy server `____`, result **pass / findings**. When there are no open findings, mark M5-06 and M5-G `done` in `imp-docs/tasks.md` and V011-03 `done` in `imp-docs/ROADMAP.md`.
