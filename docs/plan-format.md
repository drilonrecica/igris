# The igris plan format (format 1)

This is the format of the markdown plans igris runs: how a plan is split into phases and tasks, what each column means, and what igris changes when it runs one. It is written for people writing plans by hand and for tools that produce them. It describes **format 1**, the only format so far, and is frozen for igris 1.x ([SPEC §19](../SPEC.md#19-compatibility)). [SPEC §3–4](../SPEC.md#3-canonical-plan-format) is the normative text; this page says the same with more examples. If they ever disagree, the SPEC wins and this page is fixed.

`igris check` validates a plan against all of this and prints every problem at once as `file:line: message`. A plan in another format can be converted with [`igris adapt`](../README.md#different-format-igris-adapt).

## A complete example

```markdown
# Shop backend

<!-- igris:format 1 -->

Anything outside the task tables is ignored: notes, legends, working rules.

## M0 — Foundation

| ID | Task | Deps | Status | Model | Owner | Mode | Spec |
|---|---|---|---|---|---|---|---|
| M0-01 | **Go module** — init module, pin the Go version | — | done | sonnet | agent | — | §1 |
| M0-02 | **Config loader** — read `shop.toml` | M0-01 | ready | sonnet | agent | — | §2 |
| M0-03 | **Wire format** — `v1 \| nonce \| ciphertext` | M0-01 | ready | opus | agent | plan | §3 |
| M0-04 | **Create the GitHub repository** | — | ready | — | user | — | — |

## Phase 2 — API

| ID | Task | Deps | Status | Model | Owner | Timeout | Verify |
|---|---|---|---|---|---|---|---|
| A-01 | **Wait for API keys from the client** | — | ready | — | user | — | — |
| A-02 | **Orders endpoint** | M0-02…M0-03, A-01 | blocked | sonnet | agent | 45m | — |
| A-03 | **Docs pass** | A-02 | blocked | haiku | agent | — | none |
| A-04 | **Old importer** | — | skipped (not needed) | — | agent | — | — |
| A-G | **API gate** — owner smoke test | A-02, A-03 | blocked | sonnet | agent + user | — | — |
```

This plan has two phases, `M0` and `Phase-2`, and 9 tasks. `M0-02` and `M0-03` are ready; `A-02` waits on them and on your own task `A-01`.

## Format marker

A plan may declare its format on a line of its own **before the first `##` heading**:

```text
<!-- igris:format 1 -->
```

- It is optional, and stays optional: a plan without a marker is format 1, in every igris 1.x and later.
- Spaces inside the comment don't matter (`<!--igris:format 1-->` is the same). A marker inside a fenced code block is not one.
- A marker that isn't a whole number, a second marker, or a marker after the first `##` heading is a validation error.
- A plan marked with a format igris doesn't know (`<!-- igris:format 2 -->`) is refused with `plan format 2 needs a newer igris`, and nothing else about it is reported or changed. A future format will need its marker, so an unmarked plan never has to be edited.

## Phases

- A phase is a level-2 heading (`## `) whose section has a task table. `###` and deeper headings stay inside the current phase. `##` sections without a task table are ignored.
- The **phase ID** is the first word of the heading: `## M0 — Foundation` is `M0`. When the first word is alphabetic and the second is all digits, both make the ID with a hyphen: `## Phase 2 — API` is `Phase-2` (but `## Phase 2: API` is `Phase`, since `2:` isn't all digits).
- Phase IDs are unique within a plan, compared without regard to case (`## m0` and `## M0` clash). Wherever a phase is named (the command line, `[phases.<id>]` in `igris.toml`), case doesn't matter either.
- Phases run in file order. Headings and tables inside fenced code blocks (```` ``` ```` or `~~~`) are ignored.
- A plan needs at least one task table.

## Task tables

A task table is a GitHub-flavored markdown table whose header has at least **ID**, **Status** and **Model**. A phase has at most one task table; other tables next to it (a legend, say) are ignored. A task table before the first `##` heading is an error.

| Column | Required | Values |
|---|---|---|
| `ID` | yes | Unique across the plan: a letter or digit, then letters, digits, `.`, `_` or `-` (`M0-01`, `A.3`, `42`). |
| `Task` | recommended | The description. Its first `**bold**` span is the task's title; without one, its first 80 characters. |
| `Deps` | no | Dependencies, below. Without the column, no task has any. |
| `Status` | yes | Below. |
| `Model` | yes | A rank from `[models]` in `igris.toml` (`sonnet`, `opus`, `fable`, `haiku` by default), or `—` for none. |
| `Owner` | no | `agent`, `user` or `agent + user`. Without the column, or empty: `agent`. |
| `Mode` | no | `default`, `accept`, `auto`, `plan`, `yolo`, or `—`: the run mode for this task's session. |
| `Verify` | no | A verify profile from `igris.toml`, `none` to skip verification, or `—`/empty for the default. Never a command. |
| `Timeout` | no | A Go duration of at least `1s` (`45m`, `1h30m`) after which the task is reported overdue. |
| `Context` | no | Up to 20 comma-separated paths inside the project that the session is told to read first. |

Reading the header and cells:

- Column names are matched without regard to case, with surrounding spaces, tabs, `*` and backticks ignored (`**Status**` is `Status`). `igris.toml` can alias other names (`[columns]`, e.g. `"Depends on" = "Deps"`). A header that names a column twice is an error.
- A table with a column that looks like dependencies (`Depends on`, `Requires`, `Blocked by`, …) but no `Deps` column is valid; `igris check` warns that igris reads no dependencies from it.
- **Extra columns** (`Spec` above) are kept as they are and given to the session as context.
- Cells are split on unescaped pipes only: `\|` in a cell is a literal `|`.
- In `Model`, `Mode` and `Deps`, an empty cell, `—`, `-` and `none` (any case) all mean "none". In `Verify`, `Timeout` and `Context`, empty, `—` and `-` mean "not set"; in `Verify`, `none` means "don't verify".
- `Owner`, `Mode` and `Verify` values are read in any case (`Agent + User`, `YOLO`, `Fast`). **Model ranks are case-sensitive** (`Sonnet` is not `sonnet`; `igris check` suggests the right one).
- Backticks around a `Model`, `Mode`, `Verify`, `Timeout` or `Status` value are ignored.
- A control character (other than tab) in a task row or a phase heading is an error.

Rules that tie the columns together:

- `user` tasks have Model `—`. `agent` and `agent + user` tasks need a Model that `[models]` resolves, unless they are `done` or `skipped`.
- `Verify`, `Timeout` and `Context` are checked only on tasks that aren't `done` or `skipped`. On a `user` task they are ignored, with a warning.
- A `Context` path must exist inside the project: no absolute paths, no `..`, not `.` itself, no trailing `/` on a file, no symlink out of the project.

## Status

`ready`, `blocked`, `in progress`, `done`, `skipped`, in any case, optionally in backticks. Anything after the word is kept and ignored: `skipped (not needed)` is `skipped`. Any other value is an error.

- `done` and `skipped` satisfy a dependency.
- A task whose dependencies are all satisfied should be `ready`, otherwise `blocked`. igris keeps this true as it runs; `igris check` warns where the file disagrees.

## Dependencies

- Empty, `—`, `-` or `none`: no dependencies. Otherwise a comma-separated list of task IDs, from any phase.
- A **range** `A…B` (or `A...B`, `A..B`) is every task from `A` to `B` in file order. Both ends must exist and `A` must come first. An entry that is itself a task ID is never read as a range, since IDs may contain dots.
- Duplicates are ignored. An unknown ID, a task depending on itself and a cycle are errors (a cycle is shown as its path, `a → b → a`).
- To wait on something outside the plan (keys from a client, a deploy, a date), depend on a `user` task that names it, like `A-01` above.

## What igris changes in a plan

- **Only Status cells.** igris writes `in progress`, `done` and `skipped`, and moves tasks between `ready` and `blocked` as dependencies are met. Every other byte stays as it is: other cells, whitespace, line endings (CRLF too), the trailing newline, a UTF-8 byte order mark, the format marker.
- In a Status cell, the padding and backticks are kept (`` `ready` `` becomes `` `done` ``); text after the old word is dropped, since it described the old status.
- igris never adds, removes or reorders rows. The only exception is `igris adapt`, which replaces the plan only after you accept its proposal.
- Writes are atomic, and a plan someone else changed in the meantime is re-read rather than overwritten.

## What can change in igris 1.x

New **optional** columns may be added to format 1. An older igris 1.x keeps such a column as an extra column, so a plan that uses one still runs. Nothing else in this page changes in 1.x: no column, status word, rule or value is removed or changes meaning.
