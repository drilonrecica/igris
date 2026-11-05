# v1.9 gate

The v1.9 gate (V19-G) checks the features of 1.9 end to end, with real Claude Code sessions. It covers:

- the editor extension in a real VS Code (`@vscode/test-electron`) against this build: onboarding on a clean profile, a run started with one button and served headless on tmux, its sessions as terminal tabs, a question answered from the status bar and one from a notification, a task's diff in the native diff editor;
- in the TUI, the start-run wizard's Tasks step (Only, From, Until) and **Arise** of the next phase from the end-of-run summary, both from home and from a run started in the shell;
- `igris serve` with `backend = "auto"` from a plain shell outside herdr and tmux: headless on tmux, and the error without tmux in `PATH`;
- the four v1.8 gate leftovers (V19-12…V19-15), live;
- `session_host` and Open session from the same tmux server, another server and outside tmux (V19-16);
- the full suite, race, fuzz and govulncheck.

It ran unattended on Linux (Fedora 44) on 2026-11-05, 10:00–10:45, from the V19-G task session. The build was this tree (`master` at `367760b`, then with the fixes below), built with `-X main.version=1.9.0` into a scratch `GOBIN` first in `PATH`. Every project was a synthetic scratch git repo under a `mktemp -d` directory, with sonnet tasks that write one file and run `igris done`, and `verify = "true"`. Mode was `auto` throughout, never `yolo` or `plan:yolo`; every `[models]` key mapped to sonnet.

**Where the runs ran.** A private tmux server, `tmux -L gate -f /dev/null`, started from `env -i` with only `HOME`, `USER`, `LANG`, `TERM`, `COLORTERM`, `XDG_RUNTIME_DIR`, the D-Bus address, `SHELL=/bin/bash`, the gate's `PATH` and `TMUX_TMPDIR` set to a directory of the scratch tree, so tmux's *default* server (the one headless runs use) was private too: `$TMUX_TMPDIR/tmux-1000/default`, never the owner's `/tmp/tmux-1000`. Windows ran `bash --noprofile --norc`; the first said `igris is /tmp/igris-gate-v19.…/bin/igris` and `igris 1.9.0`. "A plain shell" is a gate window with `TMUX` and `TMUX_PANE` unset (no `HERDR_*` or `CLAUDE*` variable was set anywhere in the gate's environment). VS Code was started from such a shell under `xvfb-run`, so the extension host had no `TMUX`, `HERDR_*` or `CLAUDE*` either, as an editor started from a desktop.

The gate played the owner with `tmux send-keys` in igris's views and, where a session asked, in its pane. A helper answered Claude Code's folder-trust question (Down, then Enter: it defaults to `No, exit`) in session windows (`<ID> · <rank>`) only, once per pane. In VS Code the owner was played through the editor's own prompts: `showQuickPick`, `showInformationMessage` and the extension's `Ui` seam, answered by rules and logged. No account, token or private plan was used; the sessions ran on the machine's Claude Code login, as at every gate.

**Versions:** Claude Code 2.1.318, tmux 3.7c, git 2.56.0, Go 1.27.3, Node 24.21.0, VS Code 1.100.0 (the oldest the extension claims, from `@vscode/test-electron` 3.1.0).

## The extension in VS Code (project e)

A scratch suite (not committed) loaded the extension built from this tree (`npm run build`, `dist/extension.js`) as the development extension of VS Code 1.100.0 with a fresh `--user-data-dir` and `--extensions-dir` and `--disable-extensions`, the workspace a git repo with one commit and nothing of igris. Before anything activated the extension, the suite put the gate's `PATH` first in the extension host (VS Code rewrites it), so every `igris` the extension ran was this build. 8 of 8 steps passed in 35 s.

