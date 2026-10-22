# FAQ

## Does igris use my Claude subscription or the API?

Whatever Claude Code normally uses for you. igris starts ordinary interactive `claude` sessions, so they run on your login (Pro or Max) and count against its limits. The one exception is your environment: if `ANTHROPIC_API_KEY` is set, Claude Code bills the API instead. igris warns about that at start and asks you to confirm. It never sets that variable, and never reads, prints or stores your Claude credentials.

## Why a fresh session per task?

A long session fills its context and drifts, and one task's context shouldn't leak into the next. A fresh session gets the task, the plan and your project rules, and nothing else.

## Can igris pick the model for me?

No, on purpose. The plan's Model column is the only input: igris launches exactly that rank and never a more expensive one. A task without a usable model is a validation error, not a guess.

## What if a task needs me?

Claude asks in its pane like it always does. igris notices the stalled session, shows it in the TUI and [notifies you](notifications.md). Answer in the session: press `o` (or click **Open session**) to jump to its tab or window. A session that only waits on its own background work isn't stalled; it counts as needing you only after `waiting_limit` (30 minutes by default).

## Is skip-permissions safe?

Only where you'd trust an unattended agent: a container, or with worktree mode (`--worktree`), so the run's changes stay on a branch of their own. It needs a typed confirmation every run, by design. See [modes](modes.md#skip-permissions).

## What stops a session from changing the plan or igris itself?

igris treats sessions as untrusted. `igris.toml` is read once per run (a mid-run change is reported and the old settings stay). A plan change igris didn't write (a later task's Mode, Model or Verify, a Status, task text) pauses the run until you resume. What igris reads back from `.igris/` is checked before it is used. See the [threat model](../../docs/threat-model.md).

## I don't use herdr.

Use tmux: run igris inside a tmux session and leave `backend = "auto"`. Running without any multiplexer isn't supported: sessions have to outlive igris and your connection.

## Does it run on Windows?

Through WSL2, with herdr or tmux and Claude Code installed inside WSL too; see [Windows (WSL2)](install.md#windows-wsl2). A native Windows build is not planned.

## Can I run several projects at once?

Yes, one run per project. They share your subscription's limits. See [running several projects](several-projects.md).

## Can two tasks run in parallel?

Only where the plan says so. A phase runs one task at a time unless you mark it `<!-- igris:parallel -->` (since 1.5); then independent tasks run together, each in its own worktree, and are merged back in order. It costs more than it looks: see [Parallel phases](parallel-phases.md).

## Can I control a run from my phone?

Yes, since 1.6, once you set `[serve] http` to a Tailscale or loopback address (for `ssh -L`) and pair the phone with `igris serve token`. A web page follows the run and answers its questions, and ntfy messages can carry answer buttons. It is off by default, goes through no relay of ours, and never offers skip-permissions mode. See [Control from your phone](control-from-your-phone.md).

## Will an update break my plans and config?

Not within 1.x. The plan format, `igris.toml`, the CLI, the `--json` output and the files in `.igris/` are frozen until 2.0: 1.x only adds. See [compatibility](../../docs/compatibility.md) and the [upgrade guide](upgrade.md).

## Where does igris keep its data?

In the project: `igris.toml` at the root and `.igris/` (state, run log, signals, hook state, a lock). igris adds `.igris/` to `.gitignore`. It sends nothing anywhere except the notifications you configure, and asks GitHub for the latest release only when you run `igris update` or press `u` on home; it has no telemetry or accounts.
