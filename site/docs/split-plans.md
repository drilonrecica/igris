# Split plans

Since 1.2 a long plan can live in several files. The **main plan** is the file igris reads (`tasks.md`, or `plan` in `igris.toml`); a line in it pulls in another file's phases at that point. The rules are in the [plan format](../../docs/plan-format.md#split-plans-format-2).

## Split a plan

Declare format 2 before the first `##` heading and put an include on a line of its own:

```markdown
<!-- igris:format 2 -->

# Shop backend

## M0 — Foundation

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M0-01 | Skeleton and CI | — | ready | sonnet | agent |

<!-- igris:include phases/v2.md -->
```

`phases/v2.md` holds `## V2 — Payments` and its task table. Its phases take the include line's place, in order. Check it:

```sh
igris check
igris phases
igris arise V2 --dry-run
```

`check`, `phases`, `status` and `arise --dry-run` read the plan as one. A problem names the file it is in: `phases/v2.md:12: task V2-01 is also in tasks.md:40`.

## The rules

- Paths are relative to the main plan's directory, end in `.md`, have no `..` and stay inside the project (symlinks resolved). Nothing under `.igris/`.
- Only the main plan includes files, one level deep, each file once.
- At most 32 files and 16 MiB in all.
- Phase and task IDs are unique across all files. A dependency may point into another file, and a range `A…B` follows the order of the whole plan.
- A plan with an include must declare `<!-- igris:format 2 -->`. Without it, each include is an error that says to add it.

## While it runs

- **igris writes each Status cell in the file its row is in.** Include lines and every other byte stay as they are.
- **An edit to any file holds the run**, as an edit to a one-file plan does, and so does an include added or removed, or a row moved to another file.
- The task prompt names the file the row is in (`TaskFile` in a `prompt_template`, equal to `PlanFile` in a one-file plan). In worktree mode, sessions can read each file of the plan.

## Things to know

- **A split plan needs igris 1.2 on every machine that runs it.** igris 1.0 and 1.1 refuse a format-2 plan with `plan format 2 needs a newer igris`; the marker is there so they stop instead of running only the main file's phases.
- **`igris adapt` refuses a split plan.** Inline the included files, or adapt one of them with `--plan`.
- A write across several files is not atomic. If igris is killed between two renames, the next loop recomputes the readiness cells of the files left behind.
