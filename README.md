<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/igris-logo-dark.svg">
    <img src="docs/brand/igris-logo-light.svg" alt="igris" height="96">
  </picture>
</h1>

<p align="center"><em>Arise.</em> One task, one fresh session, the right rank.</p>

**igris** runs the tasks in your markdown project plan **one at a time**, each in a **fresh Claude Code session**, started with **exactly the model your plan assigns to that task**.

You write the plan. You decide which tasks need Fable, which need Opus and which are fine on Sonnet. Igris makes sure that's what actually happens: it never runs a Sonnet task on a more expensive model, never lets one task's context bleed into the next, and stops to wait for you whenever a task needs a decision.

> **Status:** early development. v0.1.0 is not released yet. This README describes the planned v1 behavior; see [`SPEC.md`](SPEC.md).

---

## Why

Long Claude Code sessions fill their context and drift. Picking the model by hand for every task is tedious and easy to get wrong, and the expensive mistake is the silent one: a routine task quietly running on your most expensive model.

If you already plan your work as a task table with dependencies and a model per task, igris turns that plan into an execution queue:

```
igris arise M0
```

1. Takes the first ready task in phase `M0`.
2. Opens a new pane and starts Claude Code with that task's model (`--model sonnet`, `opus`, `fable`, …).
3. Hands it the task, the plan and your project rules.
4. If Claude needs you — a decision, an approval, a plan to review — igris waits and notifies you.
5. When the task is truly done, Claude runs `igris done M0-03`. Igris optionally runs your checks, offers to commit, marks the task `done`, unblocks dependents, closes the session.
6. Starts a fresh session for the next task. Repeat until the phase is finished.

Strictly sequential. Deterministic. No LLM decides which model runs what.

## Shadows have ranks

Like summoning the right shadow for the fight, each task gets the rank your plan gives it:

| Rank (your plan) | Typical use | Claude Code model |
|---|---|---|
| `sonnet` | routine, well-specified work | `--model sonnet` |
| `opus` | complex or security-sensitive logic | `--model opus` |
| `fable` | the highest-stakes design and correctness work | `--model fable` |

Rank aliases are configurable in `igris.toml`. Every key is optional; unknown keys are rejected so typos don't go unnoticed, and secrets such as the ntfy token can be given as `env:VAR_NAME` instead of being written in the file.

## Requirements

- **Claude Code**, logged in with your subscription (Pro/Max) or however you normally use it. Igris starts ordinary interactive sessions, so it uses your normal login and limits.
  > If `ANTHROPIC_API_KEY` is set in your environment, Claude Code bills the API instead of your subscription. Igris warns you about this at start.
