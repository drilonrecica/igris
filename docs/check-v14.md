# v1.4 gate

The v1.4 gate (V14-G) checks serve and attach end to end: a phase started with `arise`, detached with `q`, its terminal killed and reattached from another pane; two clients at once on one question; version skew in both directions; a headless `serve` outside tmux in its detached `igris-<project>` session; `igris stop`; skip-permissions refused without a terminal; a long project path on the socket fallback; then the full suite, race, fuzz and govulncheck. igris draft (V13) ships in the same release; its gate is [check-v13.md](check-v13.md).

It ran unattended on Linux (Fedora 44) on 2026-10-22, from the V14-G task session (owner's decision 2026-10-21). The build was this tree (`master` at `71385e1`), built with `-X main.version=1.4.1` into a scratch `GOBIN` put first in `PATH`. The "older build" was `7a414c5` (V14-04, the last commit that changed code before the docs task), extracted with `git archive` and built as `1.4.0`. Every project was a synthetic scratch git repo under a `mktemp -d` directory: two tasks on sonnet, `G0-01` (`sleep 90`, then a file, then `igris done`) and `G0-02` (a file, then `igris done`), with `commit = "ask"` so each task ends on a question. The nested runs used a private tmux server (`tmux -L igris-gate-v14`, `HERDR_*` unset, `SHELL=/bin/bash`, new windows started as `bash --noprofile --norc` so `PATH` kept the gate build). Mode was `auto` throughout, never `yolo` or `plan:yolo`. The gate played the owner with `tmux send-keys`: folder trust in each session window, `q` and the Detach dialog, and the questions. Processes were addressed by exact PID. Afterwards the tmux servers and their socket files, the socket fallback directories the gate created (`$XDG_RUNTIME_DIR/igris`, `/tmp/igris-1000`, neither existed before) and the scratch tree were removed. No igris, Claude Code or test process was left behind.

**Versions:** Claude Code 2.1.308, tmux 3.7c, Go 1.27.3.

## Detach, a killed terminal, reattach

| Step | Result |
|---|---|
| `igris arise G0 --mode auto` in pane A | arise started `igris serve --mode auto -- G0` in the background (its own PID, `.igris/serve.log` 0600, `.igris/igris.sock` 0600, `igris.lock` naming serve's PID) and attached. The run view looked as before |
| `q` | **Detach** (focused) · Stop run · Cancel, with the explanation. `enter`: `detached; the run goes on in igris serve: …`, exit 0. serve and the session went on |
| `igris attach` in A, then `tmux kill-pane` on A | The attach client got SIGHUP and exited. serve went on |
| `igris attach` in pane B | The fresh snapshot: tasks, the current card with its tail and tokens, the log so far |
| A second project: `arise` in a pane killed with `kill-window` while G0-01 ran | arise exited, serve went on. Reattached from another pane, the owner answered both commit questions there, and the run ended `completed` with two commits |
| Bare `igris` in a project with a live serve | Home showed **RUNNING in igris serve · pid … on this host**, `igris serve hosts this run: Attach (t) shows its run view and controls it`, and `[›Attach‹]` first. `enter` pushed the run view with **Home** in place of Quit. `q` → **Home** returned to home, and `q` left |

When the run ended, every attached view showed `the run stopped: completed — press q to quit`. `q` then closed it without asking (exit 0). Socket and lock were removed, and `runs.jsonl` held the usual events.

## Two clients, one question

B and C were both attached when `Commit G0-01?` came up; both showed the dialog. Commit was answered in C, and "Leave uncommitted" in B straight after. One commit was made (`G0-01: Slow file`), and both views closed the dialog and logged `G0-01 committed`. The late answer was logged by both and by serve as `warning: ignored answer: no task is running` (see Observations). G0-02's question, answered in B, closed in C the same way.

## Version skew

| Case | Result |
|---|---|
| Older serve (`1.4.0`, started by hand) | Warned at start: `sessions run igris done with …/bin/igris (1.4.1), not this igris (1.4.0); put this igris first in PATH` |
| Newer client (`1.4.1`) attached to it | Header `… · tmux · served by igris 1.4.0`, and the log line `this run is served by igris 1.4.0 and keeps that igris until it ends`. A `1.4.0` client at the same time showed neither |
| Commands across versions | Pause from the 1.4.1 client and unpause from the 1.4.0 client: both views and serve logged `pause after task: on` / `off` |
| `igris stop` (1.4.1) on the 1.4.0 serve | `the run stopped; its sessions stay open`, exit 0, at once. Both clients showed the stop |
| The other way: `arise` (1.4.1) resumed that run | The 1.4.1 serve reattached to the session the 1.4.0 serve left (`G0-01 resumed: reattached to its session`) |
| Older client (`1.4.0`) attached to it | `served by igris 1.4.1` and the note. **Stop run** from its `q` dialog stopped the run (`igris stopped; a running session keeps running — igris arise resumes`), and arise's view showed the end |

Both builds speak protocol 1 from the same code, so this covers the version handling (hello, the header, the note, commands both ways), not a protocol difference. There is none in 1.x yet.

## Headless serve

`igris serve G0 --mode auto </dev/null`, started with `setsid` from a shell with `TMUX` and `HERDR_*` unset. The default tmux server was confined with `TMUX_TMPDIR` under the scratch directory, so the owner's own default server was never touched.

- It printed `serving the run on …/.igris/igris.sock` and `sessions open in the tmux session igris-p3: tmux attach -t igris-p3 shows them`, created that detached session, and opened `G0-01 · sonnet` in it.
- `tmux attach -t igris-p3` from another terminal showed the window. Detaching that tmux client changed nothing.
- `igris attach` worked from a pane of the gate server, and from a client outside any multiplexer (`env -u TMUX igris attach`). The commit question was answered from the latter.
- `igris stop` while G0-02 ran: `the run stopped; its sessions stay open`, exit 0. Socket and lock were removed, and the session and its window stayed. A second `igris stop`: `no run is live in this project (nothing answers on …)`, exit 1.
- SIGTERM to serve's exact PID with a question pending: `run stopped: stopped`, clean exit, socket removed.

## Skip-permissions without a terminal

`igris serve G0 --mode yolo </dev/null` and `--mode plan:yolo`, without a terminal. Both were refused with exit 1 and `igris serve: task G0-02 would run in yolo mode: skip-permissions mode needs the typed phrase "skip permissions", and stdin is not a terminal to type it on; nothing was started (run igris serve in a terminal, or start the run with igris arise)` (`plan:yolo mode` for the second). No session was opened, the socket and lock were removed, and no run was logged. The phrase was never typed.

## Socket fallback

A project at a path whose `.igris/igris.sock` is 121 bytes (over Linux's 107):

- With `XDG_RUNTIME_DIR` set (arise): `/run/user/1000/igris/da7bdf680a97e467.sock`. That is the first 16 hex of sha256 of the root, socket 0600, directory 0700 created by serve. `.igris/igris.sock.path` named it. `igris attach` and home's Attach found the run through the path file. `igris stop` removed the socket and the path file.
- With `XDG_RUNTIME_DIR` unset (headless serve): `/tmp/igris-1000/da7bdf680a97e467.sock` (0700 directory, 0600 socket). A client with `XDG_RUNTIME_DIR` set still found it through the path file. `igris stop` with the session-lost question pending removed it.

That headless serve resumed a run whose session lived in the gate's tmux server. On its own server the pane check found nothing, so it reported `G0-01 SESSION LOST` and asked, as for any lost session. It never took over a stranger's pane.

## The suite

- `go test -race -count=1 ./...`: every package ok (29).
- `make test`: every package ok (29). The first `make test` after the scenarios failed twice (`TestServeYolo`: `the socket outlived serve`; `TestAriseTUIConfirmsBeforeTheTUIStarts/yolo typed`: `read the run's snapshot: the connection closed before the snapshot`). Both had passed the race run just before. Repeated with `-count`, `TestServeYolo` failed 2 to 3 times in 30 on the tree before this gate's changes. Both turned out to be real races, fixed below. After the fixes, `cmd/igris` ran 25 times under `-race`, `internal/serve` and `internal/project` 20 times, and `internal/tui` and `internal/engine` 10 times, all without a failure.
- Compatibility suite (`cmd/igris/compat_test.go`, over `testdata/compat` v0.1.0 to v1.2.1): all six tests pass.
- `make fuzz FUZZTIME=30s`: all ten targets ran 30 s each with no failure (`FuzzParse` config and plan, `FuzzSplitRow`, `FuzzLoadIncludes`, `FuzzSplice`, `FuzzSpliceGenerated`, `FuzzExtend`, `FuzzParseSignal`, `FuzzParseAgentState`, `FuzzParseImport`). It ran before the fixes to `internal/serve`, `internal/engine` and `cmd/igris` below, none of which a fuzz target covers.
- govulncheck: `Your code is affected by 0 vulnerabilities`. One module finding is not reachable: GO-2026-5024 in `golang.org/x/sys` (v0.36.0, fixed in v0.44.0, Windows only, not called), as at the V13 gate.
- `make fmt lint`: 0 issues.

Every test run went through a `systemd-run --user --scope` fence (MemoryMax 6G, TasksMax 400), one at a time, with `pgrep` before and after. No `igris.test` process was left.

## Findings

Fixed (with a regression test):

- **serve's output told the owner to type commands it never reads.** serve printed `--no-tui`'s lines unchanged, hints included: `? commit … [y/n]`, `type \`retry continue\`, \`retry fresh\`, \`done [note]\`, \`skip <reason>\` or \`stop\``, `pause after task: on (type \`pause\` again to turn it off)`, `paused before …; type \`pause\` to continue`, and the YOUR TURN line's `type \`done [note]\``. serve reads no commands from stdin (SPEC §14). Seen live in a headless serve's output on a commit question and on a lost session. **Fixed:** serve formats its lines with `formatServed`: questions are followed by `answer in \`igris attach\``, YOUR TURN names `igris done ID` or attach, and pause lines lose the typing advice. Everything else is `--no-tui`'s line as before, and `--no-tui` is unchanged. SPEC §14, README and the site's "Detach and attach" say so. Test: `TestFormatServed` (cmd/igris).
- **An attached view outside herdr and tmux offered Open session, and `o` did nothing.** `igris attach` from a shell outside any multiplexer is the phone-over-ssh case. That client can't build a backend, so its view has no Focus, but the bar showed `[›Open session‹]`, the card `[o] Open session`, and NEEDS YOU `o opens the session`. Pressing `o` did nothing and said nothing, against §15.3's "never shows buttons that do nothing". **Fixed:** the run view offers Open session (bar, card, the NEEDS YOU hint) only when it can focus a session (`canOpen`). Every view with a reachable backend is unchanged. SPEC §15.3 and README say so. Test: `TestNoOpenSessionWithoutFocus` (internal/tui) at every card size. The teatest fixture now has a Focus, as every real view with a backend does.

- **A serve that ended before its accept loop started left its socket behind.** serve runs `srv.Serve(ln)` in a goroutine. When serve ended at once (the yolo refusal, a start-up refusal), `Shutdown` could run before that goroutine had registered the listener. `Serve` then saw a closed server and returned without closing `ln`, so `.igris/igris.sock` (or the fallback) stayed until the next serve replaced it, and serve printed `warning: use of closed network connection`. This was the cause of the `TestServeYolo` flake. **Fixed:** `Serve` closes the listener it was given in that case too and returns nil, as it does when closed while accepting. serve also closes its listener itself as it exits, so the socket is gone when the process ends. Tests: `TestServeAfterShutdownClosesTheListener` (after Shutdown and after Close), and `TestServeYolo` (60 runs clean, was 2–3 failures in 30).
- **A run that ended at once could beat its starter's snapshot.** serve started the run from the starting client's `hello` (to take its yolo confirmation) *before* building and queueing that client's snapshot. A run that ended at once, such as a phase already complete or stuck, or the fake backend in tests, could shut serve down first. arise then failed with `read the run's snapshot: the connection closed before the snapshot` instead of showing the result. **Fixed:** the server queues the snapshot first and calls the `Hello` hook after, so the run's events, its end included, follow that snapshot. Test: `TestHelloStartsTheRunAfterTheSnapshot` (the hook ends the run and shuts serve down; the client still gets the snapshot, then `run_stopped`). `TestHelloSnapshotThenEvents` now waits for the hook on a channel, since it runs after the snapshot. Because the run now starts a moment after the starter's snapshot, `TestAriseServeAttachStop` has its first view leave once the task runs: a stop sent sooner is the stop-before-the-run case, covered by its own test.
- **An attached view that saw the run end could exit before serve released the lock.** serve releases `igris.lock` only after its clients have the run's end. So `igris attach` → Stop run (or a run ending while attached), followed at once by `igris arise`, could find the lock still held. That made `TestAriseServeAttachStop` flaky (`the lock outlived serve`). V14-05 had fixed the same for `igris stop`. **Fixed:** `attach` and an attached `arise` wait up to 5 s for the lock to be released when the run ended while they were attached, as `stop` does. `waitLockFree` returns at once for a lock whose process is gone, since a dead serve's lock is never released. Tests: `TestAriseServeAttachStop` (60 runs clean) and `TestWaitLockFreeSkipsADeadHolder`.
- **After a resume, the run view never offered Open session.** `TaskResumed` carried no session ref; only `SessionOpened` did, and a resume reattaches instead of opening. So after `arise` resumed a task whose session was alive, the view showed no `[o] Open session`, against §15.3 ("while a session exists"). This dates from before 1.4 (the 1.4.0 build showed it in the version skew run above), but in 1.4 stopping and resuming is the everyday flow. **Fixed:** the engine puts the reattached session (and its Claude session ID, when valid) on `TaskResumed`, and the run view and serve's snapshot take it from there. Tests: `TestResumeReattaches` (engine), `TestSnapshotResumedSession` (serve), `TestResumedSessionCanBeOpened` (tui: offered and focuses that session; a resume whose session is gone offers none).

These last four were checked live too, on a fresh build in a second private tmux server (`igris-gate-v14b`, removed afterwards). arise → Detach → `igris attach` → Stop run, then `igris arise` at once, resumed the run with no lock complaint. After a stop in the middle of a task, `arise` resumed it with `[›Open session‹]` offered, and `o` switched the tmux client to that task's window.

Needs a decision (not changed):

- **Open session from a client that is not on the session's tmux server.** An attached view focuses through its *own* backend, built from its own environment. Seen: a client in another tmux server than a headless serve's session. `o` found that server's unrelated `%1`, correctly refused it, and logged `could not open the session: attach tmux pane %1 for G0-01: session is gone: the pane was not started for this session; …`. The session is not gone, so the message misleads. From the code, not seen: a client in the owner's tmux session on the *same* default server as `igris-<project>` passes the check, and `select-window` then changes that other session's window, which the client doesn't show. The same holds for a herdr client on a tmux run, or the reverse. Options: put the headless session's name (or the session's backend and server) in the snapshot and say `the session runs in tmux session igris-p3: tmux attach -t igris-p3` instead of focusing; use `switch-client` when the client runs inside tmux on the same server; or only offer Open session when the client's backend matches the run's. Any of these touches what the snapshot carries or §11.5's Focus, so it is left for the owner.

Observations (no change):

- The late answer of the second client reads `ignored answer: no task is running`. It is accurate, since the engine was between tasks, but with several clients `ignored answer: the question was already answered` would say what happened. Answers carry no question ID (protocol 1), so a late answer could only meet a *later* question if it arrived after a whole task. That is not realistic, and the views close the dialog on the answering event.
- `serve --mode yolo` without a terminal prints `serving the run on …` (and the headless line) before the refusal, because the socket comes up before the engine checks the mode. Nothing starts, and the socket is removed. Cosmetic.
- The headless `igris-<project>` session keeps the idle shell window that `tmux new-session -d` creates (`1:zsh`) next to the task windows, and it stays after the run, like the session (§11.5).
- Right after a resume the card shows `tokens 0 in · 0 out` until the next usage read.
- Two `.test` processes seen while checking for leftovers (`migrate.test`, `uploads.test`) belonged to another of the owner's projects running its own verify. They were not the gate's and were left alone.
- A gate-only pitfall: in a tmux server started by a Claude Code session, new windows ran a login shell that rebuilt `PATH` without the scratch `GOBIN`, so `igris attach` there was 1.2.1 (`unknown command "attach"`). The gate set `default-command` to a non-login bash. The Claude windows igris opens run `claude` directly with the server's environment, so they always had the gate build.

## Owner checks left

Only these need the owner before v1.4.0 ships (with V14-R):

1. **Reattach from the phone over ssh.** Start a run in a terminal on the machine (`igris arise <phase>`), detach with `q`, then ssh in from the phone (Termius) and run `igris attach` in the project from the plain ssh shell, *outside* tmux. Check that the narrow layout is readable, that a question can be answered, and that `q` → Detach leaves the run going. Since the fix above, Open session is not offered there. Then close the ssh app without detaching: the run must go on, and `igris attach` from the desktop shows it.
2. **Decide Open session across tmux servers or sessions** (Needs a decision above): a message naming `tmux attach -t igris-<project>`, `switch-client`, or offering it only when the backends match.
3. **Optional, once on herdr.** This gate ran on tmux only, by the unattended-run rules. Serve and attach don't depend on the backend, but `arise` → `q` → Detach → `igris attach` once in a herdr tab confirms the herdr path, and that a herdr session's Open session still focuses its tab from an attached view.
