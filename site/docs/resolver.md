# The conflict resolver

In a [parallel phase](parallel-phases.md) a task that passes verify is rebased onto the run branch and verified again. When that fails, because the rebase conflicts or because verify fails on the merged tree, igris reruns the task once from the start, and then asks you. A rerun costs a whole session. `on_conflict = "resolve"` puts a cheaper step first: a **resolver session** with one narrow job.

```toml
[parallel]
on_conflict = "resolve"      # rerun (the default) | ask | resolve
resolver_rank = "sonnet"     # optional; a [models] key. Unset: the task's own rank
```

## The order

With `resolve`, a task that doesn't integrate goes through: **resolve, then rerun once, then Needs you.** At most one resolver and one rerun per task and run, never a loop. With `ask` you are asked at once.

## What the resolver is

A fresh Claude Code session in the task's worktree, with the task's rules, hooks, mode and environment. It gets:

- the task as the plan gives it, and why it is here: the files the rebase stopped on, or the last lines of the verify output;
- the other side: the tasks the run branch took meanwhile, each with its ID, title and text from the plan;
- how to read both (`git diff`, `git log`, `git show`) and the job: a resolution in which both tasks' intent survives.

It edits files; igris finishes the rebase. It ends as every task session does: `igris done` means resolved, and `igris skip` means it gave up, which is a failed resolution with its reason, never a skip of the task.

Its model is never chosen by igris. It is `resolver_rank`, or the task's own rank. The rank audit checks it like any other session.

## What igris checks

After the done, igris continues the rebase (or folds a verify fix into the task's commit, still one commit per task) and checks the branch's shape: no rebase in progress, no unmerged paths, no conflict markers, one commit ahead of the target. Then **verify runs on the integrated tree, always**, and only then does igris fast-forward the run branch. A resolution that fails any of this is thrown away with its worktree, and the task is rerun on the new head.

## Resolve on demand

On the "didn't integrate" question, **Resolve** opens one resolver for that task, if none has run for it in this run, whatever `on_conflict` says.

## In the log and on screen

The run log gets `resolver_opened`, `resolver_done` and `resolver_failed` ([run log](../../docs/runlog.md)); its tokens are `usage` lines with `"resolver": true` under the task's attempt, so it is no attempt and budgets count it as the task's. The card shows `Working` with the resolver on it. There is no notification of its own: a failure ends in the usual Needs you.

Stop leaves the resolver open and the worktree mid-rebase, and the next `arise` reattaches it.

## What it can do

It is a session like any other, so it can do anything a task session can in its worktree, including producing a resolution that drops one side's intent. What it cannot do is touch the run branch: only igris's fast-forward does, after verify. See the [threat model](../../docs/threat-model.md).
