# Changelog

All notable changes to igris are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and igris uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Until 1.0, minor versions may change the plan format, config or CLI; such changes are called out here.

## [1.4.0] - Unreleased

igris 1.4 adds two things to 1.2 and breaks nothing: Claude can write your plan (`igris draft`), and a run survives a closed terminal and can be opened again from any terminal (`igris serve`, `attach` and `stop`). There is no 1.3 release; both ship as 1.4. Plans, `igris.toml` and scripts written for 1.0 to 1.2 need no changes ([compatibility](docs/compatibility.md), [SPEC §19](SPEC.md#19-compatibility)). All of it is additive: four commands, a `[draft]` config section, a socket and two files in `.igris/`, and a directory for drafts. `state.json` stays version 3. One behavior changes by design: `q` in the run view asks Detach, Stop run or Cancel instead of leaving at once.

### Fixed

- **igris never starts a copy of itself that isn't igris.** The `igris serve` that `arise` and home start (since 1.4) is refused from a test binary and from a process igris started itself (`IGRIS_SPAWN_DEPTH`), and a serve never starts another serve. The test suite installs an in-process serve, exits at once if its binary was started as `igris serve`, and fails on a process left running; `make test` runs with a timeout.

### Added

- **Detach and attach.** `igris arise` (and home's Arise) starts the run in a background `igris serve` and attaches to it, so closing the terminal or losing an ssh connection only detaches: the run goes on. The start-up confirmations, the skip-permissions phrase included, are asked first as before; the phrase travels in the socket handshake, never as a flag. `arise --no-tui` and `--dry-run` stay in one process with no socket.
- **`igris serve [PHASE]`.** Runs a phase headless with `arise`'s run flags and plain log lines on stdout, hosting the run on `.igris/igris.sock`. It holds the lock, stops on SIGHUP, SIGINT and SIGTERM (sessions stay open; a second signal exits at once), and `--mode yolo` asks for the typed phrase on a terminal and refuses without one. Outside herdr and tmux its sessions open in a detached tmux session `igris-<project>`.
- **`igris attach`.** The run view of the live run, from any terminal: another pane, or ssh from your phone. Several can be attached at once and the first answer to a question wins. `q` asks Detach (focused) · Stop run · Cancel; Ctrl-C, a closed terminal or a dropped connection detach. A lost connection shows `reconnecting…` and returns with a fresh snapshot. A serve of another igris version works, and the header says `served by igris X.Y.Z`.
- **`igris stop`.** Stops the live run over the socket as Stop run does (sessions stay open), waits until it has, and exits 1 when no run is live. For scripts and cron.
- **Home: Attach.** A live run on this host shows **Attach** first (`enter` or `t`); Arise from home starts a serve and attaches.
- **The socket.** `.igris/igris.sock` (0600, in the 0700 `.igris/`; a long path falls back to a socket under `$XDG_RUNTIME_DIR` or a 0700 directory in the temp directory, named by `.igris/igris.sock.path`), speaking protocol 1: newline-delimited JSON, events with the run log's v1 envelope, commands answered `ack` or `error`. On Linux the server checks each peer's uid. Protocol 1 covers all of 1.x. `.igris/serve.log` holds serve's output, cut to 16 MiB.
- **`igris draft goal [TEXT]`.** A session interviews you in its pane (what, who for, constraints, what done means), then writes a proposal. What it learned goes in the plan's `## Draft notes`, and `M0-01` is your task of writing the spec from that summary.
- **`igris draft spec FILE`.** A plan from a spec or PRD: a regular, UTF-8 file of at most 1 MiB inside the project, which the session reads itself. Requirements it leaves out are listed in `## Draft notes`.
- **`igris draft import FILE [--format github|linear|jira]`.** A plan from an issue export: the JSON `gh issue list --json number,title,body,state,labels` prints, or Linear's or Jira's CSV export (detected, or named with `--format`). igris parses it itself (at most 16 MiB and 1000 items) and the session reads the cleaned result; closed issues become `done` rows. igris never contacts GitHub, Linear or Jira.
- **`igris draft extend [TEXT]`.** New phases or tasks for your current plan, also a split one. The session writes only the new part, the review lists only what it adds, and Accept backs up each file it changes to `.igris/draft/` and keeps every other byte. Never during a run.
- **The review.** Every draft ends in the review `adapt` uses; nothing reaches the plan before Accept. Ranks a draft sets are suggestions, ones the session is unsure of stay `?`, and a proposal that fails `check` needs a second confirmation.
- **Home: Draft… (`D`).** A picker of the four sources with the model, progress with Open session and Cancel, then the review. GET STARTED offers it next to Plan from template….
- `[draft] model` (`sonnet` or `opus`, default `opus`), `--model` and `--plan` flags, and a `.igris/draft/` directory. The session runs in `manual` mode and finishes with `igris done DRAFT`.

`goal`, `spec` and `import` write a plan only where none is: pass `--plan PATH` for a new file next to an existing plan, or use `extend`. A spec or an export is text igris didn't write, so the session writes only a proposal and every permission prompt goes to you; see [SPEC §9.1](SPEC.md#91-igris-draft--plans-from-a-goal-a-spec-or-an-export-since-13).

### Security

- A new boundary, the run's socket ([threat model B8](docs/threat-model.md#b8--igris-serve--attached-clients-since-14), [SPEC §16](SPEC.md#16-errors-and-safety)): only the owner's user reaches it; every message is bounded (1 MiB a line, 16 KiB of command text) and shape-checked before it reaches the engine; a client that stops reading is dropped, never stalls the run; `.igris/igris.sock.path` is read bounded and never through a symlink; skip-permissions mode still needs the typed phrase. Accepted: any process of your user can drive a run over the socket, as it can already edit the plan or `igris.toml`.

### Documentation

- A site page [Detach and attach](site/docs/detach-attach.md), the README, a 1.2 to 1.4 section in the [upgrade guide](site/docs/upgrade.md), SPEC §9.1, §13, §14, §15 and §19, the compatibility map (C4, C14) and the roadmap (1.5 parallel, 1.6 remote) updated.

### Verification

- The v1.3 gate ([`docs/check-v13.md`](docs/check-v13.md)), on tmux with Claude Code 2.1.307: a plan drafted from a goal with the interview answered in the pane, and from a spec; imports of GitHub JSON, Linear CSV and Jira CSV exports; an extend of a plan and of a split plan, every other byte kept. Each went through the review: Accept and Reject, the second confirmation for ranks left at `?`, a plan in the way refused, and a file that appeared during the review never overwritten. A `--format` that names another tool than the export's now says which tool the file looks like.
- The v1.4 gate ([`docs/check-v14.md`](docs/check-v14.md)), on tmux with Claude Code 2.1.308: a run started with `arise`, detached with `q`, its terminal killed and reattached from another pane; two clients on one question, the first answer winning; a 1.4.0 client on a 1.4.1 serve and the other way; a headless `serve` in its detached `igris-<project>` session; `igris stop` and SIGTERM; skip-permissions refused without a terminal; a long path on the socket fallback under `$XDG_RUNTIME_DIR` and the temp directory. serve's output now says to answer in `igris attach` where `--no-tui` says to type a command; an attached view outside herdr and tmux no longer offers an Open session it can't open; a serve that ends at once no longer leaves its socket behind, nor beats its starter's snapshot; `attach` and an attached `arise` wait for serve to release the lock when the run ended, so `igris arise` right after finds the project free; and a resumed task's session can be opened from the run view.

## [1.2.1] - 2026-10-21

A security patch: igris 1.2.1 is igris 1.2.0 built with Go 1.27.3, which fixes GO-2026-6809 in `crypto/x509`. No code change, and no change to the plan format, config keys, CLI or file formats.

### Security

- Built with Go 1.27.3 (`toolchain go1.27.3` in `go.mod`). Go 1.27.2's `crypto/x509` didn't reject a set of compromised intermediate certificates on systems whose trust store doesn't already distrust them ([GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809)). igris makes HTTPS requests only to your notification endpoints and, when you ask, to GitHub's releases API (`igris update`); both verify certificates with that code. govulncheck finds no vulnerability igris calls.

## [1.2.0] - 2026-10-21

igris 1.2 adds to what 1.0 promised and breaks nothing: you can open a side session of your own next to a run, split a long plan across files, start a project from a plan template, and check for updates from igris when you choose to. Plans, `igris.toml` and scripts written for 1.0 or 1.1 need no changes ([compatibility](docs/compatibility.md), [SPEC §19](SPEC.md#19-compatibility)). All of it is additive: a command flag, config keys with defaults, plan format 2, `--json` fields, run-log events and a directory in `.igris/`. One thing needs planning: a split plan needs igris 1.2 on every machine that runs it ([upgrade guide](site/docs/upgrade.md#from-11-to-12)).

### Added

- **Side sessions.** `S` on home and in the run view opens a Claude Code session of your own in a pane next to the run's, for a question or a look into something. It is not a task: no `igris done` watch, no attempt, no Status, no Needs you, no notification, no timeout. The dialog offers a preset (**Blank**, **About this task**, **igris problem**) with an editable first prompt, a model and a mode (`default`, `manual`, `accept`, `auto` or `plan`; never skip-permissions), and during a run the shared tree (with a warning, `manual` mode preselected) or its own worktree. The header shows `side N`, home shows `SIDE N open`, and the **Side sessions** page lists them with focus and close. Quitting igris never closes them; the next home or run view reattaches each to its own pane. Inside one, `igris done`, `skip` and `reset` refuse (`IGRIS_SIDE`). New `[side]` settings: `model`, `mode`. Records live in `.igris/side/`; `state.json` stays version 3. Not with `--no-tui`, and no CLI in 1.2.
- **Side session usage.** A side session's transcript is read as a task's (counts and model ids only) and logged as `side_usage` when it ends, with `side_opened` and `side_closed` lines and a `side` field. It is never an attempt and never audited. `igris usage` shows a table of side sessions of its own, outside every total, and `--json` gets an additive `side` array.
- **Split plans (plan format 2).** `<!-- igris:include PATH -->` on a line of its own pulls another file's phases into the plan, in a plan that declares `<!-- igris:format 2 -->`. One level, paths relative to the plan and inside the project (symlinks resolved), at most 32 files and 16 MiB, IDs unique across files, dependencies and ranges across files. `check`, `phases`, `status` and `arise --dry-run` read a split plan as one and report every problem as `file:line`. `arise` writes each Status cell in the file its row is in, leaving every other byte alone; the planwatch re-reads every file, so an edit to any of them, or an include added, removed or moved, holds the run. The task prompt names the file the row is in (`TaskFile` in a `prompt_template`), and a worktree run lets sessions read each file. `igris adapt` refuses a split plan. A fuzz target covers the include loader. igris 1.0 and 1.1 refuse a format-2 plan.
- **Plan templates.** `igris init --template go-cli|web-app|library` writes a starter plan when there is none: phases `M0`, `M1`, … each ending in a gate, a first task for you (write the spec) that everything waits on, and a decision task in plan mode. Ranks are suggestions, said so in the plan and by `init`. It never overwrites a plan or touches an existing `igris.toml`. Home's **Example plan** is now **Plan from template…**. The files are in [`examples/templates/`](examples/templates/), and `make compat` captures them.
- **`manual` mode.** A new Mode value, `--mode`, `default_mode` and `[side] mode` value that passes `--permission-mode manual`: every permission prompt goes to you, whatever Claude Code's own default is. It comes after `yolo` in the TUI's mode picker, so the first five keep their number keys. `default` is unchanged and still passes no permission flag, so it follows Claude Code's own default mode, which is auto if you opted into auto as Claude Code's default; use `manual` for prompts either way. No `plan:manual`, and `[plan_mode] then` doesn't take it. igris 1.0 and 1.1 reject a plan, config or state that uses it.
- **igris in PATH check.** `check`, `arise` (and `--dry-run`), `doctor` and home's health warn, first, when the `igris` in `PATH`, which sessions run `igris done` with, is missing or is another binary than the running one: `sessions run igris done with /path/to/igris (1.1.0), not this igris (1.2.0); put this igris first in PATH`. A warning only, never a confirmation, and it never fails `check --strict`; `--json` gets the check ID `igris-path`.
- **Check for updates.** `igris update [--check] [--yes]` and `u` (**Updates**) on home ask GitHub's releases API for the latest release and, when it is newer and you say yes, update igris with the package manager that installed it: `brew upgrade igris` for Homebrew, `go install github.com/drilonrecica/igris/cmd/igris@vX.Y.Z` with `GOBIN` set to the running binary's directory for `go install` (with a hint to retry with `GOPROXY=direct GONOSUMDB=github.com/drilonrecica/igris` when the checksum database doesn't know the tag yet), and the release link and verify steps for an archive. `--check` only prints the versions and the method; without a terminal, `--yes` is needed. A development build is never updated, home offers no Update while a run holds the project, and after an update igris says to restart (`Updated: igris in PATH is now X.Y.Z. Quit and start igris again.`). It is the only network call igris makes besides your notifications, and only when you ask: no check at start or on a timer, no setting. A new command, additive.
- `--json`: `file` on each phase and a `files` list in `check`, `phases` and `status`; `side` in `usage`. Schemas in [`docs/json/`](docs/json/) updated.
- Docs: site pages for [side sessions](site/docs/side-sessions.md), [split plans](site/docs/split-plans.md) and [plan templates](site/docs/templates.md), a 1.1 to 1.2 section in the [upgrade guide](site/docs/upgrade.md), two [troubleshooting](site/docs/troubleshooting.md) entries, and the threat model for both (below).
- The update dialog's archive steps can be copied whole with **Copy steps (y)**.

### Changed

- `igris usage`: an attempt that ran on several models is counted under each model it used, not in one row keyed `a, b`. A run with no `usage` lines says why (made before 1.1, or `[usage] enabled = false`) instead of printing a table of zeros.
- `igris init` and home's init step print the template step when one was chosen; without `--template` the output is unchanged.

### Security

- The update check reads at most 1 MiB of GitHub's answer within 10 s and uses only a `vX.Y.Z` tag, showing none of its text; the update command is built from that version only, as argv with a timeout, without the notification secrets' variables, and igris never replaces its own binary (the package manager checks what it installs). SPEC §1 now names GitHub's releases API, when the owner checks for updates, as the one network call besides notifications. ([threat model](docs/threat-model.md) R51–R53)
- A session can add or change an include like any plan edit, and a side session is not exempt from the plan hold or the config snapshot. Includes are bounded, kept inside the project and read as regular files; the writer re-hashes every file before it renames and gives up rather than overwrite one that changed. `IGRIS_SIDE` stops an honest mistake, not a session that unsets it. Side session records are read bounded and never through a symlink, a side session reattaches only to the pane started for it, and its first prompt is cleaned and bounded. See [`docs/threat-model.md`](docs/threat-model.md) (rules R41–R50, accepted risks 18–20).

### Verification

- The v1.2 gate ([`docs/check-v12.md`](docs/check-v12.md)): a side session on herdr and on tmux during a run, a plan edit from it holding the run and its usage shown apart; a split plan run end to end with Status written in the included file; each template through `init`, `check` and `--dry-run`; the update check against GitHub's real releases API for every install method, blocked during a run, and a real Homebrew upgrade (1.0.0 to 1.1.0) driven by `igris update --yes`. `manual` mode and the `igris done` allow rule were verified with Claude Code 2.1.305.

## [1.1.0] - 2026-10-20

igris 1.1 adds to what 1.0 promised and breaks nothing: you can approve a plan-mode session's plan from igris, see the tokens each run, task and model used, and be told when a task ran on a model its rank doesn't name. Plans, `igris.toml` and scripts written for 1.0 need no changes ([compatibility](docs/compatibility.md), [SPEC §19](SPEC.md#19-compatibility)). All of it is additive: new config keys with defaults, a command, `--json` fields and a schema, run-log events and a notification event.

### Added

- **Plan approval from igris.** When a plan-mode session presents its plan, igris shows it in a dialog and marks the task **needs you**: **Approve**, **Reject with feedback** (Claude revises and presents again) or **Answer in the pane**. Claude Code's own dialog stays open in the pane; whichever you answer first wins. After `[plan_mode] approve_timeout` (30 minutes by default; `"0"` turns it off), or when igris is gone, the plan waits in the pane as before. Every plan is kept under `.igris/plans/`, logged as `plan_presented`, `plan_approved` and `plan_rejected` lines (the file and a hash, never the text), and the approved plan goes into the commit body (`{{.Plan}}` in `commit_message`). Not with `--no-tui`. New `[plan_mode]` settings: `then`, `approve_timeout`.
- **Mode after plan approval.** `plan:default`, `plan:accept`, `plan:auto` and `plan:yolo` in a task's Mode column, `--mode` and the TUI name the mode the session continues in once you approve; plain `plan` uses `[plan_mode] then`. The switch is made through the hook's decision, never by typing into the pane. `plan:yolo` needs the same typed confirmation as `yolo`, every run.
- **Token usage.** igris reads the token counts and model ids (never the messages) from each session's transcript and its subagents' files, about every 10 seconds and when the attempt ends, and logs a `usage` line per attempt. These are the tokens in the transcripts, never a cost. `[usage] enabled = false` turns it off.
- **Rank audit.** If a session's main thread runs on a model its rank doesn't resolve to, the task is marked **needs you** with `RANK MISMATCH` on its card, a `rank_mismatch` notification (a new default event) is sent and a `rank_mismatch` line is logged, once per attempt. igris never stops or switches the session. `usage` lines record `ok`, `mismatch`, `unchecked` or `unavailable`.
- **`igris usage [RUN | --since D] [--json]`**: tokens by task, model and rank for a run or across runs, the top consumers, the audit result and the last recorded quota. `igris report` gets a Tokens column and a `⚠ rank` mark; the TUI's run card shows the current task's counter.
- **Subscription quota** (opt-in). With `[usage] statusline = true`, igris's own status line runs in task panes and records your 5-hour and 7-day limits in `.igris/quota.json`; the TUI header shows them and `igris usage` prints the last quota. Off by default because it replaces your status line in those panes; without it, or without a subscription login, the quota is `unavailable`. igris never reads credentials or calls an API for it.
- **Parallelism report.** `igris phases` and `igris arise --dry-run` show, per phase, the critical path, the width of the dependency graph and the sets of tasks that could run together. Read-only: igris still runs one task at a time.
- `--json`: [`docs/json/usage.schema.json`](docs/json/usage.schema.json); a `parallelism` object per phase in `phases --json`; `usage`, `sub`, `models` and `audit` per task in `report --json`.
- Docs: a [usage and plan approval](site/docs/usage.md) page on the site and a 1.0 to 1.1 section in the [upgrade guide](site/docs/upgrade.md).

### Changed

- `rank_mismatch` joins the default `events` of every notification channel. A channel that sets `events` itself is left as it is, and the config hash of one that doesn't is unchanged, so an interrupted run resumes without a "config changed" notice.
- Run log, `.igris/` and the hook gained fields and files (`runs.jsonl` events above, `agent-state/` records the transcript path, `plans/`, `quota.json`): older readers ignore them ([`docs/runlog.md`](docs/runlog.md)). `state.json` stays version 3.

### Security

- A session can write `.igris/` like everything else in the project, so it could forge an approval of its own plan, its transcript or `quota.json`. It can't choose the mode the session switches to, and igris reads each file bounded and shape-checked. Details: [`docs/threat-model.md`](docs/threat-model.md).

### Verification

- Plan approval was verified against Claude Code 2.1.304 (the `ExitPlanMode` permission hook, its decision fields, the pane and timeout fallbacks); the decisions are in [`docs/decisions.md`](docs/decisions.md). The gate run (a real plan-mode task approved from igris on herdr and tmux, a usage report of a real run, a forced rank mismatch caught) is recorded in [`docs/check-v11.md`](docs/check-v11.md).

## [1.0.0] - 2026-10-20

igris 1.0. From here until 2.0, igris keeps its promises: the plan format, `igris.toml`, the CLI and its exit codes, `--json`, `.igris/`, the run log, the template variables, the generic webhook and the task-hook environment don't break. 1.x may add to them (new optional columns, config keys with defaults, commands, flags, JSON fields, run-log events, template variables), never remove or change them. What is covered, and the test that keeps each surface honest, is in [`docs/compatibility.md`](docs/compatibility.md) ([SPEC §19](SPEC.md#19-compatibility)).

1.0.0 is [1.0.0-rc.2](#100-rc2---2026-10-20) with no further code changes. If you ran an rc, nothing more is needed. Coming from 0.x, install 1.0.0 and run `igris config migrate` once in each project; plans need nothing. The full upgrade path is [`site/docs/upgrade.md`](site/docs/upgrade.md) (on the site: `/docs/upgrade.html`).

What got igris here, release by release: the 1.0 contract, the removal of `claude.command`, the `--json` and exit-code changes, `igris config migrate` and the compatibility suite are in [1.0.0-rc.1](#100-rc1---2026-10-14); the stricter config validation, the final security review's fixes and the docs pages are in [1.0.0-rc.2](#100-rc2---2026-10-20). Both candidates are summarized under **Migration** there, and the **Changed** and **Removed** lists are what to check when upgrading from 0.x.

### Verification

- **Soaked on real projects** with 1.0.0-rc.1, on Linux and macOS, on herdr, driven over Termius, with `adapt` used on a non-canonical plan.
- **Waivers.** tmux was not soaked by hand; it is covered by the backend conformance suite in `make test`. rc.2, which changed reattach and config confirmation, was approved without a separate soak: those fixes are covered by their regression tests and the conformance suite, not by a real resume on herdr or tmux.
- A **final security review** before 1.0 ([`docs/threat-model.md`](docs/threat-model.md)); the compatibility suite (what every release from 0.1.0 on wrote still loads) and the fuzz targets run in CI on every push.

## [1.0.0-rc.2] - 2026-10-20

The second release candidate for igris 1.0. It carries the fixes from the final security review, which make a few configs that rc.1 accepted invalid (**Changed**, below; `igris check` names the key), and the docs pages on the project site. Plans need no changes.

### Added

- Docs pages on the project site (`/docs/`): install, quick start, plan format, config reference, modes, notifications, adapt, running several projects at once, FAQ, troubleshooting and the upgrade guide from 0.x.
- [`docs/config.md`](docs/config.md), every `igris.toml` key with its default, generated from the code (`make config-ref`); a test fails when it drifts.

### Changed

Configs that 1.0.0-rc.1 accepted and the next release rejects (from the final security review; [`docs/decisions.md`](docs/decisions.md), V07-P1 amendment of 2026-10-20). Nothing else in the 1.0 contract changes. `igris check` names the key to fix.

- `claude.extra_args` with a flag that changes the session's tools, permissions, directory, system prompt or shape: `--allowedTools`, `--disallowedTools`, `--tools`, `--add-dir`, `--permission-prompt-tool`, `--bare`, `--setting-sources`, `--plugin-dir`, `--plugin-url`, `--mcp-config`, `--strict-mcp-config`, `--agent`, `--agents`, `--system-prompt`, `--system-prompt-snapshot`, `--ide`, `--worktree`/`-w`, `--fork-session`, `--no-session-persistence` (and the kebab-case spellings).
- A control character in a `claude.extra_args` value or in a `[models]` rank or model.
- `notify.ntfy.server` or `notify.gotify.server` with a `user:password@` part; use `token`.
- An `igris.toml` that isn't a regular file or is over 1 MiB, and a plan over 16 MiB.
- `arise` asks before using an `igris.toml` that changed during the previous run; unattended scripts that hit this exit 1 until someone confirms once.

### Security

The final security review before 1.0 (V10-05, [`docs/threat-model.md`](docs/threat-model.md)). Nothing in the 1.0 contract changes; `state.json` gains an optional `config_changed` field within version 3 and the run log a `config_changed` event.

- A session could rewrite `state.json` so that the next `igris arise` reattached to any pane of the tmux server (your shell, say) and typed the recorded prompt there. Backends now reattach only to the pane they started for the task (tmux: the pane's start command; herdr: the task's agent name), and the held task prompt is built again from the plan instead of being read back.
- An `igris.toml` edited during a run (which the run itself ignores) was used by the next run without a word. It is now recorded, and the next run asks before using it; `check` and `doctor` warn meanwhile.
- `claude.extra_args` also rejects flags that change the session's permissions, tools, directory or system prompt (`--allowedTools`, `--add-dir`, `--bare`, `--setting-sources`, …), and control characters in any argument or `[models]` value.
- `doctor` reads `.claude/settings.json` too, and warns about a rule that allows every shell command or a `defaultMode` that bypasses prompts.
- The plan, `igris.toml`, `state.json` and the lock are read as bounded regular files, so a FIFO in their place can't hang igris. Stray signals are reported once per task, not once per rewrite.
- Text cleaning also strips tag characters, invisible operators and other format characters that a terminal doesn't show but a model reads; `[models]` values are cleaned before they are drawn; the standalone adapt review cleans like the rest.
- `notify.ntfy.server` and `notify.gotify.server` reject a `user:password@` part (it would be shown); the site generator escapes link targets; webhook receivers are told how to handle a replayed payload.

## [1.0.0-rc.1] - 2026-10-14

The first release candidate for igris 1.0. It adds no features: it decides, writes down and tests what 1.0 promises not to break, and makes the few breaking changes that had to come before that promise. Plans need no changes. Configs written by `igris init` need one command, `igris config migrate`, and scripts that read `--json` need small updates (Migration, below).

### The 1.0 contract

From 1.0 until 2.0, these keep working unchanged ([SPEC §19](SPEC.md#19-compatibility)); 1.x may add to them (new optional columns, config keys with defaults, commands, flags, JSON fields, run-log events, template variables) but never removes or changes them:

- the **plan format**, format 1: [`docs/plan-format.md`](docs/plan-format.md);
- **`igris.toml`**: its keys, defaults and validation;
- the **CLI**: commands, flags, arguments and exit codes (`0` success, `1` failure, `2` usage error, `3`–`9` reserved; SPEC §14);
- **`--json`** of `check`, `phases`, `status`, `history`, `report` and `doctor`: [`docs/json/`](docs/json/);
- `igris done`, `skip` and `reset` and the **signal files** they write;
- **`.igris/`**: its layout, `state.json` (version 3, older versions migrated on read) and the run log (`"v": 1`, [`docs/runlog.md`](docs/runlog.md));
- the **template variables** of `prompt_template`, `commit_message` and notification templates;
- the **generic webhook**: payload, `X-Igris-Event` and `X-Igris-Signature`;
- the **task-hook environment**: `IGRIS_TASK_ID`, `IGRIS_PHASE`, `IGRIS_RANK`, `IGRIS_MODEL`, `IGRIS_RESULT`.

Not part of it: the TUI, the default prompt's wording, text output (`--json` is the machine interface), the hidden `hook` and `__complete` commands, the contents of `.igris/agent-state/`, `hooks/`, `prompts/`, `config/` and `igris.lock`, the text sent to ntfy, Discord, Slack, Gotify and the backend, and the Go packages.

### Removed

- **`claude.command`** in `igris.toml`. It was deprecated in 0.1.2 and never read, since igris always starts `claude` from your `PATH`. 0.1.2 said it would go in 0.2, which didn't happen; it goes now, before 1.0. Every `igris.toml` that `igris init` wrote before 0.7 has it, so after upgrading `igris check` and `igris arise` stop with `claude.command was removed in 0.7`: run **`igris config migrate`** once to delete the line. The config's hash doesn't change, so an interrupted run resumes after the migration.

### Changed

- **`--json` output, breaking** (the last change before the 1.0 freeze, SPEC §19). Scripts that read `--json` need these updates:
  - Every output starts with `"v": 1`. It stays 1 for all of 1.x, which only adds fields.
  - `doctor --json` prints an object, `{"v": 1, "checks": [...]}`, instead of a bare array.
  - `status --json` leaves out a task's `rank` and `mode` when the cell has none, instead of `"—"`.
  - The `counts` key `"in progress"` (`phases`, `status`) is now `"in_progress"`. A task's `status` value is still `"in progress"`.
  - `status --json` lists the run's pending signals as `{"task": "M1-03", "action": "done"}` objects instead of `"M1-03 done"` strings, and unreadable signal files as a count, `signals_unreadable`, instead of an `"N unreadable"` entry.
- **`.igris/state.json` is version 3** (SPEC §13). Older state files (versions 1 and 2, igris 0.2–0.6) are read and upgraded, so an interrupted run resumes after the upgrade. A state file from a newer igris now says to upgrade igris, and a damaged one says it is damaged, instead of both saying "unsupported version". igris 0.6 and older can't read a version-3 file: finish the run, or delete `.igris/state.json`, before going back to an older igris.
- **Exit codes, settled for 1.0** (SPEC §14): a malformed task ID in `igris done` and `igris skip`, and an unknown `igris notify test --event`, are usage errors now (exit 2, was 1), as in every other command. `igris adapt` stopped with Ctrl-C exits 0 (was 1), as `igris arise` does. `igris version` with an argument exits 2.
- **Help**: `igris help COMMAND` shows that command's help (it showed the general help). A command's help (`-h`) goes to stdout and starts with its synopsis, for every command.

### Added

- **`igris config migrate`** removes deprecated settings from `igris.toml`: it shows each line it would remove, asks (`--yes` doesn't), keeps a backup in `.igris/config/`, and changes nothing else in the file, comments included. Today that is `claude.command`, which every `igris.toml` written by `igris init` before 0.7 has. `igris check` and `igris doctor` point to it.
- **An exit-code table** in SPEC §14: `0` success, `1` failure, `2` usage error, `3`–`9` reserved, and what exits `1` for each command. It is part of the 1.x contract.
- **The plan format on its own page**, [`docs/plan-format.md`](docs/plan-format.md): format 1, with every rule and an example plan (a test keeps the example valid), for people and tools writing plans. It is what 1.x promises; the leniency igris always had (Mode and Verify values in any case, phase IDs compared without case, tabs around header names) is now written down as part of it.
- **An optional format marker**, `<!-- igris:format 1 -->`, before a plan's first `##` heading. Plans without one are format 1, always. A plan marked with a format igris doesn't know gets one error, `plan format N needs a newer igris`, and `check`, `doctor` and the home screen say to upgrade instead of offering `igris adapt`.
- **A compatibility suite** (`testdata/compat/`, run by `go test` and CI): the configs, plans and signal files every release from 0.1.0 to 0.6.0 wrote, captured by building each release from its tag, plus the state files and run logs of each format. All of them keep loading; `make compat TAG=vX.Y.Z` adds each new release.
- **JSON schemas** for every `--json` output in [`docs/json/`](docs/json/), checked against igris's own output in its tests.

### Migration

- **Run `igris config migrate` once in each project** whose `igris.toml` came from `igris init` before 0.7 (or that sets `claude.command`). It shows the one line it removes, asks, and keeps a backup; until then `check`, `arise` and `doctor` stop with `claude.command was removed in 0.7`. The config hash stays the same, so a run interrupted under 0.6 resumes.
- **Scripts that read `--json`**: read `doctor`'s checks from `.checks`, the `in_progress` count key, signals as `{task, action}` objects, and treat a missing `rank` or `mode` as none. Ignore fields you don't know: 1.x adds some.
- **Scripts that test exit codes**: test for non-zero, or for `2`, never for exactly `1`. A malformed task ID in `done`/`skip` and an unknown `notify test --event` now exit `2`.
- **Plans need nothing.** The optional `<!-- igris:format 1 -->` marker is just that: optional.
- **Going back to 0.6** after a run under this release: finish the run, or delete `.igris/state.json`, since 0.6 can't read state version 3.

## [0.6.0] - 2026-10-14

igris v0.6 is about trust. Releases are signed and come with an SBOM, a run can work on a branch of its own so your checkout stays as it is, the commands igris runs no longer see your notification secrets, and the threat model is written down, reviewed and tied to tests. It also stops calling for you while Claude is only waiting on background work.

### Added

- **Signed releases** (SPEC §18): from 0.6.0, `checksums.txt` and the release tag are signed with the igris release key, an SSH key. `checksums.txt.sig` is the signature; [`allowed_signers`](allowed_signers) and the README hold the key. Checking a download needs only `ssh-keygen`, see [Verifying a download](README.md#verifying-a-download).
- **An SBOM per release archive**: `<archive>.sbom.json` (SPDX JSON, made by syft) lists the modules each binary is built from, and is listed in the signed `checksums.txt`.
- **Worktree mode** (SPEC §6.8): `igris arise --worktree` (or `[run] worktree = true`) runs the sessions, verify, task hooks and commits in a git worktree `.igris/worktrees/<run-id>` on a new branch `igris/<run-id>`, so your checkout and branch stay as they are. The plan stays in your checkout, where igris updates it as usual. An interrupted run continues in its worktree; a completed one removes it when nothing is uncommitted and keeps the branch. igris never merges: it prints the `git merge` command. `--dry-run --worktree` shows where the run would work. See [Worktree mode](README.md#worktree-mode).
- **`[run] env`**, an allowlist of the environment variables verify commands and task hooks get (names, or prefixes like `LC_*`; `PATH` and `HOME` always pass). Unset, they get igris's environment as before, minus the secrets' variables (see Security).
- **Waiting on background work is not "needs you"** (SPEC §6.3): when Claude ends its turn with a background task (tests on a simulator, a long build) or a scheduled wakeup pending, the card says `waiting on background work (4m)` and nothing is raised until `waiting_limit` (new, default `30m`). Claude Code's `Stop` hook reports the pending work; with herdr, igris uses it even though herdr shows the turn as finished.
- **One reminder before Needs you**: an agent that stops without `igris done` and with nothing pending gets one message first (finish with `igris done`, or say what it waits for); Needs you follows if it stays idle. Never for a session waiting on a permission or a question. `nudge = false` turns it off.
- **Hover in the TUI**: with the mouse on, the button, row or dialog option under the pointer is underlined, on every screen. Underline is no color, so it shows under `NO_COLOR`; hovering never moves the focus. `[tui] mouse = false` turns it off with the rest of the mouse support.
- **A threat model**, [`docs/threat-model.md`](docs/threat-model.md): what igris protects and from whom, the boundaries and their threats, the accepted risks, the security reviews, and the test that enforces each safety rule (a test keeps that map true).

### Security

- **The commands igris runs never get the variables your notification secrets come from** (the `env:` references in `igris.toml`): verify, task hooks, git, and the start-up git checks. A session writes the code verify runs and sees its output, and `git status` can run a program a repository's config names.
- **Second security review** ([`docs/threat-model.md`](docs/threat-model.md#reviews)), 12 findings, 10 fixed:
  - igris checks more of what it reads back from `.igris/state.json`, which a session can rewrite: the worktree (only one igris made, never a symlink), the current task's ID, mode and verify attempts.
  - Everything igris prints to stderr is cleaned of escape sequences.
  - A tmux pane's process can no longer make igris think the session is lost.
  - The verify excerpt sent back to a session is capped at 16 KiB.
  - At most 256 signal files are read per poll.
  - `check` and `arise` warn about a webhook URL from an `env:` reference that is plain http.
  - A secret reading `env:PATH` or `env:HOME` is a config error.
- **In worktree mode a session gets only the `igris done` allow rules your project's `.claude/settings.local.json` already has**, plus reading the plan, which is outside the worktree.
- **Built with Go 1.27.2**, now pinned in `go.mod`. Releases up to 0.5.1 were built with Go 1.27.1, which has 8 standard-library advisories fixed in 1.27.2 (GO-2026-6603, -6605, -6607, -6608, -6610, -6611, -6613, -6617). igris only makes outbound HTTPS requests to the notification endpoints you configure, so the server-side ones don't apply, and the client-side ones need a hostile notification server or a broken TLS connection.

### Changed

- CI runs govulncheck on every push and weekly, a short run of every fuzz target, and a check that the release build is reproducible on Linux and macOS.
- The settings file igris passes each session with `--settings` held hooks only; in worktree mode it also holds the allow rules above (SPEC §7.4).
- The README says that on a public ntfy server without a token the topic works like a password: use a long random one, or set a token.

### Migration

- **No plan or config migration is needed.** `waiting_limit`, `nudge`, `[run] worktree` and `[run] env` are optional, and a config that doesn't set them keeps its config hash, so a run interrupted under v0.5 resumes without the "igris.toml changed" notice.
- **`state.json` is checked more strictly.** A file whose current task, mode or verify attempts, or worktree, isn't one igris wrote (a session's or a hand edit) is reported as damaged, with "delete the file to start a new run". A v0.5 file igris wrote reads as before; `worktree` is a new optional field.
- **Releases are signed from 0.6.0**, and earlier ones stay checksums-only. Homebrew and `go install` work as before.
- **Builds use Go 1.27.2.** `go install` fetches it on a machine with an older Go (`GOTOOLCHAIN=auto`).
- **Going back to v0.5** is safe for a run without a worktree; v0.5 doesn't know worktree mode and would run a resumed worktree run in the project root.

## [0.5.1] - 2026-10-08

A patch for small bugs found at the v0.5 gate and its review: a first prompt that stays unsubmitted, blank tail rows, `report` help and completion, a second Ctrl-C, an external interrupt that could hang the TUI, and reading a FIFO or huge run log. No change to the plan format, config keys or file formats.

### Fixed

- A session's first prompt that stays pasted but unsubmitted (idle 15 s, hooks never saw `UserPromptSubmit`) gets Enter pressed once in its pane, with the feed warning `the prompt didn't seem submitted; sent Enter once`; the prompt text is never re-sent, and **Needs you** follows if that doesn't help (SPEC §6.3, `backend.Submitter`).
- Live tail: a line wider than the card, such as Claude Code's right-aligned `◐ medium · /effort`, loses its leading spaces instead of showing as a blank row ending in `…` (SPEC §15.3).
- `igris report -h` and `igris history -h` show the positional `RUN` argument, and shell completion offers run IDs from `.igris/runs.jsonl` after `igris report`, newest first (SPEC §14).
- A second Ctrl-C exits at once on every path: while `arise` or a run started from home winds down it prints `stopping… (Ctrl-C again to quit now)`, and `adapt` and `notify test` exit on a second SIGINT too (SPEC §13, §15).
- An external SIGINT or SIGTERM (`kill -INT`, `kill`) no longer hangs the home screen, the run view or the `adapt` review now and then with the alternate screen left on: Bubble Tea's own signal handler is off, igris handles both signals itself.
- Reading `.igris/runs.jsonl` (`history`, `report`, run-ID completion, the home screen, the ETA) no longer blocks on a FIFO in its place and no longer loads a huge log whole: the log must be a regular file, and only its last 16 MiB are read, with the note `runs.jsonl is over 16 MiB; only its newest runs are read` (SPEC §13, [`docs/runlog.md`](docs/runlog.md); a reader limit, the format is unchanged).

## [0.5.0] - 2026-10-08

igris v0.5 shows you what a run is doing and what it did. The run view gets the phase's progress, an estimate for the current task and a live tail of its session; `igris report` turns a finished run into markdown for a PR description; and the run log is a documented, versioned format. Notifications reach Slack, Gotify and any URL through a signed webhook, with your own message templates, quiet hours and digests. Everything is additive: a v0.4 plan and `igris.toml` mean what they meant before (the new channels, quiet hours and digests stay off until you configure them; only the live tail is on by default), and a v0.4 `runs.jsonl` still reads; a run interrupted under v0.4 resumes without a config-changed notice.

### Added

- **`igris report [RUN] [--json]`** (SPEC §14): one run as markdown, to paste into a PR description or a journal: its run ID, start, end and outcome, phases and slice, counts (done, skipped, unfinished, commits) and how long igris waited on you, then per phase a table with each task's result, duration, attempts, verify results (`fast ✗ ✓`), short commit SHA and needs-you time, the done notes and skip reasons, and a `claude --resume <uuid>` per agent session you can reopen. `RUN` is `1` (the newest, the default), `2`, … or a run ID from `igris history`; `--json` prints one object. Needs-you time is the time igris waited on you: an idle or blocked wait ends at the session's `igris done`, a `task_overdue` or verify-limit wait once the agent works again, a changed plan's from when the run holds until you resume; a repeated wait (a second skip request) counts once. Text from the log is markdown-escaped, so a title or note can't make links or formatting; a known duration under a second shows as `1s`. Read-only, never creates `.igris/`; exits 1 when there is no such run (any index past the end), 2 for a malformed `RUN`.
- **Run log v1** (SPEC §13, [`docs/runlog.md`](docs/runlog.md)): every line igris writes to `.igris/runs.jsonl` has `"v":1`, the run ID (`20261008-091500-3fa2`), the task's attempt, its phase, title and owner, the Claude session UUID, the verify profile, the full commit SHA and durations where they apply. New events: `task_retried`, `needs_you` with a `reason` (`idle`, `blocked`, `task_overdue`, `verify_limit`, …) and `needs_you_clear`. `docs/runlog.md` documents every field and event, the compatibility rules and an example line per type. A task reset and started again in the same run keeps counting its attempts.
- **Progress in the run view** (SPEC §15): the header shows `phase M1 · 7/12 · 58% ██████░░░░` (narrow: `M1 · 7/12 · 58%` and a shorter bar), counting the run's tasks of the current phase, or the slice's (labelled `slice`) for `--only`/`--from`/`--until`.
- **ETA** for the current task: with at least 3 finished tasks of its rank in `runs.jsonl` (the 20 most recent, started fresh in their run), the card adds `· ≈ 14m left` from their median, or `· over ≈ 18m typical` once past it, also for a run started from the home screen. Always approximate; no phase ETA.
- **Live tail** (SPEC §11.1, §15): the current-task card shows the session's last 6 lines (3 on a narrow terminal) above Claude Code's input box, read every `poll_interval` through `herdr agent read --format text` or `tmux capture-pane`, cleaned of escape sequences and cut to the card's width. It is only drawn: never logged, notified, stored or reported. `[tui] tail = false` turns it off and igris then never reads the pane. Backends implement it through the optional `backend.Tailer`, with a conformance case.
- **Slack** (`[notify.slack] webhook_url`): an incoming-webhook message, with `&`, `<` and `>` escaped so a task title can't ping `@channel` or add links.
- Discord messages escape `[` and `]`, so a task title can't make a masked link (`[text](url)`) that shows one address and opens another; ntfy's default body is cut to its 4096-byte limit like a template's.
- **Gotify** (`[notify.gotify] server, token`): a push to `<server>/message` with the token in the `X-Gotify-Key` header, never in the URL; priority 8 for urgent events, 3 for `task_done`, 5 for everything else.
- **Generic webhook** (`[notify.webhook] url, secret`): a JSON POST with every key present, `{"v":1,"event","project","phase","task","title","what","run","at","urgent","text"}`, the event in `X-Igris-Event` and, with a secret, `X-Igris-Signature: sha256=<hex>`, the HMAC-SHA256 of the raw body. A digest lists at most 50 held messages in `messages`, with the rest counted in `truncated`.
- **Message templates**: `template` on ntfy, Discord, Slack, Gotify and the webhook, a Go `text/template` over `.Event`, `.Project`, `.Phase`, `.TaskID`, `.Title`, `.What`, `.RunID` and `.At`. It is tried on a sample message when `igris.toml` loads, so a typo is a config error naming the channel; its output is cleaned and cut to the channel's limit. Variables, `if`/`else` and `with` only: `range`, `define`, `template` and `block` are config errors, so a template can't loop.
- **Quiet hours**: `[notify] quiet = "22:00-07:00"` (local time, may cross midnight) holds messages to the remote channels, except the `break_through` events (default `needs_input`, `session_lost`, `task_overdue`); each channel gets one digest when the window ends, and whatever is still held when the run stops. The herdr or tmux toast is never held.
- **`[notify] task_done_digest`**: `N` (≥ 2) sends one `task_done` message per N finished tasks (`3 tasks done: M1-01, M1-02, M1-03`), `"phase"` one per phase; the rest is flushed at phase end and run stop. An invalid value is reported together with the file's other problems.
- `secret`, `token` and URL keys of the new channels accept `env:VAR` references, are never logged and are scrubbed from errors (the longest secret first); `igris doctor` lists the new channels and `igris notify test` sends to them (ignoring quiet hours and `task_done_digest`).
- Shell completion for `report` and its `--json` flag.
- `igris check` (and `arise`, `doctor`) warns when ntfy with a token, Gotify or a webhook `url` written in `igris.toml` uses plain `http://`.
- `examples/igris.toml` lists `[tui] tail`, the `[notify]` quiet-hours and digest keys, the Slack, Gotify and webhook channels and a template.

### Changed

- **Text igris didn't write is cleaned of bidi controls and zero-width characters too** (U+202A–U+202E, U+2066–U+2069, U+200E, U+200F, U+061C, U+200B, U+2060, U+FEFF), so a task title can't read in another order than it is; the emoji joiners stay.
- **The TUI clips text by grapheme**, so a title with emoji like `⚠️` no longer runs past the edge of the card or the screen.
- **`igris history` no longer fails on a damaged `runs.jsonl` line**: lines it can't read (not JSON, too long, a field of the wrong type) are skipped with the note `N unreadable lines in runs.jsonl skipped`, and the rest still shows; `report` and the home screen read the log the same way. igris starts a new line after a cut-off one instead of gluing its next event to it.
- **`igris history` shows each run's ID** when the log has one (text `Run <started> <id> …`, `"run"` in `--json`), so it can be passed to `igris report`. Runs logged by igris v0.4 and earlier show none and are otherwise listed as before.
- The narrow run view's top line shows the phase's progress (`M0 · 3/7`) instead of `phase M0`.
- The run log records how each notification went: `<event> via <channel>`, `<event> held for <channel> (quiet hours)` or `digest of N via <channel>`.
- `docs/reverify.md` probes use `sonnet` instead of `haiku`.
- `igris notify test --event <event>` says `no channel sends <event>; add it to a channel's events in igris.toml` when channels are set up but none sends that event, instead of claiming none is set up; the hint for no channel names the backend toast, not herdr's.
- **A second `Ctrl-C` exits `igris arise` at once**, even while the run is still sending its last notifications; that final flush makes one attempt per message, without the retry, and takes at most 15 s.
- **Notification channels no longer follow redirects** (ntfy and Discord included): following one could carry a token, the webhook signature or a secret URL to another host, and its `200` counted as delivered. A `3xx` is now a failure, `server answered 307 (redirect); set the final URL in igris.toml`.
- **Notification failures are told in fixed words** (`can't resolve host`, `connection refused`, `TLS failed`, `timed out`, `connection failed`, `server answered 500 Internal Server Error`, `<key> is not a valid URL`): net/http's own text, which names the URL and host, and the server's reason phrase are no longer shown.
- **Discord and Slack webhook URLs must be `https` with a host**, checked when `igris.toml` loads (or, for an `env:` reference, when it is resolved); the error never shows the URL.

### Migration

- **No plan or config migration is needed.** New keys are optional; without them notifications, the run log readers and the TUI behave as in v0.4, apart from the live tail (on by default; `[tui] tail = false` turns it off) and the progress and ETA shown in the run view.
- **A v0.4 config keeps its config hash**: the keys v0.5 adds are left out of it while unset or at their default (`[tui] tail = true` included), so resuming a run interrupted under v0.4 shows no `igris.toml changed since the interrupted run` notice. Setting a new key (or `tail = false`) changes the hash, as any edit does.
- **`runs.jsonl` lines are versioned now** (`"v":1`, new fields and events). Lines written by v0.2–v0.4 still read, mixed in one file with v1 lines; `history` and `report` show `—` for what old lines don't record. Scripts reading the file should ignore unknown fields and event types (`task_retried`, `needs_you`, `needs_you_clear`); the existing fields keep their meaning. See [`docs/runlog.md`](docs/runlog.md).
- **Scripts parsing `igris history`** text output see the run ID after the start time on each run line; `--json` gains a `"run"` key.
- **Going back to v0.4** is safe: v0.4 reads v1 lines as before and ignores the fields it doesn't know.

## [0.4.0] - 2026-10-08

igris v0.4 lets a plan say more about each task: which checks to run, how long it should take and what to read first. It also runs part of a plan, puts a task back, runs your own commands around each session and lints plans for CI. Every addition is optional, so most plans need no migration: a v0.3 plan and a v0.3 `igris.toml` mean what they meant before, with three exceptions in Migration: sessions now run in `auto` mode unless `default_mode` says otherwise, a column that already had one of the new names is read as the new column, and a config written by v0.3 `igris init` doesn't get the new `task_overdue` notification on ntfy or Discord by itself.

### Added

- **Verify profiles** (SPEC §6.4): `[verify]` in `igris.toml` names shell commands (`fast = "go test ./internal/..."`, `full = "make fmt lint test"`); `run.verify` is the profile `default` and stays supported. `[phases.<id>] verify` sets a phase's profile, and the plan's new optional **`Verify`** column picks one per task, or `none` to skip verification for it. Plans name profiles, never commands. An unknown profile in a cell, or a command written there, fails `check` and `arise`; a `[phases.<id>]` naming no phase is a warning.
- **`Timeout` column** (SPEC §6.3): a Go duration of at least `1s` (`45m`, `1h30m`). The clock starts when the session's first prompt is delivered (also one Claude Code held back at a startup question). A session running longer is marked **overdue** on the task card and **Needs you**, logged as `task_overdue` and notified once per attempt. igris never stops or closes the session for it; a retry starts a new clock.
- **`task_overdue` notification event**, urgent like `needs_input` (ntfy priority high, the toast's `request` sound), in every channel's default `events`.
- **`Context` column** (SPEC §3.2, §6.1): comma-separated repo-relative files or directories (at most 20) that the default task prompt lists as required reading; custom templates get `.Context`. igris names the paths only and never reads or sends their contents. Every path must exist and stay inside the project (no absolute paths, no `..`, not `.` for the whole project, no symlinks out); the same path written twice (`docs`, `./docs/`) counts once.
- **Task hooks** (SPEC §6.7): `[hooks] before_task` and `after_task` run your own commands, as argv lists without a shell, around every agent session, with `IGRIS_TASK_ID`, `IGRIS_PHASE`, `IGRIS_RANK`, `IGRIS_MODEL` and (after) `IGRIS_RESULT` in the environment and a `timeout` (default 2 m). A failing `before_task` opens no session and offers retry, done, skip or stop; a failing `after_task` is a warning and a `run_error` notification. The log says when a hook starts and shows the end of a failed hook's output; the run log and notifications get a short fixed reason (`exit status 1`, `not found`, `killed by signal 9 (killed)`, …), never the command line. A hook that exits 0 but leaves a background helper running passes. Hooks never run for user tasks, `adapt` or `--dry-run`.
- **`igris arise --only ID[,ID…]`, `--from ID`, `--until ID`** (SPEC §5.5): run a slice of the plan. Without a phase the range comes from the named tasks. Bad IDs or a task outside the range stop `arise` before anything is written; a slice task that waits on unfinished work is reported as `not run: …` and never marked. A bare `arise` resumes the same slice, `--dry-run` walks it, and `igris status` shows it (a `Slice` row; `selection` in `--json`).
- **`igris reset ID [--force]`**: puts a task back to `ready` or `blocked`; `in progress` tasks directly, `done` and `skipped` ones with `--force`. With no igris running it rewrites the Status cell itself, under the run lock; with one running it hands the reset over as a request in its own signal slot (`.igris/signals/<ID>.reset.json`), which that igris asks you to confirm (a session could write it too) and applies at its next check, never in the middle of a verify or a commit. A reset requested while the task is verified or committed is settled before the task is accepted, never lost behind its done. Resetting the current task closes its session and pauses the run. The run log gets `task_reset`. A malformed ID is a usage error (exit 2).
- **`igris check --strict`**: lints the plan (a Task cell without a `**bold**` title or over 400 characters, an `agent + user` row that never says what the owner does, a `-G` gate missing some of its phase's tasks, not counting tasks that depend on the gate, `yolo` mode, a lone `fable` task) and exits 1 on any plan or config warning. Machine warnings never fail it, so it suits CI without Claude Code installed. `--json` adds `"strict": true` and a `"lint"` name per hint.
- `igris status` shows VERIFY, TIMEOUT and CONTEXT columns when some task sets them; `status --json` adds `verify`, `timeout` and `context` per task.
- `examples/advanced/`: the example plan with Verify profiles, Timeouts and Context paths, and the `igris.toml` that defines its profiles. `examples/igris.toml` lists the new `[verify]`, `[phases.<id>]` and `[hooks]` keys.
- Shell completion for `reset`, `check --strict` and the `arise` slice flags (task IDs from your plan; `--only` completes the ID after the last comma).

### Changed

- **`auto` is the default run mode** (SPEC §7.1, §12): `default_mode` defaults to `auto` (Claude Code `--permission-mode auto`) instead of `default`, and `igris init` writes `default_mode = "auto"`. Sessions approve routine actions themselves and still ask you about risky ones; skip-permissions (`yolo`) still needs the typed confirmation every run.
- `Verify`, `Timeout` and `Context` are canonical columns now: matched case-insensitively and aliasable through `[columns]`, they no longer reach the session prompt as extra columns. They are checked only on tasks that aren't `done` or `skipped`; on a `user` task they are ignored with a warning.
- The verify failure sent into a session, the run log's `verify_passed`/`verify_failed` detail and the dry run name the verify profile; the dry run shows `none` for a task whose verification is turned off.
- `run_started` in the run log and `state.json` record the slice of a sliced run. Such a `state.json` is version 2 (a whole-phase run stays 1), and its task IDs are checked when it is read back.
- Plan edits igris didn't make (SPEC §5.4) include the Verify, Timeout and Context cells, so a session turning a later task's verification off holds the run like any other edit.
- Claude Code 2.1.294 is the newest version re-verified with `docs/reverify.md` (SPEC §11.4); herdr 0.9.1 and tmux 3.7 are unchanged.
- `igris adapt` keeps Verify, Timeout and Context cells as written and never invents a profile, a timeout or a path; its prompt lists your verify profile names (never their commands). A column of one of those names that holds something else (a command under Verify, say) is renamed so it stays an extra column, and noted.

### Migration

- **No plan migration is needed** for plans without a column named `Verify`, `Timeout` or `Context`. Those plans, and configs without the new keys, behave as in v0.3, apart from the run mode and the notification events below.
- **Configs that don't set `default_mode` now run sessions in auto mode**; set `default_mode = "default"` to keep every prompt going to you. Configs that set it, a task's `Mode` column and the mode you pick for a run are unchanged.
- **A plan that already has a column named `Verify`, `Timeout` or `Context`** for something else (a command, an estimate, notes) is now read as the new column and may fail `igris check`. Rename that column (e.g. `Verify notes`), or let `igris adapt` do it. To keep the plan as it is, alias the column away in `igris.toml`: `[columns]` `Context = "Background"` keeps it as the extra column `Background`, as before.
- **Configs written by v0.3 `igris init` list their notification `events`**, so ntfy and Discord don't send the new `task_overdue` until you add it to `events` or remove the `events` line (the default then applies). `igris init` now leaves `events` out while it is the default list, so later defaults reach new configs. Configs without an `events` line (like `examples/minimal`) get `task_overdue` already.
- **The config hash of a config that doesn't set `default_mode` or an `events` list changes**, since the hash covers the parsed config with its defaults and both defaults changed (`examples/minimal`, say; a config written by v0.3 `igris init` sets both, so its hash stays the same). Resuming a run interrupted under v0.3 with such a config shows a one-time warning, `igris.toml changed since the interrupted run; this run uses the file as it is now`. It is expected, and the run carries on.
- `check --strict` is new and opt-in: plain `igris check` output and exit codes are unchanged.
- **Going back to v0.3 with a sliced run interrupted:** v0.3 refuses its `state.json` (`unsupported version 2`) rather than running the whole phases. Finish the run with v0.4, or delete `.igris/state.json`.

## [0.3.0] - 2026-10-08

igris runs on tmux as well as herdr, and knows what Claude Code is doing from Claude Code's own hooks instead of depending on herdr's integration. One config default and two check IDs change; see Migration. No change to the plan format.

### Added

- **tmux backend** (SPEC §11.5): run igris inside tmux 3.2 or newer and each session opens in its own background window named `<task> · <rank>`. Prompts go through a paste buffer (bracketed paste), never as arguments; `o` switches to the session's window; toasts show in tmux's status line; an exited Claude Code stays visible in its window until igris closes it. Folder trust and other startup questions hold the task prompt and raise **Needs you**, as on herdr.
- **`backend = "auto"`** (SPEC §11.3), the new default and what `igris init` writes: herdr inside a herdr pane, else tmux inside tmux, else a message saying where to run igris. `"herdr"` and `"tmux"` pin one.
- **Agent state from Claude Code hooks** (SPEC §6.3): every session gets `--settings .igris/hooks/<task>.settings.json`, a settings file with only hooks, which run the hidden `igris hook` on Claude's lifecycle events and record working / blocked / idle / exited in `.igris/agent-state/`. tmux uses it; herdr uses it when its own integration isn't installed. Your `.claude/` settings and your own sessions are untouched. The state only drives **Needs you**; tasks still advance only on `igris done` or your action.
- A backend conformance suite (`internal/backend/conformance`) runs the same behavioral tests against the fake, herdr and tmux backends.
- `docs/smoke-tmux.md`, and hook and tmux probes in `docs/reverify.md`.

### Changed

- `igris doctor`, `check` and `arise` check the version of the backend you run in (`herdr --version` or `tmux -V`) instead of always herdr's. The doctor check `herdr-available` is now `backend` (`--json` id), and says whether herdr or tmux can host sessions.
- A missing herdr Claude integration is no longer a warning: igris reads the agent state from hooks without it. `herdr integration install claude` remains optional.
- Home shows the backend it uses in the header (`herdr ✓`, `tmux ✓`, or `herdr/tmux ⨯` outside both), and its hints say where to run igris.
- An interrupted run resumed under a different backend (started in herdr, resumed in tmux) treats the session as lost and offers continue, fresh, done, skip or stop.

### Migration

- Existing `igris.toml` files with `backend = "herdr"` keep working unchanged. Without a `backend` key the default is now `auto`, which still picks herdr inside herdr.
- Scripts reading `igris doctor --json` should look for `"id": "backend"` instead of `"herdr-available"`.
- igris now passes `--settings` to Claude Code itself; `claude.extra_args` still may not contain it.

## [0.2.1] - 2026-10-07

A patch for two small home-screen and history wording bugs found at the v0.2 gate, plus a release-doc fix. No change to the plan format, config keys or file formats.

### Fixed

- Home: after a run whose phases all completed, the NOW card is READY with a `last run P1 completed` line and the bar says Arise…, instead of STOPPED with Resume (SPEC §15.6).
- History and home: a task still being worked on in a live run reads `running` (RUNNING card `recent:` line, `igris history`, `--json`), not `unfinished`, which is kept for tasks a run ended before they did.
- Docs: the Homebrew release steps (`docs/homebrew-tap.md`) build the real release with `goreleaser release --clean --skip=publish` and `genformula`; `make release-local` is the snapshot dry run.

## [0.2.0] - 2026-10-07

igris v0.2 gets an app: bare `igris` on a terminal opens a home screen, and the onboarding steps (`init`, `doctor`, `check`, `status`, `history`, completions) work from the command line and from inside it. Two behaviors change for people who ran `igris` or `igris arise` by hand; see Migration. No change to the plan format, config keys or file formats.

### Added

- Home screen dashboard (SPEC §15.6): bare `igris` shows the project at a glance: header facts, PHASES with progress bars and outcome words, a NOW card for the state igris is in (get started, plan missing or invalid, config invalid, ready, interrupted, stopped, running elsewhere, stale or remote lock, herdr absent), HEALTH, RECENT, a status line and an action bar that shows only the actions that apply. Wide and narrow layouts, keyboard focus regions and mouse; `NO_COLOR` keeps every state as a glyph plus a word.
- Pages inside the app, each with its safe next actions: **Preview** (the dry run, with **Arise with these settings**), **phase and task** pages, **Check**, **Doctor** (`y` copies the fix command), **History** (runs, tasks, attempts), **Settings** (the effective `igris.toml`; secrets never shown), **Notify test**, and the **start-run wizard** (resume or pick a phase, `--through`, the mode, typed `skip permissions` confirmation). A run opens inside the app with **Home** in place of Quit.
- Onboarding in the app: with no `igris.toml`, a GET STARTED stepper offers **Init** (lists the files it touches, never overwrites) and **Example plan**; `A` runs `igris adapt` from the app (sonnet or opus) and shows the review; `e` opens the plan or `igris.toml` in `$VISUAL`/`$EDITOR` and reports whether it is valid when it comes back.
- `igris doctor [--json]`: a read-only health check of the machine and the project (Claude Code, herdr, `ANTHROPIC_API_KEY`, git, `igris.toml`, the plan, the `igris done` allow rules, `.igris/` modes, a stale or foreign lock, `/mnt/` under WSL, notification channels). Each problem is followed by the command that fixes it; it exits 1 only on a `fail`. `check`, `arise`, `doctor` and home share one ordered check list.
- `igris history [TASK-ID] [-n N] [--json]`: past runs from `.igris/runs.jsonl` with phases, tasks done and skipped, durations, verify attempts, commits and how each run ended; with a task ID, every attempt of that task. Read-only.
- `igris status` shows a `Run` block (phases, current task, mode, since, session, lock, pending signals); `--json` adds a `run` object.
- `igris completion bash|zsh|fish`: completes subcommands, flags, phase IDs and task IDs from your plan.
- `igris init --example` also writes the example plan when there is none.
- Run view: the current-task card shows what the task is about (its text from the plan, cut with `… t: details` when it doesn't fit), with **[t] Details** next to **[o] Open session**; `t` opens the details of the current or selected task, and so does a click on the card's title. While a session needs you the card says `NEEDS YOU — o opens the session`.
- Copy with OSC 52: `y` copies the resume command (`claude --resume <id>`), the log line at the bottom of the log, or the adapt proposal's path, over SSH and through herdr or tmux where the terminal allows it.
- `igris adapt` review compares your plan and the proposal table by table (tasks by ID, cells by column name) and lists only what changed; a plan without tables falls back to a line diff.
- External blockers: the plan convention is a `user` task that names the blocker, with the waiting task depending on it. `igris adapt` turns prose like "waits on" or "blocked by" into such a task.
- Homebrew: `brew install drilonrecica/tap/igris`.
- README and project page: the home screen walkthrough and screenshots, `doctor`, `history`, the `status` run block, completions, and notes on Claude Code's folder-trust prompt, `accept` mode and the resume state.

### Changed

- **Bare `igris` on a terminal opens the home screen** instead of printing the help. When stdin or stdout isn't a terminal (a pipe, a script, cron), stdin is `/dev/null`, or `TERM` is `dumb`, it still prints the help to stderr and exits 2.
- **`igris arise` with no phase and nothing to resume** opens the start-run wizard on a terminal (with `--mode`, `--through` and `--force-unlock` prefilled) instead of exiting 1 and asking for a phase. With `--no-tui`, `--dry-run` or no terminal it behaves as before. `igris arise PHASE` is unchanged.
- The task mode can't be set for `user` tasks (they have no session): the key and button are hidden, and `--no-tui` answers `mode <task> <m>` with "is a user task".
- The example plan (`examples/tasks.md`, written by `igris init --example`) starts with P1-01 "Project skeleton" `ready` instead of `done`, so the first session builds the skeleton a fresh project doesn't have.
- YOUR TURN says what it means: the card, the log, `--no-tui` and the notification say a user task is yours to do outside igris and how to finish it (`d` done · `s` skip); the card gets **[d] Done…** and **[s] Skip…**. `d` on a user task asks **Mark ID done?** with an optional note (`enter` confirms), recorded like `igris done --note`; an agent task's `d` is unchanged.
- One click on a task row in the run view opens its details (it used to select the row, and a second click opened them).
- The TUI's run view is shared with the app; it looks and behaves as before, with **Home** where **Quit** was when the run was started from home.

### Migration

- **Scripts that call bare `igris`** and expect the help (or a non-zero exit) keep that behavior unless a terminal is attached to both stdin and stdout. Run `igris -h` or `igris help` for the help in any case.
- **Scripts or habits that run `igris arise` without a phase** to see an error: on a terminal this now opens the start-run wizard. Add `--no-tui`, or run it without a terminal, to keep the old exit.

### Notes

- Claude Code asks "Is this a project you trust?" for a folder it hasn't seen; the first session of a new project waits on it and igris shows "needs you". `accept` mode still asks before Bash commands. `status` keeps showing the last run after you quit, as the resume state.

## [0.1.3] - 2026-10-07

Windows through WSL2: documented, and tested by reproducing WSL conditions on Linux (not yet on a real Windows machine). One parser fix for plans saved by Windows editors. No change to the plan format, config keys, CLI flags or file formats.

### Added

- README and project page: a "Windows (WSL2)" section. Install herdr, Claude Code and igris inside WSL, and keep the project on the Linux filesystem rather than under `/mnt/c`. SPEC §1 lists native Windows as a non-goal.
- `docs/check-wsl.md`: a checklist for running the herdr smoke test on a real WSL2 machine (Linux filesystem, a project on the Windows drive, files from Windows editors). It also records the v0.1.3 run that reproduced these conditions on Linux, which passed: a path with spaces, the plan a symlink onto another filesystem, BOM and CRLF in `tasks.md` and `igris.toml`, and `core.autocrlf`.

### Fixed

- A plan starting with a UTF-8 byte order mark (as some Windows editors save it) was invalid when its first line was a `##` phase heading or a table: the BOM hid the heading, so the first table was "outside a phase". igris now skips a leading BOM when parsing and keeps it in the file when it writes a Status cell. The parser and writer fuzz targets have a BOM seed.

### Known limitations

- herdr only; a tmux backend is planned.
- Linux and macOS; Windows through WSL2 only, and WSL2 is not yet verified on a real Windows machine.
- Release artifacts are not signed (checksums only); signing is planned.
- No Homebrew tap yet.

## [0.1.2] - 2026-10-07

Robustness: warnings when Claude Code or herdr drift from the versions igris was verified with, fuzzing of the parser and the plan writer, and error messages that say what to do next. No change to the plan format, CLI flags or file formats; one config key is deprecated (it never had an effect).

### Added

- `igris check` and `igris arise` (also `--dry-run`) run `claude --version` and `herdr --version` and warn when a tool is not in `PATH`, its version can't be read, it is older than the oldest version igris is verified with (Claude Code 2.1.291, herdr 0.9.1), or it is a newer major version than the newest one re-verified (Claude Code 2.1.292, herdr 0.9.1). A warning only: igris never refuses to run because of a version. In `check --json` these warnings have no `file` or `line` (SPEC §11.4).
- `docs/reverify.md`: the Claude Code and herdr checks from P0-02/P0-03 as a repeatable checklist, with the commands, the expected output shapes and how to refresh the herdr fixtures. It ran clean against Claude Code 2.1.292 and herdr 0.9.1.
- Fuzz targets (Go's built-in fuzzing) for the table tokenizer and plan parser, the Status writer, the config loader and the signal reader. The writer's target checks the core promise for any input: a status change touches nothing but that task's Status cell. `make fuzz` runs them (`FUZZTIME`, default 1m); `make test` replays the seeds.
- `igris check` warns when `ANTHROPIC_API_KEY` is set (as SPEC §7.4 always said; only `arise` and `adapt` did), and when it runs in a subdirectory of a project, where it reads no `igris.toml` and silently used the defaults. Without any `igris.toml` it prints a note (not a warning): a folder with just a plan stays a valid project. `phases` and `status` show the subdirectory hint on stderr.

### Changed

- Error messages say what to do next. Among them: `arise` without a plan file, herdr not installed (instead of a long "not reachable" chain), a session that fails to start or can't get its prompt, a failing `git commit` (e.g. a hook), a broken `run.commit_message`, a lock that can't be cleared, an invalid task ID in `igris done`/`skip`, usage errors (`see igris <command> -h`), and a TUI failure (`igris arise --no-tui`). `check`, `phases` and `status` now prefix their errors with the command (`igris check: …`).
- `igris done` and `igris skip` say when no igris run is active in the project: the signal is kept, and the next `igris arise` applies it.
- `igris arise`, `adapt` and `notify test` outside a project (no `igris.toml` or `.igris/` here or in a parent, and no plan file here) exit with a hint to run `igris init`. Before, they used the current directory and created `.igris/` in it.

### Deprecated

- `claude.command` in `igris.toml`: it was required but never used, since herdr always starts `claude` from `PATH`. Any value other than `"claude"` now loads with a warning in `check` and `arise` instead of having no effect silently, and an empty value is no longer an error. The key is removed in v0.2.

## [0.1.1] - 2026-10-07

Fixes from running the v0.1.0 owner gates on real plans, a phone and real notification channels. No change to the config keys, CLI flags or file formats; one relaxation of the plan format (finished tasks need no Model).

### Added

- `CONTRIBUTING.md`, `SECURITY.md` (private vulnerability reporting through GitHub), a bug report template that asks for the igris, Claude Code and herdr versions, and a pull request template.

### Changed

- Finished agent tasks (`done`, `skipped`) no longer need a Model: igris never starts a session for them. A task that goes back to `ready` or `blocked` needs one again, and `igris check` says so. Plans that were valid stay valid.

### Fixed

- A task prompt was lost when the folder-trust prompt was accepted and igris polled within a fraction of a second: Claude Code shows its input box a moment before it takes input, and herdr accepted the prompt anyway. The session then sat at an empty prompt. igris now waits until Claude Code has been idle for 2 s before delivering a prompt held at the trust prompt. Seen in `igris arise` and `igris adapt` in new project folders.
- A task could be lost when igris stopped (quit, Ctrl-C, a crash) while its new session sat at Claude Code's folder-trust prompt: the task prompt was held only in memory, so after `igris arise` reattached, Claude waited at an empty prompt forever. The held prompt is now kept in `state.json` and delivered once Claude Code is ready.
- A dependency column under another name (`Depends`, `Depends on`, `Requires`, …) without a `[columns]` alias was silently read as an extra column, so the plan ran with no dependencies at all. `igris check` and `igris arise` now warn about it and say how to alias it.
- The `task_done` notification was accepted in `events` but never sent. It is now sent when an agent task is done, to the channels that list it (the herdr toast keeps its default events).
- "Needs you" notifications say why in a few words — `(waiting for a permission or an answer)`, `(idle 20s without igris done)`, `(the session asks to skip)` — so two in a row can be told apart.
- Notifications show task titles without markdown: `` `hello.txt` `` and `**bold**` appeared as raw characters on the phone, and a trailing period ran into the `:` after it.
- `igris notify test` sent seven identical messages; each sample now names its event and says what a real one would (`test of needs_input: needs you`).
- With `verify_max_attempts = 1` the verify limit said "verify failed 1 times"; it says "once".
- An unknown status that is a common synonym gets a hint: `unknown status "dropped" (did you mean skipped?)`, likewise for `completed` → done and `wip` → in progress. It is still an error.
- Errors for cells wrapped in markdown say what was meant: `` `M0-01` `` → write it as M0-01; `✅ Done` → did you mean done?; `**Opus**` → did you mean opus?
- "Second task table" errors under `###` sub-headings say that only `##` headings start a phase.
- "No task table found" names a table that has an ID column but lacks Status or Model, and how to alias the column.
- The "agent task needs a Model" error no longer tells you to set Owner `user` when the table has no Owner column; it says to add one.
- `igris status` no longer prints `Overview — :` for a phase heading without a title.

## [0.1.0] - 2026-10-07

First release. Linux and macOS, herdr backend only. The full behavior is specified in [`SPEC.md`](SPEC.md).

### Added

**Plans**
- Canonical plan format: a markdown file with one task table per `##` phase; columns ID, Task, Deps, Status, Model, Owner and an optional Mode, with `[columns]` aliases for other header names.
- Dependencies as comma lists and ranges (`M0-01…M0-04`, `...`, `..`), across phases too.
- Validation with `file:line` errors: unknown IDs, statuses, ranks, owners and modes; self-dependencies; cycles (with the cycle path); malformed ranges; duplicate IDs or phases; a second table in a phase; control characters in task rows.
- Readiness sync and drift warnings when `ready`/`blocked` cells don't match the dependencies.
- Surgical status writer: igris changes only Status cells and keeps padding, backticks, line endings and everything else byte for byte.
- `igris check`, `igris phases` and `igris status [PHASE]`, each with `--plan PATH` and `--json`.

**Running a plan**
- `igris arise [PHASE] [--through PHASE]`: runs tasks one at a time, each in a fresh Claude Code session started with exactly the model the plan assigns to its rank (`[models]` in `igris.toml`).
- Completion protocol: the session runs `igris done ID [--note TEXT]` when it's finished; `igris skip ID --reason TEXT` from a session is only a request that the owner confirms. Signals are files under `.igris/signals/` and are only applied to the current task.
- Watching sessions: needs-input detection per idle episode, session-lost handling, stray signals, close after idle.
- Verification: `verify` runs on every `done`; on failure the last 60 lines go back into the same session; after `verify_max_attempts` failures in a row igris calls the owner.
- Commit policy `ask` (default), `auto` or `never`, with a `commit_message` template and the done note as the commit body.
- User tasks (`Owner = user`): igris pauses until the owner marks them done or skipped; `agent + user` tasks tell the session to get the owner's sign-off.
- Run modes `default`, `accept`, `auto`, `plan` and skip-permissions (`yolo`), resolved per task from the owner's override, the Mode column, the run mode and `default_mode`. Skip-permissions needs a typed `skip permissions` confirmation every run.
- `claude.extra_args`, with model, mode, session and settings flags rejected.
- Resume: `igris arise` reattaches to a running session, or continues its conversation via the stored session ID, or starts a fresh session that is told to check the work already in the tree.
- One run per project with a lock; `--force-unlock` clears a stale one.
- `--dry-run` shows the launch order with each task's model and mode without starting anything or writing a byte; `--no-tui` prints plain log lines and reads commands (`y`/`n`, `done`, `skip`, `retry`, `pause`, `stop`, `mode`, `help`) from stdin.
- Start-up warnings when `ANTHROPIC_API_KEY` is set (asks to confirm), the project isn't a git repository, or the tree has uncommitted changes.
- Default task prompt and igris rules (passed via `--append-system-prompt`), with `prompt_template` to replace the task message.

**herdr backend**
- Each session runs in its own herdr tab; state from `herdr pane get`, toasts via `notification show`.
- Availability checks with a clear message outside herdr, and a hint when `herdr integration install claude` is missing.
- Agent start is retried for a bounded time while a new tab's shell isn't ready yet (`agent_pane_busy`).

**TUI**
- Wide layout (task list, current task, log, action bar) and a single-column narrow layout usable at 50×20, e.g. over SSH from a phone.
- Every action is a button reachable by click, by `tab`/arrow-key focus plus `enter`, and by its shortcut key; choice dialogs open on the safe option.
- Run and per-task mode picker, task details, a scrollable full log, "Open session" to jump to the current pane.
- Rank colors (configurable under `[tui.rank_colors]`), dark/light detection with a `theme` override, `NO_COLOR` support, nothing shown by color alone; `mouse = false` keeps plain text selection.

**Notifications**
- herdr toasts, [ntfy](https://ntfy.sh) and Discord webhooks, with per-channel event filters, a 10 s timeout and one retry; failures are warnings, never fatal.
- Secrets as `env:VAR` references, never logged and scrubbed from error messages.
- `igris notify test [--event NAME]` sends a sample of each event to every configured channel.

**`igris adapt`**
- Converts a plan igris can't read into the canonical format in one Claude Code session (`--model`, `--plan`). It keeps every task, ID, description, dependency, status and model, never guesses a model (`?` plus `## Adapt notes`), and writes a proposal to `.igris/adapt/`.
- Diff review in the TUI with the `check` result; accepting backs up the original and replaces the plan atomically, rejecting changes nothing.

**Project setup and distribution**
- `igris init`: creates `igris.toml` and `.igris/`, ignores `.igris/` in git, and merges a `Bash(igris done:*)` allow rule into `.claude/settings.local.json`. Safe to re-run.
- Examples: [`examples/tasks.md`](examples/tasks.md) and a commented [`examples/igris.toml`](examples/igris.toml).
- Static binaries for linux and darwin on amd64 and arm64, `checksums.txt` (SHA-256), `make release-local` (GoReleaser snapshot, never publishes).
- `igris version` reports the module version for `go install …@vX.Y.Z` builds too.

### Security

- Sessions are treated as untrusted. `igris.toml` is read once per run, and changes mid-run are reported and ignored. Plan edits igris didn't make (another task's Status, Model, Mode, Owner, Deps or text, or added or removed rows) are reported and notified, and they pause the run until the owner resumes.
- Plan text never goes through a shell; every external command is an argv with a timeout. Only `verify` runs through a shell, and it comes from the config snapshot.
- Done notes, verify output, plan cells and anything typed into a pane are cleaned of escape sequences and control characters.
- Session UUIDs and herdr pane references read back from `.igris/state.json` are validated before they become command arguments.
- Igris never reads, prints or stores Claude credentials and never sets `ANTHROPIC_API_KEY`.
- `.igris/` state is written atomically with 0600/0700 permissions; the lock file is created complete; signals are read only as regular files up to 64 KiB.

### Known limitations

- herdr is required; a tmux backend is planned.
- Linux and macOS only.
- Release artifacts are not signed (checksums only); signing is planned.
- No Homebrew tap yet.

[Unreleased]: https://github.com/drilonrecica/igris/compare/v1.2.1...HEAD
[1.2.1]: https://github.com/drilonrecica/igris/compare/v1.2.0...v1.2.1
[1.2.0]: https://github.com/drilonrecica/igris/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/drilonrecica/igris/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/drilonrecica/igris/compare/v1.0.0-rc.2...v1.0.0
[1.0.0-rc.2]: https://github.com/drilonrecica/igris/compare/v1.0.0-rc.1...v1.0.0-rc.2
[1.0.0-rc.1]: https://github.com/drilonrecica/igris/compare/v0.6.0...v1.0.0-rc.1
[0.6.0]: https://github.com/drilonrecica/igris/compare/v0.5.1...v0.6.0
[0.5.1]: https://github.com/drilonrecica/igris/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/drilonrecica/igris/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/drilonrecica/igris/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/drilonrecica/igris/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/drilonrecica/igris/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/drilonrecica/igris/compare/v0.1.3...v0.2.0
[0.1.3]: https://github.com/drilonrecica/igris/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/drilonrecica/igris/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/drilonrecica/igris/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/drilonrecica/igris/releases/tag/v0.1.0
