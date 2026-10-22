# Draft a plan

Since 1.4 `igris draft` has Claude write the plan for you: from a goal, from a spec file, from an issue export, or as new phases for the plan you already have. Nothing is written until you have read the result and accepted it.

```sh
igris draft goal "a CLI that syncs bookmarks between browsers"
igris draft spec docs/prd.md
igris draft import issues.json
igris draft extend "add a phase for an offline mode"
```

On home, press `D` (**Draft…**) for the same four, with a picker, progress, **Open session** and the review.

Drafting needs a backend (herdr or tmux), as `arise` and `adapt` do.

## How it works

1. A Claude Code session opens in a pane in `manual` mode, so every permission prompt comes to you. It uses `opus`, or `sonnet` with `--model sonnet` or `[draft] model` in `igris.toml`.
2. It writes a *proposal* under `.igris/draft/` and finishes with `igris done DRAFT`. It never touches your plan.
3. igris reads the proposal with the normal plan parser and shows the same review as [`igris adapt`](adapt.md): what the plan would contain, and what is wrong with it.
4. You accept or reject. A proposal that doesn't pass `igris check` (usually ranks the session left at `?`) needs a second confirmation. Only Accept writes the plan.

**The ranks are suggestions.** The review's header says how many Model cells were set and how many were left at `?` for you to decide. The proposal ends in a `## Draft notes` section with one line per task giving its rank and why. Change the Model column before the first run.

## The four sources

| Command | Input | Notes |
|---|---|---|
| `igris draft goal [TEXT]` | a sentence, or the interview | Without TEXT the session asks for the goal first. It asks a few rounds of questions in the pane, then writes the plan. `M0-01` is your task of writing the spec from its summary, which `## Draft notes` holds. |
| `igris draft spec FILE` | a spec or PRD | A regular UTF-8 file of at most 1 MiB inside the project. The session reads it itself. Every requirement becomes a task or is listed in `## Draft notes` as left out. |
| `igris draft import FILE` | an issue export | `--format github\|linear\|jira`, or detected. GitHub: the JSON `gh issue list --json number,title,body,state,labels` prints. Linear and Jira: their CSV exports. At most 16 MiB and 1000 items. Closed and done issues become `done` rows. |
| `igris draft extend [TEXT]` | a short request | New phases, or tasks for existing phases, in a valid plan. Also a [split plan](split-plans.md). |

`import` is read-only: igris parses the file itself and never contacts GitHub, Linear or Jira. The session reads the cleaned result, not the raw export.

## Where a plan is written

`goal`, `spec` and `import` write a plan only where there is none. If the plan file exists, igris refuses before starting a session and says so:

- add to the plan with `igris draft extend`, or
- draft into a new file next to it with `--plan PATH`.

`extend` writes only what it adds. The review lists just the new rows, and on Accept igris backs up each file it changes to `.igris/draft/` and keeps every other byte. It writes outside Status cells, so it needs your approval, like `adapt`, and never runs while `arise` does.

## Untrusted input

A spec or an issue body can contain instructions. The session therefore runs in `manual` mode, writes only a proposal, and nothing reaches the plan without your Accept. Check the proposal as you would a pull request.

## Settings

```toml
[draft]
model = "opus"   # or "sonnet"
```

Flags: `--model sonnet|opus`, `--plan PATH`, and for `import`, `--format`. Exit codes: 0 when the proposal is accepted or rejected, 1 when a plan is in the way, the plan is invalid (`extend`), the file can't be read or parsed, or the session is lost, 2 for a usage error.
