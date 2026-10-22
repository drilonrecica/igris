# Parallel phases

Since 1.5 the tasks of a phase can run at the same time. It is opt-in per phase: a phase without the marker runs one task at a time, as in every earlier release, so no existing plan changes meaning.

Read [what it costs](#what-it-costs) before you turn it on. Parallel is faster on the clock, not cheaper, and not always safe.

## Turn it on

Declare plan format 3 and mark the phase:

```markdown
<!-- igris:format 3 -->

## M1 — Services
<!-- igris:parallel -->

| ID | Task | Deps | Status | Model | Owner | Group |
|---|---|---|---|---|---|---|
| M1-01 | Auth service | M0-05 | ready | sonnet | agent | |
| M1-02 | Billing service | M0-05 | ready | sonnet | agent | |
| M1-03 | Search service | M0-05 | ready | opus | agent | db |
| M1-04 | Reports service | M0-05 | ready | opus | agent | db |
| M1-05 | Integration gate | M1-01, M1-02, M1-03, M1-04 | blocked | sonnet | agent + user | |
```

The marker means "the dependencies in this phase are complete": any two tasks that don't depend on each other, directly or through others, may run together. File order stops being a dependency. If task B really needs task A, give B a `Deps` entry; `igris check` warns when a task's text names an earlier task it doesn't depend on. igris 1.0 to 1.4 refuse a format-3 plan, so finish a run before you go back to them.

`[parallel] max` in `igris.toml` (default `2`, up to `8`) or `igris arise --parallel N` says how many run at once. See [Configuration](../../docs/config.md#parallel).

## What runs together

- **Dependencies first.** A task starts only when everything it waits on is done *and integrated*.
- **`max` at a time.** The first ready tasks in file order, up to `max`.
- **Rank caps.** `[parallel.ranks]` limits how many tasks of one rank are active; the default is `fable = 1`, so two of your most expensive tasks don't run side by side unless you say so.
- **`Group`.** Tasks with the same non-empty `Group` never run together: the way to say "these touch the same database or the same module".
- **Tasks that need you run alone.** A `user` or `agent + user` task, gates included, starts only when nothing else is active, and nothing starts beside it. Set `exclusive_owner_tasks = false` to schedule them like the others.

A task that needs you (a question, a permission) doesn't hold the others: you answer in turn, in plan order, in one queue.

## How it works

Each task gets its own git worktree, `.igris/worktrees/<run-id>-<ID>` on branch `igris/<run-id>-<ID>`, cut from the run branch's current head. Its session, verify and task hooks run there; a session never sees another task. The plan stays in your checkout, and igris is the only thing that writes its Status cells.

When a task passes verify, igris commits in its worktree, rebases it onto the run branch, runs verify again on the integrated tree and fast-forwards the run branch. Only then is the task `done`, so a dependent never starts on code that isn't on the branch. The history stays linear, one commit per task, in the order tasks finished.

Each task also gets `IGRIS_SLOT`, a number from `0` to `max - 1` that no other active task holds, in its session, verify and hooks. Use it to offset ports and test databases: `PORT=$((8000 + IGRIS_SLOT))`.

If two tasks change the same file, igris warns once (`M1-03 and M1-04 both change internal/search/index.go`). Nothing is held; the integration finds out whether it matters.

### When a task doesn't integrate

A rebase conflict, or verify failing after the rebase, discards the task's branch and runs the task again from the start on the new base, once. A second failure is **Needs you** with the conflicting files or the verify output, and the worktree kept: **Rerun fresh**, **Mark done** (you integrated it by hand), **Skip** or **Stop igris**. `on_conflict = "ask"` skips the automatic rerun.

## What you see

On a wide terminal the run view shows one card per active task, with its slot, and a header that says `3 active` and `needs you: M1-03, M1-05`. On a narrow one, a switcher line above the selected card; `[` and `]` move between tasks. Notifications raised together go out as one: `2 tasks need you`. Detach, attach and Stop work as before, for every active task: Stop leaves every session open, and the next `arise` reattaches them all.

## What it costs

- **Your limits burn N times faster.** Three sessions use your Claude Code plan's limits three times as fast. A phase that took a day of quota can hit the cap in hours, and then every task waits for the window to reset. If you run on a plan with limits, start with `max = 2`.
- **Worktrees duplicate your caches.** A fresh worktree has no build caches, `node_modules`, virtualenv or generated files, so each task pays for its own install and cold build, and N worktrees use N times the disk. Put shared caches outside the tree (a Go module cache, `CARGO_TARGET_DIR` per slot, a package manager's global store) and commit what a worktree needs, because untracked and ignored files stay in your checkout.
- **Hidden dependencies make rework.** A plan written to run in order often leaves the order unsaid. Two tasks that both edit a shared file, or one that quietly needs another's output, conflict at integration, and the rerun throws the work away and does it again. That is paid in tokens and time. Fix a dependency you find by adding it to `Deps` (or a `Group`), not by raising `max`.
- **Verify runs more often.** After a rebase that changed the tree, verify runs a second time on the integrated tree. With slow verify commands, `serial_verify = true` runs one at a time while the sessions stay parallel.
- **Parallel runs need git and commits.** `commit = "never"` and projects without a commit run one task at a time instead, with a warning.
- **It is harder to follow.** N sessions need N times your attention when they ask questions. Don't run it unattended in a mode that asks a lot.

A good first phase is small, independent and well-gated: separate packages or services, no shared generated files, a verify that is quick. Run `igris phases` first: it shows which tasks could run together.

## Safety

Parallel changes nothing about the permission mode. Skip-permissions still needs the typed phrase every run, and each task's session has only the rules its own worktree needs. Combine it with [worktree mode](https://github.com/drilonrecica/igris#worktree-mode) (`--worktree`) to keep the whole run's work, task commits included, off your current branch.

See [SPEC §3.8, §5.1 and §6.11](https://github.com/drilonrecica/igris/blob/master/SPEC.md) for the exact rules, and the [plan format](../../docs/plan-format.md#parallel-phases-format-3) for the marker and `Group`.
