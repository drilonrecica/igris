# igris

> *Arise.* One task, one fresh session, the right rank.

**igris** runs the tasks in your markdown project plan **one at a time**, each in a **fresh Claude Code session**, started with **exactly the model your plan assigns to that task**.

You write the plan. You decide which tasks need Fable, which need Opus and which are fine on Sonnet. Igris makes sure that's what actually happens: it never runs a Sonnet task on a more expensive model, never lets one task's context bleed into the next, and stops to wait for you whenever a task needs a decision.

> **Status:** early development. v0.1.0 is not released yet. This README describes the planned v1 behavior; see [`SPEC.md`](SPEC.md).

---

## Why

Long Claude Code sessions fill their context and drift. Picking the model by hand for every task is tedious and easy to get wrong, and the expensive mistake is the silent one: a routine task quietly running on your most expensive model.

If you already plan your work as a task table with dependencies and a model per task, igris turns that plan into an execution queue:

```
igris arise M0
```

1. Takes the first ready task in phase `M0`.
2. Opens a new pane and starts Claude Code with that task's model (`--model sonnet`, `opus`, `fable`, …).
3. Hands it the task, the plan and your project rules.
4. If Claude needs you — a decision, an approval, a plan to review — igris waits and notifies you.
5. When the task is truly done, Claude runs `igris done M0-03`. Igris optionally runs your checks, offers to commit, marks the task `done`, unblocks dependents, closes the session.
6. Starts a fresh session for the next task. Repeat until the phase is finished.

Strictly sequential. Deterministic. No LLM decides which model runs what.

## Shadows have ranks

Like summoning the right shadow for the fight, each task gets the rank your plan gives it:

| Rank (your plan) | Typical use | Claude Code model |
|---|---|---|
| `sonnet` | routine, well-specified work | `--model sonnet` |
| `opus` | complex or security-sensitive logic | `--model opus` |
| `fable` | the highest-stakes design and correctness work | `--model fable` |

Rank aliases are configurable in `igris.toml`.

## Requirements

- **Claude Code**, logged in with your subscription (Pro/Max) or however you normally use it. Igris starts ordinary interactive sessions, so it uses your normal login and limits.
  > If `ANTHROPIC_API_KEY` is set in your environment, Claude Code bills the API instead of your subscription. Igris warns you about this at start.
- **[herdr](https://herdr.dev)** — v1 runs sessions in herdr tabs, so everything survives disconnects and you can reconnect over SSH (e.g. from your phone). tmux support is planned.
- Linux or macOS.

## Install

```sh
go install github.com/drilonrecica/igris/cmd/igris@latest
```

Prebuilt binaries will be on the [Releases](https://github.com/drilonrecica/igris/releases) page.

## Quick start

```sh
cd your-project
igris init          # creates igris.toml and .igris/, adds Claude allow rules for `igris done`
igris check         # validates your plan
igris status        # shows phases, tasks, what's ready and what's blocked
igris arise M0      # runs phase M0 (inside a herdr pane)
```

Recommended once: `herdr integration install claude`, so herdr reports Claude's state accurately.

## Your plan

A plan is a markdown file (default `tasks.md`) with one task table per `##` phase:

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

- **Deps:** comma-separated IDs or ranges (`M0-01…M0-04`), across phases too.
- **Status:** `ready`, `blocked`, `in progress`, `done`, `skipped`. Igris keeps this column up to date and touches nothing else in the file.
- **Owner:** `agent`, `agent + user` (the agent must get your decision or sign-off), or `user` (your own task: igris pauses until you mark it done).
- **Optional `Mode` column** to force a mode per task (e.g. `plan` for design-heavy tasks).

Different format? Run **`igris adapt`**: a Claude session (Sonnet or Opus) converts your plan into the canonical format, you review the diff, and nothing changes unless you accept. It never invents models; missing ones are flagged for you.

## Modes

Choose the permission mode in the TUI, per run or per task:

| Mode | What it does |
|---|---|
| Default | Your normal Claude Code permission prompts. |
| Accept edits | Edits are accepted automatically. |
| Auto | Claude Code approves routine actions itself and asks you about risky ones. |
| Plan | Claude plans first; you approve the plan, then it implements. |
| Skip permissions | `--dangerously-skip-permissions`. Needs typed confirmation every run, and shows a red badge while active. Use it in a worktree or container you trust. |

## Notifications

Igris tells you when it needs you (a question, a plan to approve, a stalled session), when a phase is done or stuck, and when something fails:

- herdr toasts
- [ntfy](https://ntfy.sh) push to your phone
- Discord webhook

## The TUI

Igris runs in its own herdr pane: the task list with status and rank, the current task and how long it's been running, and a log. Claude sessions run in their own tabs; press `o` to jump to the current one. The layout collapses to a single column on small terminals, so it works over SSH from a phone.

`q` quits the TUI without stopping anything. `igris arise` picks up exactly where it left off.

## Roadmap

- tmux backend (and other multiplexers) for people who don't use herdr
- Homebrew tap

## License

MIT
