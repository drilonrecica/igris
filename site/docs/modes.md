# Modes

A mode is the permission mode of a task's Claude Code session: how much it may do without asking you.

| Mode | What it does |
|---|---|
| `default` | Claude Code's own default mode: igris passes no permission flag. That prompts, unless you opted into auto as Claude Code's default; then it runs in auto. Use `manual` for prompts either way. |
| `accept` | Edits are accepted automatically. Bash commands still ask, even read-only ones (that is Claude Code's behavior), so igris shows "needs you" when one comes up. |
| `auto` | Claude Code approves routine actions itself and asks you about risky ones. |
| `plan` | Claude plans first; you approve the plan, then it implements. When you approve in igris, it continues in the mode `[plan_mode] then` names (`auto` by default). |
| `plan:default`, `plan:accept`, `plan:auto` | Plan first, then continue in `default`, `accept` or `auto` once you approve the plan in igris. |
| `plan:yolo` | Plan first, then skip permissions once you approve the plan in igris. Needs the same typed confirmation as `yolo`. See below. |
| `yolo` | Skip permissions: `--dangerously-skip-permissions`. See below. |
| `manual` | Every permission prompt goes to you, whatever Claude Code's own default is: `--permission-mode manual` (since 1.2). There is no `plan:manual`. |

## Which mode a task gets

A task's mode is, in order:

1. your override in the TUI;
2. its `Mode` column in the plan;
3. the run mode, `igris arise --mode`;
4. `default_mode` in `igris.toml`, which is `auto` unless you set it (`default_mode = "manual"` gets you every permission prompt).

Choose a mode in the TUI per run or per task: `m` (or **Mode**) for the run, or select a task and press `M` (**Task mode**) to override it for that task. The mode picker (`m`, `M` and the wizard, since 1.7) lists plan, auto, accept, manual and default; **More modes…** adds plan:default, plan:accept, plan:auto, yolo and plan:yolo. A change applies to the next session; a running one keeps its mode. User tasks have no session, so they have no mode.

Use the plan's `Mode` column for tasks that always want one, such as `plan` for design-heavy work. The mode names are also listed in the [plan format](../../docs/plan-format.md).

## Skip permissions

Skip permissions is never a single click or key. You type `skip permissions` to confirm, **every run**, and a red badge shows while it is active. There is no setting that remembers it. The same goes for `plan:yolo`: its session starts in plan mode with `--allow-dangerously-skip-permissions`, because Claude Code ignores a switch to skip permissions without it, and Claude Code warns that plan mode then won't ask before most edits or commands. So the badge shows from the start, not only after you approve.

Use it only where you would trust an unattended agent: in a container you trust, or with **worktree mode**. `igris arise P1 --worktree` (or `worktree = true` under `[run]`) runs the whole run in a git worktree on a branch of its own, `igris/<run-id>`, so your checkout and current branch stay as they are. Whatever the sessions do lands on a branch you can review, merge or throw away. igris never merges: when the run completes, it removes the worktree, keeps the branch and tells you the `git merge` command.

## What you cannot set

`claude.extra_args` in `igris.toml` can't carry flags that change the session's model, mode, permissions, tools, directory or system prompt (`--model`, `--permission-mode`, `--settings`, `--allowedTools`, `--add-dir`, `--bare`, `-c`/`-r`/`-w`, …): igris sets what it needs itself and rejects a config that has them.
