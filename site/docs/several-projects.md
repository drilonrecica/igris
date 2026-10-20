# Running several projects at once

You can run igris in as many projects at the same time as you like. Each project is independent; the one thing they share is your Claude subscription.

## One run per project

Everything igris keeps for a project is in that project's own `.igris/` folder: the run state, the run log, the signal files and a lock, `.igris/igris.lock`. The lock holds the process ID, host and start time of the `igris arise` that owns the project.

- **Two projects, two runs.** Start `igris arise` in each project, each in its own herdr pane or tmux window. They don't know about each other and don't wait for each other.
- **One project, one run.** A second `igris arise` in a project that already has a live run refuses to start. The same goes for `adapt`, which can't run while `arise` runs in the project. Worktree mode doesn't change that: it moves the sessions' work to a branch, not the lock.
- **A stale lock.** If a crashed run left its lock behind, igris says so, and `igris arise --force-unlock` clears it. A lock held by a live process on this host is always refused.
- **Read-only views never take the lock.** `status`, `doctor`, `history`, `report` and the home screen work while a run is going; the home screen of a project with a live run is read-only and shows that run.

Each project has its own `igris.toml`, plan, verify commands, notification channels and quiet hours. Nothing is shared through your home directory.

## Subscription limits are shared

igris starts ordinary interactive Claude Code sessions, so every session in every project runs on your one login and counts against the same limits. Two runs use up a usage window faster than one. igris does not coordinate between runs, throttle them or pick which project gets the usage.

When a session hits a limit, it looks like any stalled session: the task shows **Needs you** after `needs_input_after` (30 seconds by default) and you get a `needs_input` notification. igris doesn't try to tell a usage limit from a question. Answer or wait in the session, and the task carries on.

Two things help when you run several projects:

- Keep the rank in your plans honest. A routine task on `sonnet` costs less of the shared limit than one on `opus`, in every project at once.
- Run less at a time: `--only`, `--from` and `--until` run part of a phase, and `p` in a run pauses after the current task.

## Telling the projects apart

Notifications name the project, phase and task, so a ntfy message says which project needs you. Sessions are named by task and rank (`<task> · <rank>` in tmux).

If you want one phone topic per project, set a different `topic` in each project's `[notify.ntfy]`; if you want one topic for all, use the same.

## Related

- [Notifications](notifications.md)
- [Troubleshooting](troubleshooting.md#another-igris-is-running-in-this-project)
