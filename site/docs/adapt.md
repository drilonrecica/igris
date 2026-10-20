# Adapt a plan

`igris adapt` converts a plan igris can't read into the [canonical format](../../docs/plan-format.md). It never replaces your plan until you accept a proposal you have read.

```sh
igris check            # tells you the plan isn't valid
igris adapt            # opens a session that proposes a conversion
igris adapt --model opus --plan docs/roadmap.md
```

It needs a backend: run it in a herdr pane or a tmux window, like `arise`.

## What happens

1. igris opens one Claude Code session (mode `default`) with the rank in `adapt.model` from `igris.toml`: `sonnet` by default, `--model opus` for a hard one. `--plan PATH` adapts a plan other than the configured one.
2. The session gets the format, `igris check`'s complaints, your `[models]` and the names of your verify profiles (never their commands). It writes the converted copy to `.igris/adapt/<plan>.proposed.md` and finishes with `igris done ADAPT`. It may ask you things in its pane, like any session.
3. igris opens a review of what the proposal changes.
4. Nothing replaces your plan until you accept.

## What it keeps and what it never invents

It keeps every task, ID, description, dependency, status and model; it only restructures. It never invents models: a task without a usable one gets Model `?` and is listed under `## Adapt notes`, and `igris check` keeps rejecting it until you fill it in. Prose like "waits on" or "blocked by deploy" becomes a `user` task that names the blocker. Verify, Timeout and Context cells are kept as written and never invented.

## The review

- The review shows the `igris check` result and any problems on top. When your plan already has task tables, it compares them table by table (tasks by ID, cells by column name) and lists only what changed: added (`+`) and removed (`-`) phases, tasks and columns, and one `~` line per changed cell. Otherwise it is a line diff of the two files.
- Scroll with the arrow keys, `pgup`/`pgdn` or the wheel. **Reject** has the focus first, so `enter` changes nothing.
- `a` or **Accept** replaces your plan after backing it up to `.igris/adapt/<plan>.<timestamp>.bak.md`. `r`, `esc` or `q` reject; the proposal stays in `.igris/adapt/`.
- A proposal that doesn't pass `check` yet, with models left at `?`, asks once more before it replaces anything.
- If the plan changed on disk while you reviewed, nothing is replaced.

`adapt` can't run while `igris arise` runs in the project, and `Ctrl-C` stops waiting without closing the session. From the home screen, `A` runs the same thing when the plan is invalid. A plan that already passes `check` has nothing to adapt.
