# CLI onboarding checkpoint (V02-CG)

Run 2026-10-07 on Linux (Fedora), igris `v0.1.3-16-ge8ed04c` (`make install`), herdr 0.9.1, Claude Code 2.1.292. Scratch project in a fresh `mktemp -d` git repo, driven from herdr panes.

## Results

| Step | Result |
|---|---|
| `igris init --example` | ok: created `igris.toml`, `.igris/`, `.gitignore` entry, allow rule, `tasks.md`; noted herdr's Claude integration is installed |
| `igris doctor` | ok: exit 0, 17 checks ok, 1 expected warning (uncommitted working tree after `init`), with a `next: git status` hint |
| `igris arise P1` | ok: TUI rendered, P1-02 ran in a herdr pane, ended with `igris done`, P1-03 started next. Stopped with `q` after that: "igris stopped; a running session keeps running" |
| `igris status` from a second terminal | ok: `Run` block (phases, task, mode, since, session, `Lock running here (pid …)`), P1-02 `in progress` |
| `igris status --json` | ok: top-level `phases`, `plan`, `run` (`phases`, `started_at`, `task`, `mode`, `since`, `session`, `lock`, `lock_detail`, `signals`) |
| `igris history` / `history P1-02` / `history P1-03` | ok: run listed as `stopped (2m17s)`, `1 done`; P1-02 attempt with note; P1-03 `unfinished` |
| Completions, bash | ok: subcommands, phase IDs (`arise`, `status`, `--through`), task IDs (`history`, `done`), `--mode` values, `completion` shells |
| Completions, zsh | ok: real Tab in a pane (`zsh -f`, `compinit`): `arise P` → `P1 P2`, `done P1-0` → task IDs |
| Completions, fish | **untested**: fish isn't installed here and installing packages was out of scope. The generated script wasn't syntax-checked (`fish --no-execute` unavailable). Owner decision: leave it untested here; it is re-checked at the V02-G gate |

Deviations from the stock example: P1-02's text was replaced with a trivial "create hello.txt" task, `commit = "never"` was set, and `--mode accept` was used, to keep the run short and cheap. The run was stopped after P1-02 so no Opus session was spent; the leftover P1-03 pane was closed by hand.

## Findings

No blocking bugs; nothing needs a fix task before V02-H. Observations, in order of how much they might matter:

1. **Folder-trust prompt blocks the first task.** In a fresh directory, Claude Code asks "Is this a project you trust?" (it also lists the `igris done` allow rule). The session sits there until someone answers, and igris reports `needs you: the agent is blocked` after 30s, which is accurate but doesn't say why. Idea: have `doctor` or the first-run hint mention that the first session in a new folder shows Claude's trust prompt. Docs-only (V02-09) is enough; no code change needed.
2. **`accept` mode still prompts for Bash.** `--mode accept` (acceptEdits) asked for approval on a read-only `rtk grep … ; ls` command, which is Claude Code's behavior and also fired the "needs you" state twice. Worth one sentence in the README mode table (V02-09), not a bug.
3. **`status` after `q` still shows the Run block** (`Lock none`, task P1-03). Intended per the owner: it is the resume state.
4. **Task duration includes waiting for the owner** (P1-02 `2m00s`, mostly the trust and Bash prompts). Fine, just not a measure of agent time.

Findings 1-3 are documented in the README (V02-09): the folder-trust note under Quick start, the `accept` row of the mode table, and the resume-state note about `status`.

---

# v0.2 gate (V02-G)

Run 2026-10-07 on Linux (Fedora) with the **release binary**: `make release-local`, `dist/igris_linux_amd64_v1/igris`, version `0.1.3-SNAPSHOT-9fa3128`; herdr 0.9.1, Claude Code 2.1.293. Scratch projects from `igris init --example` in fresh git repos, driven from herdr panes; the fixed sizes ran in tmux (`new-session -x 120 -y 40` and `-x 50 -y 20`). To keep the runs cheap the plan was trimmed the same way as at V02-CG: P1-02 and P1-03 became "create `hello.txt` / `bye.txt`" on sonnet, `commit = "never"`, `--mode accept`. Phases P1 ran to completion each time.

## Results

