<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/brand/igris-logo-dark.svg">
    <img src="docs/brand/igris-logo-light.svg" alt="igris" height="96">
  </picture>
</h1>

<p align="center"><em>Arise.</em> One task, one fresh session, the right rank.</p>

<p align="center">
  <a href="https://github.com/drilonrecica/igris/actions/workflows/ci.yml"><img src="https://github.com/drilonrecica/igris/actions/workflows/ci.yml/badge.svg?branch=master" alt="CI"></a>
  <a href="https://github.com/drilonrecica/igris/releases/latest"><img src="https://img.shields.io/github/v/release/drilonrecica/igris" alt="Latest release"></a>
  <a href="https://pkg.go.dev/github.com/drilonrecica/igris"><img src="https://pkg.go.dev/badge/github.com/drilonrecica/igris.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/drilonrecica/igris" alt="MIT license"></a>
</p>

<p align="center"><strong><a href="https://drilonrecica.github.io/igris/">drilonrecica.github.io/igris</a></strong></p>

<p align="center"><img src="docs/demo/igris-demo.gif" alt="igris running a two-task phase in herdr: each task gets a fresh Claude Code session on the model the plan assigns, sonnet then opus, and the phase completes" width="900"></p>

**igris** runs the tasks in your markdown project plan **one at a time**, each in a **fresh Claude Code session**, started with **exactly the model your plan assigns to that task**.

You write the plan. You decide which tasks need Fable, which need Opus and which are fine on Sonnet. Igris makes sure that's what actually happens: it never runs a Sonnet task on a more expensive model, never lets one task's context bleed into the next, and stops to wait for you whenever a task needs a decision.

