# v1.2 gate

The v1.2 gate (V12-G) checks the exit criteria for igris 1.2: a side session on herdr and on tmux during an active run (a plan edit from it trips the hold, its usage shows as side), a real split plan run end to end with Status written in an included file, each template through `init`, `check` and `--dry-run`, and the compatibility suite green. Publishing (V12-R) is the owner's.

It ran on Linux (Fedora 44) on 2026-10-21, from the owner's Claude Code session, which drove igris in a herdr tab it created itself and in a private tmux server (`tmux -L igrisgate`, with the `HERDR_*` variables unset so `backend = "auto"` picked tmux). Both projects were synthetic scratch repos on sonnet. The build was `master` at `74dcdb8` (`igris 1.1.1-0.20261020232616-74dcdb86e12b`).

**Versions:** Claude Code 2.1.305, herdr 0.9.1, tmux 3.7c.

## Side session during a run, and the split plan

One scratch project per backend: `tasks.md` declares `<!-- igris:format 2 -->` and holds S0-01 (a task that waits on `sleep 150`), then includes `phases/s1.md` holding S1-01. `igris arise S0 --through S1 --mode auto`. While S0-01 worked, `S` opened a side session from the TUI (preset Blank, a first prompt asking it to change the S1-01 task text in `phases/s1.md`, sonnet).

| Backend | Result |
|---|---|
| herdr | The dialog showed Blank, first prompt, model, mode (default first, no yolo), Where with the warning `S0-01 is working in this tree; a plan edit holds the run`, and a confirmation; the side session opened in its own tab (`igside-<id>`), `.igris/side/<id>.json` was written, the header showed `side 1` and the log `side <id> opened`. It first declined to edit the plan (its rules say so) and edited the included file once told to. When S0-01 ended, igris logged `needs_you` (`plan_changed`, `S1-01 Task text changed — igris paused`), turned pause-after-task on and did not start S1-01. `S` then `x` closed the side session: `side_closed` and `side_usage` in `runs.jsonl`, `.igris/side/` emptied. `p` resumed; S1-01 ran and finished; the run ended `completed` |
| tmux | The same sequence in a tmux window named `side · sonnet`; the hold, `side_closed`, `side_usage`, resume and completion were identical |

Status was written in the file each row lives in: `S0-01` `done` in `tasks.md`, `S1-01` `done` in `phases/s1.md`, with the include line and every other byte untouched. The plan edit that tripped the hold was made in the included file, so the watch over every file of the plan is covered too.

`igris usage` on both runs: the attempts table lists S0-01 and S1-01 only; **Side sessions** is a table of its own (`529480` on herdr, `820963` on tmux; rank, ended, in, out, models) marked `not attempts, and not in the figures above`; `--json` has the same rows under `side`, and the totals do not include them.

## Templates

`igris init --template go-cli`, `library` and `web-app` in empty git repos: each wrote `tasks.md` and said its ranks are suggestions; `igris check` passed with 0 warnings (13, 15 and 18 tasks); `igris arise M0 --through <last phase> --dry-run` walked every phase with no `STUCK` and no error (12, 13 and 16 sessions). `igris init` without `--template` created no `tasks.md`, as before.

## The suite

`make fmt lint test` on the gate's tree: 0 lint issues, every package ok, including the compatibility suite over every release's captured files (`testdata/compat`, v0.1.0 to v1.1.0) and the contract map.

## Findings

- An older `igris` on `PATH` (1.1.0 here) is what a task session runs for `igris done`, so a split plan run needs igris 1.2 everywhere: 1.1 skipped the included phase, `igris done S1-01` failed with it and the session reported it to the owner. This is the documented behavior (README, plan format), not a bug; the first herdr attempt hit it before the gate build was put on `PATH`.
- An appended blank line is not a plan edit: igris compares rows, not bytes, so only a changed cell holds the run.
- A side session in `default` mode on this machine showed `auto mode on` in its footer: `default` leaves the choice to Claude Code's own settings, which here default to auto. The recorded mode in `.igris/side/<id>.json` is `default`.

## Updater (V12-G2)

The update check added after the gate (V12-P4, V12-09), run on 2026-10-21 against the real GitHub releases API (latest release v1.1.0) with builds of `master` at `a164804` that claim other versions: `-X main.version=1.1.0`, a plain `go build` (development), and `-X main.version=1.0.0` at a plain path and at a `…/Cellar/igris/1.0.0/bin/igris` path. Home ran in a herdr tab the owner watched.

| Case | Result |
|---|---|
| Up to date (1.1.0) | `u` on home: **igris is up to date** · `igris 1.1.0 is the latest release.` · Close |
| Development build | **Development build**: names the pseudo-version, says igris doesn't update it, links v1.1.0; no Update |
| Archive (1.0.0, plain path) | **igris 1.1.0 is out**: `1.0.0 → 1.1.0`, "can't tell how it was installed, so it runs nothing", the release link and the download, verify and install steps; Close only. The same steps run by hand from `igris update --check` downloaded 1.1.0, printed `Good "file" signature for igris`, passed the checksum and installed `igris 1.1.0` |
| Homebrew (1.0.0, Cellar path) | `installed with Homebrew`, `Update runs: brew upgrade igris`, the note on runs in other projects, **Update** · Not now. With no `brew` on the host, Update showed **Update failed** naming the missing `brew`, also in the feed |
| During a run | A real `arise P1 --only P1-04` (a user task) held the lock in a second pane; home showed `RUNNING ELSEWHERE` and the Homebrew dialog said `Update after the run ends: a run holds this project.` with Close only |
| Real Homebrew upgrade | In the `homebrew/brew` container: the tap's previous formula installed igris 1.0.0, its binary was swapped for the 1.0.0-claiming build (1.0.0 itself has no `update`), the current formula restored; `igris update --yes` ran `brew upgrade igris`, which upgraded to 1.1.0, and printed `Updated: igris in PATH is now 1.1.0.` (exit 0); `igris` then resolved to `Cellar/igris/1.1.0` |
| CLI edges | No terminal and no `--yes` on a Homebrew install: exit 1, `pass --yes to update without asking`; an unknown flag: usage, exit 2 |

Not run: a real `go install` update. It needs a pushed tag newer than the running build, so it is first possible from 1.2.0 to the next release; the command, its `GOBIN` and the checksum-lag hint are covered by tests.

Findings:
- In the TUI dialog the archive steps wrap at the dialog's width (the archive URL, a long install path), so steps copied from the terminal break; `igris update --check` prints them unwrapped. **Fixed:** the dialog has **Copy steps (y)**, which copies them whole (OSC 52, as the other Copy actions).
- A missing `brew` read `"brew" not found in PATH; install it or fix PATH: exec: "brew": executable file not found in $PATH`: the runner's wrapped error repeated itself. **Fixed** in the runner for every command: the text says it once and the error is still `exec.ErrNotFound` (`TestExecNotFound`); re-run: `run brew upgrade igris: "brew" not found in PATH; install it or fix PATH`, exit 1.
- The `igris-path` warning (V12-F2) showed at `arise` start in the scratch project, as intended (the `igris` in PATH is 1.1.0).
