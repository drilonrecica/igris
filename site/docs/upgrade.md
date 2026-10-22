# Upgrade to 1.0

This is for anyone coming from a 0.x release. 1.0 adds no features over the last 0.x; it decides what 1.x promises not to break, writes it down and tests it. See [compatibility](../../docs/compatibility.md).

## Steps

1. **Install the new igris** the way you installed the old one: `brew upgrade igris`, `go install github.com/drilonrecica/igris/cmd/igris@latest`, or a [signed binary](install.md#verifying-a-download). Check with `igris version`.
2. **In each project, run `igris config migrate` once.** It removes settings a release removed, shows the lines, asks first and keeps a backup in `.igris/config/`. Today that is one key, `claude.command`, which every `igris init` before 0.7 wrote. `--yes` skips the question.
3. **Run `igris doctor` and `igris check`.** Both should be clean.

That is all. Plans need nothing. A run interrupted under the old version resumes: its state is read and upgraded on the next save, and the config keeps its hash, so you don't see a "config changed" notice.

## What changed on the way to 1.0

These came in 0.7 and the 1.0 release candidates. They are the last breaking changes: from 1.0 on, 1.x only adds.

- **`claude.command` was removed** from `igris.toml`. Until you run `igris config migrate`, `check`, `arise` and `doctor` stop with `claude.command was removed in 0.7`.
- **`--json` output changed** (the last time). Every output starts with `"v": 1`; `doctor --json` is an object, `{"v": 1, "checks": [...]}`, instead of a bare array; `status --json` leaves out a task's `rank` and `mode` when the cell has none, instead of `"—"`; the `counts` key `"in progress"` is now `"in_progress"`; and pending signals are `{"task", "action"}` objects. Each output has a JSON schema in [`docs/json/`](../../docs/json/).
- **Exit codes settled.** A malformed task ID in `igris done` and `igris skip`, and an unknown `igris notify test --event`, are usage errors now (exit `2`, was `1`). `igris adapt` stopped with Ctrl-C exits `0`. Exit codes `3`–`9` are reserved.
- **`.igris/state.json` is version 3.** igris reads 1, 2 and 3 and upgrades them. A newer version is refused with a message to upgrade igris.
- **The plan format has a name:** format 1. `<!-- igris:format 1 -->` is optional, forever. A plan declaring a format igris doesn't know is refused with "plan format N needs a newer igris".
- **New:** `igris config migrate`, and `igris help COMMAND` shows that command's help.

The [CHANGELOG](../../CHANGELOG.md) lists every release in full.

## If you script igris

- Read `doctor`'s checks from `.checks`, use the `in_progress` key, read signals as `{task, action}` objects, and treat a missing `rank` or `mode` as none.
- Test for a non-zero exit code, or for `2` (a usage error), never for exactly `1`: a 1.x release may give a failure its own code from `3` to `9`.
- Read `--json`, the run log and the webhook ignoring fields and event types you don't know. 1.x adds them.
- Text output is for people and may change.

## Going back

A config that uses a key added in a later 1.x fails on an older igris as an unknown key; the fix is to upgrade. igris 0.6 and older can't read a version-3 `state.json`: finish the run first, or delete `.igris/state.json` to start a new one.

## From 1.0 to 1.1

Install the new igris and you are done: 1.1 only adds to 1.0, and nothing in your plan, `igris.toml`, scripts or `.igris/` needs to change. A run interrupted under 1.0 resumes.

What is new, all additive:

- **Plan approval in igris** for plan-mode tasks, with `plan:accept`-style Modes and `[plan_mode]` settings. See [usage and plans](usage.md#approving-plans-in-igris).
- **Token usage and the rank audit.** `igris usage`, a counter on the run card and a `rank_mismatch` notification, which is now a default event. Turn the reading off with `[usage] enabled = false`.
- **Subscription quota**, off until you set `[usage] statusline = true`.
- **A parallelism report** in `igris phases` and `arise --dry-run`.
- New `--json` fields and a `usage` schema, new run-log events and a `plans/` and `quota.json` in `.igris/`. Read all of them ignoring what you don't know, as 1.0 already asked.

## From 1.1 to 1.2

Install the new igris and you are done: 1.2 only adds to 1.1, and nothing in your plan, `igris.toml`, scripts or `.igris/` needs to change. A run interrupted under 1.1 resumes. `state.json` stays version 3.

What is new, all additive:

- **[Side sessions](side-sessions.md).** `S` opens a Claude Code session of your own next to a run. New `[side]` settings (`model`, `mode`), a `.igris/side/` directory, and `IGRIS_SIDE` in the session's environment, with which `igris done`, `skip` and `reset` refuse.
- **[Split plans](split-plans.md).** `<!-- igris:include PATH -->` in a plan that declares `<!-- igris:format 2 -->`. A plan without includes is unchanged and stays format 1.
- **[Plan templates](templates.md).** `igris init --template go-cli|web-app|library`, and **Plan from template…** on home.
- **[Updating igris](install.md#updating-igris).** `igris update [--check] [--yes]` and `u` on home check GitHub for a newer release and, after asking, update igris with Homebrew or `go install`. From 1.2 on, that is the easiest way to the next release.
- **Usage.** A model an attempt used is counted once under each model it ran on, and a run with no `usage` lines says why. Side sessions have a table of their own in `igris usage`.
- New `--json` fields (`file` on each phase and a `files` list in `check`, `phases` and `status`; a `side` array in `usage`), three new run-log events (`side_opened`, `side_closed`, `side_usage`) with a `side` field. Read all of them ignoring what you don't know, as 1.0 already asked.

**One thing to plan for:** a split plan needs igris 1.2 on every machine that runs it. igris 1.0 and 1.1 refuse a format-2 plan with `plan format 2 needs a newer igris` rather than run only the main file's phases. Going back to 1.1 is fine as long as no plan declares format 2.

## From 1.2 to 1.4

There is no 1.3 release: `igris draft` and serve/attach ship together as 1.4. Install the new igris (or run `igris update`) and you are done: 1.4 only adds to 1.2, and nothing in your plan, `igris.toml`, scripts or `.igris/` needs to change. A run interrupted under 1.2 resumes. `state.json` stays version 3.

What is new, all additive:

- **[Draft a plan](draft.md).** `igris draft goal|spec|import|extend`, and **Draft…** (`D`) on home: a session writes a proposal, you review it as for `adapt`, and only Accept writes your plan. New `[draft]` setting (`model`, default `opus`) and a `.igris/draft/` directory.

- **[Detach and attach](detach-attach.md).** `igris arise` now starts the run in a background `igris serve` and attaches to it, so closing the terminal only detaches. New commands `igris serve`, `igris attach` and `igris stop`, a socket `.igris/igris.sock` and a log `.igris/serve.log`. `arise --no-tui` and `--dry-run` work as before, in one process.

**Two things to know:** `q` in the run view now asks **Detach** · Stop run · Cancel (Detach is focused) instead of leaving at once, and a run started by `arise` has a `serve` process that outlives your terminal: `igris stop` ends it. After `igris update` during a run, an attached view says `served by igris X.Y.Z` and the run keeps that igris until it ends.

Going back to 1.2 is fine: the plans a draft wrote are ordinary plans, and 1.4 changes no file format, so 1.2 can resume a run 1.4 left (stop it first with `igris stop`).

## From 1.4 to 1.5

Install the new igris (or run `igris update`) and you are done: 1.5 only adds to 1.4, and a plan without the new marker runs exactly as before. A run interrupted under 1.4 resumes.

What is new, all additive:

- **[Parallel phases](parallel-phases.md).** Mark a phase `<!-- igris:parallel -->` in a plan that declares `<!-- igris:format 3 -->` and its independent tasks run at the same time, each in its own git worktree, merged back in the order they finish with your verify run on the merged tree. New optional `Group` column, `[parallel]` and `[parallel.ranks]` in `igris.toml`, `--parallel N` on `arise` and `serve`, `IGRIS_SLOT` in a task's environment, and the run-log events `integrated`, `integration_failed` and `overlap`.
- **A run view for several tasks:** one card per active task, one Needs-you queue, aggregated notifications.

**Three things to know:** parallel costs more than it looks (your limits burn N times faster, every worktree needs its own caches, a dependency the plan leaves unsaid becomes a conflict: [what it costs](parallel-phases.md#what-it-costs)); `state.json` is now version 4, which igris 1.0 to 1.4 refuse, so finish a run (or `igris stop` and delete its state) before going back; and igris 1.0 to 1.4 refuse a format-3 plan with `plan format 3 needs a newer igris`.

## From 1.5 to 1.6

Install the new igris (or run `igris update`) and you are done: 1.6 only adds to 1.5, and nothing listens on the network until you set an address. A run interrupted under 1.5 resumes.

What is new, all additive:

- **[Control from your phone](control-from-your-phone.md).** `[serve] http = "ADDR:PORT"` (or `igris serve --http`) makes `igris serve` also answer on HTTP: a web UI for your phone that follows the run and answers its questions, plus `igris serve token` to pair a device by URL or QR code (`--rotate` unpairs all). Loopback and Tailscale addresses bind directly; any other needs `--listen-public` and `tls_cert` and `tls_key`. New file `.igris/serve-token`.
- **ntfy answer buttons.** `actions = true` under `[notify.ntfy]` adds buttons to a question's message whose links answer that one question, once, for 10 minutes.
- **The commit question is now notified** as `needs_input`, like every other question, so a run left alone says that it waits.

**Two things to know:** whoever holds the pairing token controls the run (short of skip-permissions mode, which is never set remotely) until you run `igris serve token --rotate`; and the web UI's pages are not part of what 1.x promises, only the HTTP API is. `state.json` stays version 4 and plans need no change, so going back to 1.5 is fine (it ignores `.igris/serve-token`).
