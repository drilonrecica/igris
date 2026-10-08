# v0.5 gate: observability and notifications

The v0.5 gate (V05-G) covers seven things. A real herdr run must produce a correct `report` and `history`. The TUI's progress, ETA and live tail must work on herdr and on tmux, wide and narrow. Every notification channel is tested with `igris notify test` and with real events. Quiet hours, digests and redirect safety are checked, and the full test suite must be green. The gate ran on Linux (Fedora 44) on 2026-10-08, from the owner's Claude Code session, which drove igris in a herdr tab it created itself (`herdr tab create`, `pane run`, `pane read`, `send-keys`). All projects, plans and answers were synthetic scratch repos (`git init`).

Versions: igris built from `3ec5ace` (`0.4.1-0.20261008131603-3ec5ace9b5cb`, the build that becomes v0.5.0), herdr 0.9.1, Claude Code 2.1.294, tmux 3.7c, Gotify server 3.1.1 (`docker.io/gotify/server` in podman), Python 3 for the local listeners.

Every scratch config maps all `[models]` ranks to `sonnet` and sets `default_mode = "auto"` and `[notify.backend] enabled = false`. The transcripts show Sonnet 5.5. The folder-trust question for the scratch project was answered once beforehand, by starting `claude` in it and choosing "Yes, I trust this folder". `.claude/settings.local.json` puts the HEAD build first on `PATH`, so the sessions' `igris done` was the build under test.

Times in the TUI and feed are local (UTC+2). Times in `runs.jsonl` and `report` are UTC.

Scratch project `p1`:

```toml
backend = "herdr"            # "tmux" for phase C
default_mode = "auto"
needs_input_after = "30s"
[models]                     # every rank → "sonnet"
[run]
  verify = "true"            # profile "default"
  commit = "auto"
[verify]
  twice = "test -f .git/igris-gate-verified || { touch .git/igris-gate-verified; echo first verify fails on purpose; exit 1; }; test -f a.txt"
[notify.backend]
  enabled = false
[notify.ntfy]                # https://ntfy.sh, random topic igris-gate05-<16 hex>
[notify.webhook]
  url = "http://127.0.0.1:18705/hook"
  secret = "env:IGRIS_WEBHOOK_SECRET"
  events = ["needs_input", "task_done", "phase_done", "phase_stuck", "run_error", "verify_failed_limit", "session_lost", "task_overdue"]
```

The local listener (`listen.py` in the scratchpad) logs each request as one JSON line, with its headers and body. It recomputes `sha256=HMAC-SHA256(secret, body)` and compares it with `X-Igris-Signature`. On `/redirect…` it answers `307 Location: /target` and logs any request to `/target`. A second instance served TLS on port 18706 with a self-signed certificate for `127.0.0.1`.

## Results

| # | Check | Result |
|---|---|---|
| 1 | Real 3-task phase on herdr (verify fail → pass, retry, owner wait), `report` (markdown, `--json`), `history` | pass |
| 2 | TUI on herdr: progress, elapsed, ETA `≈`, live tail of real output, narrow layout | pass |
| 3 | Same tail check on tmux (`tmux -L gate05 -f /dev/null` inside a herdr tab) | pass |
| 4 | `notify test` for webhook, Slack, Gotify and ntfy, with a template, plus real events | pass |
| 5 | Quiet hours: held, break-through, digest at window end, `task_done_digest = 2` | pass |
| 6 | Redirect safety (307) | pass |
| 7 | `make fmt lint test`, `go test -race ./...`, `check --strict` on `imp-docs/tasks.md` | pass |

### 1. Real run, report and history

Phase A of `p1`:

- A-01 has Verify `twice`, which fails once and then passes.
- A-02 runs `sleep 40` first. While it slept, `r` (Retry) → "1. Start fresh" was pressed in the TUI.
- A-03 asks the owner which word to write and waits. The answer (`banana`) was typed into its session pane 60 s after Needs you came up.