> **Status:** v0.1.0, the first release. Linux and macOS, herdr backend only. The full behavior is specified in [`SPEC.md`](SPEC.md).

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
- **[herdr](https://herdr.dev)** — v1 runs sessions in herdr tabs, so everything survives disconnects and you can reconnect over SSH (e.g. from your phone). tmux support is planned. See [herdr setup](#herdr-setup).
- **git** (recommended): igris can commit after each task and warns when the tree is dirty.
- Linux or macOS (amd64 or arm64).

## Install

With Go (the version pinned in `go.mod` or newer):

```sh
go install github.com/drilonrecica/igris/cmd/igris@latest
```

Or download a prebuilt binary from the [Releases](https://github.com/drilonrecica/igris/releases) page: `igris_<version>_<os>_<arch>.tar.gz` for linux and darwin on amd64 and arm64. Check the download against `checksums.txt` (SHA-256), then put the binary on your `PATH`:

```sh
sha256sum --ignore-missing -c checksums.txt    # macOS: shasum -a 256 --ignore-missing -c checksums.txt
tar xzf igris_<version>_<os>_<arch>.tar.gz
install -m 755 igris ~/.local/bin/igris
igris version
```

The binaries are static and not signed in v0.1.0; the checksums guard against a corrupted download.

## herdr setup

Igris starts every Claude Code session in its own herdr tab, so `igris arise` must run **inside a herdr pane** with the herdr server running:

1. Install [herdr](https://herdr.dev) and start it (`herdr`), ideally on the machine where your project lives; reconnect over SSH whenever you like and everything is still running.
2. Once: `herdr integration install claude`, so herdr reports Claude's state (working, waiting for you) accurately. `igris arise` warns when it isn't installed.
3. In a herdr pane, `cd` to your project and run igris (see Quick start).

Outside herdr, `igris arise` exits and says so. `init`, `check`, `phases`, `status`, `done`, `skip` and `notify test` need no herdr (`adapt` does).

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

`igris arise` needs herdr (see [herdr setup](#herdr-setup)); `check`, `status`, `done` and `skip` work without it.

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

A complete small plan is in [`examples/tasks.md`](examples/tasks.md) and a commented config in [`examples/igris.toml`](examples/igris.toml); copy them and run `igris check` and `igris arise P1 --dry-run` to see how igris reads them.

### Different format? `igris adapt`

`igris adapt` converts a plan igris can't read into the canonical format. It opens one Claude Code session (in herdr, mode default) with `adapt.model` from `igris.toml` (`sonnet` by default; `--model opus` for a hard one) and `--plan PATH` for a plan other than the configured one. The session gets the format, `igris check`'s complaints and your `[models]`, writes the converted copy to `.igris/adapt/<plan>.proposed.md`, and finishes with `igris done ADAPT`. It may ask you things in its pane, like any igris session.

It keeps every task, ID, description, dependency, status and model; it only restructures. It never invents models: a task without a usable one gets Model `?` and is listed under `## Adapt notes` at the end, and `igris check` keeps rejecting it until you fill it in. When the session is done, igris validates the proposal and opens a review: the diff of your plan against the proposal (`-` removed, `+` added, long unchanged stretches folded), with the `igris check` result and any problems on top. Scroll with the arrow keys, `pgup`/`pgdn` or the wheel. **Reject** has the focus first, so `enter` changes nothing; `a` or **Accept** replaces your plan after backing it up to `.igris/adapt/<plan>.<timestamp>.bak.md`, and `r`, `esc` or `q` reject (the proposal stays in `.igris/adapt/` for reference). A proposal that doesn't pass `check` yet, e.g. with models left at `?`, asks once more before it replaces anything, and igris then lists what is left to fix. If the plan changed on disk while you reviewed, nothing is replaced. `adapt` can't run while `igris arise` runs in the project, and `Ctrl-C` stops waiting without closing the session.

## Verification and commits

Set `verify` in `igris.toml` (e.g. `verify = "make fmt lint test"`) and igris runs it each time a session says it's done. When it fails, the last 60 lines go back into the same session to fix (as plain text: escape sequences are stripped); after `verify_max_attempts` failures in a row igris calls you instead. If you mark a task done yourself, igris takes your word and skips verify. Keep in mind that `verify` runs your project's own code, as you: a session that can edit files can change what it does.

After a task passes, igris commits everything in the tree (`commit = "ask"`, the default, asks you first; `auto` just commits; `never` leaves git alone). The message comes from `commit_message`, `{{.ID}}: {{.Title}}` by default, with the session's done note as the body.

## Modes

Choose the permission mode in the TUI, per run or per task: press `m` (or click **Mode**) for the run, or select a task and press `M` (**Task mode**) to override it for that task. A change applies to the next session; a running one keeps its mode. A task's mode is, in order: your override, its `Mode` column, the run mode, `default_mode` in `igris.toml`. `claude.extra_args` can't carry model, mode, session or settings flags (`--model`, `--permission-mode`, `--settings`, `-c`/`-r`, …); igris sets those itself and rejects a config that has them.

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

## FAQ

**Does igris use my Claude subscription or the API?**
Whatever Claude Code normally uses for you. Igris starts ordinary interactive `claude` sessions, so they run on your login (Pro/Max) and count against its limits. The one exception is in your environment: if `ANTHROPIC_API_KEY` is set, Claude Code bills the API instead. Igris warns about that at start and asks you to confirm. It never sets that variable, and never reads, prints or stores your Claude credentials.

**Why a fresh session per task?**
A long session fills its context and drifts, and one task's context shouldn't leak into the next. A fresh session gets the task, the plan and your project rules, and nothing else.

**Can igris pick the model for me?**
No, on purpose. The plan's Model column is the only input: igris launches exactly that rank and never a more expensive one. A task without a usable model is a validation error, not a guess.

**What if a task needs me?**
Claude asks in its pane like it always does; igris notices the stalled session, shows it in the TUI and notifies you (herdr toast, ntfy, Discord). Answer in the session, or use **Open session** to jump to it.

**Is skip-permissions safe?**
Only where you'd trust an unattended agent: a worktree or container. It needs a typed confirmation every run, by design.

**What stops a session from changing the plan or igris itself?**
Igris treats sessions as untrusted. `igris.toml` is read once per run; if it changes mid-run you're told and the old settings stay. If the plan changes in a way igris didn't write (a later task's Mode or Model, a Status, task text), igris lists the cells, notifies you and pauses until you resume — your own mid-run edits cost one resume. Session IDs and pane IDs read back from `.igris/state.json` are checked before they become command arguments, and anything a session wrote (done notes, verify output, plan text) is cleaned of escape sequences before it reaches your terminal or a session's pane. What igris can't prevent: a session edits files, and `verify` and git hooks run your project's code as you, so the permission mode is the real boundary.

**I don't use herdr.**
v1 needs it. A tmux backend is on the roadmap.

## Roadmap

- tmux backend (and other multiplexers) for people who don't use herdr
- Homebrew tap
- Signed release artifacts (cosign/minisign)

## Building releases

`make release-local` needs [GoReleaser](https://goreleaser.com/install/). It builds the four release targets (linux and darwin, amd64 and arm64) as static binaries, packs each with `LICENSE` and `README.md`, and writes the archives and `checksums.txt` to `dist/`. It never publishes anything; a release is uploaded to GitHub by hand. What changed in each release is in [`CHANGELOG.md`](CHANGELOG.md); the GitHub release text lives in [`docs/release-notes/`](docs/release-notes/).

The [project page](https://drilonrecica.github.io/igris/) is a static page in [`site/`](site/). `make site` builds it into `_site/` (preview with `python3 -m http.server -d _site`), and `.github/workflows/pages.yml` deploys it to GitHub Pages when it changes on `master`.

`make demo` re-records the GIF at the top of this README with [VHS](https://github.com/charmbracelet/vhs); see [`docs/demo/`](docs/demo/README.md).

## Contributing

Bug reports and pull requests are welcome; see [`CONTRIBUTING.md`](CONTRIBUTING.md). Report security problems privately as described in [`SECURITY.md`](SECURITY.md).

## License

MIT