| Step | Result |
|---|---|
| Activation | no refusal (`igris --version` said `igris 1.9.0`); the run view empty with no message, so its welcome content shows |
| Onboarding, **Igris: Set up** | the checklist from `igris doctor --json` in a folder with no project: `▲ no plan at tasks.md — igris init --example`, `▲ .claude/settings.local.json not found … [igris init]`, then `✓` lines (`igris 1.9.0`, `igris in PATH is this igris`, `Claude Code 2.1.318`, `tmux 3.7.0`, `igris is outside herdr and tmux: its sessions run in the detached tmux session igris-e (…)`, …), `Check again` |
| `igris init` | picked from the list: wrote `igris.toml`, `.gitignore` and `.claude/`; the list came back with `✗ no plan at tasks.md` on top |
| A starter plan | **Create a starter plan…** offered Go CLI · Library · Web app · The example plan; the example (`igris init --example`) wrote `tasks.md`; the list then read **Igris: ready** with `▶ Start a run…` first and `tasks.md is valid (2 phases, 8 tasks)` |
| The owner's own plan | the gate replaced `tasks.md` and `igris.toml` with a synthetic two-task plan (E-01, E-02 sequential, `backend = "auto"`, `commit = "ask"`) and committed; refresh showed `E Editor`, bar `igris Idle · no run` |
| **Start a run…** | phases `Next phase with work` · `E`; modes `Project's mode` · default · accept · auto · plan · manual · plan:default · plan:accept · plan:auto (no `yolo`, no `plan:yolo`); the modal `Igris: start the run?` · `Runs igris serve E in this project.` · Start. Serve started detached; 3 s later the view was live, bar `igris ● Working`, and `tmux ls` on the private default server showed `igris-e` |
| E-01 as a terminal tab | `tabs()` `E-01 · sonnet`; the terminal's process `tmux -S …/tmux-1000/default new-session -t =igris-e -s igris-tab-E-01-<pid>-1 ; set-option … destroy-unattached on ; set-option … status-position top ; set-option … status-format[0] " E-01 · sonnet │ If Claude Code asks to trust this folder, that question is Claude Code's: answer it here " ; select-window -t @1`. On the server: `igris-tab-E-01-…-1` in group `igris-e`, attached, `destroy-unattached on`, its one client on `@1 E-01 · sonnet` |
| The tab's first line | the same argv replayed in a gate window: the first line read ` E-01 · sonnet │ If Claude Code asks to trust this folder, that question is Claude Code's: answer it here`; closing that window removed its grouped session |
| The commit question, from the status bar | the notification `Igris: E-01 · commit?: Commit E-01?` with Commit · Leave uncommitted · Answer… was dismissed; the bar read `igris ! Needs you · 1 question` (tone needs); its click (`igris.showRun`) revealed `E-01 Lemon · asks you`, and answering it offered Commit · Leave uncommitted · Commit all (no more commit questions this run); **Commit** → `igris: Commit sent`, `4a95111 E-01: Lemon` on `main` |
| Tabs follow the run | E-01's tab closed and E-02's opened (`igris-tab-E-02-…-2`); E-01's grouped session was gone |
| E-01's changes | `igris.viewChanges` on E-01: one source, `4a951113 E-01: Lemon`, with `lemon.txt [added]` and `tasks.md [modified]`; `lemon.txt` opened a native diff editor `lemon.txt · E-01 (4a951113)` (`igris-git:` URIs), right side `lemon\n` |
| E-02, from the notification | **Commit** on `Igris: E-02 · commit?` → `4ce29b1 E-02: Lime`; the bar read `igris Done · ✓ completed`; no tab left, and once VS Code exited no `igris-tab-*` session either, only the headless `igris-e` (left open by design, §11.5) |

## The wizard's Tasks step and Arise the next phase (project w)

Project w: phase `W1` (parallel, `max = 2`): W1-01 and W1-02 both write `greeting.txt` (W1-02 first waits about 40 s), W1-03 writes `colour.txt`; phase `W2` (sequential): W2-01…W2-03, one file each. `commit = "auto"`. In the TUI at 160×45.

| Step | Result |
|---|---|
| Home → Arise… | Phase (`W1 Greetings · 0/3 next`) → Through (`Only W1` · `W1 through W2`) → **Tasks**: `Run every task of W1, or part of them: …` with All tasks · Only… · From… · Until… |
| From… then Until… | From lists W1-01…W1-03 with `· ready · sonnet`; picking W1-02 opened Until with `To the end of the range`, W1-02, W1-03; Until W1-02 → Mode → Summary `Arise W1 · from W1-02 until W1-02 · mode as planned · 1 task · first W1-02 sonnet`. Its **Preview** (`Preview · W1; from W1-02 until W1-02`, `1. W1-02 sonnet → sonnet · mode auto · verify default`, `Total: 1 session`) matched `igris arise W1 --from W1-02 --until W1-02 --dry-run` (`1 · W1-02 sonnet sonnet auto default Greeting two`, `1 session, 0 user tasks`) |
| Until… alone | Until W1-01 → `Arise W1 · until W1-01 · mode as planned · 1 task · first W1-01 sonnet` |
| Only… | `Only · 2 tasks picked` with `[x] W1-01`, `[x] W1-02`, `[ ] W1-03` → `Arise W1 · only W1-01, W1-02 · mode as planned · 2 tasks`; **Arise**: header `phase W1 · slice 0/2`, both in their slots; W1-03 did not run |
| W2 when it is the last phase | the wizard skipped Through, as it should |
| W2 from home | after the W1 slice: home's wizard (via Resume… → Start a phase…) → W2 → Until W2-01 → `Arise W2 · until W2-01 · … · 1 task`; only W2-01 ran; its summary offered no Arise (no later phase) |

**Arise from the summary.** From home, the end of the W1 slice (`Done ✓ completed`) offered `[q Home] [a Arise W2…] [h History] [y Copy report command]` with `next: igris arise W2`; `a` went back to home, which opened the wizard on Phase with **W2** selected. From the shell, `igris arise W1 --mode auto` (W1-03 left) ended with `[q Quit] [a Arise W2…] [y Copy report command]`; `a` closed the view, opened home with the wizard on W2 (§15.3: the new run replaces the view), and From W2-02 to the end gave `Arise W2 · from W2-02 · mode as planned · 2 tasks · first W2-02 sonnet`; W2-02 and W2-03 ran and committed. The shell then printed the first run's `run completed`. Both ends showed no Arise after a run with no later phase with work.

## `backend = "auto"` from a plain shell (project a)

Project a: `backend = "auto"`, `commit = "ask"`, A-01 and A-02. In a gate window with `TMUX`, `TMUX_PANE` and every `HERDR_*` unset:

| Step | Result |
|---|---|
| Without tmux in `PATH` (`PATH` = the scratch `bin` and a directory with only git, claude, bash, sh, env) | `igris serve A` and `igris arise A` each printed `igris needs herdr or tmux (backend = "auto" found neither HERDR_WORKSPACE_ID nor TMUX, and no tmux in PATH); install tmux 3.2 or newer and igris runs its sessions in a detached tmux session, or run igris inside a herdr pane` and exited 1; no tmux server was started |
| With tmux | `igris serve A --mode auto`: `serving the run on …/.igris/igris.sock; …` and `sessions open in the tmux session igris-a: tmux attach -t igris-a shows them`; `igris-a` on the private default server with window `A-01 · sonnet`; A-01 ran to its commit question |
| The snapshot | a `hello` on the socket: `backend tmux`, `session_host` `{"backend": "tmux", "socket_path": "…/tmux-1000/default", "session": "igris-a"}`, A-01's session `tab_id @1`, `pane_id %1` |

## `session_host` and Open session (project a)

With A-01 at its commit question, `igris attach` in three places:

| Where the view ran | Result |
|---|---|
| The same server, another session (`own` on the default server, with a client) | `[o Open session]` offered; `o` switched that client to `igris-a`, window `@1` `A-01 · sonnet` (`list-clients`) |
| Another server (a window of `tmux -L gate`) | no `o`; the card: `Session: tmux session igris-a — tmux attach -t igris-a`; `y` → header `copied`, tmux's buffer `tmux attach -t igris-a`. Run from a shell of that server, the command fails (Needs a decision, 1) |
| Outside tmux (`TMUX` unset) | no `o`; the same card line; the command works from such a shell (`tmux has-session -t igris-a` → 0) |

`igris stop` then ended the run (`run stopped · stopped`); `igris-a` stayed open, as §11.5 says, until the gate removed it.

## The four v1.8 leftovers, live (projects q, q2, w)

Project q: phase `Q` (sequential) with Q-01 and Q-02; `commit = "ask"`, `[usage] statusline = true`, `[usage.budget] run = "50k"`. `igris arise Q --mode auto` in the TUI.

| Leftover | Result |
|---|---|
| The budget line during a sequential commit question (V19-15) | `budget_exceeded` (`used 50k`) at 09:05:22.15, before Q-01's verify at :24, so at the done read. At the commit question the card read `tokens 105k in · 197 out · ≈ $0.12` and `budget 105k / 50k · 211%`; one budget question, open on top, the commit question under it (`[Answer…]`); Commit, then the budget question alone |
| `1 task and the run` (V19-14) | the banner `! Q-01 needs you · commit? · 1 task and the run need you`, and `igris status` `! Needs you · Q-01 · commit? · 1 task and the run need you`. With only the run's question left: `! needs you · budget reached` and `! Needs you · budget reached` (no count with one thing waiting, as for one task) |
| The hold's resume time (V19-13) | a synthetic `quota.json` (5h `used 96`, `resets_at` 10:09:01) written after Q-01's commit; **Raise…** `5M` → `budget_raised`, then at once `limit_hold` (`resets_at 09:09:01Z`: the run log keeps the reset). Header `Paused · 5h limit 96% · resumes 10:10 (≈ 2m)`, card `held before Q-02` · `5h limit 96% · resumes 10:10 (≈ 2m)`, `igris status` the same. At 10:09:02, past the reset, still `resumes 10:10 (≈ 1m)`: no past time without a countdown. `limit_resume` at 10:10:02.6 (reset + 61.6 s), Q-02 started at once |
| Commits counted after a rerun (V19-12) | in w, W1-02's rebase onto the run branch conflicted in `greeting.txt` (`✗ W1-02 didn't integrate: … igris runs the task again from the start`), and the rerun integrated. The run log had 3 `committed` and 2 `integrated` lines; the summary said `2 commits · last W1-02`, `igris report` `2 commits` with W1-02's commit `22b4f1d` (the one on `main`), `history` one `commit:` line per task; `main` had 2 |

