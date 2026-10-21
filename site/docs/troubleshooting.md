# Troubleshooting

Start with `igris doctor`. It checks the machine and the project without changing anything, prints one line per check (`ok`, `warn` or `fail`), and follows each problem with the command that fixes it. `igris check` validates the plan.

## igris says it isn't in herdr or tmux

`igris arise` and `igris adapt` run inside **herdr** or **tmux**, because sessions have to outlive igris and your connection. Start one (`herdr`, or `tmux new -s work`), `cd` to the project inside it, and run igris again. `backend = "auto"` picks herdr inside a herdr pane and tmux inside tmux; pin one with `backend = "herdr"` or `"tmux"`. See [where igris runs](install.md#where-igris-runs).

## The task says "needs you: the agent is blocked"

The session is waiting for something igris can't answer. Press `o` (or click **Open session**) to jump to it. Common causes:

- **The trust prompt.** The first time, Claude Code asks "Is this a project you trust?". Answer once; later sessions in that folder don't ask.
- **A permission prompt or a question.** Answer it in the session, or pick a [mode](modes.md) that asks less.
- **A plan awaiting approval** in `plan` mode.
- **A usage limit.** igris doesn't tell limits from other stalls. See [several projects](several-projects.md#subscription-limits-are-shared).
- **Accept-edits mode and a Bash command.** Claude Code asks even for read-only commands in that mode.

A session that only waits on background work (a long build, a scheduled wakeup) is not "needs you" until `waiting_limit` passes.

## igris.toml: unknown key

Unknown keys are errors, which catches typos. The message names the key. Check its spelling against the [configuration reference](../../docs/config.md).

## igris.toml: claude.command was removed

`claude.command` was deprecated in 0.1.2 and removed in 0.7. Every `igris.toml` that `igris init` wrote before 0.7 has it. Run `igris config migrate`: it shows the lines it removes, asks, and keeps a backup in `.igris/config/`. See the [upgrade guide](upgrade.md).

## Another igris is running in this project

A second `igris arise` refuses to start while the first is alive, and `adapt` can't run during an `arise`. Find the running igris (another pane, tmux window or SSH session) and use it, or stop it there. `igris status` shows the run, and the home screen shows it read-only.

## A stale lock

If a crashed run left `.igris/igris.lock` behind, igris says the lock is stale and `igris arise` exits 1. Run `igris arise --force-unlock`. A lock held by a live process on this host is always refused. If the process ID was reused by another program (after a reboot, say), the error says to delete the file. A lock from another host can't be checked; clear it only when you know that run is gone.

## The plan is invalid, or igris says it changed

`igris check` prints every problem as `file:line: message`. Common ones:

- a **Model** that isn't in `[models]` (ranks are case-sensitive: `Sonnet` is not `sonnet`);
- a **Deps** column named something else: alias it under `[columns]`, e.g. `"Depends on" = "Deps"`;
- a **Verify** cell holding a command: it names a profile defined under `[verify]`, never a command;
- `plan format N needs a newer igris`: the plan declares a format this igris doesn't know. Upgrade igris.

If you edit the plan during a run, igris pauses and asks you to resume when the change is one it didn't write (a later task's Mode, Model or Verify, a Status, task text). Your own edits cost one resume. A plan in another shape entirely: [adapt it](adapt.md).

## Verify keeps failing

On a failure, the last 60 lines of output go back to the same session. After `verify_max_attempts` failures in a row (3 by default) igris calls you instead. Check the command by hand in the project, and that `verify_timeout` (15 minutes) is enough. In worktree mode the first verify may take longer: a fresh worktree has no build caches or `node_modules`. If you finish a task yourself with `d` or `igris done`, igris takes your word and skips verify.

## An interrupted run

Quitting igris, a closed terminal or a lost connection leaves the session running. Run `igris arise` with no phase: it reattaches to the running session, or lets you continue its conversation, or start a fresh session that is told to check the work already in the tree. Under the other backend (herdr instead of tmux) igris can't reattach and offers the same choices.

