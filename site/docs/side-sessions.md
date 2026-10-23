# Side sessions

Since 1.2 you can open a Claude Code session of your own from igris, in a pane next to the run's, for a question or a look into something, without stopping the run and without it becoming a task.

A side session is **not a task**. igris doesn't watch it for `igris done`, counts no attempt, writes no Status for it, never marks it **needs you**, sends no notification for it and applies no timeout. Inside it, `igris done`, `skip` and `reset` refuse (its environment has `IGRIS_SIDE`).

## Open one

Press `S` on the home screen or in the run view. It needs a reachable backend (herdr or tmux) and is not available with `--no-tui`. The dialog asks:

1. **What it is about.**
   - **Blank**: no first prompt; the session opens at its input box.
   - **About this task** (with a task selected or current): the task's ID, title and phase, the plan's path, the task text, whether it runs now, then `Question: `.
   - **igris problem**: igris's version, the backend, the run state, the last 10 run-log events and the summary of `igris doctor`, and a pointer to `.igris/runs.jsonl` and `.igris/state.json`. Never config values or secrets.
2. **The first prompt.** An editable field holding the prefill. It is cleaned of escape sequences, at most 8 KiB, and typed once the session is ready. Empty types nothing.
3. **The model**, from your `[models]` ranks. `[side] model` is preselected (`sonnet` unless you set another).
4. **The mode**: `default`, `manual`, `accept`, `auto` or `plan` (approved in the pane, not by igris). `[side] mode` is preselected. Skip-permissions is never offered, and `[side] mode = "yolo"` is a config error.
5. **During a run, where it works.** The **shared tree** (the default) or **its own worktree**.

```toml
[side]
model = "sonnet"
mode = "default"
```

## During a run

A side session in the shared tree works in the same files as the task. The dialog says so (`M2-04 is working in this tree; a plan edit holds the run`, or in a parallel run `The run merges into this tree; a plan edit holds the run.`) and preselects `manual` mode whatever `[side] mode` says, so every edit asks first, whatever Claude Code's own default is. The rules igris gives the session also tell it to ask you before editing files while a task is running.

Two things to know:

- **A plan edit from a side session holds the run**, like an edit from any session, and so does an `igris.toml` edit. igris has no exemption for side sessions: it would let any session claim to be one.
- **Its own worktree** (offered during a run only) runs the session in `.igris/worktrees/side-<id>` on the branch `igris/side-<id>`, so it shares no files with the task. Closing the session removes a worktree with no changes (the branch stays) and keeps one with changes, naming it.

## List, focus and close

The header shows `side N` while any are open, and home shows `SIDE N open`. `S` then opens the **Side sessions** page: one row per session with its state, age, subject, model, mode and `worktree`.

- `enter` or `o` brings one to the front.
- `x` asks before closing (**Keep it** or **Close session**).
- **New…** opens another. At most 8 can be open.

Quitting igris never closes them. The next home or run view finds them again and reattaches each to its own pane. A session whose pane is gone is logged as ended.

## Usage

igris reads a side session's transcript as a task's (token counts and model ids, never messages) and logs one `side_usage` line when the session ends. It is never counted as an attempt and never audited against a rank. `igris usage` shows side sessions in a table of their own, outside every total, and `igris usage --json` has an additive `side` array. See [usage and plans](usage.md).

## What a side session could do

It is a Claude Code session in your project, so it can write what any session can, `.igris/` included. `IGRIS_SIDE` stops an honest mistake (finishing a task from the wrong pane), not a session set on unsetting it. Records under `.igris/side/` are read as small regular files, and a record that names another pane ends without touching it. The full list is in the [threat model](https://github.com/drilonrecica/igris/blob/main/docs/threat-model.md).
