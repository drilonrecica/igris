# v0.5.1 gate: patch for the v0.5 leftovers

The v0.5.1 gate (V051-G) checks the patch's four fixes in real runs: a lost Enter on tmux and on herdr, the narrow tail on herdr, `report` help and completion, and a second Ctrl-C from home and from `arise`. It also checks that an external interrupt no longer hangs the screen, and that the full test suite is green. The gate ran on Linux (Fedora 44) on 2026-10-08, from the owner's Claude Code session. That session drove igris only in herdr tabs it created itself (`herdr tab create`, `pane run`, `pane read`, `send-keys`), and in a scratch tmux server inside one of them. All projects and plans were synthetic scratch repos (`git init`).

Versions: igris built from `71609c0` (`0.5.1-0.20261008150155-71609c080d23`, the build that becomes v0.5.1), herdr 0.9.1, Claude Code 2.1.294, tmux 3.7c, Python 3.14 for the test harnesses.

Every scratch config maps all `[models]` ranks to `sonnet` and sets `default_mode = "auto"` and `[notify.backend] enabled = false`. The transcripts show Sonnet 5.5. The folder-trust question was answered once per scratch project beforehand, by starting `claude` in it. `.claude/settings.local.json` puts the HEAD build first on `PATH`, so the sessions' `igris done` was the build under test. Headless commands ran with `HERDR_*` and `TMUX` unset.

Scratch project `p1` (`p2` is a clone of it with only phase D):

```toml
backend = "tmux"             # "herdr" for phases H and T
default_mode = "auto"
needs_input_after = "60s"
poll_interval = "2s"
[models]                     # every rank → "sonnet"
[run]
  verify = "true"
  commit = "auto"
[notify.backend]
  enabled = false
[notify]                     # phases C and D only
  task_done_digest = 2
[notify.webhook]             # phases C and D only: a listener that never answers
  url = "http://127.0.0.1:18751/hook"
  events = ["task_done"]
```

## Results

| # | Check | Result |
|---|---|---|
| 1 | herdr key name `enter` for `pane send-keys` | pass |
| 2 | Lost Enter on tmux: one Enter, task completes; none in a normal run | pass |
| 3 | Lost Enter on herdr | pass |
| 4 | Narrow (60-column) tail on herdr: no blank `…` rows, right-aligned text shown | pass |
| 5 | `report -h`, `history -h`, bash completion of run IDs, newest first | pass |
| 6 | Second Ctrl-C from `arise` and from home; external `kill -INT` of home ×5 | pass |
| 7 | `make fmt lint test`, `go test -race ./...`, `check --strict` on `imp-docs/tasks.md` | pass |

### 1. herdr's Enter key

In a pane of the gate's own tab, `cat -v` got `herdr pane send-text <pane> abc`, then `herdr pane send-keys <pane> enter`. The line came back as `abc`, so the key ended the line. To see the byte itself, the pane ran `bash -c 'stty raw -echo; head -c 1 | od -An -c > enter.od; stty sane'`, and `send-keys <pane> enter` wrote `\r` into the file. That is Enter, the key Claude Code submits on. The name the patch uses (`KeyEnter = "enter"`, `herdr pane send-keys <pane> enter`) is right, and no fix was needed. `herdr pane send-keys --help` (0.9.1) lists no key names. It only mentions `esc`/`escape`.

### 2. Lost Enter on tmux

**Method.** The lost Enter can't be produced on demand with real Claude Code, so the gate removed it on purpose, between tmux and a real Claude Code session:

