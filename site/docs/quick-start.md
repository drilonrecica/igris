# Quick start

From a new project to a finished phase. Run these inside a herdr pane or a tmux window ([where igris runs](install.md#where-igris-runs)).

```sh
cd your-project
igris init          # creates igris.toml and .igris/, ignores .igris/ in git, allows `igris done` for Claude
igris doctor        # is everything in place? one line per check, with the fix for each problem
igris check         # validates your plan
igris status        # phases and tasks: what's ready, what's blocked
igris arise M0      # runs phase M0
```

Or run bare `igris` in a terminal: the home screen walks you through the same steps, from a new project to a run.

## 1. A plan

igris reads a markdown file, `tasks.md` by default, with one task table per `##` phase:

```markdown
## M0 — Repository foundation

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M0-01 | **Go module** — init module, pin Go version | — | ready | sonnet | agent |
| M0-02 | **Entrypoint** — main.go with subcommands | M0-01 | blocked | sonnet | agent |
| M0-03 | **Crypto envelope** — versioned secret format | M0-01 | blocked | opus | agent |
| M0-04 | **Create GitHub repo** | — | ready | — | user |
| M0-G | **M0 gate** — owner smoke test | M0-01…M0-04 | blocked | sonnet | agent + user |
```

The **Model** column is the only input for the model: a rank from `[models]` in `igris.toml` (`sonnet`, `opus`, `fable` and `haiku` by default). The [plan format](../../docs/plan-format.md) explains every column.

No plan yet? `igris init --example` writes an example one. A plan in another format? [`igris adapt`](adapt.md) converts it.

## 2. Look before you run

- `igris check` validates the plan and prints every problem as `file:line: message`.
- `igris arise M0 --dry-run` shows the launch order with each task's model, mode and verify profile, without starting anything or writing a byte.

## 3. Run a phase

`igris arise M0` takes the first ready task, opens a new herdr tab or tmux window, starts Claude Code with that task's model, and hands it the task, the plan and your project rules. When Claude runs `igris done M0-01`, igris runs your checks if you set them, commits if you want, marks the task `done`, unblocks what depends on it and starts a fresh session for the next task, until the phase is finished.

- `--through M2` continues through later phases. `--only M1-03,M1-05`, `--from` and `--until` run part of a phase.
- Quitting igris leaves the session running. `igris arise` with no phase resumes exactly where it left off.
- If a task needs a decision, igris waits and [notifies you](notifications.md). Press `o` in the TUI to jump to the session.

**The first time,** Claude Code asks whether you trust the project folder ("Is this a project you trust?"). igris can't answer that for you: the task waits, and after about 30 seconds igris shows **needs you: the agent is blocked**. Open the session (`o`), answer once, and the task carries on. Later sessions in that folder don't ask again.

## 4. Check and commit

Set a verify command in `igris.toml` and igris runs it each time a session says it's done:

```toml
[run]
verify = "make fmt lint test"
commit = "ask"        # or "auto", or "never"
```

A failing verify goes back into the same session to fix; after three failures in a row igris calls you. See [configuration](../../docs/config.md) for every key.

## Next

- [Modes](modes.md): how much a session may do without asking you.
- [Notifications](notifications.md): hear about it on your phone.
- [Several projects](several-projects.md) at once.