- **[herdr](https://herdr.dev)** — v1 runs sessions in herdr tabs, so everything survives disconnects and you can reconnect over SSH (e.g. from your phone). tmux support is planned.
- Linux or macOS.

## Install

```sh
go install github.com/drilonrecica/igris/cmd/igris@latest
```

Prebuilt binaries will be on the [Releases](https://github.com/drilonrecica/igris/releases) page.

## Quick start

```sh
cd your-project
igris init          # creates igris.toml and .igris/, ignores .igris/ in git, allows `igris done` for Claude
igris check         # validates your plan
igris status        # shows phases, tasks, what's ready and what's blocked
igris arise M0      # runs phase M0 (inside a herdr pane)
```

`igris arise M0 --dry-run` shows the launch order with each task's model and mode without starting anything or writing a byte. `igris arise M0 --no-tui` runs with plain log lines instead of the TUI and reads your answers and commands from stdin (`y`/`n`, `done [note]`, `skip <reason>`, `retry [continue|fresh]`, `pause`, `stop`, `mode [task] <m>`, `help`).

Before a run starts, igris warns if `ANTHROPIC_API_KEY` is set (your sessions would bill the API, not your subscription; it asks you to confirm), if the project isn't a git repository, or if the tree has uncommitted changes.

`igris init` is safe to re-run: it never overwrites an existing `igris.toml`, appends to `.gitignore` only if `.igris/` isn't ignored yet, and merges the `Bash(igris done:*)` allow rule into `.claude/settings.local.json` without touching your other settings. `igris skip` is deliberately not allowed, so a session that wants to skip has to ask you.

`check`, `phases` and `status` read your plan without changing it. Each takes `--plan PATH` (default: `plan` in `igris.toml`, else `tasks.md`) and `--json`. `check` exits 0 for a valid plan, 1 for an invalid one, and warns when `ready`/`blocked` cells don't match the dependencies; `phases` lists task counts per phase; `status [PHASE]` lists tasks with rank, owner and what each is waiting on.

`igris done ID [--note TEXT]` and `igris skip ID --reason TEXT` work from anywhere inside the project (igris finds the nearest `igris.toml` or `.igris/`). They only drop a signal file in `.igris/signals/`; the running `igris arise` applies it. A signal for a task that isn't the current one is kept and shown, never applied. A `skip` from an agent session is only a request: igris asks you to confirm it.

Each session gets two prompts: fixed igris rules (one task only, never touch Status, run `igris done` last) passed as a system-prompt file, and a first message built from a template. Set `prompt_template` in `igris.toml` to your own Go template to replace the default message; the variables are listed in SPEC §6.1.

`igris arise` needs herdr: run it inside a herdr pane with the herdr server running, otherwise it exits and says so (tmux support is planned). `check`, `status`, `done` and `skip` work without herdr.

Recommended once: `herdr integration install claude`, so herdr reports Claude's state accurately; `arise` warns when it isn't installed.

## Your plan

A plan is a markdown file (default `tasks.md`) with one task table per `##` phase:

```markdown
## M0 — Repository foundation

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M0-01 | **Go module** — init module, pin Go version | — | ready | sonnet | agent |
| M0-02 | **Entrypoint** — main.go with subcommands | M0-01 | blocked | sonnet | agent |
| M0-03 | **Crypto envelope** — versioned secret format | M0-01 | blocked | opus | agent |
| M0-04 | **Create GitHub repo** | — | ready | — | user |
| M0-G | **M0 gate** — owner smoke test | M0-01…M0-04 | blocked | sonnet | agent + user |
```

- **Deps:** comma-separated IDs or ranges (`M0-01…M0-04`), across phases too.
- **Status:** `ready`, `blocked`, `in progress`, `done`, `skipped`. Igris keeps this column up to date and touches nothing else in the file.
- **Owner:** `agent`, `agent + user` (the agent must get your decision or sign-off), or `user` (your own task: igris pauses until you mark it done).
- **Optional `Mode` column** to force a mode per task (e.g. `plan` for design-heavy tasks).

### Different format? `igris adapt`

`igris adapt` converts a plan igris can't read into the canonical format. It opens one Claude Code session (in herdr, mode default) with `adapt.model` from `igris.toml` (`sonnet` by default; `--model opus` for a hard one) and `--plan PATH` for a plan other than the configured one. The session gets the format, `igris check`'s complaints and your `[models]`, writes the converted copy to `.igris/adapt/<plan>.proposed.md`, and finishes with `igris done ADAPT`. It may ask you things in its pane, like any igris session.

It keeps every task, ID, description, dependency, status and model; it only restructures. It never invents models: a task without a usable one gets Model `?` and is listed under `## Adapt notes` at the end, and `igris check` keeps rejecting it until you fill it in. Your plan stays untouched: igris validates the proposal and prints the result; compare the two files and copy the proposal over your plan when it looks right. `adapt` can't run while `igris arise` runs in the project, and `Ctrl-C` stops waiting without closing the session.

## Verification and commits

Set `verify` in `igris.toml` (e.g. `verify = "make fmt lint test"`) and igris runs it each time a session says it's done. When it fails, the last 60 lines go back into the same session to fix; after `verify_max_attempts` failures in a row igris calls you instead. If you mark a task done yourself, igris takes your word and skips verify.

After a task passes, igris commits everything in the tree (`commit = "ask"`, the default, asks you first; `auto` just commits; `never` leaves git alone). The message comes from `commit_message`, `{{.ID}}: {{.Title}}` by default, with the session's done note as the body.

## Modes

Choose the permission mode in the TUI, per run or per task: press `m` (or click **Mode**) for the run, or select a task and press `M` (**Task mode**) to override it for that task. A change applies to the next session; a running one keeps its mode. A task's mode is, in order: your override, its `Mode` column, the run mode, `default_mode` in `igris.toml`.

| Mode | What it does |
|---|---|
| Default | Your normal Claude Code permission prompts. |
| Accept edits | Edits are accepted automatically. |
| Auto | Claude Code approves routine actions itself and asks you about risky ones. |
| Plan | Claude plans first; you approve the plan, then it implements. |
| Skip permissions | `--dangerously-skip-permissions`. Never a single click or key: you type `skip permissions` to confirm, every run, and a red badge shows while it is active. Use it in a worktree or container you trust. |

## Notifications

Igris tells you when it needs you (a question, a plan to approve, a stalled session), when a phase is done or stuck, and when something fails:

- herdr toasts
- [ntfy](https://ntfy.sh) push to your phone
- Discord webhook

```toml
[notify.ntfy]
topic = "my-igris-topic"          # leave empty to turn ntfy off
token = "env:NTFY_TOKEN"          # optional

[notify.discord]
webhook_url = "env:IGRIS_DISCORD_WEBHOOK"
events = ["needs_input", "phase_done"]   # default: needs_input, session_lost, phase_done, phase_stuck, run_error, verify_failed_limit
```

Messages carry the project, phase, task ID and title and the event, never file contents, diffs or command output. Each channel has a 10 s timeout and one retry; a channel that fails is shown as a warning and never stops the run. Secrets can be `env:VAR_NAME` references, are never logged, and are scrubbed from error messages. ntfy sends urgent events (`needs_input`, `session_lost`) at high priority.

Check your setup with `igris notify test`: it sends a sample of each event to every configured channel (the herdr toast too, when run inside herdr) and prints `ok` or the reason for each failure. `--event needs_input` sends just one.

## The TUI

Igris runs in its own herdr pane: the task list with status and rank, the current task and how long it's been running, and a log. Claude sessions run in their own tabs; press `o` (or click **Open session**) to jump to the current one. Every action is a button you can click or tap, reach with `tab` and the arrow keys, or trigger with its shortcut key; when igris needs an answer (commit? session lost?) it opens a choice dialog, like Claude Code's prompts. The keys: `o` open session, `m`/`M` run/task mode, `p` pause/resume, `d` done, `s` skip (asks for a reason), `r` retry (fresh or continue), `x` stop (asks first), `q` quit, `?` help; `tab` moves between the task list, the action bar and the log, the arrow keys move within them, and `enter` activates. Dialogs open on the safe choice, and `esc` backs out. Click a task (or select it and press `enter`) to see its full text, its dependencies and their states, and the plan's extra columns; `enter` on the log opens the whole log, scrollable with the wheel or the keys. The layout collapses to a single column on small terminals, so it works over SSH from a phone.

Colors follow the terminal: igris detects a dark or light background, and `theme = "dark"` or `"light"` under `[tui]` settles it when the detection is wrong (it can be inside a multiplexer or over SSH). Each rank has its own color; `[tui.rank_colors]` changes them or adds your own ranks, e.g. `opus = "#B48CFF"` or `fable = "220"` (an ANSI color number). Nothing is shown by color alone: statuses are glyphs, ranks are named, and the focused button or row is in reverse video with `›` markers. With `NO_COLOR` set igris draws no color and keeps the bold and reverse video.

The TUI captures the mouse, so selecting text in the terminal needs `shift`+drag; set `mouse = false` under `[tui]` in `igris.toml` to keep plain selection. Start-up questions (an API key in the environment, plan drift, skip-permissions confirmation) are asked as plain prompts before the TUI opens.

`q` quits the TUI without stopping anything. `igris arise` (no phase needed) picks up exactly where it left off: it reattaches to the running session, or, if that's gone, lets you continue its conversation or start a fresh session that is told to check the work already in the tree.

Only one `igris arise` runs per project at a time. If a crashed run left its lock behind, igris says so; `igris arise --force-unlock` clears it.

## Roadmap

- tmux backend (and other multiplexers) for people who don't use herdr
- Homebrew tap

## License

MIT
