# Changelog

All notable changes to igris are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and igris uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Until 1.0, minor versions may change the plan format, config or CLI; such changes are called out here.

## [Unreleased]

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

[Unreleased]: https://github.com/drilonrecica/igris/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/drilonrecica/igris/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/drilonrecica/igris/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/drilonrecica/igris/releases/tag/v0.1.0