## igris says igris.toml changed during the last run

A session can edit `igris.toml`. The run it happened in kept the configuration it started with, and igris won't let a later run use the edited file until you have looked at it. Review the change (`git diff igris.toml`, or the file itself if the run committed it), fix or keep it, then answer **y** to `Use the changed igris.toml?` (or **Use it** in the home screen). `igris check` and `igris doctor` show the same warning until a run has used the file.

## state.json was written by a newer igris

You downgraded. The message says to upgrade igris, or delete `.igris/state.json` to start a new run. A file reported as *damaged* (bad JSON) is a different message; delete it the same way.

## A plan waits in the pane instead of igris

igris shows a plan-mode session's plan only in the TUI, so `arise --no-tui` leaves it to the pane. In the TUI it falls back to the pane when you don't answer within `[plan_mode] approve_timeout` or igris stopped meanwhile. Answer there, or reattach with `igris arise`. See [usage and plans](usage.md#approving-plans-in-igris).

## RANK MISMATCH on a task

The session's main thread answered on a model its rank doesn't resolve to, usually after a `/model` in the pane. igris only tells you: switch the model back in the pane, or carry on if you meant it. A `[models]` value igris can't check (`default`, `opusplan`, a custom name) never raises it; `igris usage` shows it as `unchecked`.

## igris usage says unavailable

For tokens: igris couldn't read the session's transcript (it says so once in the feed), or `[usage] enabled = false`. For quota: it is opt-in (`[usage] statusline = true`), and Claude Code reports it only with a subscription login after its first response. See [usage and plans](usage.md).

## plan format 2 needs a newer igris

The plan declares `<!-- igris:format 2 -->`, which igris 1.2 introduced for plans that include other files. Upgrade igris on this machine (`igris version` shows what it runs), or remove the includes and the marker. See [split plans](split-plans.md).

## An include is rejected

`igris check` names the file and line. The usual causes: the plan doesn't declare `<!-- igris:format 2 -->`, the path has a `..` or leaves the project, the file isn't `.md`, it is included twice, or the included file has an include of its own (only the main plan includes). The rules are in [split plans](split-plans.md#the-rules).

## A notification doesn't arrive

Run `igris notify test`. It sends a sample of each event to every channel and prints `ok` or the reason per channel: `connection refused`, `TLS failed`, `server answered 403 Forbidden`. igris doesn't follow redirects, so a `3xx` means the URL should be the final one. During quiet hours only the `break_through` events reach ntfy, Discord, Slack, Gotify and the webhook; the rest arrive as one digest afterwards. See [notifications](notifications.md).

## The ANTHROPIC_API_KEY warning

If `ANTHROPIC_API_KEY` is set, Claude Code bills the API instead of your subscription. igris asks you to confirm at start. Unset the variable in the shell you run igris from to use your subscription.

## igris in PATH is another igris, or missing

Sessions run `igris done` with the `igris` in your `PATH`, while igris's hooks name the binary you started. When they differ, `check`, `arise`, `doctor` and home warn: `sessions run igris done with /usr/local/bin/igris (1.1.0), not this igris (1.2.0)`. An older igris there can fail at `igris done`, for example on a split plan it can't read. Put the igris you run first in your `PATH` (`which igris` shows which one wins), or run that one. A development build run from elsewhere gets the same warning.

## The project is under /mnt (WSL)

`doctor` warns about a project on the Windows drive. It is much slower from WSL and shared with Windows tools. Move it to the Linux filesystem (`~/code/…`). See [Windows (WSL2)](install.md#windows-wsl2).

## Still stuck

`igris doctor --json` and `igris check --json` give output you can paste into an [issue](https://github.com/drilonrecica/igris/issues); igris sends nothing itself. Report security problems privately, as [SECURITY.md](../../SECURITY.md) describes.