```
15:20 A-01 verify failed: twice (`test -f .git/igris-gate-verified || …`) failed (exit status …
15:20 A-01 verify passed (twice)
15:20 A-01 committed: A-01: Make a.txt
15:20 A-01 done · Created a.txt containing 'one'; first verify failed on purpose, rerunning
15:20 A-02 started (sonnet → sonnet, auto) · Make b.txt
15:20 A-02 new session (fresh)
15:21 A-02 needs you: the agent is done and has not run `igris done` for 30s
15:22 A-02 is working again
15:22 A-02 done · Ran sleep 40, then created b.txt containing 'two'
15:22 A-03 needs you: the agent is done and has not run `igris done` for 30s
15:23 A-03 is working again
15:24 A-03 done · Asked owner for word, created c.txt containing 'banana'
15:24 phase A complete
15:24 run stopped: completed
```

`igris report`:

```
# igris report · p1

Run 20261008-132013-1821 · 2026-10-08T13:20:13Z → 2026-10-08T13:24:07Z (3m53s) · completed
Phase A
3 done · 0 skipped · 0 unfinished · 3 commits · needs you 1m10s

## Phase A

| Task | Result | Duration | Attempts | Verify | Commit | Needs you |
|---|---|---|---|---|---|---|
| A-01 Make a.txt | done | 18s | 1 | twice ✗ ✓ | 5d088e3 | — |
| A-02 Make b.txt | done | 1m29s | 2 | default ✓ | e2b2bbd | 8s |
| A-03 Ask first | done | 1m54s | 1 | default ✓ | fcd5948 | 1m02s |

Notes
- A-01 done: Created a.txt containing 'one'; first verify failed on purpose, rerunning
- A-02 done: Ran sleep 40, then created b.txt containing 'two'
- A-03 done: Asked owner for word, created c.txt containing 'banana'

Resume
- A-01: `claude --resume 727af1f6-2ff1-46af-8733-9df1957338c8` (run it in the project root)
- A-02: `claude --resume 386601d6-2384-479a-8210-fa1c244f6dbb` (run it in the project root)
- A-03: `claude --resume c4b78eec-66ad-465a-9d8f-45dec3923d27` (run it in the project root)
```

- **Commits.** The three short SHAs match `git log` (`5d088e3e…`, `e2b2bbd8…`, `fcd5948d…`). `--json` has the full SHAs in `commits` and in each task's `commit`.
- **Attempts.** A-02 has 2 attempts, and `--json` lists both sessions (`0742c9d8-…`, `386601d6-…`). Resume names the latest one only.
- **Profiles.** `--json` has `"verify": [{"profile":"twice","passed":false,"duration_s":1}, {"profile":"twice","passed":true,"duration_s":1}]`. The verify commands took 12 ms and 4 ms (`duration_ms`), and a known duration under a second shows as 1s.
- **Needs-you time.** A-03's 1m02s is the owner wait: `needs_you` (reason `idle`) at 13:22:56, the answer typed at 13:23:57, `needs_you_clear` at 13:23:58. A-02's 8s is the only other wait. That is the fresh session, where herdr reported the agent `done` while its `sleep 40` was most likely still running (Claude Code ended the turn while it waited on the command). Needs you came up at 13:21:51 and cleared 9 s later, when the agent worked again. No verify time or agent work was counted. The run total is 1m10s (`needs_you_s` 70 = 8 + 62).
- **Resume UUIDs.** All four session UUIDs exist as `<uuid>.jsonl` file names under `~/.claude/projects/-tmp-claude-1000-…-scratchpad-gate05-p1/`. Only the file names were checked. The fifth file there is the trust-answering session.