| Pass | Result |
|---|---|
| CLI, start to finish | ok: `init --example` → `doctor` (16 checks ok, exit 0) → `arise P1 --mode accept`: P1-02 and P1-03 done by sonnet sessions, P1-04 (user) done with `d`, P1-G done, run `completed`. A second terminal's `status` showed the `Run` block and `status --json` the `run` object (`lock: running here`) mid-run. Afterwards `history` listed the run (`4 done`, `10m09s`) and `history P1-02` its attempt and note; `status` showed `Lock none`. No tabs left behind |
| Home, 120×40 | ok: empty directory → GET STARTED → **Init** → **Example plan** → (plan edited on disk: NOW card went `PLAN INVALID`, the Check page named the line, fixed → `READY`, refreshed within the 2 s poll) → **Preview** → wizard (phase, through, mode, summary) → run view in the same program → both user tasks with `d` → `q` back to home with fresh data (`P1 5/5 done`, `RECENT … completed`, `STOPPED`). Doctor, History, Settings and the Notify dialog rendered. A second terminal's home during the run showed `RUNNING ELSEWHERE` with the live task, no Arise button, `a` did nothing, `q` exited |
| Termius, 50×20 | ok (owner, see below). Simulated in tmux at 50×20: home, wizard dialogs, run view, help overlay and the return home all fit; the NOW card moves above PHASES, the bar wraps to two lines |
| Completions, fish | ok: tested in a Fedora container with fish 4.6.0 (`podman run … dnf install fish`). `fish --no-execute` accepts the generated script, and `complete -C` returned subcommands (`igris `, `igris ar`), phase IDs (`arise `, `arise P`, `--through `, `status `), task IDs (`done P1-0`, `history P1-0`), `--mode ` values and `completion ` shells |

## Findings

1. **Bug: `status` says "state unreadable" while a user task is current.** A user task has no session and `state.json` stores `"mode": ""` for it, but `checkShape` in `internal/report/run.go:97` accepts only the five real modes. So from the moment a run reaches a user task (and after leaving it there with Home or `q`), `igris status` prints `State  state unreadable: unknown mode in state.json` instead of the Run block, and home shows `READY` with `Arise…` instead of the interrupted/stopped card with `Resume…`, so the owner can't resume that run from home. It is invisible when a run is stopped on an agent task or has completed, which is why V02-CG and the 120×40 pass didn't hit it. Fix: allow an empty mode for the current task (and show `—`), with a test for a user task in `report/run_test.go`.
2. **Fixed in V02-31 (f3b539f).** **Example plan's P1-G can't pass as written.** P1-01 is pre-marked `done` but there is no skeleton, so the agent task P1-G ("run the binary") finds nothing to run and asks the owner what to do. A new user running the stock example hits it. Either change P1-G to something checkable or turn P1-01 into a task that creates the skeleton. Not a code bug.
3. **Minor:** after a completed run the NOW card says `STOPPED · … Resume continues P1` although P1 is `5/5 done` and P2 is next. It matches the CLI's resume semantics and the documented note about `status`, but a "run completed — start P2" hint would read better.
4. **Minor:** in the second terminal's home, `RECENT`/the NOW card list the task being worked on as `recent: P1-02 unfinished` while it is still running.

## Re-check after the fix (9142a2d)

Binary rebuilt from HEAD (`v0.1.3-34-g9142a2d-dirty`, the dirty flag is only this doc). At 50×20 in tmux, from a fresh example project with P1-02/P1-03 set `done` by hand so the run starts on the user task P1-04:

- `status` while the run sits on P1-04: the `Run` block shows (`Task P1-04`, no `Mode` line, `Lock running here`); no "state unreadable".
- Leave with `q` (Home): `status` and `status --json` still show the run (`task P1-04`, empty mode, `Lock none`); home shows `INTERRUPTED · last run P1`, with `Resume…` as the default button.
- `a` offers `Resume last run (P1-04 first)`; Resume → summary → run view back on P1-04 (`YOUR TURN`); `d` on P1-04 and P1-G completed P1; `q` returned home with `P1 5/5 done`.
- Result: finding 1 is fixed. One cosmetic leftover (fixed in 5ec9490, which shares one `modeTail` between both cards and tests both): the interrupted card's task line (`runLines`, `internal/tui/home_state.go:510`) still appends ` · mode ` with an empty mode, so at 50 columns it reads `user · mode`. The running card was fixed first; this one wasn't.

Finding 2 is fixed in V02-31; findings 3–4 stay as follow-ups for after v0.2.0.

## Termius (owner)

Scratch project ready for the owner: `…/scratchpad/proj-termius` (planned like the others); run `igris` in it over Termius at 50×20 with the release binary and tick: home reads, `a` walks through the wizard, the run view fits, `?` scrolls, `y` copies (if the terminal allows OSC 52), Home returns with fresh data, and the focus/refresh behaviour holds when you switch away and back. Result (2026-10-07): the owner ran home and the run view in Termius (Android, 50 columns wide): home READY with PHASES, HEALTH and RECENT, the action bar wrapping to two lines, a run started from home with yolo and its badge, a user task's YOUR TURN, and the return home. Two owner requests came out of it and were done before release:

- **V02-30 (8b2388f):** the run view's card shows what the task is about (its text, cut with `… t: details`), `t` / `[t] Details` / the card title / one click on a task open the details, and NEEDS YOU says `o opens the session`.
- **V02-32 (38fb4be):** YOUR TURN says the task is the owner's to do outside igris and how to finish it (`d` done · `s` skip), and `d` on a user task takes an optional note that lands in `igris history`.

Both were checked live in herdr tabs (a card-demo run with an agent task that asks, a user task, `t`, `o` focusing the session tab, `d` with a note, `history` showing it) and the owner reviewed the card demo. Gate signed off by the owner on 2026-10-07.
