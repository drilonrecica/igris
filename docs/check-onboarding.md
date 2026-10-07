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