- `wrap/claude` is first on `PATH`. While a flag file exists, it runs the real `claude` (same arguments, so igris's session ID and hooks file apply) behind `dropenter.py`, a small pty proxy. Otherwise it `exec`s the real `claude` unchanged.
- The proxy forwards the terminal both ways and watches the keys typed into the pane. After the first bracketed paste ends (`ESC [ 201 ~`), it drops the next `\r` once, and logs every `\r` after that paste.
- The result is the bug as seen at the v0.5 gate: igris pastes the prompt with `load-buffer`/`paste-buffer` and presses Enter with `send-keys Enter`, but Claude Code never receives that Enter. The prompt stays in its input box, unsubmitted. Everything else is real: Claude Code, its hooks, `igris hook` and `igris done`.

A scratch server ran in the gate's herdr tab (`tmux -L gate051 -f /dev/null new-session -s g`, with `HERDR_*` unset). `igris arise --only L-01 --no-tui` ran with the flag file present (run `20261008-150537-8a2a`):

```
17:05:37 run started: phase L; only L-01
17:05:37 phase L started
17:05:37 L-01 started (sonnet → sonnet, mode auto) · Make l.txt
17:05:40 L-01 session open
17:05:56 warning: the prompt didn't seem submitted; sent Enter once
17:06:04 L-01 verifying (default): true
17:06:04 L-01 verify passed (default)
17:06:04 L-01 committed: L-01: Make l.txt
17:06:06 L-01 done · Created l.txt containing 'lost'
17:06:06 run stopped: completed
```

The proxy's log:

```
17:05:37 start pid=2123236 argv0=/home/drilonrecica/.local/bin/claude
17:05:40 paste ended
17:05:40 dropped CR (Enter lost)
17:05:56 passed CR
```

- **Before the Enter.** Between 17:05:40 and 17:05:56, `capture-pane` of the session window `L-01 · sonnet` showed `❯ [Pasted text #1 +29 lines]` in the input box. The hook record was `{"state":"idle","event":"SessionStart",…}`.
- **The Enter.** igris pressed Enter 16 s after the prompt went out, one poll after the 15 s mark. The proxy saw exactly one more `\r`. The session then worked, ran `igris done`, and the task was verified and committed (`d7e00de`, `l.txt` = `lost`).
- **Normal run.** L-02 then ran without the flag file, so real `claude` ran directly (run `20261008-150622-64b8`). Its feed has no `didn't seem submitted` line, and the task was done in 19 s (`8342bca`). No proxy ran, so its log got no new lines.

The warning appears in the feed only. It isn't a run-log event, as SPEC §6.3 and the CHANGELOG say.

### 3. Lost Enter on herdr

With herdr, `herdr agent start` types `claude` into the pane's interactive shell, which resolves it through the owner's shell profile. A proxy in front of `claude` would also hide the agent from herdr's detection. So on herdr the Enter was removed one step earlier, in the `herdr` call itself:

- `hwrap/herdr` is first on igris's `PATH`. While a flag file exists, the next `herdr agent prompt <agent> <text>` is not passed on, once. Instead it looks up the agent's pane (`herdr agent get`), types the text as a bracketed paste with `herdr pane send-text <pane> ESC[200~…ESC[201~` and no Enter, and prints the `agent get` result in the `agent_prompted` shape.
- Every other call goes to the real herdr unchanged, and `pane send-keys` calls are logged.
- That is the v0.5 gate's failure: herdr pasted the prompt but didn't submit it. Igris's own `Submit` call went to the real herdr.

`igris arise --only H-01 --no-tui` in the gate's herdr tab (run `20261008-150813-7bb6`):

```
17:08:13 H-01 started (sonnet → sonnet, mode auto) · Make h.txt
17:08:17 H-01 session open
17:08:34 warning: the prompt didn't seem submitted; sent Enter once
17:08:42 H-01 verifying (default): true
17:08:42 H-01 verify passed (default)
17:08:42 H-01 committed: H-01: Make h.txt
17:08:46 H-01 done · Created h.txt containing 'herdr'
```

The wrapper's log:

```
17:08:17 pasted prompt into w2M:p3 without Enter
17:08:34 pane send-keys w2M:p3 enter
```

igris sent one `pane send-keys <pane> enter` to the session's pane, 17 s after the prompt, and nothing else. The task completed (`cfc3217`), and igris closed the session's tab.

### 4. Narrow tail on herdr

Phase T ran in the TUI on herdr (`igris arise --only T-01`, run `20261008-150857-b890`). T-01 runs a 90-second `for` loop. The igris pane was split (`herdr pane split --direction right --ratio 0.32`), which left it 60 columns wide; its rules are 59 cells. At 8 s, Claude Code's right-aligned effort indicator was in the tail as text:

```
igris · p1 · T · slice 0/1 · 0% ░░░░░░ · herdr
── CURRENT ────────────────────────────────────────────────
T-01 Count slowly
rank sonnet → model sonnet
mode auto · 13s · ≈ 1m left
state: working
⎿  $ for i in $(seq 1 90); do echo tail-line-$i; sleep 1; …
✶ Nebulizing… (7s · ↓ 100 tokens)
◐ medium · /effort
run the shell command `for i in $(seq 1 90); do echo
tail-line-$i; sleep 1; done` and wait for it to finish,
then create `t.txt` containing `tail`
[o] Open session  [t] Details
── TASKS ──────────────────────────────────────────────────
 ● T-01 sonnet
```

At v0.5.0 this line showed as a blank row ending in `…`. The pane was read five times, about 8 s apart, over the run. Every line was at most 59 cells, and no row held only a `…`. Later reads had `(ctrl+b to run in background)` as the tail's middle line. The task completed (`be36ba1`). The wide tmux run view of phase C also showed `◐ medium · /effort` left-aligned in the tail, inside the card.

### 5. `report` help and completion

```
$ igris report -h                                  # exit 0
Usage:
  igris report [RUN] [--json]                 one run as markdown (or JSON): RUN is 1 (the newest, default), 2, … or a run ID

Flags:
  -json
    	machine-readable output
$ igris history -h                                 # exit 0
Usage:
  igris history [TASK-ID] [-n N] [--json]     past runs from .igris/runs.jsonl; with a task ID, its attempts

Flags:
  -json
    	machine-readable output
  -n int
    	number of runs to show (default 10)
```

The `igris completion bash` script was sourced in `bash --norc --noprofile` in `p1`, with the HEAD build first on `PATH`. Completion was then called through `_igris` with `COMP_WORDS=(igris report "$cur")` and `COMP_CWORD=2`:

```
complete -F _igris igris
cur=[] -> 20261008-150857-b890 20261008-150813-7bb6 20261008-150622-64b8 20261008-150537-8a2a 20261008-150456-9841
cur=[20261008-1508] -> 20261008-150857-b890 20261008-150813-7bb6
cur=[-] -> --json
```

These are all five run IDs in `runs.jsonl`, newest first, as the script's `compopt -o nosort` keeps them. The newest was the T-01 run, still going at the time.

### 6. Ctrl-C

For a wind-down long enough to press Ctrl-C into, phases C and D used `task_done_digest = 2` and a webhook pointed at a local listener (`hang.py`) that accepts and never answers. The first task's `task_done` is held for the digest. When the run stops, it goes out in the final flush, which then waits up to its 15 s limit.

- **`arise` (TUI, tmux).** `igris arise --only C-01,C-02` (run `20261008-151132-8b36`). C-01 was done, and C-02 was running `sleep 600`. Ctrl-C in the run view (17:11:54.4) quit the TUI. The alternate screen closed, the terminal showed `stopping… (Ctrl-C again to quit now)`, and the listener logged the digest POST at 17:11:54. The process was still alive 5 s later, in the flush. A second Ctrl-C at 17:11:59.9 ended it: the process was gone and the shell prompt was back (exit 130) within 0.5 s, about 10 s before the flush would have given up.
- **Home → run (tmux).** In `p2`, igris opened the home screen. A run was started with **Arise…** → phase D → mode "as planned" → **Arise** (run `20261008-151349-bcf0`). With D-01 done and D-02 sleeping, Ctrl-C in the run view (17:14:15.6) closed the app (`alternate_on` 0). `stopping… (Ctrl-C again to quit now)` was shown, and the digest POST was held at the listener. A second Ctrl-C at 17:14:19.98 ended the process within 0.5 s.
- **External `kill -INT` of home.** Five times, the home screen was opened in `p2`, and its exact PID (the tmux pane shell's `igris` child) got `kill -INT`. Each time, 1.5 s later, the process was gone, tmux reported `alternate_on` 0 (the alternate screen was restored), and the shell printed `EXIT=0`. Two more tries with `kill -TERM` gave the same result.

A run ended by the second Ctrl-C leaves no `run_stopped` line and keeps its lock, as a hard exit does. Home then shows `LAST RUN DID NOT CLEAN UP (pid … gone)`, and **Resume** offers to reattach and asks before clearing the lock.

### 7. Test suite and plan

On HEAD `71609c0`, `make fmt lint test` passed (exit 0, `golangci-lint` 0 issues, tree unchanged). So did `go test -race -count=1 ./...` (20 packages ok, 2 with no test files). Both ran with `HERDR_*` and `TMUX` unset. `igris check --strict --plan imp-docs/tasks.md` gave `OK (24 phases, 167 tasks, 0 warnings)`, exit 0.

## Observations (not blocking)

- **Harness only.** Behind `dropenter.py`, `claude --version` exits 1 (stdin isn't a terminal), so `arise` printed `couldn't determine the Claude Code version` for the proxied run. Without the proxy, the warning doesn't appear.
- **Feed wording.** The success warning `the prompt didn't seem submitted; sent Enter once` doesn't name the task, while its failure variant does (`the prompt of <ID> didn't seem submitted; pressing Enter failed: …`). Only one task runs at a time, so the feed is still clear.

No bugs were found and no fixes were needed. No blockers for v0.5.1.
