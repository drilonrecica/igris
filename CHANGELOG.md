# Changelog

All notable changes to igris are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and igris uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Until 1.0, minor versions may change the plan format, config or CLI; such changes are called out here.

## [Unreleased]

## [0.5.0] - 2026-10-08

igris v0.5 shows you what a run is doing and what it did. The run view gets the phase's progress, an estimate for the current task and a live tail of its session; `igris report` turns a finished run into markdown for a PR description; and the run log is a documented, versioned format. Notifications reach Slack, Gotify and any URL through a signed webhook, with your own message templates, quiet hours and digests. Everything is additive: a v0.4 plan and `igris.toml` mean what they meant before (the new channels, quiet hours and digests stay off until you configure them; only the live tail is on by default), and a v0.4 `runs.jsonl` still reads; a run interrupted under v0.4 resumes without a config-changed notice.

### Added

- **`igris report [RUN] [--json]`** (SPEC §14): one run as markdown, to paste into a PR description or a journal: its run ID, start, end and outcome, phases and slice, counts (done, skipped, unfinished, commits) and how long igris waited on you, then per phase a table with each task's result, duration, attempts, verify results (`fast ✗ ✓`), short commit SHA and needs-you time, the done notes and skip reasons, and a `claude --resume <uuid>` per agent session you can reopen. `RUN` is `1` (the newest, the default), `2`, … or a run ID from `igris history`; `--json` prints one object. Needs-you time is the time igris waited on you: an idle or blocked wait ends at the session's `igris done`, a `task_overdue` or verify-limit wait once the agent works again, a changed plan's from when the run holds until you resume; a repeated wait (a second skip request) counts once. Text from the log is markdown-escaped, so a title or note can't make links or formatting; a known duration under a second shows as `1s`. Read-only, never creates `.igris/`; exits 1 when there is no such run (any index past the end), 2 for a malformed `RUN`.
- **Run log v1** (SPEC §13, [`docs/runlog.md`](docs/runlog.md)): every line igris writes to `.igris/runs.jsonl` has `"v":1`, the run ID (`20261008-091500-3fa2`), the task's attempt, its phase, title and owner, the Claude session UUID, the verify profile, the full commit SHA and durations where they apply. New events: `task_retried`, `needs_you` with a `reason` (`idle`, `blocked`, `task_overdue`, `verify_limit`, …) and `needs_you_clear`. `docs/runlog.md` documents every field and event, the compatibility rules and an example line per type. A task reset and started again in the same run keeps counting its attempts.
- **Progress in the run view** (SPEC §15): the header shows `phase M1 · 7/12 · 58% ██████░░░░` (narrow: `M1 · 7/12 · 58%` and a shorter bar), counting the run's tasks of the current phase, or the slice's (labelled `slice`) for `--only`/`--from`/`--until`.
- **ETA** for the current task: with at least 3 finished tasks of its rank in `runs.jsonl` (the 20 most recent, started fresh in their run), the card adds `· ≈ 14m left` from their median, or `· over ≈ 18m typical` once past it. Always approximate; no phase ETA.
- **Live tail** (SPEC §11.1, §15): the current-task card shows the session's last 6 lines (3 on a narrow terminal), read every `poll_interval` through `herdr agent read` or `tmux capture-pane`, cleaned of escape sequences and cut to the card's width. It is only drawn: never logged, notified, stored or reported. `[tui] tail = false` turns it off and igris then never reads the pane. Backends implement it through the optional `backend.Tailer`, with a conformance case.
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

[Unreleased]: https://github.com/drilonrecica/igris/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/drilonrecica/igris/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/drilonrecica/igris/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/drilonrecica/igris/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/drilonrecica/igris/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/drilonrecica/igris/compare/v0.1.3...v0.2.0
[0.1.3]: https://github.com/drilonrecica/igris/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/drilonrecica/igris/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/drilonrecica/igris/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/drilonrecica/igris/releases/tag/v0.1.0
