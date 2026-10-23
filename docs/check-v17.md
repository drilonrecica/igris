# v1.7 gate

The v1.7 gate (V17-G) checks the design language end to end, in nested runs with real Claude Code sessions. It covers:

- the run screen, wide and narrow, on tmux and on herdr: a parallel phase with two active tasks, a question answered from a floating dialog, a plan approved and rejected, a side session, the end-of-run summary;
- home in each of its states, every page and the start wizard;
- the web UI at 390 pixels in light and dark, driven by a headless browser;
- the CLI's text output against its goldens and under `NO_COLOR`;
- a notification of each kind on a throwaway ntfy topic;
- the full suite, race, fuzz and govulncheck.

It ran unattended on Linux (Fedora 44) on 2026-10-23, 04:26–05:10, from the V17-G task session (owner's decision 2026-10-21). The build was this tree (`master` at `dca2f0b`, then with the fixes below), built with `-X main.version=1.7.0` into a scratch `GOBIN` put first in `PATH`. Every project was a synthetic scratch git repo under a `mktemp -d` directory, with tasks on sonnet that write one file and run `igris done`. The configs had `commit = "ask"` (p4: `auto`) and `verify = "true"` unless noted. Mode was `auto` or as planned throughout (A-02 in `plan:auto`), never `yolo` or `plan:yolo`. The mode picker's More modes… was opened and looked at, nothing picked from it.

**Where the runs ran.**

- **tmux:** a private server, `tmux -L igris-gate-v17`, started from `env -i` with only `HOME`, `USER`, `LANG`, `TERM`, `COLORTERM`, `XDG_RUNTIME_DIR`, the D-Bus address and the gate's `PATH`. A first server that inherited the gate session's own environment was killed unused: it carried `CLAUDECODE`, the gate session's messaging socket and `IGRIS_SLOT=0`. Windows ran `bash --noprofile --norc`.
- **herdr:** a private named session, `herdr --session igris-gate-v17`, with its own socket under `~/.config/herdr/sessions/`. Its client ran in a window of that tmux server. Every `herdr` call of the gate set that socket explicitly, so the owner's herdr session was never addressed.

The gate played the owner with `tmux send-keys` and `herdr pane send-keys`: Claude Code's folder-trust question (Down, then Enter), and every answer in igris's own view. Processes were addressed by exact PID or by exact window. The browser was Playwright's Chromium at 390×844, `prefers-color-scheme` emulated light and dark. ntfy was `https://ntfy.sh` on a random throwaway topic: no account and no token, read back with `/json?poll=1`.

**Cleanup.** The gate removed:

- every window and the tmux server, and its socket file (`/tmp/tmux-1000` itself held other servers and was left);
- the herdr session (`herdr session stop`, then `delete`);
- the browser, its `.playwright-mcp/` output in the repo, and the scratch tree.

No igris, Claude Code, herdr or test process of the gate was left. The owner's own igris runs (4 processes) and herdr session were not touched.

**Versions:** Claude Code 2.1.309, tmux 3.7c, herdr 0.9.1, Go 1.27.3, Chromium (Playwright).

## The run screen on tmux

Project p1: phase `A` marked parallel (`max = 2`, the default) with `A-01`, `A-02` (Mode `plan:auto`) and `A-03` (deps `A-01`); phase `B` with `B-01`. `[serve] http` was on loopback and `[notify.ntfy]` had the throwaway topic, `actions = true` and `task_done` added. The run was started from home through the wizard, in a 120×40 window. A second window ran `igris attach` at 60×24, then 99×30, then 50×20.

| Step | Result |
|---|---|
| Arise (wizard: phase A, Only A, As planned) | log `Arise. phase A · 3 tasks` (no run-wide mode, so none in the header either), then `A-01` and `A-02` started in slots 0 and 1. Header `phase A · 0/3 · 0% ░░░░░░░░░░ · 2 active · tmux` and `● Working`. Two cards, the selected one heavy (`┏━ › A-01 · slot 0 Alpha`), TASKS as wide as its rows |
| Both ask (A-01 commit, A-02 plan) | header `! Needs you` in plume, banner `! A-01 needs you · commit? · 2 tasks need you`, both cards `! Needs you · your answer`. The commit dialog floats centred over the dimmed screen, rounded, `enter choose · 1–2 pick · esc keep asking` |
| Narrow (attached, 60×24) | the dialog takes the whole screen. After `esc`: header `igris · p1 · A · 0/3 · 0% · 2 active · tmux` with `! Needs you`, the switcher `‹ ! A-01 · 1 of 2 › ! A-02`, the selected card, TASKS in one row with glyphs, the last log lines, and a bar wrapping to two rows ending `[?] [q Quit]`. At 99×30 the bar keeps its full labels |
| Commit from the floating dialog | committed, `A-01 integrated onto main`, done. `A-03` took the free slot, and the plan dialog followed: `A-02 presents a plan`, the plan as text up to 100 columns, **Approve: the session switches to auto** · Reject with feedback… · Answer in the pane |
| Approve | `plan approved; the session switches to mode auto`. Then a false Needs you (finding 1, fixed) |
| Side session (`S` in the run view) | dialog → Blank → First prompt (empty) → Model → Mode (`manual` first, `A run works in this tree…`) → Where → Open. Window `side · sonnet` opened, header `· side 1`, log `side 4df85e opened`. The Side sessions page listed it (`side 4df85e · idle · 11s · sonnet · manual`); Close a session… → **Keep it** · Close session → `side 4df85e closed`. The dialog said `in a herdr tab` on this tmux run (finding 4, fixed) |
| A-03 commit from the web sheet, A-02 commit from `Answer…` | `phase A complete`. The summary: helm, `Done ✓ completed`, `phase A · Parallel pair`, `3 done · 0 skipped`, `wall 6m55s`, `tokens 699k in · 1.7k out`, `needs you 6m16s`, `3 commits · last A-02`, `verify 4 ✓`, `next: igris arise B · igris report <run>`. Bar `[›q Home‹] [h History] [y Copy report command] [?]` |
| Summary narrow (attached, 99×30 and 50×20) | the same, with `[q Quit]` and no History (no home beneath). At 50 columns the helm is dropped and the columns stack. The attached view said `needs you 3m07s` (finding 3, fixed) |
| `q Home` | home in its last-run-complete state: `READY · phase B`, `last run A completed`, RECENT `Oct 23 04:30  A  3 done · completed` |

Colors, read from `capture-pane -e`: plume appeared only on the wordmark's `i`, `! Needs you`, the banner, the `!` glyphs and state lines of tasks that need you, and the helm's plume. Glow was on `●`, `✓`, the progress bar and the focused region. Frame `#2C5AA0` was on rules and borders, sonnet's Sky `#6EA8FF` on rank names, and `[SKIP PERMISSIONS]` in plume words (`#FF5C74`) in the picker. Ember was on `▲ Overdue` (p4).

## The run screen on herdr

Project p2: the same plan, run with `igris arise A` (the direct entry, no home beneath) in a herdr pane about 174 columns wide, then narrowed to about 60 columns by shrinking the host window.

| Step | Result |
|---|---|
| Arise | both sessions opened as herdr tabs (`A-01 · sonnet`, `A-02 · sonnet`) at once: herdr's hook arrives at the trust question, so there was no 15 s wait between them. Wide view as on tmux, header `… · 2 active · herdr` |
| Backend toast | herdr's notification in the corner: `● ! Needs you · p2` / `phase A · A-02 Beta: needs you (plan awaiting approval)` |
| Commit, narrow | the full-screen dialog; answered with Enter |
| Reject with feedback…, narrow | empty feedback refused with `Type what the session should change.`; "also print the file with cat at the end" sent; the session presented a revised plan with that step; **Approve** |
| Leave uncommitted (A-03) | in a parallel phase the commit is asked again (the task must be committed to integrate, by design since V15-05); then Commit |
| Side session, narrow | About this task — A-02, through the five steps; a herdr tab `side · A-02 · sonnet` opened in `manual` with the 11-line prefill, header `· side 1`. The dialog said `in a herdr tab`, right here |
| Summary, narrow then wide | as on tmux: `Done ✓ completed`, `needs you 4m02s`, `next: igris arise B · igris report <run>`, header `… · herdr · side 1  Done` |

## Home in each of its states

All at 120 columns unless noted; ready also at 200, 80 and 50.

| State | How it was made | Result |
|---|---|---|
| Get started | empty git repo | the helm and the tagline, `GET STARTED` with five steps (`✗ 1 igris.toml not found · I Init writes it`, `· 2 plan …`), `▲ doctor: 2 warnings`, bar led by `[›I Init‹]` |
| Plan missing | `igris init`, no plan | `✓ 1 igris.toml found`, `✗ 2 plan tasks.md not found · D Draft… or E Plan from template…` |
| Plan invalid | a dep on an unknown task | `PLAN INVALID · tasks.md · 1 problem`, the problem with its line, `Next: c Check lists every problem; e Edit plan fixes them by hand, or A Adapt…` |
| Config invalid | an unknown key | `CONFIG INVALID · igris.toml · 1 problem`, `igris uses the defaults meanwhile. Edit igris.toml from Settings.` |
| No multiplexer | `env -u TMUX igris` | header `herdr/tmux ✗`, `Arise needs herdr or tmux: run igris inside a herdr pane or a tmux session.`. The PHASES row read `0/1 n…` (finding 7, fixed) |
| Ready | p1 before the run | `READY · phase A Parallel pair`, `next`/`then`, `3 left · 1 wait on A-01`, HEALTH, RECENT `no runs yet`; 80 and 50 columns stack into one column and fold the bar into `More…` |
| Served / served needs you | `igris serve B` in p1 | header `! Needs you`, `RUNNING · igris serve · pid … on this host · since 04:44`, `! B-01 needs you · commit?`, `Attach to answer…`, bar led by `[›a Attach‹]` |
| Interrupted | `igris stop` with B-01 in progress | `▲ Interrupted · last run B · 32s ago`, the task, `session: tmux @9 %9 (Resume reattaches if it is still open)`, `[›a Resume…‹]` (see Needs a decision, 1) |
| Stale lock | `igris serve B`, then SIGKILL to its exact PID | with this tree as first built, home kept showing RUNNING and Attach for the dead PID (finding 5, fixed). With the fix, within one poll: `LAST RUN DID NOT CLEAN UP (pid 1533101 gone)`, `Resume asks before clearing the lock.` |
| Last run complete | after p1's run | above |

## Pages and the start wizard

From home, wide (120×40) and narrow (50×20):

- **Preview** (`v`): the dry run, `nothing is written and no session starts`.
- **Check** (`c`): `✓ plan valid · 2 phases · 4 tasks`, `no warnings`.
- **Doctor** (`i`): `all 16 checks ok`, each check with its `next:` (`scroll` at 50).
- **Settings** (`,`): the effective `igris.toml`, scrolling with `── 1–36 of 64 · ↑↓ scroll ──`.
- **Notify** (`n`): the floating `Send 17 test messages to ntfy, tmux toast?` with **Send** · Cancel; cancelled here, sent from the CLI below.
- **Side…** (`S`): the side-session dialog.
- **Updates** (`u`): `igris 1.7.0 is newer than the latest release, 1.6.0.`
- **Draft…** (`D`): `Extend the plan…` · Cancel; cancelled, no session.
- **Help** (`?`): every binding of home, with the focus keys.

Every dialog floated, rounded and centred, with the bar dimmed beneath it; at 50 columns each took the whole screen.

**The start wizard** (`a`) is four steps:

1. **Phase**, with each phase's progress.
2. **Through**: Only A · A through B.
3. **Mode**: As planned first, then plan, auto, accept, manual, default and **More modes…**. More modes… adds plan:default, plan:accept, plan:auto, `yolo [SKIP PERMISSIONS]` and `plan:yolo … [SKIP PERMISSIONS]`, and focuses the first of them.
4. **Confirm**: `Arise A · mode as planned · 3 tasks · first A-01 sonnet` with **Arise** · Preview · Cancel.

## The web UI, at 390 pixels

p1 while A-01's commit and A-02's plan were both pending, then p5 (the same plan) with the fixed build.

| Step | Result |
|---|---|
| Not paired, dark and light | `401`. Brand header, `Not paired`, `Run igris serve token on the machine and open its URL on this device.` The page carries no stylesheet by design (see Observations) |
| Pair | the pairing URL redirected to `/`, titled `igris · p5` |
| Sheet open, light and dark | the sheet over the dimmed page: `A-01 COMMIT?`, `1 of 2 ›`, `Commit A-01?`, **Commit**, **Leave uncommitted**, `▾ collapse`. Plan · Pause after this task · Stop stay below it |
| Stepper | `1 of 2 ›` went to A-02's plan (`2 of 2 ›`, with the plan text and **Approve: the session switches to auto** · **Reject with feedback**); `2 of 2 ›` goes back to `#q-1`, no dead link |
| Sheet collapsed, light and dark | a peek strip `! A-02 · plan ▴` above the bar; each card that asks shows `Asks you` and `▸ answer in the sheet` instead of its own actions |
| Answer from the sheet | **Commit** for A-03: committed and integrated |
| Phase segments | three labelled links (`A-01: needs you`, `A-02: needs you`, `A-03: to do`) |
| Recent | `A-02 asked: plan`, `A-01 verify passed`… with `1m ago` |
| Clocks | the status line (`2 questions · 1m22s`) and the phase line's elapsed time (`0 of 3 done · 2m01s`) stood still between events while the cards' times ran (finding 6, fixed). With the fix, both advanced 5 s in 5 s |
| Tail | the card's tail box showed Claude Code's banner, with the latest lines below its fold (finding 2, fixed). With the fix it sits at the newest line, and a reader who scrolled up stays where they are across refreshes |

## The CLI

In p1, piped, which means no terminal:

| Command | Output |
|---|---|
| `status`, `status A` | the run first (`Run ▲ Interrupted · B-01 Delta`, `Lock stale (pid …: process no longer running)`, `Signals …`), then per phase a table with headers and glyphs |
| `history`, `history A-02` | the runs; one task's attempts with `✓ verify passed`, then `next: igris report <run>` |
| `arise B --dry-run` | `#  TASK  RANK  MODEL  MODE  VERIFY  TITLE`, `1 · B-01 … (resumed: fresh session)`, `✓ phase B complete`, `next: igris arise B` |
| `update --check` | `✓ igris 1.7.0 is up to date (the latest release is 1.6.0)` |
| A typo | `statsu` → `did you mean status?`, exit 2. `frobnicate` → `run igris help`, exit 2 |
| A bad flag | `status --bogus` → `igris status: unknown flag --bogus; see igris status -h` |
| Missing arguments | `skip B-01` → `skip needs a reason …`; `notify` → `missing subcommand; use igris notify test` |

Also run piped: `version`, `check`, `phases`, `report`, `usage`, `doctor`, `done`, `help`. None of the 18 printed an escape sequence or a line with trailing spaces.

On a terminal (a tmux pane), `status` and `doctor` drew `✓`/`●` in glow and `▲` in Ember. The same pane with `NO_COLOR=1`, and with `TERM=dumb`, drew no escape at all. The goldens themselves (plain, colored with color forced, and `NO_COLOR`) are compared by `make test`, which passed.

`serve token` draws its QR code black on white with escapes even when piped and under `NO_COLOR` (see Needs a decision, 4).

## Notifications

Every one of the nine kinds came through `igris notify test` (the TEST samples). Kinds that came up in real runs as well are marked; p4 was made to produce them.

| Kind | Title (priority) | Real body seen |
|---|---|---|
| `needs_input` | `! Needs you · p1` (4, high) | `phase A · A-01 Alpha: needs you (commit question)`, with **Commit** · **Leave uncommitted**; for a plan, **Approve** · **Open** |
| `session_lost` | `! Needs you · p4` (4) | `C-02 Lost: session lost`, with **Retry** · **Stop**; the session's window was killed |
| `verify_failed_limit` | `! Needs you · p4` (4) | `C-01 …: verification keeps failing; needs you`; `verify = "test -e never-there"` with `verify_max_attempts = 1` |
| `task_overdue` | `▲ Overdue · p4` (3, default) | `running longer than its Timeout 5s` |
| `rank_mismatch` | `▲ Rank mismatch · p1` (3) | sample only: `on claude-opus-5-5, not its rank sonnet`. A real one needs a session on another model |
| `run_error` | `✗ Failed · p4` (3) | `before_task hook failed: exit status 1`, with **Retry** · **Stop** |
| `phase_stuck` | `▲ Stuck · p4` (3) | `phase C: stuck: 1 unfinished task(s), none can start` (finding 8, fixed: `1 unfinished task`) |
| `phase_done` | `✓ Phase done · p1` (3) | `phase A: complete` |
| `task_done` | `✓ Task done · p1` (3) | `phase A · A-01 Alpha: done` |

Only the three Needs-you kinds had high priority. The herdr toast used the same title. p4's run ended `Done ▲ stuck` with `▲ C-03 waits on D-01 (ready, phase D)` and `– C-01 skipped · gate: verify never passes`.

## The suite

- **Race:** `go test -race -count=1 ./...` passed in every package, before and after the fixes.
- **Lint and tests:** `make fmt lint test` gave 0 issues and every package ok, including the compatibility suite, before and after the fixes.
- **Fuzz:** `make fuzz FUZZTIME=30s` ran all eleven targets 30 s each with no failure, before and after the fixes: `FuzzParse` (config and plan), `FuzzParseImport`, `FuzzExtend`, `FuzzSplitRow`, `FuzzLoadIncludes`, `FuzzSplice`, `FuzzSpliceGenerated`, `FuzzParseSignal`, `FuzzParseAgentState` and `FuzzLoadRun`.
- **govulncheck:** `Your code is affected by 0 vulnerabilities`. One module finding is not reachable: GO-2026-5024 in `golang.org/x/sys` v0.36.0 (Windows only, not called), as at the V13–V16 gates.

Every test run went through a `systemd-run --user --scope` fence (MemoryMax 6G, TasksMax 400), one at a time, with `pgrep` before and after. No `.test` process was left.

## Findings

### Fixed (each with a regression test)

1. **A plan approved or rejected in igris raised a false Needs you at once.** The session's idle clock ran from the moment it went blocked on its plan. An answer given after `needs_input_after` was therefore followed, in the same poll, by `! A-02 needs you: the agent is blocked and has not run igris done for 20s` and a high-priority `needs_input` on ntfy. Claude Code still showed the prompt for the one or two seconds its hook took to hand over. Seen in both runs: p1 approved at 04:34:06, the false Needs you came at 04:34:06 and cleared at 04:34:08. This has been in igris since 1.1.

   **Fixed** in `internal/engine/planapproval.go`: Approve and Reject start the idle clock again. Answer in the pane, and a hook that gave up, keep the old clock, since then the pane waits on the owner. Test: `TestPlanAnswerRestartsTheIdleClock` (approve and reject, the answer a minute after the plan), which failed before. Live with the fix (p5): approved at 03:00:34 after 2m20s, with no needs-you between `plan_approved` and the commit question. SPEC §6.3 now says so, and the CHANGELOG has it under Fixed.

2. **The web UI's tail showed the oldest of its 40 lines.** The box is `max-height: 9rem` and scrolls, but the script set its text without scrolling it, so on a phone it showed Claude Code's banner and the newest lines sat below the fold. This has been in igris since 1.6.

   **Fixed** in `app.js`: `fillTails` follows the end unless the reader has scrolled up. Test: `TestUITailFollowsTheNewestLines` reads the script (`make test` has no JavaScript runtime), and failed before. Live: `scrollTop` was at the end after each refresh, and stayed at 0 for 9 s after a manual scroll to the top. In the CHANGELOG.

3. **The end-of-run summary undercounted the needs-you time in an attached view.** `restore`, which runs on attach and on every reconnect, rebuilt the summary from the feed without the needs-you clock. A question pending at attach was counted only from the next event, and a reconnect dropped everything counted so far. Seen: 3m07s in the attached view against 6m16s in the view that saw the whole run.

   **Fixed** in `internal/tui/attached.go`: a reconnect to the same run keeps the time counted so far, and a pending question counts from when it was asked. Test: `TestSummaryNeedsYouOfAnAttachedView`, which failed before. Live (p5): a view attached a minute in said `needs you 4m22s`, exactly the run log's 2m29s + 1m53s. This was new in 1.7, so there is no CHANGELOG entry.

4. **The run view's side-session dialog said "in a herdr tab" on tmux.** It passed `Options.Backend` to `sessionPlace`. A view pushed by home holds the config's `auto` there, and one opened by `igris attach` holds nothing, while the header already preferred the served run's backend. This has been in igris since 1.2.

   **Fixed:** one `backendName()` helper serves the header and the dialog. Test: `TestSideDialogNamesTheRunsBackend`, which failed before. Live: both views say `in a tmux window`. In the CHANGELOG.

5. **Home kept showing a dead serve as RUNNING, with Attach.** Home reloads only when a watched file's size or time changes, and a holder killed without cleaning up changes no file. After `kill -9` of serve, home still offered Attach 40 s later.

   **Fixed:** `report.Stamp` also carries whether the lock's holder runs (`LockAlive`), so its death triggers a reload, which already classifies the lock as stale. Test: `TestStampFollowsTheLockHolder` holds the lock with a `sleep` child, kills it and expects the stamp to change, and failed before. Live: `LAST RUN DID NOT CLEAN UP (pid 1533101 gone)` within 4 s. SPEC §15.6 (Refresh) and the CHANGELOG say so.

6. **The web UI's status-line clock and the run's elapsed time stood still between events.** Both were rendered by the server only. The cards' times ran, so the page showed `2 questions · 1m22s` next to a card at `2m31s`.

   **Fixed:** both are `.ran[data-started]` spans that the script's `tick` already keeps current. `status` now carries the clock's start, `Since`. The goldens were updated with `-update` and the diff reviewed: only the two spans were added. Tests: `TestUIClocksTick` and `TestUIStatusWord`, which checks `Since`; both failed before. Live: both advanced 5 s in 5 s. This was new in 1.7, so there is no CHANGELOG entry.

7. **Home's PHASES column cut its own row when every phase title is shorter than four cells.** `phaseLines` keeps a title 4 cells wide, but `phaseWidth` sized the column by the real width. A phase titled `x` read `0/1 n…`.

   **Fixed:** `phaseWidth` uses the same minimum. Test: `TestHomePhasesFitShortTitles`, which failed before. Live: `› · A  x    ░░░░░░░░░░ 0/1 next`. In the CHANGELOG.

8. **The stuck notification said `1 unfinished task(s)`**, against §15.4's rule that a count carries its noun, pluralised. `igris notify test`'s sample said the same.

   **Fixed** with `look.Count`. `TestRunStuck` now checks the exact words, and failed before. Live: `▲ Stuck · p4`, `phase C: stuck: 1 unfinished task, none can start`. In the CHANGELOG.

9. **A Needs-you notification could be filed under another task in the run log.** In a parallel phase the poll's gathered `needs_input` messages go out after its steps, when the first active task is in focus again. So A-03's delivery was logged as `"task":"A-02"` (p5, 03:00:40), though the message itself named A-03. This has been in igris since 1.5. The run log's format is unchanged; only the `task` of that line is now right.

   **Fixed** in `internal/engine/event.go`: `notifyAs` sends each message with its own task in focus. Test: `TestParallelNeedsYouLoggedForItsTask`, which failed before. This fix came after the gate's last live build, so it is verified by the test and the suite only. In the CHANGELOG.

The release notes for 1.7.0 got one line naming the user-visible fixes (1, 2, 5).

### Needs a decision

1. **A clean `igris stop` with a task in progress shows home as `▲ Interrupted`.** §15.6 makes it so: INTERRUPTED whenever `state.json` has a current task, STOPPED only between tasks. But §15.4 defines Interrupted as "the last run ended without ending itself". Since 1.7 that word is an Ember warning, so the owner's own stop now reads as a warning on home and in `igris status`. The two options are a STOPPED card that names the task to resume, or the warning kept only for runs that didn't stop themselves. Either changes §15.4/§15.6 and the goldens.
2. **The LOG and `--no-tui` lines keep their pre-1.7 wording**, which the goldens pin:
   - `A-01 started (sonnet → sonnet, auto) · Alpha`
   - `! A-02 needs you: the session presents a plan…`
   - `? commit the changes of A-01 …`

   §15.1's mock shows `M1-03 started · sonnet · mode plan` and `! M1-04 needs you · …`. And `?` is not in the glyph table, which says each glyph has one meaning. Rewording every event line is a wording decision for the owner: follow the mock, or amend it to the lines as they are.
3. **The side-session dialog in a parallel run says `A-02 is working in this tree; a plan edit holds the run.`**, though A-02 works in its own worktree (slot 1). The same flag also puts `manual` first, and integration does merge into the shared tree, so a session there can still collide with a fast-forward. The choice is between wording ("the run merges into this tree…") and behaviour (whether `manual` should stay first).
4. **`igris serve token` writes the QR code's black-on-white escapes when piped and under `NO_COLOR`.** It does so deliberately, so a camera can read the code on any theme. §15.4 and the V17-01 rule say no color without a terminal or with `NO_COLOR`. The options: keep the colors as functional, print the QR only on a terminal, or draw it without color under `NO_COLOR`. Separately, its `next: igris arise` reads oddly while a run is live.

### Observations (no change)

- **The not-paired and refusal pages have no stylesheet**, so they render in the browser's default serif. This is by design, and `TestUINotPairedPage` pins it: an unpaired browser can't load the stylesheet, and each failed request of a stale cookie counts against its address. The CSP forbids inline styles. Giving the page a style would mean serving one stylesheet unauthenticated, a design choice for the owner.
- **A tail read for a task that just ended answers `400`**, and the console shows it once per finished task. The script ignores it; it is a race between the event and the 4 s tail loop.
- **Each new parallel worktree asked Claude Code's folder-trust question** on tmux. The V15 gate saw later worktrees of a repo not ask. On herdr, igris was not held by it, because herdr's hook arrives at the question.
- **A dialog dims the screen only in part.** Segments drawn with their own style after a reset (`PHASES`, the NOW card's title, text after a rank color) keep full weight. It reads fine; noted for the look.
- **The mode picker's numbering** `10.`/`11.` shifts those rows one column, and `plan:yolo — then [SKIP PERMISSIONS]` reads oddly beside `plan:auto — plan, then auto`.
- **Odd wordings around the verify limit:**
  - after the limit, a re-verify reads `attempt 2 of 1`;
  - the card of a task at its verify limit says `! Needs you · idle 0s` while the agent is busy;
  - an overdue task also raises Needs you (§6.3), so the card stacks the two reasons.
- **Home's RUNNING card keeps `●` on the task line** under `! B-01 needs you · commit?`, as the golden pins it. Preview's `Total: 3 sessions` keeps a colon.
- **Not seen live, against the plural rule:** the adapt review's `24 table change(s)` (three goldens) and config errors' `unknown key(s) … check spelling against SPEC §12`.
- **`.igris/worktrees/` is `0755`** while the rest of `.igris/` is `0700`. It is reachable only through the `0700` `.igris/`.
- **The plan's Status cells stay uncommitted** after a parallel phase (§6.11), so the next phase's serve warns about uncommitted changes, as at the V15 gate.
- **Gate-only pitfalls:**
  - zsh doesn't split an unquoted variable, as at the V16 gate. It happened three times here: a shell function holding `env -i …`, a loop of `igris $c` and `set -- $W`. Each was redone under bash, and the last delayed killing C-02's window by a minute.
  - A `t.Fatal` in a first draft of `TestStampFollowsTheLockHolder` left its `sleep 60` running; it was killed by PID and the test now cleans up with `t.Cleanup`.
  - To prove that test failed on the old `app.js`, the gate stashed `app.js` once (named, applied by SHA, dropped); the stash list was left as it was.
  - Playwright saves screenshots only inside the repo, so they went to `.playwright-mcp/`, which was then removed.
- Answering folder trust recorded the scratch paths in Claude Code's own config, and the sessions left transcripts under `~/.claude/projects/`, as at earlier gates.

## Owner checks left

Only these need the owner before v1.7.0 ships (with V17-R):

1. **The phone UI on the phone.** Pair it by QR over Tailscale as at the V16 owner check (`[serve] http` on the Tailscale address, `igris serve token`), in a scratch project with a parallel phase of two tasks and `commit = "ask"`. Check on the phone, in its light and dark themes:
   - the sheet open and folded to the peek strip;
   - Plan, Pause and Stop reachable under it;
   - the stepper with two questions, and answering from the sheet;
   - the phase segments;
   - that a card's tail shows the newest lines;
   - that the status line's clock runs.

   The gate used Chromium at 390×844, not a phone.
2. **Termius at its width.** From the phone, `ssh` in and run `igris attach` on that run. Check the narrow run screen at Termius's real width: the switcher (`‹ ! A-01 · 1 of 2 ›`, `[`/`]` or a tap), the selected card, the full-screen dialog, the two-row bar, and the end-of-run summary stacking.
3. **The look in your own terminal theme.** Run a small phase in your usual terminal, plus `igris`, `igris status` and `igris doctor`. Judge the wide run screen with a floating dialog, home, GET STARTED and the summary's helm. Check that Ember warnings and plume Needs-you read well on your background, and in light mode too if you use it (`[tui] theme`). The gate read colors from tmux captures, not a real terminal.
4. **A Needs-you notification on the phone.** With your real topic, let a task reach its commit question. The phone should show `! Needs you · <project>` at high priority with **Commit** · **Leave uncommitted**. A `▲`, `✗` or `✓` message should arrive at normal priority.
5. **Decide or defer the four items under Needs a decision.** None blocks the release; each can stay as is for 1.7.0.
