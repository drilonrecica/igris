# Running several projects at once

You can run igris in as many projects at the same time as you like. Each project is independent; the one thing they share is your Claude subscription.

## One run per project

Everything igris keeps for a project is in that project's own `.igris/` folder: the run state, the run log, the signal files and a lock, `.igris/igris.lock`. The lock holds the process ID, host and start time of the `igris arise` that owns the project.

- **Two projects, two runs.** Start `igris arise` in each project, each in its own herdr pane or tmux window. They don't know about each other and don't wait for each other.
- **One project, one run.** A second `igris arise` in a project that already has a live run refuses to start. The same goes for `adapt`, which can't run while `arise` runs in the project. Worktree mode doesn't change that: it moves the sessions' work to a branch, not the lock.
- **A stale lock.** If a crashed run left its lock behind, igris says so, and `igris arise --force-unlock` clears it. A lock held by a live process on this host is always refused.
- **Read-only views never take the lock.** `status`, `doctor`, `history`, `report` and the home screen work while a run is going; the home screen of a project with a live run is read-only and shows that run.

Each project has its own `igris.toml`, plan, verify commands, notification channels and quiet hours. Nothing is shared through your home directory, except the hub's token if you use [the hub](#one-page-for-all-your-runs).

## Subscription limits are shared

igris starts ordinary interactive Claude Code sessions, so every session in every project runs on your one login and counts against the same limits. Two runs use up a usage window faster than one. igris does not coordinate between runs, throttle them or pick which project gets the usage.

When a session hits a limit, it looks like any stalled session: the task shows **Needs you** after `needs_input_after` (30 seconds by default) and you get a `needs_input` notification. igris doesn't try to tell a usage limit from a question. Answer or wait in the session, and the task carries on.

Two things help when you run several projects:

- Keep the rank in your plans honest. A routine task on `sonnet` costs less of the shared limit than one on `opus`, in every project at once.
- Run less at a time: `--only`, `--from` and `--until` run part of a phase, and `p` in a run pauses after the current task.

## One page for all your runs

Since 1.11, `igris hub` puts the live runs of several projects behind one web page, one pairing and one QR code, so a phone or a laptop can manage them all without a listener, URL and pairing per project.

```sh
igris hub --all --http 100.101.102.103:7777     # every live run, and the ones you start later
igris hub ~/code/sinjal ~/code/api --http 100.101.102.103:7777     # only these projects
igris hub                                       # on a terminal: a checklist of the live runs; off one: all
igris hub --http 127.0.0.1:7777                # pick the address yourself
```

Without `--http` (since 1.13) the hub listens on this machine's Tailscale address, else on `127.0.0.1` with a line saying how to reach it with `ssh -L`, always on port 7780 so a bookmark and a pairing keep working; it says which it chose. If 7780 is in use by a hub already, it tells you where that one is; by anything else, pass `--http`. With `--http` the address is the one you would give `igris serve`: loopback or Tailscale, or `--listen-public` with `--tls-cert` and `--tls-key`. The hub remembers nothing but its token. Start it after the runs, or before: with **All**, a run started later joins by itself within a couple of seconds and the hub logs `run joined: <project>`. With paths, each project must have a live run (`igris arise` or `igris serve` in it) or the hub says which hasn't.

The hub prints a pairing URL and a QR code, as `igris serve token` does; scan it once. `igris hub token` prints the URL again, `igris hub token --rotate` unpairs every device. The token is `~/.local/state/igris/hub-token` (`$XDG_STATE_HOME/igris/hub-token`), the one file igris keeps outside your projects.

The page at `/` has a row for each run: the project, the status word, the phase bar, what needs you and the time, **most urgent first**. Questions from every project are in one sheet with the project's name on each; `1 of 3 ›` steps through them. Tap a row for that run's page, the one `igris serve` draws, with its plan, Pause and Stop. Ended runs show their outcome until you reload.

The hub **manages** runs: it can't start one (that stays in the terminal), it never offers skip-permissions mode, and Skip, Stop and Done ask twice. Ending the hub (Ctrl-C) never stops a run. Runs started by an igris before 1.11 don't register, so the hub can't see them: update the igris that hosts them. With `actions = true` under `[notify.ntfy]`, the **Open** button goes to the hub's page for the task while a hub that follows the project is running.

## Telling the projects apart

Notifications name the project, phase and task, so a ntfy message says which project needs you. Sessions are named by task and rank (`<task> · <rank>` in tmux).

If you want one phone topic per project, set a different `topic` in each project's `[notify.ntfy]`; if you want one topic for all, use the same.

## Related

- [Control from your phone](control-from-your-phone.md#one-qr-code-for-all-runs)
- [Notifications](notifications.md)
- [Troubleshooting](troubleshooting.md#another-igris-is-running-in-this-project)