The end of q: `Done ✓ completed`, `2 done · 0 skipped`, `tokens 323k in · 497 out · ≈ $0.25`, `needs you 2m24s`, `2 commits · last Q-02`, `verify 2 ✓`, no Arise (no later phase).

Project q2 (q's plan, budget only), with the fixes: the budget reached at Q-01's done read (`54k used`); after its commit the card read `budget 108k / 50k · 217%`, and **Raise…** said `108k used of 50k` (finding 1). Stop from the question ended the run at once.

## The suite

- **Lint and tests:** `make fmt lint test` gave `0 issues.` and every package ok (the extension's 177 unit tests among them), before the fixes and after them. With the fixes, its first run failed lint on the gate's own new test (`tools/vsixpublish`: five gosec findings on a test's temp files and subprocesses), fixed as the repository's other tests do (0700, `//nolint:gosec` with the reason), then green.
- **Race:** `go test -race -count=1 ./...` passed in every package (34 with `tools/vsixpublish`), before the fixes and after them.
- **Fuzz:** `make fuzz FUZZTIME=30s` ran all eleven targets 30 s each with no failure, before the fixes and after them: `FuzzParse` (config and plan), `FuzzSplitRow`, `FuzzLoadIncludes`, `FuzzSplice`, `FuzzSpliceGenerated`, `FuzzExtend`, `FuzzParseSignal`, `FuzzParseAgentState`, `FuzzLoadRun` and `FuzzParseImport`.
- **govulncheck:** `Your code is affected by 0 vulnerabilities`; one module finding is not reachable, as at the V13–V18 gates.
- **The extension in VS Code:** `xvfb-run -a make test-vscode` (the repository's own suite, against the fake serve socket) gave 16 passing after the fixes, besides the gate's own run above.

Every Go test run went through a `systemd-run --user --scope` fence (MemoryMax 6G, TasksMax 400), one at a time, with `pgrep` before and after. No igris test process was left.

## Findings

### Fixed (each with a regression test)

1. **Raise said what a budget had used when it was reached, not now.** The Raise dialog took its `used` from the budget question, made when the budget was reached; the sessions running then go on, and since 1.9 the done read makes the hit come earlier. Seen in q: `Raise the run budget?` · `50k used of 50k` while the card above read `budget 160k / 50k · 321%`. Since 1.8 a budget reached mid-session did the same.

   **Fixed** in `internal/tui/budget.go`: Raise takes the budget's current use from the run's latest budget lines. Test: `TestBudgetRaiseSaysWhatIsUsedNow`, which failed before. Live (q2): `108k used of 50k`. The CHANGELOG and release notes have it.

2. **The end-of-run summary cut the price estimate.** The facts stood in two halves of the 72-cell block, 34 cells each, so a tokens line with its estimate lost its end: w's summary read `tokens 900k in · 2.4k out · ≈ $0.…`. Any run with 1k output tokens or more could hit it, since 1.8.

   **Fixed** in `internal/tui/summary.go`: when a fact is longer than its half, the left column takes what the right one leaves, and the facts stack when they don't fit side by side; with both halves fitting nothing moves (the goldens are unchanged). Test: `TestSummaryKeepsTheEstimateWhole` (160×45, 120×40, 80×24), which failed at 160×45 before. Verified by the test and the suite. The CHANGELOG and release notes have it.

3. **`make vsix-publish` published a `.vsix` it had not checked, and the store tokens reached the check's npm step.** Found by an automated security review of V19-17's commit during the gate, and confirmed in the script. `tools/vsix-publish.sh` ran `vsix-check` (which rebuilds the `.vsix` from the tag in a scratch copy and compares it with CI's) and then published `bin/igris-X.Y.Z.vsix` from the working tree, whatever had built it: docs/threat-model.md R80 says the published `.vsix` is the checked one. And `VSCE_PAT` and `OVSX_PAT` stayed in the environment of `vsix-check`'s `npm ci` and build, so a dependency's install script could read them; the Makefile's `vscode-deps` prerequisite ran `npm ci` with them set too.

   **Fixed:** `vsix-check.sh` copies the `.vsix` it verified to a directory given as its second argument; `vsix-publish.sh` takes the tokens out of its environment first, refuses a missing one before anything runs, and publishes that verified file with each token in the environment of its own command only; `vsix-publish` has no `vscode-deps` prerequisite (the script says to run it when vsce or ovsx is missing). The DRY output names the checked `igris-X.Y.Z.vsix`. Tests: `tools/vsixpublish` runs the script against stand-ins for `vsix-check`, `vsce` and `ovsx` in a scratch repository with a stale `bin/` `.vsix`: `TestPublishesTheCheckedVsixWithoutTokensInTheCheck`, `TestRefusesWithoutATokenBeforeTheCheck` and `TestDryRunChecksAndPublishesNothing`, all three failing on the old script (it published `stale bytes` and the check saw 2 tokens). R80 maps the first. New in 1.9 (V19-17), so there is no CHANGELOG entry. Not run against the real stores; V19-R does that.

### Needs a decision

1. **Open session's command from another tmux server can't be run there.** From a view on another server (and outside tmux) the card says `tmux attach -t igris-a`, which is right for a shell outside tmux. Inside another tmux server, plain `tmux` addresses that server through `$TMUX` (`can't find session: igris-a`), and with `-S <socket>` tmux refuses to nest (`sessions should be nested with care, unset $TMUX to force`). The options: keep the command (the owner detaches first, as tmux itself says), or, for a view in another tmux server, add `-S <socket>` always and say to run it outside tmux; or offer `TMUX= tmux -S <socket> attach -t igris-a`, which nests.
2. **After a completed slice, home calls the run STOPPED and Resume runs outside the slice.** After `Only W1-01, W1-02` completed, the summary, RECENT and `history` said `completed`, but home's card read `STOPPED · last run W1 · Resume continues W1.`, as §15.6 says for a run that stopped itself while its phases have work, and **Resume last run (W1)** offered `Arise W1 · … · 1 task · first W1-03`, the task the slice left out. The options: treat a completed slice like a completed run (READY with `last run W1 completed`, Arise… as the action); or keep STOPPED and Resume, and say that Resume continues the phase, not the slice.

### Observations (no change)

- **A budget reached at the done read says the task "keeps running".** q's question and log line read `run budget 50k reached · 50k used · Q-01 keeps running` when Q-01 had signalled done (its session was still open). The `used` was the done read's, 50k; the transcript's last records came a second later (105k). Both are what SPEC §6.9 describes; Raise now says the current use (finding 1).
- **The legend line is cut on the summary** (`≈ $ is an estimate at API list prices (as of 2026-10-25; igris.toml ove…`) at every size, as in the goldens and at the V18 gate; `igris usage` and `report` print it whole.
- **Claude Code blocks a foreground `sleep 40`** ("Blocked: standalone sleep 40 …"); W1-02's sessions asked the owner each time, and the gate answered in their panes (wait in an allowed way; skip the sleep). Its two waits were not igris's Needs you, so `needs you 0s`.
- **The shell-started summary has no `h History`**, which the home-pushed one has; `next:` names `igris report` there.
- **Each new session asked Claude Code's folder-trust question** (once per scratch project or worktree), as at the V17 and V18 gates.
- **The plan's Status cells stay uncommitted** after a parallel phase, so the next wizard summary warned about uncommitted changes, as at the V15–V18 gates.
- **Gate-only pitfalls:**
  - The gate's first environment file unset `TMUX`, so the first `igris arise` in a tmux window said `igris must run inside tmux (TMUX is not set)`. The file was split into one for tmux windows and one for plain shells; every result above is from the corrected windows.
  - Home's `a` is **Resume…** once a run can be resumed, so a key sequence meant for Arise… opened `What to run`; nothing started (Cancel), and the wizard was stepped one key at a time.
  - VS Code rewrites `PATH` for the extension host, so the gate's suite put the gate's `PATH` first itself before the extension activated; `igris --version` in the host said `igris 1.9.0` from the scratch `bin`.
- Answering folder trust recorded the scratch paths in Claude Code's own config, and the sessions left transcripts under `~/.claude/projects/`, as at earlier gates.

**Cleanup.** The gate stopped its runs (`igris stop`, or each run ended), killed both of its tmux servers (the `gate` server with every window of q, q2, w and a, and the private default one with `igris-e`; `igris-a` had been removed after its checks), stopped its trust helper by exact PID, and removed the scratch tree (projects, `GOBIN`, VS Code profile, the staged extension and its suite). No igris, Claude Code, VS Code or test process of the gate was left. The owner's own runs (`igris arise V19 --only V19-G`, this run, and `S1`, `H1`, `P0`, `M0` with their serves) and the owner's tmux server in `/tmp/tmux-1000` were not touched.

## Owner checks left

Only these need the owner before v1.9.0 ships (with V19-R):

1. **Install the extension by hand in Kiro, Antigravity and Cursor.** The gate ran it in VS Code 1.100.0 only. In each, install the `.vsix` CI built (or, once published, from Open VSX), open a scratch project with an `igris.toml`, and check: the run view shows the plan with no refusal; **Igris: Set up** lists doctor's checks; **Start a run…** starts a run and its sessions open as terminal tabs (answer Claude Code's folder-trust question in the tab); the status bar's Needs you opens the question and answering it reaches the run; a done task's changes open in the diff editor.
2. **A real usage limit, if one comes** (carried from docs/check-v18.md). The gates used a synthetic quota; no real limit was hit, and the hit path (`StopFailure`, the transcript's `rate_limit` record, `autoContinueAtUsageLimit`) is covered by fixtures and e2e tests only. If a run of yours hits a limit before the release, with `[usage] statusline = true`, check:
   - the session waits by itself, and its card says `◌ Usage limit · 5h · continues HH:MM (≈ …)`, with no Needs you;
   - `runs.jsonl` has `limit_hit` with the window and `resets_at`, and one `usage_limit` notification arrives (not urgent);
   - no new task starts until the reset;
   - at the reset the session continues by itself, the run goes on, and `igris report` shows the wait as Limit wait.

   If no limit comes, this can wait for 1.9.x.
3. **Decide or defer the two items under Needs a decision.** Neither blocks the release; each can stay as it is for 1.9.0.