The run log has `v:1` lines with `run`, `attempt` (A-02's events after `task_retried` carry attempt 2), `session`, `profile`, `commit`, `duration_ms` and `reason`. `igris history` shows the run ID:

```
Run 2026-10-08T13:20:13Z 20261008-132013-1821  phase A  completed  (3m53s)
  3 done, 0 skipped
  A-01  done  sonnet  sonnet  18s    verify 1 failed, 1 passed
  A-02  done  sonnet  sonnet  1m29s  verify 0 failed, 1 passed
  A-03  done  sonnet  sonnet  1m54s  verify 0 failed, 1 passed
```

**Real events.** The webhook listener got six POSTs during the run: `task_done` ×3, `needs_input` ×2 (A-02, A-03) and `phase_done`. Each had `X-Igris-Event` equal to the body's `event`, a valid `X-Igris-Signature` (`sig_ok=True` on all), `User-Agent: igris`, `"run":"20261008-132013-1821"`, and `urgent` true only for `needs_input`. ntfy (`/json?poll=1&since=all`) got:

```
4 | igris · p1 | phase A · A-02 Make b.txt: needs you (idle 30s without igris done)
4 | igris · p1 | phase A · A-03 Ask first: needs you (idle 30s without igris done)
3 | igris · p1 | phase A: complete
```

### 2. TUI on herdr

Phase B of `p1` ran after phase A. History then had 3 finished sonnet tasks (18s, 1m29s, 1m54s, median 1m29s). Each task runs a 90-second `for` loop. Wide layout (full-width tab, 190 columns), from `herdr pane read`:

```
┌ igris · p1 ──────────── … ─── phase B · 0/2 · 0% ░░░░░░░░░░ · herdr ┐
│ CURRENT                                                                                       │
│ B-01 Count slowly                                                                             │
│ mode auto · 1m47s · over ≈ 1m typical                                                         │
│ state: verifying                                                                              │
│       1 The command printed tail-line-1 through tail-line-90, one line per second.            │
│       2 Output was strictly sequential with no gaps, errors, or repeated lines.               │
│       3 The loop finished after about 90 seconds with tail-line-90 as the last line.          │
│   Running igris done B-01 --note "Ran the 90-second count l…                                  │
│   ⎿  $ igris done B-01 --note "Ran the 90-second count loop and wrote a three-line summary t… │
│ * Sock-hopping… (1m 41s · ↓ 405 tokens)                                                       │
│ run the shell command `for i in $(seq 1 90); do echo tail-line-$i; sleep 1; done` and wait    │
```

The tail is six lines of session output, between the state line and the task text. In phase A it showed, for example, `● Which word would you like me to write into c.txt?` and `✻ Crunched for 9s · done 3:22 PM`.

Narrow layout: the igris pane was split (`herdr pane split --direction right --ratio 0.32`), which left it about 60 columns wide. Every drawn line is at most 59 cells.

```
igris · p1 · B · 1/2 · 50% ███░░░ · herdr
── CURRENT ────────────────────────────────────────────────
B-02 Count again
rank sonnet → model sonnet
mode auto · 26s · ≈ 1m left
state: working
  ⎿  $ for i in $(seq 1 90); do echo again-line-$i; sleep …
     (ctrl+b to run in background)
✽ Manifesting… (21s · ↓ 219 tokens)
run the shell command `for i in $(seq 1 90); do echo
again-line-$i; sleep 1; done` and wait for it to finish,
then create `e.txt` containing `five`
[o] Open session  [t] Details
── TASKS ──────────────────────────────────────────────────
```

The narrow tail shows three lines. The session's own pane (`herdr pane read` of the `B-02 · sonnet` tab) at the same moment ends:

```
● Running 90-second counting loop · 23s
  ⎿  $ for i in $(seq 1 90); do echo again-line-$i; sleep 1; done (17s · 18 lines)
     (ctrl+b to run in background)
✶ Manifesting… (30s · ↓ 219 tokens)
────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────
  Sonnet 5.5 | /tmp/claude-1000/…/gate05/p1 | main | default
  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents
```

So the tail is the output above the input box. The box and the status footer are left out. B-02 showed `≈ 1m left` from its first second. The ETA uses the three phase-A points, loaded once at run start: 1m29s − 1s ≈ 1m. B-01 had passed the median, so it showed `over ≈ 1m typical`. Progress went `0/2 · 0%` → `1/2 · 50%` → done.

### 3. Tail on tmux

A scratch server was started in the herdr tab: `env -u HERDR_* tmux -L gate05 -f /dev/null new-session -s g`. Phase C ran there with `backend = "tmux"`, and the session opened as window `g:1` (`C-01 · sonnet`). Wide (192 columns):

```
───────────────── phase C · 0/1 · 0% ░░░░░░░░░░ · tmux ┐
 │ C-01 Count on tmux                                                                            │
 │ mode auto · 11s · ≈ 2m left                                                                   │
 │ state: working                                                                                │
 │   igris done C-01 --note "<one-line summary of what you did>"                                 │
 │   Running grep -n "C-01" tasks.md; for i in $(seq 1 90); do… · 3s                             │
 │   ⎿  $ grep -n "C-01" tasks.md; for i in $(seq 1 90); do echo tmux-line-$i; sleep 1; done | … │
 │      (ctrl+b ctrl+b (twice) to run in background)                                             │
 │ * Gallivanting… (6s · ↓ 121 tokens)                                                           │
```

For narrow, `tmux split-window -h -l 131` left the igris pane `%0` at 60×59. `capture-pane` of it shows a three-line tail, and every line is at most 60 cells:

```
igris · p1 · C · 0/1 · 0% ░░░░░░ · tmux
── CURRENT ─────────────────────────────────────────────────
C-01 Count on tmux
rank sonnet → model sonnet
mode auto · 26s · ≈ 1m left
state: working
     (ctrl+b ctrl+b (twice) to run in background)
* Gallivanting… (22s · ↓ 121 tokens)
                                                           …
run the shell command `for i in $(seq 1 90); do echo
```

The session pane's own capture has the same lines above its `─` rule, input box and footer. The scratch server was killed afterwards (`tmux -L gate05 kill-server`).

### 4. Channels via `igris notify test`

Project `n1` had ntfy, the webhook, Slack (`webhook_url = "env:IGRIS_SLACK_WEBHOOK"` → `https://127.0.0.1:18706/slack`) and Gotify (`server = "http://127.0.0.1:18780"`, `token = "env:GOTIFY_TOKEN"`). The Gotify app token was created through its REST API with `admin:admin`. Gotify had a template:

```toml
template = "{{.At.Format \"15:04\"}} [{{.Event}}] {{if .TaskID}}{{.TaskID}} {{end}}{{.What}} · run {{.RunID}}"
```

| Run | Result |
|---|---|
| plain `notify test` (self-signed cert not trusted) | ntfy, webhook and Gotify `ok`. Slack `FAILED: slack: TLS failed`. Exit 1 |
| `SSL_CERT_FILE=cert.pem notify test --event needs_input` / `--event phase_done` | all four `ok`, exit 0. Go reads `SSL_CERT_FILE`, so igris trusted the local certificate |
| Slack URL `http://127.0.0.1:18705/slack` | exit 1, `notify.slack.webhook_url is not an https URL with a host (the value is a secret, so it isn't shown); …` |

What arrived:

- **Webhook.** Each POST had `Content-Type: application/json`, `X-Igris-Event` matching `event`, and a valid signature. Every key was present: `v, event, project, phase, task, title, what, run, at, urgent, text`. For example: `{"v":1,"event":"needs_input","project":"n1","phase":"TEST","task":"TEST-1","title":"Notification check","what":"test of needs_input: needs you","run":"","at":"2026-10-08T13:32:34Z","urgent":true,"text":"phase TEST · TEST-1 Notification check: test of needs_input: needs you"}`.
- **Slack** (the TLS listener). `{"text": "*igris · n1* · phase TEST · TEST-1 Notification check: test of needs_input: needs you (needs_input)"}` and `{"text": "*igris · n1* · phase TEST: test of phase_done: complete (phase_done)"}`.
- **Gotify**, read back with `GET /message`. The template was applied, and the priorities were 8 for urgent events, 5 for other events and 3 for `task_done`:
  ```
  prio 8 | igris · n1 | 15:32 [needs_input] TEST-1 test of needs_input: needs you · run
  prio 5 | igris · n1 | 15:32 [verify_failed_limit] TEST-1 test of verify_failed_limit: verification keeps failing; needs you · run
  prio 3 | igris · n1 | 15:32 [task_done] TEST-1 test of task_done: done · run
  prio 5 | igris · n1 | 15:32 [phase_done] test of phase_done: complete · run
  prio 5 | igris · n1 | 15:32 [run_error] test of run_error: the run stopped with an error · run
  ```
  Real events (phases Q and R below) also reached Gotify: `needs_input` at 8, `phase_done` at 5, the grouped `task_done` at 3, and the quiet-hours digest holding only `task_done` at 3.
- **ntfy** (`https://ntfy.sh/<topic>-n/json?poll=1&since=all`): `4 | igris · n1 | phase TEST · TEST-1 Notification check: test of needs_input: needs you` and `3 | igris · n1 | phase TEST: test of phase_done: complete`, twice (once per run).

The real events of check 1 also went to ntfy and the webhook.

### 5. Quiet hours and `task_done_digest`

`p1` got `[notify] quiet = "<now−1m>-<now+5m>"`, `task_done_digest = 2` and the Gotify channel (`needs_input`, `task_done`, `phase_done`). Phase R has four one-line agent tasks, then a user task R-05 that was marked done only after the window ended. The window was `15:40-15:46`. Run log (UTC):

```
13:42:47 R-02 task_done held for webhook (quiet hours)
13:42:47 R-02 task_done held for gotify (quiet hours)
13:43:46 R-04 task_done held for webhook (quiet hours)
13:43:46 R-04 task_done held for gotify (quiet hours)
13:43:46 R-05 needs_input via ntfy
13:43:46 R-05 needs_input via webhook
13:43:46 R-05 needs_input via gotify
13:46:01  digest of 2 via webhook
13:46:01  digest of 2 via gotify
```

- The grouped `task_done` messages (2 tasks each, so 2 groups) were held.
- `needs_input` broke through at once.
- The digest went out at 15:46:01, while igris waited on the owner for R-05. That was one poll after the window ended.

The webhook digest had `X-Igris-Event: digest`, a valid signature, `"event":"digest"`, empty `task`/`title`/`what`, the `run`, `"truncated":0` and two `messages`, each `"what":"2 tasks done: R-0x, R-0y"` with no `text`:

```
quiet hours 15:40–15:46: 2 held
15:42 task_done: phase R: 2 tasks done: R-01, R-02
15:43 task_done: phase R: 2 tasks done: R-03, R-04
```

Gotify got the same text at priority 3. After R-05, `phase_done` went out normally. An earlier attempt (phase Q, window `15:33-15:40`) showed a `needs_input` breaking through inside the window (Q-02 at 13:35:11, see the observations). The run then left the window before any group was complete, and outside it the groups of 2 were sent directly (`2 tasks done: Q-01, Q-02`, `2 tasks done: Q-03, Q-04`). A user-task-only attempt (`q1`) confirmed that user tasks send `needs_input` and no `task_done`, as specified, so it could not test the digest.

### 6. Redirect safety

Project `r1` pointed the webhook at `http://127.0.0.1:18705/redirect/hook` and Gotify at `http://127.0.0.1:18705/redirect`. The listener answers both with `307 Location: /target`.

```
needs_input          webhook  FAILED: webhook: server answered 307 (redirect); set the final URL in igris.toml
needs_input          gotify   FAILED: gotify: server answered 307 (redirect); set the final URL in igris.toml
```

The listener logged four POSTs: `/redirect/hook` and `/redirect/message`, each twice (the one retry). It logged no request to `/target`, so neither the signature nor the Gotify key followed the redirect. The error doesn't show the `Location`.

### 7. Test suite and plan

On HEAD `3ec5ace`, `make fmt lint test` passed (exit 0, tree unchanged), and so did `go test -race -count=1 ./...` (20 packages ok, 2 with no test files). `igris check --strict --plan imp-docs/tasks.md` first warned that V05-G was `blocked` with all its dependencies done. With V05-G set to `in progress` for the gate it gave `OK (23 phases, 160 tasks, 0 warnings)`, exit 0.

## Observations (not blocking)

- **Lost Enter.** In 1 of 14 herdr sessions (Q-02), herdr's `agent prompt` left the task prompt pasted in Claude Code's input box (`[Pasted text #1 +31 lines]`) but not submitted. igris raised Needs you (idle) after `needs_input_after`, as designed, and an Enter in the session pane let it go on. This looks like a herdr 0.9.1 / Claude Code timing issue, not igris's. If it recurs, igris could re-send Enter once when a fresh session stays idle with an unsent prompt.
- **Right-aligned lines in the tail.** Lines Claude Code right-aligns, like the effort indicator (`◐ medium · /effort`) or a tmux hint (`tmux focus`), are clipped to the card width from the left. They then show as a blank row ending in `…`, which uses one of the narrow layout's three lines. Cosmetic.
- **Idle during a long command.** The A-02 session was reported `done` while it most likely still waited on `sleep 40`, so 8 s of Needs you were counted. This comes from the agent state herdr reports, and the time is honest: igris did notify and wait.
- **Local http.** A literal `http://` webhook URL and an `http://` Gotify server are flagged by `check --strict` (`… go unencrypted; use an https:// URL`), as decided. The gate used them on 127.0.0.1 only.

No bugs were found and no fixes were needed. No blockers for v0.5.0.
