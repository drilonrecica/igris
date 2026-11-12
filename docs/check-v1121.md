# v1.12.1 gate

The v1.12.1 gate (V1121-G) runs the repro of V1121-01 (decisions.md V1121-P1) again with the fixed igris. It covers:

- a task in plan mode answered in the session's pane in each of the three ways: approve, typed feedback (*Tell Claude what to change*) and Esc (Claude Code 2.1.322 has no *No, keep planning* item; Esc is it). For each: when the TUI's plan view closed, what the web UI's sheet (paired with `igris serve token`) and `igris status` said, and which `igris hook` processes were left, by exact PID;
- an approve and a reject with feedback from igris, and the mode the session was in after each;
- `make fmt lint test`, the race detector and govulncheck.

It ran unattended on Linux (Fedora 44) on 2026-11-12, 01:09–01:17, from the V1121-G task session. The build was this tree (`master` at `f09cd7c`, V1121-01's fix), built with `-X main.version=1.12.1` into a scratch `GOBIN` first in `PATH`. The project was a synthetic scratch git repo under a `mktemp -d` directory: one sequential phase P with three sonnet tasks, each with Mode `plan:auto`, each told to present a one-step plan (write `lemon.txt`, `lime.txt` or `kiwi.txt` with one line), then write the file and run `igris done`. `verify = "true"`, `commit = "auto"`, the backend's toast off, `[serve] http = "127.0.0.1:18121"`, and every `[models]` key mapped to sonnet. The run mode was `auto` (`igris arise P --mode auto`, in the TUI at 160×45); no session ran in `yolo` or `plan:yolo`.

**Where it ran.** A private tmux server, `tmux -L gate -f /dev/null`, started from `env -i` inside a `systemd-run --user --scope` (MemoryMax 6G, TasksMax 400), with only `HOME`, `USER`, `LANG`, `TERM`, `COLORTERM`, `SHELL=/bin/bash`, the gate's `PATH` (scratch `GOBIN`, then Claude Code's directory and the system's, not `~/go/bin`), and `XDG_RUNTIME_DIR`, `XDG_STATE_HOME` and `TMUX_TMPDIR` set to directories of the scratch tree. No `HERDR_*` or `CLAUDE*` variable reached it. The first window said `igris is /tmp/igris-gate-v1121.…/bin/igris` and `igris 1.12.1`; `igris status` and `igris serve token` ran in a second window of the same environment. P-01's session asked Claude Code's folder-trust question once (Down, Enter in its pane). The browser was the Playwright MCP's Chromium, paired through the URL `igris serve token` printed; a `MutationObserver` on the page logged, with a timestamp, each change of which task's plan the sheet showed and of the run's status word, so the sheet's timing below is the page's own, not a poll.

A sampler read every 250 ms: whether the TUI showed a plan view (`P-0n presents a plan`, the dialog's title), the PIDs of `<scratch>/bin/igris hook --root <scratch>/p plan-approve` (exact command line), the files in `.igris/plan-approval/`, and the session's hook state record (`.igris/agent-state/<uuid>.json`). Times below are local (CET); the records' `at` (UTC) are converted.

**Versions:** Claude Code 2.1.322, tmux 3.8, Go 1.27.4, govulncheck 1.8.0.

## Answered in the pane

Before each answer the plan view was open in the TUI, the sheet showed the plan (`P-0n presents a plan`, Approve: the session switches to auto, Reject with feedback), `igris status` read `Run ! Needs you · P-0n · plan`, and the one `plan-approve` hook was a child of the session's `claude` with no signal pending (`SigPnd`/`ShdPnd` 0 in `/proc/<pid>/status`).

| Answer | Hook (parent) | What followed | Plan view (TUI, sheet) | `igris status` after | Hook left |
|---|---|---|---|---|---|
| **Approve**, P-01 (*Yes, and use auto mode*, 01:11:06.881) | 1232316 (1229085, `claude`) | record `working` `PostToolUse` at 01:11:06.925; `plan_closed` (`the plan was answered in the session's pane, or its hook gave up`) and `needs_you_clear` at 01:11:07.075; hook and request file gone by 01:11:07.156 | closed at 01:11:07.075, 0.15 s after the session moved on (the sheet's observer was installed from P-02 on; the run log's `needs_you_clear` is the event both follow) | not sampled for P-01; see the second approve below | none |
| **Feedback**, P-02 (*Also put a trailing newline after lime, and mention it in the plan*, 01:12:29.966) | 1240128 (1238806, `claude --model sonnet --session-id 262da1d6-…`) | `working` `PostToolUse` 01:12:32.769, `blocked` `PreToolUse` 01:12:34.472, `PermissionRequest` 01:12:34.579 with a new hook 1245553 and a new request; at 01:12:36.50 igris closed the plan and presented the revised one (`plans/P-02-a1-2.md`), and wrote `{"n":"ba04de88…","behavior":"pane"}` for the old request; 1240128 ended by 01:12:36.895 | the TUI and the sheet went from the first plan to the revised one at 01:12:36.5 (the sheet's text then had `trailing newline`), 1.9 s after the revised request, 6.5 s after the answer | `Run ! Needs you · P-02 · plan` (01:12:34, and after: the revised plan waits) | 1245553 only, the revised plan's hook, which igris answered next |
| **Esc**, P-03 (01:15:24.687) | 1261252 (1259532, `claude`) | Claude Code cancelled the hook: it was gone and the request removed by 01:15:25.172, no decision written; no new hook state record (still `blocked` `Notification` 01:15:10.802), the pane `⏸ plan mode on` | `plan_closed` 01:15:25.910; TUI closed by 01:15:26.198, the sheet `none · Working` at 01:15:26.219 | `Run ● Working · P-03 Kiwi · sonnet · mode plan:auto` (01:15:29) | none |
| **Approve** again, P-03 (after Esc, *Present the same plan again for approval.* typed in the pane; *Yes, and use auto mode*, 01:16:00.362) | 1265172 (1259532, `claude`) | `working` `PostToolUse` 01:16:00.420; `needs_you_clear` 01:16:02.139; hook and request gone by 01:16:02.703 | closed at 01:16:02.139, 1.7 s after the session moved on (within one 2 s poll); the sheet `none · Working` at 01:16:02.470 | `Run ● Working · P-03 Kiwi · …` (01:16:02.9) | none |

So with the fix the plan closes in all three cases, and the hook the pane's answer left waiting ends within a second once igris writes `pane`: before it (V1121-P1) the hook of an approve or of feedback lived on with its parent, its request stayed, and the view stayed open until the session ended or a revised plan replaced it.

In the feedback case the close came from the replacement, not from the new `movedOn` check: the session was `working` for 1.7 s (01:12:32.769–01:12:34.472) and igris's poll fell before and after that window, when the latest record was `blocked` `PreToolUse` (the revised `ExitPlanMode`). The owner sees the first plan for a few seconds and then the revised one, which is what the fix intends: the old request is closed, its hook told `pane`, and the new one shown. Observation 1.

After the run (`Done ✓ completed`, `c439684 P-03: Kiwi`, `d19c0eb P-02: Lime`, `d5faeb4 P-01: Lemon`), no `igris hook` process was left (`pgrep -af "igris hook"`), `.igris/plan-approval/` was empty and the run's serve had ended.

## Answered from igris (P-02's revised plan)

| Step | Result |
|---|---|
| **Reject with feedback** (TUI, `2`, *Name the file in the plan title too*, Enter at 01:13:47.921) | `plan_rejected` at 01:13:47.925, `plan_closed` `plan rejected; the session revises it`; hook 1245553 ended and both files were gone by 01:13:48.090; the sheet `none · Working` at 01:13:48.259. The feedback reached Claude (in the transcript), the session stayed in plan mode (every `permissionMode` in the transcript `plan`), and it presented `# Task P-02 — Lime: write lime.txt` (`plans/P-02-a1-3.md`) at 01:13:49.995 with hook 1253215; the TUI's view and the sheet showed it at 01:13:51–52 |
| **Approve** (TUI, `1`, 01:14:44.737) | `plan_approved` at 01:14:44.744; hook 1253215 gone by 01:14:44.873; the sheet `none · Working` at 01:14:45.055; the transcript's `permissionMode` went `plan` → `auto` (`setMode` auto, as before); the session wrote `lime.txt` (`lime\n`, with the trailing newline the pane's feedback asked for) without a prompt and ran `igris done P-02` (01:14:52) |

Both as in 1.12.0.

## The suite

- **Lint and tests:** `make fmt lint test` passed (exit 0; lint clean, every Go package ok, the extension's 178 unit tests passing). No change was made to the code, so it ran on `f09cd7c` as is.
- **Race:** `go test -race -count=1 ./...` passed in every package (36 ok).
- **govulncheck:** `Your code is affected by 0 vulnerabilities`; one module finding is not reachable, as at the earlier gates.

Every Go test run went through a `systemd-run --user --scope` fence (MemoryMax 6G, TasksMax 400), one at a time, with `pgrep` before and after; no igris test process was left.

## Findings

No defect found; nothing was changed.

### Observations (no change)

1. **Feedback in the pane closes the plan through the replacement.** A revised plan came 4.6 s after the answer, and the `working` episode in between (1.7 s) was shorter than the poll, so the check that the session moved on didn't see it; the next poll found the new request and closed the old one (`pane` written, its hook ended). The view showed the first plan until then. A longer revision would be caught by the new check; either way the plan the owner sees is right within one poll of the revised request.
2. **The old `pane` decision stays next to a newer request.** After the replacement `.igris/plan-approval/` held the old request's decision (`pane`, its `n`) beside the new request until igris's next decision replaced it. The new hook ignores a decision with another `n` (§6.3), and the old hook leaves files a newer request replaced, as §6.3 says. Harmless.
3. **After Esc, Needs you comes back as a blocked session.** Claude Code writes no hook record after Esc (V1121-P1), so the latest record stayed `blocked` `Notification`, and 30 s later (`needs_input_after`) igris raised `! P-03 needs you · waits for you in its pane · the agent is blocked and has not run igris done for 30s`. The session was indeed waiting at its prompt in plan mode. Not new.
4. **"is working again" twice.** When P-03's re-presented plan was answered, the plan's Needs you and that blocked one (observation 3) cleared at the same instant (two `needs_you_clear`, reasons `plan_approval` and `blocked`), and the log said `P-03 is working again` twice. Cosmetic, and only after an Esc followed by a new plan.
5. **Gate-only pitfall.** A tmux window opened from the gate's own shell (`tmux new-window`, as a client) took that shell's environment, so its `igris` was the owner's `~/go/bin/igris` (1.8), which refused the scratch `igris.toml` (`unknown key(s) "tui.screen_reader"`, a 1.11 key). The window was replaced by one started through `env -i` with the gate's environment; every result above is from that window.
6. Answering folder trust recorded the scratch path in Claude Code's own config, and the sessions left transcripts under `~/.claude/projects/` and their plans under `~/.claude/plans/`, as at earlier gates.

**Cleanup.** The run ended by itself (`run stopped · completed`); the gate quit the TUI, killed its tmux server by its socket, closed the browser page, removed the Playwright snapshot the MCP had written into the repository, and removed the scratch tree (project, `GOBIN`, runtime, state and tmux directories). No igris, `igris hook`, Claude Code or test process of the gate was left. The owner's own runs (`igris arise B8`, `igris arise V1121` and their serves) were not touched.
