# Modes

A mode is the permission mode of a task's Claude Code session: how much it may do without asking you.

| Mode | What it does |
|---|---|
| `default` | Your normal Claude Code permission prompts. |
| `accept` | Edits are accepted automatically. Bash commands still ask, even read-only ones (that is Claude Code's behavior), so igris shows "needs you" when one comes up. |
| `auto` | Claude Code approves routine actions itself and asks you about risky ones. |
| `plan` | Claude plans first; you approve the plan, then it implements. |
| `yolo` | Skip permissions: `--dangerously-skip-permissions`. See below. |

## Which mode a task gets

A task's mode is, in order:

1. your override in the TUI;
2. its `Mode` column in the plan;
3. the run mode, `igris arise --mode`;
4. `default_mode` in `igris.toml`, which is `auto` unless you set it (`default_mode = "default"` gets you every permission prompt).

Choose a mode in the TUI per run or per task: `m` (or **Mode**) for the run, or select a task and press `M` (**Task mode**) to override it for that task. A change applies to the next session; a running one keeps its mode. User tasks have no session, so they have no mode.

Use the plan's `Mode` column for tasks that always want one, such as `plan` for design-heavy work. The mode names are also listed in the [plan format](../../docs/plan-format.md).

## Skip permissions

Skip permissions is never a single click or key. You type `skip permissions` to confirm, **every run**, and a red badge shows while it is active. There is no setting that remembers it.

Use it only where you would trust an unattended agent: in a container you trust, or with **worktree mode**. `igris arise P1 --worktree` (or `worktree = true` under `[run]`) runs the whole run in a git worktree on a branch of its own, `igris/<run-id>`, so your checkout and current branch stay as they are. Whatever the sessions do lands on a branch you can review, merge or throw away. igris never merges: when the run completes, it removes the worktree, keeps the branch and tells you the `git merge` command.

## What you cannot set

`claude.extra_args` in `igris.toml` can't carry flags that change the session's model, mode, permissions, tools, directory or system prompt (`--model`, `--permission-mode`, `--settings`, `--allowedTools`, `--add-dir`, `--bare`, `-c`/`-r`/`-w`, …): igris sets what it needs itself and rejects a config that has them.
