# Igris — Specification (v1)

> *Arise.* One task, one fresh session, the right rank.

Igris is a terminal tool that runs the tasks of a markdown project plan **one at a time**, each in a **fresh Claude Code session** started with **exactly the model the plan assigns to that task**. It pauses whenever a task needs the owner, moves on only when a task is truly finished, and keeps the plan's Status column up to date.

This document is the normative source for igris v1. If code and this spec disagree, the spec wins until amended through a decision task in the implementation plan (`imp-docs/tasks.md`, kept private).

---

## 1. Goals and non-goals

### Goals
1. **Cost discipline.** A task marked `sonnet` is never run on `opus` or `fable`. The model comes from the plan, deterministically — never from an LLM's judgment.
2. **Clean context.** Every task starts in a brand-new Claude Code session. Nothing from the previous task leaks into the next one.
3. **Strictly sequential.** One task at a time, in plan order, respecting dependencies. No parallelism in v1.
4. **Human in the loop.** When a task needs a decision, approval or credentials, igris waits. It never advances on a guess.
5. **Subscription-friendly.** Sessions are ordinary interactive Claude Code sessions using the user's normal login (Pro/Max). Igris never handles API keys.
6. **Works from anywhere.** Runs inside a persistent multiplexer (herdr or tmux), so the owner can detach, reconnect over SSH (e.g. Termius), and find everything as it was.
7. **Bring your own plan.** Works on the owner's existing `tasks.md`, with an AI-assisted `adapt` step for plans that don't match the canonical format.

### Non-goals (v1)
- Parallel task execution or multiple agents per task.
- Running agents other than Claude Code (the backend interface leaves room; not implemented).
- Multiplexers other than herdr and tmux (zellij etc. may come later via the backend interface, §11), and running without one.
- Hosted service, web UI, accounts, telemetry. Igris makes no network calls except to the notification endpoints the user configures, and to GitHub's releases API when the owner checks for updates (§14).
- Estimating, pricing or billing token cost. Igris enforces the model and counts the tokens its own sessions' transcripts record (§6.9); it never puts a price on them.
- Native Windows. Windows is supported through WSL2, with herdr and Claude Code inside WSL as well; release builds are for Linux and macOS only.

---

## 2. Concepts

| Term | Meaning |
|---|---|
| **Plan** | The markdown file containing phases and task tables (default `tasks.md`), with the files it includes (§3.7, since 1.2). Single source of truth. |
| **Phase** | A `##` section of the plan containing one task table, identified by the first token of its heading (e.g. `P0`, `M0`). |
| **Task** | One row of a task table. |
| **Rank** | The model alias in the task's Model column (`sonnet`, `opus`, `fable`, …), resolved to a Claude Code `--model` value through config. |
| **Agent task** | Owner is `agent` or `agent + user`. Igris launches a session. |
| **User task** | Owner is `user` (Model is `—`). Igris pauses and waits for the owner to finish it. |
| **Session** | One Claude Code process in one multiplexer pane, bound to exactly one task. |
| **Side session** | Since 1.2: a Claude Code session the owner opens from igris in a pane of its own, bound to no task: igris doesn't watch it for `igris done` and it changes no Status (§6.10). |
| **Backend** | The adapter that creates panes, starts Claude Code, reads agent state and closes panes: herdr or tmux (§11). |
| **Signal** | A file written by `igris done` / `igris skip` that tells the running igris a task is finished. |
| **Run mode** | Claude Code permission mode used to launch a session: `default`, `acceptEdits`, `auto`, `plan`, `bypassPermissions`. |

---

## 3. Canonical plan format

Igris's parser is deterministic and strict. Plans that don't match this format are rejected by `igris check` and can be converted with `igris adapt` (§9).

This is **format 1**, frozen for 1.x (§19), and **format 2** (since 1.2): format 1 plus includes (§3.7), frozen for 1.x too. [`docs/plan-format.md`](docs/plan-format.md) describes it on its own, with examples, for people and tools writing plans; this section and §4 are normative.

**Format marker.** A plan may declare its format with `<!-- igris:format 1 -->` (or `2`) on a line of its own (spaces inside the comment don't matter) before the first `##` heading. Without one it is format 1, forever. A plan with an include line must declare format 2 (§3.7). A marker inside a fenced code block is not one. A marker that isn't a whole number (digits only, at least 1), a second marker, or a marker after the first `##` heading is a validation error. A plan declaring another format is refused with `plan format N needs a newer igris (this igris reads formats 1 and 2)` as its only validation issue (its tables may follow rules this igris doesn't know); `check`, `arise`, `doctor` and the home screen say to upgrade igris rather than suggest `igris adapt`, and the plan writer refuses it. igris 1.0 and 1.1 read format 1 only, so they refuse a format-2 plan the same way.

### 3.1 Phases
- A phase is a level-2 heading (`## `) whose section contains a task table.
- **Phase ID** = the first whitespace-delimited token of the heading text (`## M0 — Repository foundation` → `M0`). Matching on the command line is case-insensitive.
- If the first token is alphabetic and the second is a number (`## Phase 2 — API`), the phase ID is the two tokens joined with a hyphen: `Phase-2`. The second token must be digits only (`## Phase 2: API` has phase ID `Phase`).
- Only level-2 headings start a phase; `###` and deeper headings stay inside the current phase. Headings and tables inside fenced code blocks (```` ``` ```` / `~~~`) are ignored.
- Phase IDs must be unique within a plan, compared case-insensitively (`## m0` and `## M0` clash); `[phases.<id>]` in config (§12) matches a phase case-insensitively too.
- `##` sections without a task table (Legend, Working rules, traceability tables) are ignored. A plan needs at least one task table; a plan without any is a validation error (it is most likely in another format, §9).
- **Phase order** is file order.

### 3.2 Task tables
A task table is a GitHub-flavored markdown table whose header row contains at least the columns **ID**, **Status** and **Model** (case-insensitive, with surrounding spaces, tabs, `*` and backticks ignored). Recognized columns:

| Column | Required | Purpose |
|---|---|---|
| `ID` | yes | Unique task ID across the whole plan. Pattern: `[A-Za-z0-9][A-Za-z0-9._-]*`. |
| `Task` | recommended | Description. The first `**bold**` span is used as the task title; otherwise the first 80 characters. |
| `Deps` | no | Dependencies (§3.4). Missing column = no deps. |
| `Status` | yes | §3.3. |
| `Model` | yes | Rank alias or `—` for no model. |
| `Owner` | no | `agent`, `user`, or `agent + user`. Missing column = `agent`. |
| `Mode` | no | Per-task run-mode override (§7.2): `default`, `accept`, `auto`, `plan`, `plan:default`, `plan:accept`, `plan:auto`, `plan:yolo`, `yolo`, `manual` (since 1.2), or `—`. |
| `Verify` | no | Verify profile from config (§6.4), or `none` to skip verification for this task. Never a command. |
| `Timeout` | no | Go duration (`45m`, `1h30m`) after which a running task is reported overdue (§6.3). Never stops a session. |
| `Context` | no | Comma-separated repo-relative paths (files or directories) the task prompt names as required reading (§6.1). |

- In `Model` and `Mode`, `—`, `-`, `none` (case-insensitive) and an empty cell all mean "no model" / "no override", as in `Deps`. `Owner` is case-insensitive (`Agent + User` = `agent+user` = `agent + user`); an empty cell means `agent`. `Mode` and `Verify` values are case-insensitive too (`YOLO` = `yolo`); the `Model` rank is case-sensitive (`check` suggests the rank a near miss like `Sonnet` meant).
- `Verify` and `Timeout` cells are trimmed with surrounding backticks stripped (`` ` none ` `` is `none`). In `Verify`, `Timeout` and `Context`, an empty cell, `—` or `-` means "not set". In `Verify` only, `none` is not "not set": it turns verification off for the task, while an unset cell falls back to the phase's and the project's default (§6.4).
- `Verify`, `Timeout` and `Context` are checked only on tasks that are not `done` or `skipped`, since igris never runs those; a violation is a validation error:
  - a Verify profile must be defined in config (§12): `[verify]`, or `default` for `run.verify`; a cell that looks like a command (a space, a `/` or a shell character) is rejected with `Verify "make test" names a profile from [verify], never a command`;
  - a Timeout must parse as a Go duration of at least `1s` (`0s` and `500ms` fail with "must be at least 1s", a duration too large for Go with "is too large");
  - each Context entry is trimmed, with surrounding backticks stripped and empty entries and duplicates dropped (entries naming the same path once cleaned, like `docs` and `./docs/`, are one), at most 20 entries. An entry must not be absolute, must have no `..` element, must not name the whole project (`.`; list the files or directories to read), must exist, must not end in `/` when it is a file, and must stay inside the project root once symlinks are resolved (the root's own symlinks included, so a working directory reached through a symlink is fine). The root is the project root for `arise` and the working directory for `check`, `phases` and `status`.
- On a `user` task these three cells are ignored (there is no session); `check` and `arise` warn about each one that is set.
- Column names can be aliased in config (`[columns]`, §12), e.g. `Depends on` → `Deps`. A table without a `Deps` column whose header has a column that looks like dependencies (`Depends`, `Depends on`, `Dependencies`, `Requires`, `Blocked by`, …) is still valid, but `check` and `arise` warn that igris reads no dependencies from it.
- Extra columns (e.g. `Spec`) are preserved untouched and passed to the session prompt as context.
- Cells are split on **unescaped** pipes only; `\|` inside a cell is literal text (read as `|`; the file keeps `\|`).
- A table belongs to the phase whose heading most recently preceded it. A phase may contain only one task table; a second one is a validation error, as is a task table before the first `##` heading or a header that names the same column twice. Other tables (e.g. a legend) next to the task table are ignored.

### 3.3 Status values
`ready`, `blocked`, `in progress`, `done`, `skipped` — case-insensitive, optional surrounding backticks. Anything after the keyword (e.g. `skipped (not needed)`) is kept and ignored for logic.

- **Satisfied** (counts as a met dependency): `done`, `skipped`.
- **Owned by igris**: igris writes `in progress`, `done`, `skipped`, and recomputes `ready`/`blocked` (§5.2). Sessions are told not to edit Status (§6).
- An unknown status is a validation error.

### 3.4 Dependencies
- Empty, `—`, `-` or `none` = no dependencies.
- Otherwise a comma-separated list of task IDs.
- **Ranges:** `A…B`, `A...B` or `A..B` expands to every task ID from `A` to `B` inclusive, **in file order**. Both endpoints must exist and `A` must precede `B`. An entry that is itself a task ID is never read as a range (IDs may contain dots). Duplicate entries are ignored.
- Dependencies may point to tasks in other phases.
- **External blockers.** A task blocked on something outside the plan (an owner input, a date, a deploy, an answer from a client) depends on a `user` task that names the blocker, e.g. `X-00 · **Wait for API keys from client** · user · —`. Igris notifies when it is the user's turn (§8, §10) and continues once that task is done. The plan format has no other way to say "waiting on the outside", and none is added.
- Validation errors: unknown ID, self-dependency, dependency cycle, malformed range.

### 3.5 Owners and models
- `user` tasks must have Model `—`. `agent` and `agent + user` tasks must have a Model that resolves through `[models]` config, except finished ones (`done`, `skipped`): igris never starts a session for them, so their Model may be `—`. Violations are validation errors (igris never guesses a model).
- An unknown Owner or Mode value is a validation error.
- A control character (other than tab) in a task row or a phase heading — an escape sequence, say — is a validation error. Parsed cell text never carries one: the TUI, the prompts and the notifications only ever see cleaned text.

Validation reports **every** problem at once, each as `file:line: message` saying what to fix, sorted by line (in a split plan, the file the line is in, sorted in plan order, §3.7); a dependency cycle is reported once with its path (`a → b → a`).

### 3.6 Example

```markdown
## M0 — Repository foundation

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M0-01 | **Go module** — init module, pin Go version | — | ready | sonnet | agent |
| M0-02 | **Entrypoint** — main.go with subcommands | M0-01 | blocked | sonnet | agent |
| M0-03 | **Crypto envelope** — `v1 \| nonce \| ciphertext` | M0-01 | blocked | opus | agent |
| M0-G | **M0 gate** — owner smoke test | M0-02…M0-03 | blocked | sonnet | agent + user |
```

### 3.7 Includes (format 2)
Since 1.2 a plan may be split across files (decisions.md V12-P2). The **main plan** is the file igris is pointed at (`--plan`, `plan` in config); the files it includes are part of the plan.

- **Syntax.** `<!-- igris:include PATH -->` on a line of its own (spaces inside the comment don't matter). A line inside a fenced code block is not one. PATH is the text between `igris:include` and `-->`, trimmed, without quoting, and must end in `.md`.
- **Format 2.** A plan with an include line must declare `<!-- igris:format 2 -->`; without that marker (or with `1`) each include line is a validation error saying to add it. Only the main plan's marker counts; an included file may declare format 1 or 2 (so it can be checked on its own), and any other value is an error at its line.
- **Where.** Anywhere in the main plan outside a fenced code block. The included file's phases take the line's place: **plan order** (what "file order" means in §3.1, §3.4, §5 and §5.5) is the order of the plan with each include line replaced by its file. An include line ends the current section, so a task table after it and before the next `##` heading is an error. Text in an included file before its first `##` heading is prose.
- **One level.** Only the main plan includes; an include line in an included file is an error. Each file is included at most once: a second include of a file, or of the main plan, is an error naming the first.
- **Paths.** Relative to the main plan's directory. Not absolute, not starting with `~`, no `..` element. Once symlinks are resolved (the root's own included), the file must be inside the root Context paths are checked against (§3.2), must not be the main plan and must not be under `.igris/`. It is read as a regular file, following symlinks as the plan is, never blocking (§16).
- **Limits.** Each file, and all files of the plan together, at most 16 MiB; at most 32 files, the main plan included.
- **IDs.** Phase and task IDs are unique across all files; a clash names both places (`phases/v2.md:12: task V2-01 is also in tasks.md:40`). Dependencies may point into any file; a range follows plan order.
- **Errors** name the file the line is in, its path being the main plan's directory joined with the include's path. A problem with an include (missing, outside the root, not `.md`, too large, too many files, included twice, nested, no format 2) is reported at the include line. Context paths (§3.2) stay relative to the root wherever the row is.
- The `--json` output of `check`, `phases` and `status` adds `file` to each phase and a `files` list (the main plan first).

---

## 4. Plan writing rules

Igris edits the plan file in place, so writes must be surgical:

1. **Only Status cells change.** Every other byte of the file — whitespace, other cells, other sections, line endings, trailing newline, a leading UTF-8 byte order mark — is preserved exactly.
2. Within a Status cell, the original padding and backtick style are preserved (`` `ready` `` → `` `done` ``). Any text after the old keyword is dropped (`blocked (waits on vendor)` → `ready`), since it described the old status.
3. **Re-read before write.** The file is re-read and re-parsed immediately before every write, because the owner or a session may have edited it. If the target row no longer exists, igris stops with an error rather than writing.
4. **No lost updates.** Igris hashes the bytes it re-read and, immediately before the rename, hashes the file again. If it changed in between, the write is discarded and retried from step 3 (up to 3 times, then stop with an error).
5. **Atomic write:** write to a temp file in the same directory, fsync, rename. The file replaced is the one the plan path resolves to, so a plan that is a symlink stays a symlink.
6. Igris never reorders, adds or deletes rows, and never edits the plan outside Status cells — except `igris adapt` and, since 1.3, `igris draft extend`, each after explicit owner approval (§9, §9.1).
7. **Split plans** (§3.7). Each Status cell is written in the file its row is in; include lines are never changed. Steps 3–5 cover the whole plan: every file is re-read and the plan re-parsed, every file is hashed, a temp file is written for each file that changes, every file of the plan is hashed again, and only if none changed are the temp files renamed, one by one, each over the file its include path resolves to. The renames are not atomic together: a crash between two can leave ready/blocked cells of other files behind, which the next readiness sync recomputes (§5.2); a task's own Status change is always in one file.

---

## 5. Scheduling

### 5.1 Selecting the next task in a phase
On every loop iteration igris re-reads the plan, then selects, among the phase's tasks in **file order**:
1. the first task whose status is `in progress` (resume), else
2. the first task that is not satisfied and whose dependencies are all satisfied.

If no task qualifies:
- all tasks in the phase satisfied → **phase complete**;
- otherwise → **phase stuck**: igris stops, notifies, and lists every unfinished task with its unmet dependencies (e.g. `M0-13 waits on P0-05 (ready, phase P0)`).

### 5.2 Readiness sync
After every status change igris recomputes readiness for **all** tasks in the plan: a task whose status is `ready` or `blocked` is set to `ready` if all deps are satisfied, otherwise `blocked`. Tasks in `in progress`, `done` or `skipped` are never touched by this sync.

**Drift.** If the plan's `ready`/`blocked` cells don't match what the sync would compute (e.g. a hand-maintained plan), `igris check` reports each mismatch as a warning, and `igris arise` lists them and asks for confirmation before its first write.

### 5.3 Multi-phase runs
- `igris arise <phase>` runs one phase and stops when it completes.
- `igris arise <phase> --through <phase>` continues through the following phases (file order) up to and including the named one, stopping early if a phase gets stuck.
- Gate tasks have no special treatment; they're ordinary (usually `agent + user`) tasks.

### 5.4 Plan edits igris didn't make
Igris remembers every row as it last read or wrote it (Status, Model, Mode, Owner, Deps, Verify, Timeout, Context, Task text; rows added or removed). Whenever it re-reads the plan to select a task and finds a difference it did not write itself — a session editing a later task's Mode, Model or Verify (turning its verification off), marking tasks done, or the owner's own edit; igris can't tell them apart — it reports the changed cells (`M1-03 Mode — → auto`), sends `needs_input`, and **holds**: pause-after-task goes on and nothing is selected, not even "phase complete", until the owner resumes (`p`, or `pause` with `--no-tui`). The current task is not interrupted. Ready/blocked cells igris recomputes itself (§5.2) are never reported. In a split plan (§3.7) every file is re-read; an include added or removed shows as the rows it adds or removes, and a row moved to another file is reported (`V2-01 file tasks.md → phases/v2.md`).

### 5.5 Running a slice
`arise --only ID[,ID…]`, `--from ID` and `--until ID` run part of the phase range.
- `--only` can't be combined with `--from`/`--until`. An empty list (also one of only commas and spaces, `--only " , "`) or a malformed ID is a usage error.
- The phase range is the named phase and `--through`, as in §5.3. With no phase named it comes from the flags: from the phase of the earliest named task (file order) through the phase of the latest (or through `--through`, when given); `--from` or `--until` alone runs that task's phase.
- Checked before anything is written: every named ID exists and lies inside the phase range, and `--from` doesn't come after `--until` in file order. Otherwise `arise` exits 1 saying what to change.
- The **slice** is the tasks of the range listed in `--only`, or the tasks from `--from` (default: the first of the range) to `--until` (default: the last), in file order. §5.1 selects among the slice's tasks only: the first one `in progress`, else the first unsatisfied one whose dependencies are all satisfied.
- When unsatisfied slice tasks are left in a phase but none qualifies, each is reported as **not run** with its unmet dependencies (`not run: M1-05 waits on M1-04 (ready, phase M1)`), in the TUI and the run's final summary. It is never marked. The run goes on with the next phase of the range; this is not a stuck phase and sends no `phase_stuck`. The run ends when the slice has nothing left to run.
- `phase_done` is sent only for a phase whose tasks are all satisfied. Tasks outside the slice change only through readiness sync (§5.2).
- The interrupted task of an earlier run is still picked up first (§13), also when it is outside the slice; `arise` says so.
- `state.json` records the selection with the phases, so a bare `arise` resumes the same slice; a selection that can't be resumed (a task left the plan, say) is named as the last run's (`the last run's --only M1-09 is not a task in tasks.md…`). `--dry-run` walks the slice.

---

## 6. Session lifecycle (agent tasks)

For each selected agent task:

1. **Resolve** rank → model (`[models]` config) and run mode (§7.2). Unknown rank → stop with error, before anything is written.
2. **Mark** the task `in progress` (§4), then run the `before_task` hook, if one is configured (§6.7).
3. **Open a pane** via the backend in the project directory (the run's worktree in worktree mode, §6.8), labelled `<ID> · <rank>`.
4. **Start Claude Code** in that pane: `claude --model <model> --session-id <uuid>` + run-mode flags + `--append-system-prompt-file <igris rules file>` + `--settings <igris hooks file>` (§6.3) + `claude.extra_args` from config. Neither prompt travels as argv: herdr rejects control characters (newlines) in agent arguments (§11.2). Igris writes the rules to `.igris/prompts/<ID>.rules.md` first.
5. **Submit the task prompt (§6.1)** with the backend's prompt call once the session is `idle` (the backend's start call only returns when Claude Code is ready for input, so this cannot race with startup).
6. **Record** the session in `state.json` (§13). The task and its Claude session UUID (generated by igris) are written before step 2 and the session ref right after step 4, so a crash at any point leaves enough to resume.
7. **Watch** (§6.3) until a completion signal arrives or the owner intervenes.
8. **Verify** (§6.4), optionally **commit** (§6.5).
9. **Mark** `done`, sync readiness, append to the run log.
10. **Close** the pane (the Claude Code session ends; its transcript stays in Claude Code's own history), then run the `after_task` hook, if one is configured (§6.7).
11. Select the next task.

### 6.1 Task prompt
Two parts (§6 steps 4–5):
- **Igris rules** — the fixed, non-negotiable part (exactly one task, don't edit Status, how and when to run `igris done`, ask the owner when blocked). Passed with `--append-system-prompt-file` (verified in P0-03; accepted by Claude Code 2.1.291 though absent from `--help`) so they survive context compaction in long sessions.
- **Task prompt** — the first user message (sent via the backend's prompt call, multi-line OK), built from a Go `text/template`. The default template ships embedded; the owner can override it with `prompt_template` in config. Variables: `ID`, `Title`, `Text` (full Task cell), `Phase`, `PhaseTitle`, `Rank`, `Model`, `Owner`, `Deps`, `Extra` (map of extra columns, e.g. `Spec`; the canonical `Verify`, `Timeout` and `Context` are not in it), `Context` (list of the Context cell's paths as written, §3.2), `PlanFile` (the main plan: its path as configured, or absolute from a run's worktree, §6.8), `TaskFile` (since 1.2: the file the task's row is in, §3.7, in the same form: the include's path joined to `PlanFile`'s directory; `PlanFile` for a row of the main plan, so for every row of a one-file plan; the default prompt names it when it differs), `Resumed` (bool), `CommitPolicy`, `DoneCommand`.

Defaults live in `internal/prompt/rules.md` (static, no template variables, so the rules hold after compaction) and `internal/prompt/task.md.tmpl` (P0-06).

The default prompt must tell the session:
- It is working on **exactly one task** (`ID — Title`) as part of an automated sequential run; do nothing beyond it.
- Read the project's agent rules (`CLAUDE.md`/`AGENTS.md` if present), the task's row in the plan, and any referenced specs before changing anything.
- If anything needs the owner — a decision, approval, credentials, an unclear spec — **ask in the chat and wait**. For `agent + user` tasks: present the recommendation/result and get the owner's explicit decision or sign-off before finishing.
- **Do not edit the Status column**; igris owns it and unblocks dependents itself. (This overrides any plan rule that says the agent updates Status.)
- Commit policy: don't commit yourself; depending on `commit`, igris commits after verification (`auto`/`ask`) or not at all (`never`, in which case earlier tasks' changes may still be uncommitted in the tree).
- When the task meets the project's definition of done and nothing is waiting on the owner, run **`igris done <ID> --note "<one-line summary>"`** as the very last action. Never run it with anything unresolved.
- If the task has Context paths: list them as required reading before changing anything. igris names the paths only; it never reads or sends their contents.
- If `Resumed`: a previous session worked on this task and may have left partial changes; inspect `git status`/`git diff` first and continue from there.

### 6.2 Completion protocol
- `igris done <ID> [--note TEXT]` and `igris skip <ID> --reason TEXT` locate the project root (walk up from cwd to the nearest `igris.toml` or `.igris/`; from inside a run's worktree `<root>/.igris/worktrees/<name>`, it is `<root>`, §6.8), validate that `<ID>` exists, and write a signal file `.igris/signals/<ID>.json` (`{"id","action":"done|skip","note","at"}`) atomically. They print a one-line confirmation and exit 0; when no igris run holds the project lock (§13), the confirmation adds that the next `igris arise` applies the signal.
- Since 1.2, `igris done`, `skip` and `reset` refuse with `this is a side session; it doesn't finish, skip or reset tasks` and exit 1 when `IGRIS_SIDE` is set, as it is in every side session (§6.10). It stops a mistake, not a session set on getting around it.
- They do **not** edit the plan themselves; only the running igris consumes signals. This keeps a single writer.
- A signal for a task that isn't the current one is kept and reported in the TUI; it is never applied silently.
- Signal files are read only if they are regular files of at most 64 KiB (sessions can write into `signals/`; a FIFO or a huge file must not stall igris); anything else is reported as unreadable. The note is cleaned of control characters before it is shown, logged or used as a commit body.
- A signal counts for the current task only if it was written at or after the task started. An older file (e.g. `igris done` run before igris reached the task) is kept and reported, never applied; running the command again replaces it.
- **Skip needs the owner.** A `skip` signal for an **agent** task is treated as a request: igris marks the task **Needs you**, notifies, and applies the skip only after the owner confirms in the TUI (or via `--no-tui` stdin). Pressing `s` in the TUI applies directly. For **user** tasks a skip signal applies directly, since only the owner acts on them.
- **Reset.** `igris reset ID [--force]` (§14) puts a task back to `ready`/`blocked`. While a live igris on this host holds the lock, it writes the task's own reset slot `.igris/signals/<ID>.reset.json` (action `reset`, `"force": true|false`), replacing an earlier reset for that task only: a done or skip signal never replaces or removes a pending reset, nor the other way round. Only a `reset` is read from a reset slot, and a `reset` in a `<ID>.json` slot is unreadable (reported, never applied). Sessions can write `signals/` too, so for the running igris a reset signal is a **request the owner confirms**, like a session's skip: at its next poll igris checks the status (a `done`/`skipped` task without `force`, a `ready`/`blocked` one or an unknown ID is reported and dropped) and asks **Reset ID?** in the TUI (or `--no-tui` stdin, `reset yes|no [ID]`), with **Keep it as it is** the default, and sends `needs_input`. It can be asked while another question waits. Declined, the request is dropped; withdrawn (its file deleted or replaced) before an answer, it is dropped too. Confirmed, it is applied at the next poll, never in the middle of a verify or a commit: igris checks the status again; if the task is the current one, igris closes its session without the idle wait (§6.6), clears it from `state.json`, rewrites its Status by readiness, syncs readiness and turns pause-after-task on, so nothing is selected until the owner continues. Any other task gets the Status write and the sync. Before the current task is accepted (after its verify, after the commit question, before an owner's done or skip is applied) a reset request for it is answered first, so a reset written during a verify or while the commit question waits is never lost behind the task's done; confirmed, the task is reset instead of committed and marked. A `reset` signal still pending when the next `arise` starts is asked about the same way, and answered, before anything is selected; declining the interrupted task's reset picks the task up again as usual (a skip-permissions one then needs this run's confirmation, §7.3). The time rule above doesn't apply to resets.
- `igris init` adds the allow rule `Bash(igris done:*)` to `.claude/settings.local.json` (merging, never overwriting) so the done command doesn't trigger a permission prompt. `igris skip` is deliberately **not** allow-listed: a session that wants to skip has to go through a permission prompt and the confirmation above.

### 6.3 Watching a session
Igris polls the backend every 2 s (configurable) for the pane's agent state and checks for signals.

**Hook state (V03-P1).** Before every session (fresh, continue, adapt) igris writes `.igris/hooks/<ID>.settings.json` (0600), a Claude Code settings file that contains only `hooks`, and passes it with `--settings`. Each hook runs `'<absolute path of igris>' hook --root '<project root>' <Event>` (single-quoted for the shell Claude Code runs hooks with; timeout 5 s); the hidden `igris hook` reads the event JSON from stdin and writes the agent state to `.igris/agent-state/<session uuid>.json` (§13). Events map to states:

| Claude Code event | State |
|---|---|
| `SessionStart` (`startup`, `resume`, `clear`; `compact` is ignored) | `idle` |
| `UserPromptSubmit`, `PreToolUse`, `PostToolUse` | `working` |
| `PreToolUse` for `AskUserQuestion` or `ExitPlanMode`, `PermissionRequest`, `Notification` other than `idle_prompt` | `blocked` |
| `Stop` with work pending: a `background_tasks` entry that hasn't finished (a background shell, monitor, subagent, …) or any `session_crons` entry (a scheduled wakeup or cron) | `waiting` |
| `Notification` `idle_prompt`, `Stop` | `idle` |
| `SessionEnd` | `exited` |

`waiting` (WAIT-P1) means the turn ended but Claude Code resumes the session by itself when that work is done: a finished background task re-invokes it through `UserPromptSubmit`, and the last `Stop` then has none pending (recorded with Claude Code 2.1.299). Claude Code sends `idle_prompt` about a minute after a `Stop` even while a background task runs, so `idle_prompt` never replaces a recorded `waiting`; only the next `Stop`, prompt, tool use, permission prompt or `SessionEnd` does. Only the statuses and the count of the pending work are read; the commands and descriptions are never stored. The tmux backend takes the agent state from these files; the herdr backend uses them when herdr reports `unknown` (its Claude integration is not installed), and when herdr reports `idle` or `done` while the hooks recorded `waiting` (herdr sees a finished turn only). `igris hook` reads at most 64 KiB, needs a UUID-shaped `session_id`, prints nothing, does no network I/O, gives up after 2 s and always exits 0 (a failing hook must never block Claude Code). The files are untrusted (a session can write them): read only as regular files of at most 4 KiB, shape-checked, keyed by the UUID igris generated. A forged state can at worst raise or clear **Needs you**.

**Transcript and status line (V11-P2).** Every hook payload carries `transcript_path`; `igris hook` adds it to the session's record as `transcript` when it is absolute, clean and named `<that session's uuid>.jsonl` (§6.9). With `[usage] statusline = true` (§12) the settings file also sets `"statusLine": {"type": "command", "command": "'<igris>' hook --root '<root>' statusline '<ID>'"}`. It replaces the owner's own status line in task panes, which is why it is off by default. `hook statusline` reads at most 64 KiB within the 2 s deadline, prints one line, `<ID> · <model id> · ctx 5% · 5h 4% · 7d 52%` (parts Claude Code didn't send are left out), and saves the payload's `rate_limits` to `.igris/quota.json` when they changed (§13). Nothing else from the status line payload is kept.

**Plan approval (V11-P1).** For a session in a plan mode (`plan`, `plan:<mode>`, §7.1) the settings file also has a `PermissionRequest` entry with `"matcher": "ExitPlanMode"` that runs `'<igris>' hook --root '<root>' plan-approve --mode '<mode>' --wait '<approve_timeout>'` with a timeout of `approve_timeout` + 30 s (`[plan_mode]`, §12; Claude Code honors timeouts above 600 s, verified with 2.1.304 in V11-01). `--mode` is the Claude Code permission mode an approval switches to (`default`, `acceptEdits`, `auto`, or `bypassPermissions` for `plan:yolo` only, §7.1). Both travel as arguments in the settings file, which Claude Code reads once at start, so nothing a session writes later can change the mode an approval sets. It is the one hook that doesn't give up after 2 s: it reads at most 1 MiB, needs a UUID-shaped `session_id`, `hook_event_name` `PermissionRequest` and `tool_name` `ExitPlanMode`, writes `.igris/plan-approval/<session uuid>.json` (`{"n","at","plan","pid"}`, the plan from `tool_input.plan` only, never `planFilePath`, and the hook's own process ID) and polls every 200 ms for igris's `<session uuid>.decision.json` (`{"n","behavior","message"}`, `behavior` `allow`, `deny` or `pane`; a decision with another `n` is ignored). It answers Claude Code itself: allow echoes the `tool_input` it received as `updatedInput` (Claude Code ignores an allow for `ExitPlanMode` without it) and adds `updatedPermissions` `[{"type":"setMode","mode":"<mode>","destination":"session"}]`; deny passes the owner's feedback as `message`, and Claude revises the plan and presents it again. It prints nothing, so the pane decides, on `pane`, after `approve_timeout`, when `igris.lock` is gone or its PID isn't alive (checked before the request is written and at every poll), on SIGTERM or SIGINT (Claude Code cancelling it), or on any error. It removes both files when it ends, unless a newer request replaced them. It does no network I/O and always exits 0. Claude Code shows its own plan dialog in the pane meanwhile; the first answer wins (an answer in the pane cancels the hook; Claude Code kills it at its timeout). igris reads the request at every poll of a plan session (bounded, shape-checked, ignored while its `pid` is not alive, so a hook killed without cleaning up answers nothing). On a request igris saves the plan (§13), logs `plan_presented`, marks **Needs you** with reason `plan_approval` (`plan awaiting approval`; the session's blocked episode raises none meanwhile), and the TUI shows it (§15.5) with **Approve** (naming the mode it switches to), **Reject with feedback** (required; cleaned of escape sequences, trimmed, at most 4 KiB) and **Answer in the pane** (opens the session; Needs you stays, as for any blocked session). Approve logs `plan_approved` and records the plan for the commit (§6.5, `approved_plan` in `state.json`); reject logs `plan_rejected`. When the request goes away unanswered by igris (answered in the pane, the hook gave up), the view closes and the wait ends. The entry is not written, and requests are not read, for `--no-tui`, for `adapt`, for a session in any other mode, or with `approve_timeout = "0"`. A resumed `arise` that reattaches a plan session shows its pending request. The exchange files are untrusted like the state files; a session that forges a decision approves its own plan, an accepted risk (approval from igris is a convenience, not a guard; docs/threat-model.md).

| Observation | Action |
|---|---|
| Signal present | Proceed to verification. |
| Agent `working` | Clear any "needs you" flag. |
| Agent `idle` or `done` without a signal for ≥ `needs_input_after` (default 30 s), an agent task, not yet reminded in this attempt, `nudge` on (the default) | **Remind it once** (WAIT-P1): igris sends the session `igris: you ended your turn without running igris done <ID>. If the task is finished and nothing waits on the owner, run igris done <ID> --note "<one-line summary>" now as your last action. If you are waiting for the owner or for something else, say in one line what it is.` and shows it in the feed (not in the run log). The `needs_input_after` count starts again. At most once per attempt, so an agent that answers and stops again needs the owner. A reminder that can't be sent is reported as a warning, and Needs you follows at once. |
| Agent `blocked`, `idle` or `done` without a signal for ≥ `needs_input_after` (default 30 s), after that reminder or without one (blocked, user task, `nudge = false`, already reminded) | Mark the task **Needs you** in the TUI; send a `needs_input` notification once per idle episode. Possible causes: a question, a permission prompt, a plan awaiting approval, a usage limit, or a stall — igris doesn't try to tell them apart, except a plan it was asked to approve (reason `plan awaiting approval`, below). |
| Agent `waiting` | Not idle: the TUI says `waiting on background work (4m)`, and any Needs you of an idle or blocked episode is cleared. After `waiting_limit` (default 30m) without the agent working again: **Needs you** with reason `waiting`, once per stretch. A task's `Timeout` applies as usual. |
| Task running longer than its `Timeout` (§3.2) | Once per attempt: mark it **overdue** in the TUI (until the attempt ends) and **Needs you**, log `task_overdue` and send a `task_overdue` notification. The session is never closed or stopped for it. |
| Agent `unknown` | Nothing; it says nothing about the agent (e.g. no hook has fired yet and herdr's integration is missing). |
| Pane gone or Claude Code exited without a signal | Mark **Session lost**, notify, and offer: continue the conversation (`claude --resume <uuid>`, with a short fixed prompt telling the session to pick the task up again), retry fresh (`Resumed=true`), mark done, skip, or stop. A done signal written before the session went away still counts. |

**Unsubmitted prompt (v0.5.1).** Rarely, Claude Code shows the first prompt pasted in its input box but never submits it (seen once in 14 herdr sessions). A submitted prompt always fires `UserPromptSubmit`, so igris checks the hook record. Suppose the session is idle (not `blocked`) at least 15 s after its first prompt was delivered: the task prompt of a fresh session, the continue prompt of `retry continue`, or a held prompt once it goes out. Suppose too that its hook record still shows only `SessionStart` or the `idle_prompt` `Notification`, and igris has not seen the agent working since (the poll that delivers a held prompt reports `working` for the delivery and doesn't count). Then igris presses Enter once in the pane (`backend.Submitter`: tmux `send-keys -t %N Enter`, herdr `pane send-keys <pane_id> enter`) and reports the warning `the prompt didn't seem submitted; sent Enter once`, shown in the feed (not recorded in the run log). It does this once per prompt, never re-sends the prompt text, and does nothing when the session has no hook record at all (no evidence), after a reattach, or for `adapt`. If the prompt still isn't submitted, **Needs you** follows as in the table above. With `needs_input_after` under 15 s, **Needs you** can be raised before the Enter goes out; the check still runs.

**Timeout.** The clock starts when the session's first prompt is delivered: the task prompt of a fresh session, the continue prompt of `retry continue`, or, on resume, when igris reattaches to a live session (elapsed time is not saved). A prompt held back at a startup prompt (§13, `pending_prompt`) counts from when it goes out. Each session igris opens or reattaches for the task is one attempt, so a retry starts a new clock. It is checked while igris watches the session. User tasks ignore Timeout (§3.2).

Igris never advances on agent state alone — only on a signal or an explicit owner action. A signal for another task, or one igris can't read, is reported once and kept. A skip signal from an agent session is asked about once; declining it deletes the signal and the session carries on.

### 6.4 Verification
- **Profiles.** `[verify]` in config maps profile names to commands (`fast = "go test ./internal/plan/..."`, `full = "make fmt lint test"`). `run.verify` is the profile `default`. Plans name profiles, never commands. For each task the command is chosen in this order: the task's Verify cell, then `[phases.<id>] verify` for its phase, then `default`. `none` at either of the first two levels means no verification. With none of them set, there is no verification.
- The chosen command is run with `sh -c` in the project root, with a timeout (`verify_timeout`, default 15 min, the same for every profile).
- **Environment** (V06-P3). Verify gets igris's environment without the variables the notification secrets refer to through `env:` (§12; e.g. `token = "env:NTFY_TOKEN"` keeps `NTFY_TOKEN` out): a session writes the code verify runs, and verify's output goes back into the session. With `[run] env` set, it gets only the variables listed there (a name, or a prefix ending in `*`, such as `LC_*`), plus `PATH` and `HOME`, which are always passed. Task hooks get the same (§6.7); git for commits gets igris's environment without the secrets' variables, never limited by `run.env` (§6.5).
- The verify command (like every other config value) comes from the config snapshot taken at `arise` start (§13), never from a re-read of `igris.toml` mid-run.
- **Pass** → continue. **Fail** → delete the signal, wait until the agent is idle (it ran `igris done` as its last action, so it may still be finishing its turn; up to 30 s, then send anyway), and send the last 60 lines of output (at most 16 KiB, cut from the front: herdr gets the prompt as one argument) into the same session: "igris verification `<profile>` (`<cmd>`) failed: … Fix the problem, then run `igris done <ID>` again." The task stays in progress.
- Output is stdout and stderr interleaved as written, with escape sequences and other control characters removed before it is sent (the text is pasted into the session's terminal and must stay text); only the last 8 MiB are kept. A timeout counts as a failure. A verify command that can't be started at all stops the run with an error. The run log records each result and the profile, never the output; `--dry-run` shows each task's profile.
- After `verify_max_attempts` (default 3) consecutive failures, igris stops sending failures back, marks **Needs you**, and notifies (`verify_failed_limit`). A later `igris done` is verified again (failures still not sent back); retrying the session starts a new count.
- No verify command for the task (none set, or `none`) → the signal is accepted as is.
- When the **owner** marks an agent task done (TUI `d`, `--no-tui` `done`, or the session-lost choice), verify is skipped: that is the owner's explicit decision. The commit policy still applies.

### 6.5 Commits
- `commit = "ask"` (default) — the TUI (or `--no-tui` stdin) asks y/n after each verified task.
- `commit = "never"` — igris never touches git. Uncommitted changes then carry over into the next task's session; the task prompt says so, and `Resumed` sessions can't tell whose changes they're looking at. Use `never` only if you commit by hand between tasks.
- `commit = "auto"` — after verification passes: `git add -A && git commit -m "<template>"`. Default message template: `{{.ID}}: {{.Title}}` plus the done note as body, and, when igris approved a plan for the task (§6.3), the last plan approved, as a paragraph after the note headed `Approved plan:`, capped at 8 KiB. If there's nothing to commit, continue silently. A commit failure stops the run and notifies.
- Git runs with igris's environment without the variables the notification secrets refer to (§6.4); a commit runs the project's git hooks. `run.env` doesn't limit it: hooks and commit signing need variables (`SSH_AUTH_SOCK`, `GPG_TTY`, …) no list would think of.
- Igris checks `git status --porcelain` first: with nothing to commit, `ask` doesn't ask. The commit happens before the task is marked `done` (§6 steps 8–9). Git runs as argv, never through a shell, with a timeout.
- `commit_message` is a Go `text/template` with `ID`, `Title`, `Phase`, `Rank`, `Model`, `Note` and `Plan` (the approved plan, empty without one; since 1.1); a template that doesn't parse stops `arise` before anything starts. User tasks are never committed.

### 6.6 Closing
After a task is accepted, igris waits up to 30 s for the agent to become idle (so it can finish its final message), then closes the pane.

### 6.7 Task hooks
`[hooks]` in config (§12) names two optional commands, as argv lists: `before_task` and `after_task`. They are task hooks of igris, unrelated to the Claude Code hooks of §6.3.
- They come from the config snapshot (§13) and run through the command runner as argv, never through a shell, in the project root (the run's worktree in worktree mode, §6.8), with a timeout (`hooks.timeout`, default 2 min).
- Environment: igris's own, as verify gets it (without the secrets' variables, limited by `run.env` when set, §6.4), plus `IGRIS_TASK_ID`, `IGRIS_PHASE`, `IGRIS_RANK` (the alias), `IGRIS_MODEL` (the resolved `--model` value) and, for `after_task` only, `IGRIS_RESULT` (`done` or `skipped`).
- They run for agent tasks only: never for user tasks or `adapt`, and never in `--dry-run`, which says they would run.
- The feed (TUI and `--no-tui`) says when a hook starts: `M1-03 running before_task hook…`.
- `before_task` runs before every session igris opens for the task (fresh, retry fresh, retry continue), after the task is marked `in progress` and before the pane opens. It doesn't run when igris reattaches to a live session. If it exits non-zero, times out or can't be started, no session is opened and the task stays `in progress`. igris marks **Needs you**, logs an `error` with the task and a short reason (`before_task hook failed: exit status 1`), sends `run_error`, and offers the choices of a lost session with nothing to continue: retry (which runs the hook again), mark done, skip, or stop. A stop sent while it runs opens no session either: the run stops with the task `in progress` and no session, which the next `arise` treats as a lost session.
- `after_task` runs once after the task is marked `done` or `skipped` (by a signal or by the owner) and its session is closed. A failure is a warning in the feed, an `error` in the run log and a `run_error` notification; the run continues. It never runs for a task that is reset (§6.2) or left `in progress` by a stop.
- A hook passes when it exits 0. One that leaves a process running with the hook's stdout or stderr still open (`server &`, a daemonizing tool) still passes: igris waits up to 2 s after the hook exits, then closes the pipes, and drops what that process writes later. Hooks should redirect such a process's output (or not keep the streams open).
- The short reason is fixed words, never the command line (an argv from `igris.toml` may carry a token): `exit status N`, `timed out after D`, `not found`, `killed by signal N (name)` or `can't start it`.
- On a failure, the last 20 lines of the hook's output (stdout and stderr interleaved), cleaned of control characters, are shown in the feed (TUI and `--no-tui`). The run log and notifications get the short reason, never the output.

### 6.8 Worktree mode
`[run] worktree = true` (§12) or `igris arise --worktree` runs the run in a git worktree on a branch of its own, so the owner's checkout and branch stay as they are while it runs (decisions.md V06-P2). Off by default.
- **Where.** At the start of a run that has none to continue in, igris runs `git worktree add -b igris/<run-id> .igris/worktrees/<run-id> HEAD` in the project root (the run ID of §13). Without a git repository with at least one commit, or if the branch exists, the run doesn't start. `state.json` records the worktree (§13); the `run_started` log line names the branch.
- **What runs there:** the sessions (§6 step 3), verify (§6.4), task hooks (§6.7) and git for commits (§6.5). Everything else stays in the project root: `igris.toml`, `.igris/` (state, lock, signals, hook state) and **the plan**, which igris reads and writes there as in any run. The worktree's own copy of the plan is never updated; the task prompt names the project's plan by its absolute path (§6.1), and the branch's commits hold the sessions' work only. Context paths (§3.2) are checked in the project root; files the worktree needs must be committed.
- **Sessions there** find the run with `igris done` (§6.2). Their `--settings` file (§6.3) also carries the `igris done` allow rules that the project's `.claude/settings.local.json` has (that file, usually untracked, isn't in the worktree; only these rules are copied, and only when the project has them), and `Read` of the plan (each of its files, §3.7), which is outside the worktree, unless its path holds pattern characters (`*?[]{}!\`), which Claude Code would read as a pattern matching more than the plan; nothing else (§7.4). Claude Code trusts the worktree when it trusts the project, since the worktree is inside it.
- **Resume.** The next `igris arise` continues in the last run's worktree while it is there and the run asks for worktree mode, resumes (no phase named) or picks up a task in progress there, with or without `--worktree`. A task in progress whose worktree is gone stops the run with what to do (restore it with `git worktree add`, or `igris reset` the task). A new run without worktree mode leaves a remaining worktree alone and says so.
- **End.** igris never merges. When the run completes and the worktree has nothing uncommitted (`git status --porcelain`), igris runs `git worktree remove` (the branch stays) and says `the run's work is on branch igris/<run-id>; igris doesn't merge it: git merge igris/<run-id>`. A worktree with uncommitted changes is kept and named; a stopped or stuck run keeps its worktree for the next `arise`.
- **Start-up check.** Uncommitted changes in the project don't reach the worktree, which starts from `HEAD`: the git warning (§7.3) says so for a new worktree, and is left out for a run that continues in its worktree (the plan's Status edits in the project are igris's own).

### 6.9 Usage and rank audit
Igris counts the tokens each attempt of an agent task used, and checks that the session ran on the model its rank resolved to (V11-P2). `[usage] enabled = false` (§12) turns both off. Claude Code's transcript format is not a public interface, so all of this is best effort: whatever igris can't read makes the attempt's usage `unavailable`, with one warning in the feed, and never fails a task or a run.

- **Source.** Only the transcript of the session igris started, whose path the hooks report (§6.3), and its subagents' files next to it (`<transcript without .jsonl>/subagents/agent-*.jsonl`, at most 256). igris never guesses or searches for paths, never reads credentials, and never calls the API. It reads each file as untrusted input (`fsafe`: a regular file, never blocking), streaming, skipping lines over 1 MiB unparsed. From each line it decodes only `type`, `isSidechain`, `timestamp`, `requestedModel`, `message.id`, `message.model` and the four token counts in `message.usage` (`input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`). Message content is never kept, logged or shown.
- **Counting.** Only `assistant` records. A record whose model is `<synthetic>` (an API error) or that has no `message.id` is skipped. Claude Code writes one record per content block, each repeating the response's usage, so a `message.id` counts once. An attempt counts the records whose `timestamp` falls inside it, so a continued session (`--resume`) doesn't count the previous attempt again. Per attempt: `input`, `output`, `cache_read`, `cache_write` and `messages` (responses), the model ids seen, and the same for the subagents. igris reads only what is new since its last read: about every 10 s while the task runs, and once when the attempt ends. Calls Claude Code doesn't record in the transcript are not counted, so the figures are tokens recorded in the transcripts, not a bill. Never for `adapt`.
- **Rank audit.** Only the main thread is checked; subagents may run on other models, and their usage is counted, not audited. The resolved `[models]` value decides what is expected. `fable`, `opus`, `sonnet` and `haiku` match any id `claude-<alias>-…`, and a bracketed suffix such as `[1m]` is ignored. A full id (`claude-…`) matches itself, with or without a `-YYYYMMDD` suffix. igris can't check any other value (`default`, `opusplan`, a custom name), so the audit says `unchecked` and raises nothing. The first main-thread response on a model that doesn't match raises **Needs you** with reason `rank_mismatch`, a `rank_mismatch` notification (§10) and a `rank_mismatch` run-log line, once per attempt. The session is never stopped, switched or told anything; the owner decides. Each attempt's audit is `ok`, `mismatch`, `unchecked` or `unavailable` (nothing could be read).

### 6.10 Side sessions
Since 1.2 (decisions.md V12-P1). A side session is a Claude Code session the owner opens from the TUI (§15.7) for a question or an investigation while igris runs, or between runs. It is not a task: igris watches it for no signal, counts no attempt, writes no Status and raises no Needs you, notification, Timeout or reminder for it, and offers no plan approval for it.

- **Opening.** Through the backend's `OpenSession` (§11.1) with `SessionSpec.Side` set to the side ID (6 random lowercase hex characters) and no task ID, in the project root, or in its own worktree (below). The pane is labelled `side · <subject> · <rank>`, the subject being the task ID for *about this task*, `igris` for *igris problem* and nothing for *blank*; on herdr the agent is `igside-<id>`, a name no task's `igris-<id>` can equal. Claude Code starts as for a task (§6 step 4) with the chosen rank's model and mode, a fresh `--session-id`, `--append-system-prompt-file .igris/side/<id>.rules.md` and `--settings .igris/side/<id>.settings.json` (the state hooks of §6.3 and their transcript path; no `plan-approve` entry, no status line), plus `claude.extra_args`. The environment gets `IGRIS_SIDE=<id>`, so `igris done`, `skip` and `reset` refuse inside it (§6.2). The first prompt, when there is one, is typed with `Prompt` once the session is idle. At most 8 side sessions are open at a time.
- **Rules.** The rules file says it is a side session the owner opened, that it runs no plan task and has no `igris done`, and that it must not run `igris done`, `skip` or `reset` nor edit the plan or `igris.toml` (an edit holds the run). During a run in the shared tree it adds that a session is working on `<ID>` in this tree now, so it asks the owner before editing files.
- **Presets.** **Blank**: no first prompt. **About this task** (a task selected or current): the task's ID, title and phase, the plan's absolute path, the task text, whether it is running now, then `Question: `. **igris problem**: igris's version, the backend, the run state (phase, current task, agent state), the last 10 run-log events (type, task, `detail`), the doctor summary (its counts and the checks that aren't ok, by ID: a check's message may quote `igris.toml`) and the instruction to read `.igris/runs.jsonl` and `.igris/state.json` without changing the plan, then `Problem: `; never config values or secrets. The prefill is an editable first prompt, cleaned of escape sequences and control characters (§16) and at most 8 KiB.
- **Model and mode.** The rank comes from `[models]`, preselected from `[side] model`; the mode is `default`, `manual`, `accept`, `auto` or `plan` (approved in the pane), preselected from `[side] mode` (§12). `yolo` and the `plan:<mode>` values are never offered. During a run in the shared tree the mode preselects `manual` whatever `[side] mode` says, so every prompt goes to the owner whatever Claude Code's own default is.
- **Sharing the tree.** A side session works in the project root by default, the tree a run's sessions work in, and the dialog warns while a run is active: `<ID> is working in this tree; a plan edit holds the run`. During a run it also offers **Own worktree**: `git worktree add -b igris/side-<id> .igris/worktrees/side-<id> HEAD`, with `Read` of the plan in the settings file as in §6.8. Closing the session removes that worktree when `git status --porcelain` is empty (the branch stays) and otherwise keeps it and names it. In worktree mode (§6.8) the run's sessions work in the run's worktree, so a side session in the project root shares no files with them and only the plan warning is shown. Side sessions are not exempt from §5.4 or the config snapshot (§13): a plan edit from one holds the run and an `igris.toml` edit raises `config_changed`, like any other edit.
- **Record and restart.** Each open side session has `.igris/side/<id>.json` (§13). When home or the run view opens (and on home's refresh) igris reads the records and reattaches each with `AttachSide`, which binds it to its own pane like a task's (§13 Resume): on tmux the start command must be `claude` with the recorded `--session-id` and `.igris/side/<id>.rules.md`, on herdr the agent must be `igside-<id>`. A ref naming another pane counts as gone, with a warning. A gone session has its usage logged, its record removed and `side <id> ended` shown in the feed; a worktree it had is kept and named. When two igris processes share the project (a run and a read-only home), the one that claims the record with an atomic rename logs it. Quitting igris never closes a side session; closing one is an explicit action (§15.7).
- **Usage.** Read from the transcript as for a task (§6.9), logged as `side_usage` when the session is closed or found gone (§13), never counted as an attempt and never audited; `igris usage` shows side sessions in a table of their own (§14).

---

## 7. Run modes and permissions

### 7.1 Modes

| Mode | Plan `Mode` value | Claude Code flags | Notes |
|---|---|---|---|
| Default | `default` | none: the session starts in Claude Code's own default mode | Claude Code's own default: every permission prompt goes to the owner, unless the owner opted into auto as Claude Code's default (then the session runs in auto; use `manual`). |
| Accept edits | `accept` | `--permission-mode acceptEdits` | |
| Auto | `auto` | `--permission-mode auto` | Claude Code approves routine actions itself and asks for risky ones. Middle ground between `accept` and `yolo`. **The default** (`default_mode`, §12). |
| Plan | `plan`, `plan:<mode>` | `--permission-mode plan`; `plan:yolo` adds `--allow-dangerously-skip-permissions` | Session plans first; the owner approves the plan in igris (§6.3) or in Claude Code, then it implements. `igris done` only becomes possible after approval, since plan mode blocks commands. `plan:yolo` is shown with the red badge and needs per-run confirmation like `yolo` (§7.3). |
| Skip permissions | `yolo` | `--dangerously-skip-permissions` | Shown with a red badge everywhere; needs per-run confirmation (§7.3). |
| Manual | `manual` | `--permission-mode manual` | Every permission prompt goes to the owner, whatever Claude Code's own default is (since 1.2). |

Exact flag spellings are verified against the installed Claude Code (P0-02, 2.1.291; the verified versions are in §11.4) and kept in one table in code. `--permission-mode` accepts `acceptEdits`, `auto`, `bypassPermissions`, `manual`, `dontAsk`, `plan`; there is no `default` value, so Default mode passes **no** permission flag and the session starts in whatever Claude Code's own default is. With Claude Code's "auto as default" opt-in that is auto (verified with 2.1.305, V12-F); `manual` passes `--permission-mode manual` and prompts either way. There is no `plan:manual`, and `[plan_mode] then` doesn't take `manual`: the switch after approval goes through `setMode`, verified only for the destinations below.

**Mode after plan approval** (V11-P1). `plan:default`, `plan:accept`, `plan:auto` and `plan:yolo` name the mode the session switches to when igris approves its plan; plain `plan` means `plan:<then>` with `[plan_mode] then` (default `auto`, §12). The switch happens only through `setMode` in the hook's decision (`default` → `default`, `accept` → `acceptEdits`, `auto` → `auto`, `yolo` → `bypassPermissions`), never by keystrokes. An owner who answers in the pane picks Claude Code's own options instead. `plan:yolo` needs the typed confirmation (§7.3). Verified with 2.1.304 (V11-02): `setMode` `default` leaves plan mode with every prompt back; `setMode` `bypassPermissions` is **ignored** in a session started with only `--permission-mode plan` (it keeps prompting), and takes effect when the session was started with `--allow-dangerously-skip-permissions` too, so `plan:yolo`, and no other mode, gets that flag. Claude Code then warns in the pane that plan mode "won't ask before most edits or commands": a `plan:yolo` session is not held by plan mode's prompts even before approval, which is why its badge shows from launch.

Verified behavior that the design relies on:
- **`auto` does not pre-approve `igris done`.** An unfamiliar shell command still prompts (headless: denied; interactive: "This command requires approval"). `igris done` must therefore be allow-listed (below) in every mode that is not `yolo`.
- **Allow rule:** `Bash(igris done:*)` and `Bash(igris done *)` in `.claude/settings.local.json` (`{"permissions":{"allow":[...]}}`) both let `igris done` run without a prompt in `default`, `acceptEdits` and `auto`, and in `manual` (verified with 2.1.305 for V12-F1: an unlisted command still prompts there).
- **Plan mode blocks Bash** (including allow-listed `igris done`) until the owner approves the plan.
- `--dangerously-skip-permissions` runs `igris done` with no prompt.

### 7.2 Resolution order (per task)
1. Owner override set in the TUI for that specific task.
2. The task's `Mode` column, if present and not `—`.
3. The run mode chosen in the TUI for this run.
4. `default_mode` in config (`auto` unless set, §12).
5. `auto` (only reached if `default_mode` were empty, which the config defaults rule out).

Mode changes in the TUI apply to the **next** session launched; a running session keeps its mode.

### 7.3 Skip-permissions guardrails
- Choosing `yolo` or `plan:yolo` (for the run or any task) requires typing the confirmation shown in the TUI, every run. Config can't silently enable it: `default_mode = "yolo"` (or `"plan:yolo"`) still asks once at start.
- The TUI header shows a persistent red `SKIP PERMISSIONS` badge while any session runs in this mode, `plan:yolo` included (from launch, §7.1).
- Igris warns at start if the project is not a git repository or has uncommitted changes. When it asks for the confirmation without worktree mode, `igris arise` suggests `--worktree` (§6.8), which keeps the run's changes on a branch of their own.

### 7.4 Model and mode enforcement
- `claude.extra_args` must not contain `--model`, `--fallback-model`, `--permission-mode`, `--dangerously-skip-permissions`, `--allow-dangerously-skip-permissions`, `--session-id`, `--resume`, `--continue`, `--fork-session`, `--no-session-persistence`, `--append-system-prompt`, `--append-system-prompt-file`, `--system-prompt`, `--system-prompt-snapshot`, `--settings`, `--setting-sources`, `--allowedTools`/`--allowed-tools`, `--disallowedTools`/`--disallowed-tools`, `--tools`, `--permission-prompt-tool`, `--add-dir`, `--worktree`, `--bare`, `--agent`, `--agents`, `--plugin-dir`, `--plugin-url`, `--mcp-config`, `--strict-mcp-config` or `--ide`, in their `--flag=value` spellings either, nor the short forms `-c`/`-r`/`-w` (alone or in a cluster such as `-rd`): each would change the session's model, mode, permissions, tools, directory or system prompt (V10-05 widened the list from the model, mode, settings, session and resume flags). Igris sets the first ones itself (and passes its own `--settings` file, §6.3, which holds hooks only; in worktree mode also the allow rules for `igris done` and reading the plan, §6.8); a `--settings` of the owner's could set `permissions.defaultMode` and so change the mode of a default-mode task; `check` and `arise` reject a config that contains any of them. `--fallback-model` in particular would let a task silently run on a different model. Arguments may not contain control characters (a config error, like a control character in a `[models]` rank or value).
- The Claude session UUID that `--resume` gets on "continue" comes from `state.json`; it is only used if it has the form of a UUID (a session could rewrite the file so the value reads as another flag), otherwise only a fresh session is offered.
- `--model` on the command line takes precedence over a `model` in Claude Code's settings (verified: settings `haiku` + `--model sonnet` ran `claude-sonnet-5-5`), so the plan's rank always wins. This also holds on `--resume`: a resumed session's new turns run on the `--model` given at resume.
- Igris also checks what actually ran: the model ids recorded in the session's transcript are compared with the resolved rank while the task runs, and a mismatch (an owner's `/model`, a setting that slipped through) is `rank_mismatch` (§6.9).
- Model aliases `fable`, `opus`, `sonnet`, `haiku` all resolve (2.1.291: `claude-fable-5-1`, `claude-opus-5-5`, `claude-sonnet-5-5`, `claude-haiku-4-5-20251001`). Igris passes the alias and never pins a full model name.
- If `ANTHROPIC_API_KEY` is set in the environment, Claude Code uses it instead of the subscription login (it warns that it "takes precedence over your claude.ai login"). Igris never sets it; `check`, `arise` and `adapt` warn when it is present in igris's environment (never showing the value), because tasks would then bill the API; `arise` also asks for confirmation.

---

## 8. User tasks

For a task with Owner `user`:
1. Igris shows the full task text in the TUI, marks it **Your turn** and says what that means — `Yours to do outside igris (no session). Then: d done · s skip` on the card, the same in the log and in `--no-tui` — and sends a `needs_input` notification (`your turn: do it, then mark it done or skipped in igris`).
2. No pane, no Claude session.
3. The owner completes it outside igris, then presses `d` (done; asks **Mark ID done?** with an optional note, recorded like `igris done --note`; `enter` confirms) or `s` (skip, with a reason) in the TUI — or the card's `[d] Done…` / `[s] Skip…` — or runs `igris done <ID> [--note …]` / `igris skip <ID> --reason …` from any terminal.
4. Igris marks the status and continues. User tasks are never verified or committed; `state.json` records the task (without a session) so a restarted `arise` comes back to it.

---

## 9. `igris adapt` — AI-assisted plan conversion

For plans that fail `igris check` or use a different format.

1. `igris adapt [--model sonnet|opus] [--plan PATH]` (default model from `adapt.model`, default `sonnet`). Only `sonnet` and `opus` are offered.
2. Igris runs `igris check` and captures every validation error; a plan that passes has nothing to adapt (exit 0). A plan with an include line (§3.7) is refused before any session opens (`igris adapt converts one file; tasks.md includes phases/v2.md (line 12): inline the included files, or adapt one with --plan`, exit 1), and a proposal with an include line fails its review's validation, since accepting it would bring in files the owner never reviewed. `adapt` takes the run lock (§13), so it never runs alongside `arise`.
3. It opens a session (same backend, mode `default`) with the adapt prompt: the canonical format (§3, embedded), the validation errors, the config's `[models]` aliases and verify profile names (never their commands), and the job — **write a converted copy to `.igris/adapt/<plan-name>.proposed.md`**, preserving every task, ID, description, dependency, status and model. Allowed: restructure headings and tables, rename columns, normalize statuses, convert prose dependencies into IDs; turn prose that waits on something outside the plan ("waits on", "after the client…", "blocked by deploy") into a `user` task naming the blocker plus a dependency on it (§3.4), mentioned in `## Adapt notes`. Verify, Timeout and Context cells are kept as written; a column of that name holding something igris would misread (a command under Verify, say) is renamed so it stays an extra column, and noted. Not allowed: inventing or changing models, dropping tasks, rewriting descriptions, inventing verify profiles, timeouts or context paths, putting a command in a Verify cell. Tasks with missing or unknown models are listed in a `## Adapt notes` section at the end of the proposal and given Model `?` — which `check` rejects, so the owner must fill them in. The adapt session gets its own rules instead of the task rules (§6.1): write only the proposal file, never edit the original, never guess a model, ask when the original is ambiguous, finish with `igris done ADAPT`.
4. The session can ask the owner questions like any other session (`needs_input` and `session_lost` are notified as in §10). It finishes with `igris done ADAPT`, which `igris done` accepts without reading the plan (unless the plan has a task `ADAPT`). Interrupting `adapt` leaves the session open; a session that ends without `done` is an error and nothing changes.
5. Igris validates the proposal with the normal parser, then shows a **review** (original vs proposal) in the TUI with the validation result. When both files parse into task tables (with the config's `[columns]` aliases) and every task has a unique ID, they are compared table by table: phases matched by ID (or, for a rewritten heading, by where their tasks went), tasks by ID, cells by column name. The review lists, per phase, added (`+`) and removed (`-`) phases, tasks and columns, and one `~` line per changed value — a cell (`M2-04 Deps: "M2-01" → "M2-01, M2-03"`), a heading, a renamed column, the column order, the task order, a task moved from another phase — so reordering or renaming columns doesn't mark every row changed. Wide (≥ 100 columns) the values line up after the widest name; a value that doesn't fit gets an old (`-`) and a new (`+`) row. The text outside the task tables follows as a line diff. Otherwise (e.g. the original has no task table igris recognizes) the review is a line diff of the whole files. The line diff (hand-rolled LCS, P0-01) has `-`/`+`/space markers on every row and folds unchanged stretches to 3 lines of context. The proposal's problems are shown above the changes, with **Reject** / **Accept** buttons (keys `r`/`esc`/`q` and `a`; Reject has the focus first). Text from the plan or proposal is drawn with control characters replaced, so a proposal can't send escape sequences to the terminal.
6. Owner accepts → the original is backed up to `.igris/adapt/<plan-name>.<timestamp>.bak.md` (UTC, `20060102-150405`) and replaced atomically, keeping its file mode; if the plan changed on disk since it was read, nothing is replaced. Accepting a proposal that doesn't pass `check` (e.g. models left at `?`) needs a second confirmation, which starts on "Keep reviewing", and igris prints what is left to fix. Owner rejects (or quits the review) → nothing changes; the proposal stays in `.igris/adapt/`.
7. `igris check` remains the gate: `igris arise` refuses to run on a plan that doesn't validate.

### 9.1 `igris draft` — plans from a goal, a spec or an export (since 1.3)

`draft` writes plans with a session and the adapt review (decisions.md V13-P1). It never writes the plan directly: a session writes a proposal under `.igris/draft/`, igris validates it with the normal parser and shows the review of §9 step 5, and only the owner's **Accept** writes. Four sources:

- `igris draft goal [TEXT]`: the session interviews the owner in the pane (what, who for, constraints, what done means; without TEXT it asks for the goal first), then writes a plan.
- `igris draft spec FILE`: a plan from a spec or PRD. FILE must be inside the project (symlinks resolved), a regular file of at most 1 MiB and valid UTF-8; the session reads it itself (igris never pastes it into the pane). Requirements it leaves out are listed in `## Draft notes`, with why.
- `igris draft import FILE [--format github|linear|jira]`: a plan from an issue export: the JSON array `gh issue list --json number,title,body,state,labels` prints (`github`), or Linear's or Jira's CSV export. The format is detected unless `--format` names it. igris parses FILE itself (a regular file of at most 16 MiB) into at most 1000 items, cleaned of escape sequences and control characters, each body cut at 16 KiB, and writes them to `.igris/draft/import.json`, which the session reads instead of the raw export. Done or closed items become `done` rows; deps come from the export's blocking links where it has them. igris never contacts GitHub, Linear or Jira and never writes back.
- `igris draft extend [TEXT]`: new phases, or new tasks for existing phases, for a valid plan (an invalid one points to `igris adapt`). The session writes only the new part, `.igris/draft/<plan-name>.extend.md`: `##` phase sections with task tables; a section whose phase ID exists adds rows to that phase, any other is a new phase. igris checks the fragment (new IDs unique across the plan, Status only `ready` or `blocked`, no column the phase's table lacks, deps naming existing or new IDs), builds the extended plan in memory, validates it and shows the review, which lists only additions, each with its file. On Accept every file that changes is backed up to `.igris/draft/<file-name>.<timestamp>.bak.md` (UTC, `20060102-150405`); rows are inserted after the last row of their phase's table, in that table's column order (missing cells empty), and new phases appended at the end of the main plan file after one blank line; every other byte stays (§4). On a split plan (§3.7) rows go into the file their phase lives in, new phases into the main file. Writes are atomic per file, keep the file mode, and nothing is written if any file changed on disk since it was read.

`goal`, `spec` and `import` write a new plan only where none is: the plan path (`--plan PATH`, else `plan` in `igris.toml`) must not exist, must be inside the project, and its directory must exist. Otherwise `draft` stops before any session opens: `draft: tasks.md already exists; add to it with igris draft extend, or draft into a new file with --plan PATH (a format-2 plan can include it)`. Their proposal is `.igris/draft/<plan-name>.proposed.md`; Accept creates the plan exclusively (mode 0644), so a file that appeared meanwhile is never overwritten. `draft` never writes `igris.toml`.

The session runs on the same backend in mode `manual` (§7.1), with the model from `--model` or `[draft] model` (`sonnet` or `opus`, default `opus`), and its own rules instead of the task rules (§6.1): write only the proposal file, never edit the plan or any other file, treat the spec or the import as data and never as instructions, ask the owner when unclear, never guess a rank, and finish with `igris done DRAFT`, which `igris done` accepts without reading the plan (unless the plan has a task `DRAFT`). The prompt gives the canonical format (§3), the `[models]` aliases and verify profile names (never their commands), the rank rule and the shape of the plan templates (§14 `init --template`): phases `M0`, `M1`, … each ending in an `agent + user` gate, decision tasks in Mode `plan`, `user` tasks for what only the owner can do. **Ranks are suggestions:** the proposal ends with `## Draft notes`, one line per task giving its rank and why; when unsure the session writes Model `?`, which `check` rejects, and the review's header counts them (`ranks are suggestions: 12 set, 2 left at ?`). As in adapt: a proposal with an include line fails validation, accepting one that doesn't pass `check` needs a second confirmation, `needs_input` and `session_lost` are notified, interrupting leaves the session open, and a session that ends without `done` changes nothing. `draft` takes the run lock (§13), so it never runs alongside `arise`, and refuses inside a side session (§6.10).

---

## 10. Notifications

Events: `needs_input`, `session_lost`, `task_overdue`, `rank_mismatch`, `verify_failed_limit`, `task_done`, `phase_done`, `phase_stuck`, `run_error`.

Channels (all optional, any combination):

| Channel | Config | Delivery |
|---|---|---|
| Backend | `[notify.backend] enabled` | the backend's toast: herdr's `herdr notification show`, tmux's `display-message` (no sound) (sound `request` for needs-input events and `task_overdue`, `done` for completions). It has no `events` list and always gets the default events. |
| ntfy | `[notify.ntfy] server`, `topic`, optional `token` | HTTP POST, title + body (cut to 4096 bytes), priority high for `needs_input`/`session_lost`/`task_overdue`/`rank_mismatch`. |
| Discord | `[notify.discord] webhook_url` | Webhook POST with a short message (`content`), no embeds needed; `\`, `[` and `]` are escaped with a backslash, so a title can't make a masked link (`[text](url)`). `webhook_url` must be `https`. |
| Webhook | `[notify.webhook] url`, optional `secret` | JSON POST (below), signed when a secret is set. Since v0.5. |
| Slack | `[notify.slack] webhook_url` | Incoming-webhook POST `{"text": …}`; `&`, `<`, `>` escaped, so a title can't mention `<!channel>` or make links. `webhook_url` must be `https`. Since v0.5. |
| Gotify | `[notify.gotify] server`, `token` | POST `<server>/message` with header `X-Gotify-Key: <token>`, JSON `{"title","message","priority"}`; priority 8 for urgent events, 5 for `verify_failed_limit`, `phase_stuck`, `run_error`, `phase_done` and digests, 3 for `task_done`. Both keys or neither. Since v0.5. |

- Each channel has an `events` list; default: `needs_input`, `session_lost`, `task_overdue`, `rank_mismatch`, `phase_done`, `phase_stuck`, `run_error`, `verify_failed_limit`. `task_done` (opt-in) is sent when an `agent` or `agent + user` task is marked done. `task_overdue` is sent once per attempt when a task runs longer than its Timeout (§6.3); like `needs_input` and `session_lost` it is urgent (the toast's `request` sound, ntfy priority high). `rank_mismatch` (since 1.1) is sent once per attempt when a session runs on a model its rank doesn't resolve to (§6.9), and is urgent too (it is not added to the `break_through` default, so quiet hours hold it unless the owner lists it). `run_error` also covers a failed task hook (§6.7), which doesn't stop the run.
- Messages contain project name, phase, task ID and title, the event and the run ID — never file contents, diffs or command output.
- Delivery is best-effort with a 10 s timeout and one retry; failures are logged and shown in the TUI, never fatal. Redirects are not followed: following one would carry a token, the signature or a secret URL (as `Referer`) to whatever host it names, so a 3xx is a failure, `server answered 307 (redirect); set the final URL in igris.toml`. A failure is told in fixed words — `can't resolve host`, `connection refused`, `TLS failed`, `timed out`, `connection failed`, `server answered <code> <standard status text>`, or `<key> is not a valid URL` — never with the URL, the host, a `Location`, the server's reason phrase or its response body.
- Secrets (ntfy token, Discord and Slack webhook URLs, webhook URL and secret, Gotify token) may be given as `env:VAR_NAME` references so they stay out of the repo; igris never logs them, and they are scrubbed from every error it reports (the longest first, so a secret that contains another never shows in part). The Discord and Slack webhook URLs must be `https` with a host: a literal one is checked when the config loads, an `env:` one once it is resolved, and the error never shows the value.
- **Webhook payload.** `POST <url>`, `Content-Type: application/json`, `User-Agent: igris`, `X-Igris-Event: <event>`, and with a `secret` `X-Igris-Signature: sha256=<lowercase hex HMAC-SHA256 of the raw body with the secret>`. The body has every key, `""` when unknown: `{"v":1,"event","project","phase","task","title","what","run","at","urgent","text"}` (`at` RFC 3339 UTC, `run` the run ID §13, `urgent` true for `needs_input`/`session_lost`/`task_overdue`/`rank_mismatch`, `text` the message text). A digest has `"event":"digest"` (also in `X-Igris-Event`), empty `task`/`title`/`what`, the digest as `text`, a `messages` array with one payload object (without `text`) per held message, at most 50 (the oldest), and `truncated`, how many held messages it left out (`0` when none); other payloads have neither key. The resolved URL must be `http` or `https`. The signature proves the sender, not freshness: a valid payload can be sent again by whoever saw it, so a receiver that acts on events should treat `run` + `at` (or the whole body) as a key it has seen, and ignore an `at` older than a few minutes.
- **Templates.** ntfy, Discord, webhook, Slack and Gotify take an optional `template` (Go `text/template`, no extra functions, and no `range`, `define`, `template` or `block`: variables, `if`/`else` and `with` only, so a template can't loop) that replaces the message text: the ntfy body, Discord `content` (escaped after rendering, like the default), Slack `text` (escaped after rendering), Gotify `message`, webhook `text`; titles stay `igris · <project>`; the backend toast has none. Its variables are exactly `.Event`, `.Project`, `.Phase`, `.TaskID`, `.Title` (markdown stripped), `.What`, `.RunID` and `.At` (a local `time.Time`, e.g. `{{.At.Format "15:04"}}`) — never file contents or output. It is parsed and executed against a sample message when the config is loaded, so a bad template or an unknown variable is a config error naming the channel; at most 1000 characters. The output is cleaned of control characters (newlines kept), trimmed and cut to the channel's limit (ntfy 4096 bytes, Discord 2000 characters, Slack, Gotify and webhook 4000); an empty result falls back to the default text. Empty `template` = the default text (as before v0.5).
- **Quiet hours.** `[notify] quiet = "22:00-07:00"` (24-hour local time, wall clock, `[start, end)`, crossing midnight when start > end; empty = off). Inside the window a message to ntfy, Discord, webhook, Slack or Gotify whose event is not in `[notify] break_through` (default `needs_input`, `session_lost`, `task_overdue`) is held per channel instead of sent; the backend toast is never held. When the window ends — checked by the run loop's clock at every poll, also while igris waits on a question — each channel that holds messages gets one digest: `quiet hours 22:00–07:00: N held`, then `HH:MM <event>: <text>` per message (default text, no template; at most 20 lines, then `… and K more`). Held messages are also sent as a digest when the run stops: there each message gets one attempt, no retry, and the whole flush at most 15 s, so a dead server can't hold igris's exit. The run log records held messages and digests as `notification` events.
- **`task_done` digest.** `[notify] task_done_digest` is `0` (default: one message per task), N ≥ 2 (one `task_done` message per N tasks) or `"phase"`. Collected `task_done` messages go out as one message `N tasks done: ID, ID, …` (no task ID or title; templates apply) when N are collected (N only), when the phase ends (before `phase_done`/`phase_stuck`, or when the run moves on) and when the run stops. In quiet hours the grouped message is held like any `task_done`.
- `notify test` sends each sample at once: it bypasses quiet hours and `task_done_digest`. So does `igris adapt`, which is no run and has no poll loop to flush them.

---

## 11. Backends

### 11.1 Interface

```go
type Backend interface {
    Name() string
    Available(ctx context.Context) error                 // e.g. herdr server reachable, inside a herdr pane
    OpenSession(ctx context.Context, spec SessionSpec) (Session, error)
    Attach(ctx context.Context, ref SessionRef, taskID string) (Session, error) // reattach after igris restart; refuses a pane not started for taskID
    AttachSide(ctx context.Context, ref SessionRef, sideID string) (Session, error) // since 1.2: reattach a side session (§6.10); refuses a pane not started for sideID
    Notify(ctx context.Context, n Notification) error
}

type Session interface {
    Ref() SessionRef                                     // serializable (pane id, agent name, ...)
    Prompt(ctx context.Context, text string) error       // submit text to the agent
    State(ctx context.Context) (AgentState, error)       // working | idle | blocked | done | waiting | unknown | exited
    Focus(ctx context.Context) error                     // bring the session pane to front
    Close(ctx context.Context) error
}

// Optional (since v0.5), checked with a type assertion like PromptHolder.
type Tailer interface {
    Tail(ctx context.Context, n int) ([]string, error) // up to n last non-blank lines, oldest first
}

// Optional (since v0.5.1): press Enter once, typing nothing (§6.3).
type Submitter interface {
    Submit(ctx context.Context) error
}
```

`SessionSpec` holds: task ID (backends derive session names from it, e.g. herdr's `igris-<id>`) or, for a side session (§6.10, since 1.2), the side ID instead (herdr's `igside-<id>`), working directory, label, Claude Code argv (model, mode flags, extra args), environment additions.

`Tailer` feeds the live tail (§15.3): the last non-blank lines the session shows, raw (the TUI cleans them), without Claude Code's input box; a gone session yields a "session gone" error. Claude Code draws its prompt between two full-width rules (a line of at least 10 `─` and nothing else) with its status footer below, and leaves dozens of blank rows above it, so a backend reads the last N lines, cuts them at the second-to-last such rule when there are two or more and something non-blank comes before it (`backend.TailLines`), drops blank lines and keeps the last N. This is a heuristic: output of the session's own that ends in two rules loses what follows the first of them. herdr and tmux implement it, and so does the fake; a backend without it simply has no tail. Its output is never logged, notified or stored.

`Submitter` presses Enter once in the session's pane and types nothing; the engine uses it for a first prompt that stays unsubmitted (§6.3). A gone session yields a "session gone" error. herdr, tmux and the fake implement it; with a backend without it, igris only raises **Needs you**.

`State` reports a vanished pane or exited Claude Code as `exited` rather than an error; `Prompt`, `Focus` and `Attach` fail with a "session gone" error for it, and `Close` on a gone session is a no-op. The engine treats both as **Session lost** (§6.3).

Igris ships `herdr` (§11.2), `tmux` (§11.5, since v0.3) and `fake` (in-process, used by tests and `--dry-run`); tmux fits this interface without engine changes. `SessionSpec` and `SessionRef` also carry the Claude session UUID, so a backend can read the session's hook state (§6.3); in a ref it is shape-checked like the other IDs. A backend conformance suite (`internal/backend/conformance`) runs the same behavioral tests against every backend: open, prompt, state, focus, close, a gone session, attach after a restart, attach refusing another task's pane, a multi-line prompt with control characters, a side session (opened with `SessionSpec.Side`, reattached with `AttachSide`, which refuses a task's pane and another side session's, while `Attach` refuses a side session's), and, for a `Tailer`, `Tail` after a prompt (at most n lines, no error) and on a gone session.

### 11.2 herdr backend (v1)
Igris itself runs in a herdr pane. It uses the herdr CLI (JSON output), never the raw socket in v1:

| Step | herdr call |
|---|---|
| Availability | `herdr status server`; require `HERDR_WORKSPACE_ID` (igris must run inside a herdr pane) |
| Open pane | `herdr tab create --workspace $HERDR_WORKSPACE_ID --cwd <root> --label "<ID> · <rank>" --no-focus` → `.result.tab.tab_id`, `.result.root_pane.pane_id` |
| Start Claude | `herdr agent start <name> --kind claude --pane <pane_id> --timeout 120000 -- <claude args>`; `<name>` = `igris-<id>` lowercased, sanitized to `[a-z][a-z0-9_-]{0,31}`, or `igside-<id>` for a side session (§6.10). Timeout must be > 3000 and ≤ 300000 ms. Returns `.result.agent` (`agent_status`, `interactive_ready`, `name`, `pane_id`) and `argv` |
| Shell not ready yet | `agent_pane_busy` ("is not an available shell"): the new tab's shell hasn't reached its prompt (slow rc file, fresh herdr server) → retry `agent start` every 250 ms for up to 15 s, then fail the start (and close the tab) |
| Startup blocked (e.g. folder-trust prompt) | `agent_not_ready` (non-zero exit, message "blocked during startup"; the name stays usable) → mark **Needs you**, hold the task prompt, poll state until the agent is past the prompt, then deliver it once the agent has stayed idle for 2 s (Claude Code shows its input box a moment before it takes input; a prompt sent in that moment is lost although herdr accepts it). The held prompt is also recorded in `state.json`, so a reattach after an igris restart still delivers it (§13) |
| Wait for state | `herdr agent wait <name> [--until STATUS]… [--timeout MS]`. Without `--until` it returns at the first settled state (`idle`, `done` **or `blocked`**), so check `.result.agent.agent_status` on return; it returns at once if already settled. `timeout` error code on expiry. Replaces tight polling for "is it ready for feedback"; `Needs you` still needs `pane get` polling |
| Send follow-up / task prompt | `herdr agent prompt <name> <text> [--wait --timeout MS]`. Multi-line text works (bracketed paste). Rejected with `agent_blocked` if the agent is at an approval/question UI. Without `--wait` the returned status is the pre-turn one |
| Read state | `herdr pane get <pane_id>` → `.result.pane.agent_status` ∈ `unknown` (plain shell / unclassified), `idle`, `working`, `blocked`, `done`; `pane_not_found` → session lost. `herdr agent read <name> --source recent-unwrapped --lines N` returns plain text (empty while a blocking prompt is drawn on the alternate screen — use `--source visible`) |
| Tail | `herdr agent read <name> --source recent-unwrapped --lines N --format text`; when that is empty or blank (a dialog on the alternate screen), `--source visible --lines N --format text`; cut as §11.1 says |
| Submit (§6.3) | `herdr pane send-keys <pane_id> enter`; only the exit status counts, `pane_not_found` → session gone |
| Focus | `herdr tab focus <tab_id>` |
| Close | `herdr tab close <tab_id>` → `{"result":{"type":"ok"}}`; closing again gives `tab_not_found` |
| Notify | `herdr notification show <title> --body <body> --sound request|done|none` → `.result.shown` |

- Exact JSON shapes and flags were verified against herdr 0.9.1 (P0-03; the verified versions are in §11.4); recorded responses live in `internal/backend/herdr/testdata/` and drive the fixture tests.
- **Arguments after `--` must not contain control characters** (newline, tab, CR): herdr rejects them with `invalid_agent_argument` ("cannot be encoded safely for the target shell"). So the multi-line task prompt can **not** be a positional argument of `agent start`. Igris starts Claude with its flags only (no prompt), waits for `idle`, then submits the task prompt with `agent prompt` (§6.1).
- Error shape: JSON on stderr `{"error":{"code","message"},"id"}` and a non-zero exit (1 for errors, 2 for syntax errors; `agent_not_ready` exited 2 in P0-03 and 1 in the v0.1.2 re-verification). Igris acts on the error code, never on the exit status. Codes seen: `agent_not_ready`, `agent_pane_busy`, `agent_blocked`, `agent_not_found`, `pane_not_found`, `tab_not_found`, `timeout`, `invalid_agent_argument`, `invalid_agent_timeout`.
- `igris init` recommends `herdr integration install claude` for accurate agent state, and `igris check` warns if it's missing (where detectable).
- All herdr calls have timeouts (10 s default; agent start uses its own). Command failures surface the herdr error code in the TUI.

### 11.3 Choosing a backend
`backend` in `igris.toml` (§12) is `auto` (the default, and what `igris init` writes), `herdr` or `tmux`. `auto` picks **herdr** when igris runs inside a herdr pane (`HERDR_WORKSPACE_ID` set), else **tmux** when it runs inside tmux (`TMUX` set), else it fails with a message naming both ("run igris inside a herdr pane or a tmux session"). An explicit value is used as is; if that backend isn't available, `igris arise` says what's missing for it. A run resumed after a restart reattaches through the backend recorded in `state.json`; if that differs from the backend chosen now, `arise` says so and offers the usual session-lost choices instead of reattaching. `check`, `status`, `phases`, `history`, `report`, `done`, `skip` and `adapt --check`-style validation work without any backend.

### 11.4 Verified versions
Claude Code and herdr change often, and igris relies on their flags and output shapes. The versions igris was verified with live in one table in code (`checks.Tools`) and here:

| Tool | Oldest verified (`Min`) | Newest re-verified (`Tested`) | Verified in |
|---|---|---|---|
| Claude Code (`claude`) | 2.1.291 | 2.1.294 | P0-02; `docs/reverify.md` 2026-10-08 |
| herdr (`herdr`) | 0.9.1 | 0.9.1 | P0-03; `docs/reverify.md` 2026-10-08 |
| tmux (`tmux`) | 3.2 | 3.7 | V03-P2 (3.7c) |

- `igris check` and `igris arise` (including `--dry-run`) run `claude --version` and the chosen backend's version command (`herdr --version`, `tmux -V`) through the command runner, 5 s timeout each, and print a `warning:` when a tool is not in `PATH`, its version can't be determined (no version in the output, a non-zero exit, a timeout), it is older than `Min`, or its major version is newer than `Tested`'s. Newer minor and patch versions don't warn.
- The version is the first `N.N` or `N.N.N` in the output; anything around it is ignored.
- **igris in `PATH`** (since 1.2, V12-F). A session runs `igris done` through `PATH` (the allow rule names `igris done`, §7.1), while the hooks name the running binary (§6.3). `check`, `arise` (including `--dry-run`), `doctor` and the home screen's health run the check `igris-path` first: a `warning:` when no `igris` is in `PATH` (`igris is not in PATH; sessions can't run igris done: put this igris in PATH`) or the one in `PATH` is not the running binary (`os.SameFile`, as for the hooks: `sessions run igris done with /path/to/igris (1.1.0), not this igris (1.2.0); put this igris first in PATH`). The other binary's version comes from `<path> version` through the command runner, 5 s timeout, and each version is left out when it can't be read. Like the tool versions it is a warning only: never a failure, never a confirmation, and it never fails `check --strict`.
- These are warnings only: they never fail `check`, never ask for confirmation in `arise`, and igris never refuses to run because of a version.
- `docs/reverify.md` re-runs the P0-02/P0-03 checks. After a clean run against a newer version, `Tested` (code and this table) is raised to it; `Min` changes only when igris starts to depend on something newer.

### 11.5 tmux backend
Igris runs inside a tmux client (`TMUX` set) and drives that server with the `tmux` CLI (V03-P2, tmux 3.7c; minimum 3.2):

| Step | tmux call |
|---|---|
| Availability | `TMUX` set; `tmux display-message -p '#{version}'` succeeds |
| Open | `tmux new-window -d -P -F '#{window_id} #{pane_id}' -c <root> -n '<ID> · <rank>' [-e KEY=VALUE…] -- claude <args…>` → `@N %N`, one `-e` per environment addition (`IGRIS_SIDE` for a side session, §6.10; not a format); then `set-option -w -t @N remain-on-exit on` so an exited Claude Code stays visible |
| Startup | wait for the first hook state (`SessionStart` → `idle`); without one after 15 s (folder-trust question, slow start) hold the task prompt, report `blocked`, deliver it once the hook state is `idle` (also recorded in `state.json`, as for herdr) |
| Prompt | `tmux load-buffer -b igris-<id> -` (text on stdin) → `paste-buffer -p -d -b igris-<id> -t %N` (bracketed paste) → `send-keys -t %N Enter`. Refused while the hook state is `blocked` |
| State | `list-panes -t %N -F '#{pane_id}\t#{pane_dead}…'`: missing or dead pane → `exited`; else the hook state (§6.3); `unknown` before the first hook |
| Tail | `capture-pane -p -J -t %N -S -<N>` (the history tail plus the visible screen, wrapped lines joined); cut as §11.1 says: Claude Code's input box and blank lines dropped, the last N kept |
| Submit (§6.3) | `send-keys -t %N Enter` |
| Focus | `select-window -t @N` |
| Close | `kill-window -t @N`; `can't find window` is not an error |
| Notify | `display-message -d 5000 '<title>: <body>'` on the current client, plus the router's other channels |
| Attach | rebuild from `@N`/`%N` in the stored ref (shape `^@[0-9]+$`, `^%[0-9]+$`), check the pane with `list-panes` and its start command with `display-message -p -t %N '#{pane_start_command}'` (§13); `AttachSide` the same with the side session's rules file |

- The window command is argv: tmux execs a multi-argument command without a shell (verified). No prompt text is in it.
- `-n` names and `display-message` texts are tmux formats (`#(…)` would run a command), so igris escapes every `#` as `##` and cleans control characters. Prompts never pass through a format: buffers hold raw data.
- Errors come as text on stderr with exit 1 (`can't find pane: %N`, `can't find window: @N`, `error connecting to …`); igris classifies the missing-pane and missing-window messages as **session gone**. All calls have a 10 s timeout.

---

## 12. Configuration

`igris.toml` in the project root (created by `igris init`). All keys optional.

```toml
plan = "tasks.md"                 # path to the plan, relative to the project root
backend = "auto"                  # auto | herdr | tmux (§11.3)
default_mode = "auto"             # default | accept | auto | plan | yolo | manual
needs_input_after = "30s"
waiting_limit = "30m"             # how long a session may wait on background work before it needs you (§6.3)
nudge = true                      # remind an idle agent of `igris done` once before it needs you (§6.3)
poll_interval = "2s"

[models]                          # rank alias -> claude --model value
sonnet = "sonnet"
opus = "opus"
fable = "fable"
haiku = "haiku"

[columns]                         # optional header aliases -> canonical column
# "Depends on" = "Deps"
# "Agent" = "Model"

[claude]
extra_args = []                   # appended to every session launch; model/mode/session flags are rejected (§7.4)

[run]
verify = ""                       # e.g. "make fmt lint test"; the verify profile "default" (§6.4)
verify_timeout = "15m"            # for every verify profile
verify_max_attempts = 3
commit = "ask"                    # ask | auto | never
commit_message = "{{.ID}}: {{.Title}}"
prompt_template = ""              # path to a custom task prompt template
worktree = false                  # run in a git worktree on branch igris/<run-id> (§6.8)
# env = ["GOPATH", "LC_*"]        # optional: the only variables verify and task hooks get, besides PATH and HOME (§6.4)

[verify]                          # optional verify profiles: name -> shell command (§6.4)
# fast = "go test ./internal/plan/..."
# full = "make fmt lint test"

[phases.M0]                       # optional, per phase: the verify profile for its tasks (§6.4)
# verify = "fast"                 # a profile, or "none"

[hooks]                           # optional task hooks, argv lists, no shell (§6.7)
before_task = []                  # e.g. ["./scripts/dev-db", "up"]
after_task = []                   # e.g. ["./scripts/post-status"]
timeout = "2m"

[adapt]
model = "sonnet"                  # sonnet | opus

[draft]                           # since 1.3: igris draft (§9.1)
model = "opus"                    # sonnet | opus

[plan_mode]                       # since 1.1: plan approval from igris (§6.3, §7.1)
then = "auto"                     # mode after igris approves a plain `plan` task: default | accept | auto
approve_timeout = "30m"           # how long the plan hook waits for igris (1m–24h); "0": answer in the pane only

[usage]                           # since 1.1: token usage and the rank audit (§6.9)
enabled = true                    # read the sessions' transcripts: run-card counter, run-log usage, rank audit
statusline = false                # true: igris's status line in task panes (replaces yours) to record the subscription quota (§6.3)

[side]                            # since 1.2: side sessions (§6.10)
model = "sonnet"                  # the rank the dialog preselects, a [models] key
mode = "default"                  # the mode it preselects: default | manual | accept | auto | plan (never yolo)

[tui]
tail = true                       # live tail of the current session in the task card (§15.3)
mouse = true                      # click/tap, wheel and hover in the TUI (§15.5); false keeps the terminal's own text selection
theme = "auto"                    # auto | dark | light: which colors to use (§15.4); auto goes by the terminal's background

[tui.rank_colors]                 # optional: rank -> color, "#rrggbb" or an ANSI color number "0"–"255" (§15.4)
# opus = "#B48CFF"
# fable = "220"

[notify]
quiet = ""                        # e.g. "22:00-07:00", local time, may cross midnight; held messages go out as a digest (§10)
break_through = ["needs_input", "session_lost", "task_overdue"]   # never held in quiet hours
task_done_digest = 0              # 0: one task_done message per task; N: one per N tasks; "phase": one per phase

[notify.backend]
enabled = true

[notify.ntfy]
server = "https://ntfy.sh"
topic = ""
token = ""                        # or "env:NTFY_TOKEN"
events = ["needs_input", "session_lost", "task_overdue", "rank_mismatch", "phase_done", "phase_stuck", "run_error", "verify_failed_limit"]

[notify.discord]
webhook_url = ""                  # or "env:IGRIS_DISCORD_WEBHOOK"; https only
events = ["needs_input", "session_lost", "task_overdue", "rank_mismatch", "phase_done", "phase_stuck", "run_error", "verify_failed_limit"]

[notify.webhook]                  # since v0.5; also for ntfy, discord, slack, gotify: template = "" (§10)
url = ""                          # or "env:IGRIS_WEBHOOK_URL"; http or https
secret = ""                       # or "env:IGRIS_WEBHOOK_SECRET"; signs the body (X-Igris-Signature)
# template = "{{.Event}} {{.TaskID}}: {{.What}}"

[notify.slack]
webhook_url = ""                  # or "env:IGRIS_SLACK_WEBHOOK"; https only

[notify.gotify]
server = ""                       # e.g. "https://gotify.example.org"
token = ""                        # application token, or "env:GOTIFY_TOKEN"
```

- Unknown keys are errors (catches typos). Durations use Go syntax.
- [`docs/config.md`](docs/config.md) lists every key with its type and default, generated from the config struct (`make config-ref`); a test fails while it is stale.
- Verify profile names match `[a-z0-9_-]+` and their commands must not be empty. `none` is reserved, and setting both `run.verify` and `[verify] default` is an error (they are the same profile). `verify_timeout` and `verify_max_attempts` under `[verify]` are an error too: they are `[run]` settings (`verify.verify_timeout is a [run] setting, not a verify profile; move it under [run]`). `run.verify` stays supported. An error naming an unknown profile lists only the usable ones (valid names with a command) and `none`.
- `[phases.<id>]` matches phase IDs case-insensitively; `verify` is its only key, naming a profile or `none`. An unknown profile there is an error. A `[phases.<id>]` that names no phase of the plan is a warning in `check` and `arise` (the config is validated without the plan).
- `[hooks]`: an empty or missing list means no hook. A non-empty list needs a non-empty first element, and no element may contain a control character.
- Durations must be greater than zero. `env:VAR` references are accepted for `notify.ntfy.token`, `notify.discord.webhook_url`, `notify.webhook.url`, `notify.webhook.secret`, `notify.slack.webhook_url` and `notify.gotify.token`; an unset or empty variable is an error.
- `[notify.webhook]`, `[notify.slack]` and `[notify.gotify]` take `events` like the other channels (default list when left out) and are on once set up: the webhook with a `url` (a `secret` without one is an error; the resolved URL must be `http`/`https` with a host), Slack with a `webhook_url`, Gotify with both `server` and `token` (only one of them is an error). A `template` that doesn't parse, uses an unknown variable or is longer than 1000 characters is an error naming the channel (§10).
- `[notify] quiet` is `HH:MM-HH:MM` with different start and end, or empty; `break_through` names events like `events` (`[]` holds everything) and is left out of a written file while it is the default; `task_done_digest` is an integer (`0` or `1`: off; N ≥ 2, at most 1000000) or `"phase"`, anything else is an error (reported with the file's other problems, not as a parse error). Several problems are reported together, each saying what to fix.
- `[side] model` must be a `[models]` key; `[side] mode` must be `default`, `manual`, `accept`, `auto` or `plan`, and `yolo` or a `plan:<mode>` value is an error (`side sessions never skip permissions`).
- `igris init` writes the defaults, but leaves a channel's `events` out while it is the default list (any file igris writes does), so events a later version adds to the default reach that file; a file that lists `events` keeps exactly that list.
- The config hash recorded in the run snapshot (§13) is a SHA-256 of the parsed config as written (defaults applied, `env:` references unresolved), so secret values never enter it. Keys added after v0.4 are left out of it while unset or at their default (`[tui] tail` while `true`, the `[notify]` hold settings, templates, channels not set up), so a v0.4 config keeps its hash. A channel's `events` at the default hash as 1.0's default list, so a default event added later (`rank_mismatch`, 1.1) doesn't change the hash of a config that doesn't set `events`.
- `igris.toml` holds no secrets by default and is safe to commit; `.igris/` is local state and is added to `.gitignore` by `igris init`.

---

## 13. State, resume and locking

`.igris/` in the project root:

| Path | Content |
|---|---|
| `igris.lock` | PID + host + start time, created complete (written to a temp file and hard-linked into place) so a concurrent `--force-unlock` never mistakes a fresh lock for a damaged one. A second `igris arise` refuses to start while the PID is alive; a stale lock is reported and can be cleared with `--force-unlock`. If the PID was reused by another program (after a reboot, say), the error says to delete the file. |
| `state.json` | Current run: phases, the selection (§5.5), current task ID, session ref (backend name, pane/tab IDs, agent name), Claude session UUID, mode, attempt counters, started-at, hash of the config snapshot, the task prompt while it is held at a startup prompt, since 1.1 the plan igris approved for the current task (`approved_plan`, a `.igris/plans/<ID>-a<attempt>-<n>.md` path, checked for that shape when read back; no version change), and in worktree mode the run's worktree (`"worktree": {"path": ".igris/worktrees/<run-id>", "branch": "igris/<run-id>"}`, §6.8; no version change). Written atomically on every change. Values that become command arguments (session ref IDs, the session UUID), the selection's task IDs, the current task's ID, mode and verify attempts (never negative), and the worktree (only `.igris/worktrees/<run-id>` on `igris/<run-id>`, §6.8) are checked for their expected shape when read back; a file that fails is reported as damaged. Since 1.0 it also records `config_changed` (above, Config snapshot; no version change). A worktree that is a symlink, not a directory, is not used. **Versions** (§19): igris writes `"version": 3`. It reads the versions earlier releases wrote, 1 (igris v0.2–v0.6, whole phases) and 2 (v0.4–v0.6, a run with a selection, which v0.3 refused), and migrates them on read; the next save writes 3. A higher version is refused as written by a newer igris ("upgrade igris, or delete the file to start a new run"), decided from the version alone, so a newer file's shape is never reported as damage. A file that isn't a JSON object, has no valid version, or holds a value of the wrong type or shape is reported as damaged ("delete the file to start a new run"). igris v0.6 and older refuse a version-3 file, so a run is finished or its state deleted before downgrading. The **config hash** is part of the contract: a config key added in a later release is left out of the hash while it is unset (or at its default where it says so), so an interrupted run resumes after an upgrade without the config-changed notice. |
| `prompts/` | `<ID>.rules.md`: the igris rules a session gets with `--append-system-prompt-file` (§6, §6.1), rewritten before every session. Not frozen (§19). |
| `signals/` | Pending signal files (§6.2): `<ID>.json` for `done` and `skip`, `<ID>.reset.json` for `reset`. |
| `hooks/` | `<ID>.settings.json`: the Claude Code settings file igris passes with `--settings` (§6.3): hooks only, plus two allow rules in worktree mode (§6.8). Rewritten before every session. |
| `worktrees/` | `<run-id>/`: a run's git worktree in worktree mode (§6.8), removed when the run completes clean. |
| `agent-state/` | `<session uuid>.json`: `{"state","event","at"}`, the agent state written by `igris hook` (§6.3), since 1.1 with `transcript`, the session's transcript path (§6.9). Untrusted; read bounded and shape-checked. |
| `plan-approval/` | `<session uuid>.json` (a plan waiting for approval, `{"n","at","plan","pid"}`, written by `igris hook plan-approve`) and `<session uuid>.decision.json` (igris's answer, `{"n","behavior","message"}`), §6.3. 0600, directory 0700; removed by the hook when it ends. Untrusted; read bounded and shape-checked, never through a symlink. |
| `quota.json` | Since 1.1, with `[usage] statusline = true`: the last subscription quota the status line reported, `{"at", "five_hour": {"used", "resets_at"}, "seven_day": {…}}` (§6.3), written by `igris hook statusline`. Untrusted; read bounded and shape-checked. |
| `plans/` | `<ID>-a<attempt>-<n>.md`: every plan a session presented for approval, rejected ones too (§6.3). 0600, directory 0700. |
| `side/` | Since 1.2: `<id>.json`, one open side session (§6.10), `{"v":1, "id", "at", "backend", "ref", "claude_session", "model", "rank", "mode", "preset", "task", "dir", "worktree"}` (`preset` is `blank`, `task` or `igris`; `dir` is where it works relative to the project root, `.` or its worktree's path; `worktree` is `{"path", "branch"}` as in `state.json`), written atomically; `<id>.rules.md` and `<id>.settings.json`, its rules and hooks files. 0600, directory 0700. Untrusted; read bounded and shape-checked, never through a symlink. `state.json` holds nothing about side sessions. |
| `runs.jsonl` | Append-only log: one JSON line per event (task started/done/skipped, verify result, commits, needs-you waits, notifications, errors) with timestamps, run ID, task ID, rank and model; schema v1 below. |
| `adapt/` | Adapt proposals and backups (§9). |
| `draft/` | Since 1.3: draft proposals (`<plan-name>.proposed.md`, `<plan-name>.extend.md`), `import.json` (the parsed export) and the backups `draft extend` makes (§9.1). Contents not frozen (§19). |
| `config/` | `igris.<timestamp>.bak.toml`: the `igris.toml` `igris config migrate` replaced (§14). Not frozen (§19). |

**`runs.jsonl` schema v1** (since v0.5; the full reference with an example line per type is [`docs/runlog.md`](docs/runlog.md)). One JSON object per line; every line igris writes has `"v":1`. Fields other than `v`, `at` and `type` are omitted when empty:

| Field | Content |
|---|---|
| `v` | schema version, `1` |
| `at` | RFC 3339 UTC timestamp |
| `type` | `run_started`, `run_stopped`, `task_started`, `task_resumed`, `task_retried`, `task_done`, `task_skipped`, `task_overdue`, `task_reset`, `verify_passed`, `verify_failed`, `committed`, `needs_you`, `needs_you_clear`, `notification`, `error`, and since 1.1 `usage` and `rank_mismatch` (§6.9), `plan_presented`, `plan_approved` and `plan_rejected` (§6.3), and since 1.2 `side_opened`, `side_closed` and `side_usage` (§6.10) |
| `run` | the run ID, `YYYYMMDD-HHMMSS-xxxx` (start in UTC + 4 random hex characters), on every line a run writes; absent on lines written outside a run (`igris reset` with no igris running) |
| `side` | since 1.2, on `side_opened`, `side_closed` and `side_usage`: the side ID (§6.10); these lines have no `task`, and carry `run` only when the igris writing them has a run |
| `task` | task ID; omitted for events outside a task (`run_started`, `run_stopped`, most `error`s) |
| `rank`, `model` | the task's rank and resolved model; omitted with `task` |
| `attempt` | the agent task's attempt within this run: 1 for the session of `task_started` or the one `task_resumed` reattaches, +1 per `task_retried` (§6.3: every session opened or reattached is an attempt); a task reset and started again in the same run goes on from its last number |
| `session` | the Claude session UUID, on `task_started` and `task_retried` (agent tasks) and on `task_resumed` when `state.json` has one |
| `phase`, `title`, `owner` | on `task_started` and `task_resumed`: the phase ID, the title (markdown stripped, cleaned, at most 80 characters) and the owner |
| `profile` | the verify profile, on `verify_passed` and `verify_failed` |
| `commit` | the full commit SHA, on `committed` |
| `branch` | the worktree's branch (`igris/<run-id>`), on `run_started` in worktree mode (§6.8) |
| `duration_ms` | on `task_done`/`task_skipped`: since this run's `task_started` or `task_resumed` of the task; on `verify_*`: the verify command's run time; on `run_stopped`: the run's length. Readers take a negative value or one over 30 days as unknown |
| `reason` | on `needs_you` and `needs_you_clear`: `idle`, `blocked`, `waiting`, `skip_request`, `session_lost`, `task_overdue`, `verify_limit`, `verify_not_sent`, `hook_failed`, `commit`, `reset_request`, `plan_changed`, or since 1.1 `rank_mismatch` and `plan_approval` |
| `plan_file`, `plan_sha256` | since 1.1, on `plan_presented`, `plan_approved` and `plan_rejected`: the plan's file under `.igris/plans/` (absent if it couldn't be saved) and the sha256 of its text; never the text or the feedback (§6.3) |
| `mode` | since 1.1, on `plan_approved`: the mode the session switches to (`default`, `accept` or `auto`) |
| `usage`, `sub` | since 1.1, on `usage`: the attempt's tokens, `{"input", "output", "cache_read", "cache_write", "messages"}`, for the main thread (`usage`) and the subagents (`sub`, omitted when there were none) (§6.9) |
| `models`, `sub_models` | since 1.1: on `usage`, the model ids the attempt's main thread and subagents ran on; on `rank_mismatch`, the main thread's ids so far |
| `audit` | since 1.1, on `usage`: `ok`, `mismatch`, `unchecked` or `unavailable` (§6.9) |
| `detail` | free text, never secrets: the phase scope (`phase A, B`, plus the selection, e.g. `phase M1; only M1-03, M1-05`) for `run_started`, the outcome (`completed`, `stuck`, `stopped`, `error`) for `run_stopped`, the note or reason for `task_done`/`task_skipped`, `fresh` or `continue` for `task_retried`, the profile for `verify_passed` and `verify_failed` (`profile fast: attempt N of M: <why>`), the old status for `task_reset`, the reason for a task hook's `error`, the commit subject for `committed`, a few words for `needs_you`, why usage is `unavailable` for `usage` |

`needs_you` is logged whenever igris starts waiting on the owner: it marks Needs you (§6.3, §6.4, §6.7, the commit question under `commit = "ask"`, a reset request), loses a session, or holds the run for a changed plan (§5.4, once the current task has ended); `needs_you_clear` when that wait ends while the task goes on (the agent works again, the owner answers, a retry). Needs-you time is the time igris waited on the owner: an `idle` or `blocked` wait also ends at the session's done signal (a verify failing after it starts a new one), and `task_overdue`, `rank_mismatch`, `verify_limit` and `verify_not_sent` end when the agent is seen working again. A wait already open for the task and reason is not logged again (a second skip request, the verify limit hit again, another plan edit while the run holds); a `needs_you_clear` ends every open wait of its task and reason.

`usage` is logged once per attempt of an agent task when the attempt ends (`task_done`, `task_skipped`, `task_retried`, a lost session, or `run_stopped` while the task runs), with `task`, `rank`, `model`, `attempt` and `session`, also when the usage is `unavailable` (then without `usage`). It never carries message content or the transcript path. `rank_mismatch` is logged when the audit finds one (§6.9), at most once per attempt. Neither is written with `[usage] enabled = false`.

`side_opened` and `side_closed` are logged when a side session is opened and when it is closed or found gone (§6.10). `side_usage` is logged once per side session, when it is closed or found gone, with `side`, `rank`, `model`, `usage`, `sub`, `models` and `sub_models` as on `usage`, never with `attempt` or `audit`; it is not written with `[usage] enabled = false`.

**Compatibility.** A line without `v` is v0 (igris v0.2–v0.4: `at`, `type`, `task`, `rank`, `model`, `detail` only). Readers accept v0 and v1 lines mixed in one file, ignore unknown types and fields, and read lines with a larger `v` best effort (a field whose type changed is left out, the line kept), with a note that some details may be missing. A line that can't be read (not JSON, not an event, a v0/v1 field of the wrong type, longer than 1 MiB) is skipped and the rest read, with the note `N unreadable lines in runs.jsonl skipped`; one bad line never hides the history. Readers read the log only if it is a regular file (a FIFO or device in its place is an error and never blocks), and only its last 16 MiB, from the first whole line in them, with the note `runs.jsonl is over 16 MiB; only its newest runs are read`; this is a reader limit, not part of the format. Appending to a log whose last line was cut off starts a new line first. Within v1 fields and types are only ever added, never removed, renamed or given a new meaning; anything else is a new version. `run`, `session` and IDs are shape-checked when read (a bad value counts as absent). A run is the lines with one `run` ID; for v0 lines it is the events from a `run_started` to its `run_stopped`, or to the next `run_started` when there is none (the run was interrupted). `igris history` and `igris report` are the readers of this format.

**Resume.** `igris arise` (any phase argument, or none to resume the last run) reads `state.json`:
- current task still `in progress` and its session reattachable → reattach and keep watching. The backend reattaches only to a pane it started for that task: on tmux the pane's start command (`pane_start_command`, set when the pane is created and unchangeable afterwards) must be `claude` with the recorded `--session-id` and the task's rules file; on herdr the recorded agent must be the one igris names for the task (`igris-<id>`), to which prompts go by name. A ref naming any other pane counts as a gone session, with a warning (a session could have rewritten `state.json` to point igris at another pane, §16). If the task prompt was still held at a startup prompt (`pending_prompt` in `state.json`), the prompt is built again from the plan and delivered once Claude Code is ready; the recorded text is only the marker, never typed;
- session gone → offer (TUI, or `--no-tui` stdin; default fresh): **continue** the previous conversation (`claude --resume <uuid>`, same model) or start a **fresh** session with `Resumed=true`;
- pending signal for the current task → process it first.

Details:
- With no phase argument the previous run's phase range (and `--through`, and the selection) is used again, unless selection flags set the range themselves (§5.5); with no previous run, `arise` opens the home screen's start-run wizard on a terminal (§14, §15.6) and otherwise exits 1 asking for a phase. A resumed run starts in the phase of the interrupted task.
- The interrupted task is picked up before anything else, also when a different phase is named.
- If the plan no longer says `in progress` for it (the owner settled it while igris was down), igris warns, forgets it and goes on.
- A task the plan says is `in progress` without any record in `state.json` is treated like a lost session with nothing to continue: the owner can start a fresh session (`Resumed=true`), mark it done, skip it or stop.
- The new run uses the config as it is now, after the confirmation below when the file changed during the last run; igris warns that it differs.

**Config snapshot.** `arise` loads `igris.toml` once at start and uses that snapshot for the whole run. If the file changes during a run (a session could edit it, e.g. to weaken `verify`), igris marks **Needs you**, notifies (once per change), logs `config_changed`, records it in `state.json` (`config_changed`, cleared if the file is restored) and keeps using the snapshot. The new config only takes effect in a later `arise`, which, while the last run's `config_changed` is set and the file still differs from that run's hash, refuses to start until the owner confirms (**Use the changed igris.toml?**, `[y/N]` without the TUI, a dialog in the wizard; `check` and `doctor` warn meanwhile): a session's edit would otherwise take effect silently in the next run, and `commit = "auto"` may even have committed it with the task. Confirmed, the run starts and warns that the file changed; declined, nothing starts. A file edited between runs, or restored before the run ended, needs no confirmation.

Quitting the TUI (`q`) never kills a running session; it saves state and exits. Stopping a session requires an explicit action. While igris winds down after the TUI is gone (the run stopping, its last notifications), it prints `stopping… (Ctrl-C again to quit now)`, and Ctrl-C (SIGINT or SIGTERM) exits at once. Every command that waits on something (`arise`, home, `adapt`, `notify test`) stops on the first Ctrl-C and exits on a second.

**Reading state without a run.** `doctor`, `status`, `history`, `report` and the home screen (§15.6) read `.igris/` without taking the lock and never create it: `state.PeekRun` for `state.json`, `state.PeekLog` for `runs.jsonl` (lines that can't be read are skipped and counted, above), and `state.PeekLock` for `igris.lock`. `PeekLock` classifies the lock the same way `arise` does when it takes it (one shared classification): none, held by a live igris on this host, stale (its process is gone, or the file is unreadable) or remote (another host; igris can't tell if it is alive). While a live igris on this host holds the lock, the home screen is **read-only**: it shows that run (state, current task, the last `runs.jsonl` events) and offers to open its session, but offers no Arise, Init or Adapt, and editing the plan asks first, because an edit holds that run (§5.4). A stale or remote lock is only shown; clearing it is offered inside the start-run wizard (§15.6), never on its own.

---

## 14. CLI

```
igris                                       on a terminal: the home screen (§15.6); otherwise help, exit 2
igris init [--example | --template NAME]    create igris.toml, .igris/, .gitignore entry, Claude allow rules;
                                            --example also writes the example plan when there is none,
                                            --template a starter plan (go-cli, web-app, library)
igris doctor [--json]                       read-only health check of this project and machine; exit 1 on a failure
igris check [--plan PATH] [--json]          validate the plan; exit 0 valid, 1 invalid, 2 usage error
           [--strict]                       also lint the plan; exit 1 on any plan or config warning
igris phases [--plan PATH] [--json]         list phases with task counts per status and their parallelism
igris status [PHASE] [--plan PATH] [--json] tasks with status/rank/owner, current run, unmet deps
igris history [TASK-ID] [-n N] [--json]     past runs from .igris/runs.jsonl; with a task ID, its attempts
igris report [RUN] [--json]                 one run as markdown (or JSON): RUN is 1 (the newest, default), 2, … or a run ID
igris usage [RUN | --since D] [--json]      token usage and the rank audit of one run (RUN as for report) or of the runs
                                            started since D (7d, 36h, 2026-10-01); with the last subscription quota
igris arise [PHASE] [--through PHASE]       run (or resume) with the TUI
           [--mode default|accept|auto|plan[:MODE]|yolo|manual] [--no-tui]
           [--dry-run] [--force-unlock] [--worktree]
           [--only ID[,ID...] | [--from ID] [--until ID]]
                                            run only part of the phases (§5.5)
igris done ID [--note TEXT]                 signal that a task is finished
igris skip ID --reason TEXT                 signal that a task is skipped
igris reset ID [--force]                    put a task back to ready/blocked; --force for done/skipped
igris notify test [--event NAME]            send a sample of each notification to the configured channels
igris config migrate [--yes]                remove deprecated settings from igris.toml (asks first, keeps a backup)
igris adapt [--model sonnet|opus]           AI-assisted conversion with diff review
           [--plan PATH]
igris draft goal [TEXT]                     since 1.3: draft a plan with a session and the review (§9.1):
igris draft spec FILE                       from a goal (interview), a spec file, an issue export,
igris draft import FILE [--format github|linear|jira]
igris draft extend [TEXT]                   or new phases and tasks for the current plan
           [--model sonnet|opus] [--plan PATH]
igris update [--check] [--yes]              update igris with the package manager that installed it (asks first);
                                            --check only prints the current and latest versions and the method
igris completion bash|zsh|fish              print a shell completion script
igris help [COMMAND]                        the help, or a command's (= igris COMMAND -h); also -h, --help
igris version                               print the version; also -v, --version
```

**Exit codes** (frozen for 1.x, §19):

| Code | Meaning |
|---|---|
| `0` | Success, including a run or an adapt the owner stopped. |
| `1` | Failure: the command ran and something is wrong (below). |
| `2` | Usage error: an unknown command or flag, a bad flag value, a missing, extra or malformed argument. It is found before the project is read. |
| `3`–`9` | Reserved. A 1.x release may give a case that exits `1` today its own code from this range, so scripts test for non-zero, or for `2`, never for exactly `1`. |

No other code is used. Per command:

- **`check`**: 1 for an invalid plan (a newer plan format included, §3), a plan or `igris.toml` that can't be read, and under `--strict` any plan or config warning.
- **`phases`, `status`**: 1 for a plan or `igris.toml` that can't be read, an invalid plan, and (`status`) an unknown PHASE.
- **`doctor`**: 1 when some check is `fail`; warnings exit 0.
- **`history`**: 1 when the run log can't be read; no project or no runs is 0. `-n` below 1 and a malformed TASK-ID are usage errors (2).
- **`report`**: 1 when there are no runs or no such run; a RUN that is neither a positive number nor a run ID is a usage error (2).
- **`usage`**: since 1.2 it shows side sessions (`side_usage`, §6.10) in a table of their own and as an additive `side` array in `--json`, for the runs covered (a side line with that `run`) or, with `--since`, by time. Exit 1 when the run log can't be read, there are no runs, no such run or no run since D; a malformed RUN or D, or RUN together with `--since`, is a usage error (2).
- **`arise`**: 0 when the run completes or the owner stops it (`stop`, quitting the TUI, Ctrl-C). 1 when a phase is stuck, the run ends with an error, a start-up confirmation is declined or stdin ends before it is answered, the lock is held by a live igris (or is stale without `--force-unlock`), igris is not inside herdr or tmux, there is no project, or the selection names tasks that aren't in the plan or are out of order. `--dry-run` exits 1 when it can't walk the run (an unknown phase, nothing to resume, an invalid plan, a selection that doesn't fit); a phase it shows as stuck is a result, not a failure, so that exits 0. A bad `--mode`, `--only` with `--from`/`--until`, an empty `--only` and a malformed ID are usage errors (2).
- **Bare `igris`** (home): 0; 1 when the TUI fails or a run it held ended stuck or with an error; 2 when it isn't on a terminal.
- **`done`, `skip`**: 0 when the signal is written; 1 inside a side session (`IGRIS_SIDE` set, §6.10, since 1.2), outside a project, for a task that isn't in the plan, an unreadable plan or config, or a failed write. A missing or malformed ID and an empty `--reason` are usage errors (2).
- **`reset`**: 0 also when there is nothing to reset (the task is `ready` or `blocked`); 1 inside a side session (§6.10, since 1.2), for a `done` or `skipped` task without `--force`, a task that isn't in the plan, an invalid plan, a run on another host holding the lock, or a failed write. A malformed ID is a usage error (2).
- **`notify test`**: 1 when a delivery fails or no channel is set up to send the event; an unknown `--event` is a usage error (2).
- **`config migrate`**: 0 when it migrated, there was nothing to migrate (or no `igris.toml`), or the owner declined; 1 outside a project, for an `igris.toml` that can't be read or holds a deprecated key it can't remove by itself, when stdin ends without an answer, when the file changed while it asked, or on a failed write. A missing or unknown subcommand is a usage error (2).
- **`adapt`**: 0 when the plan is already valid, the proposal is accepted or rejected, or the owner stops waiting with Ctrl-C (the session stays open); 1 when the session is lost or skipped, the backend or project can't be used, or the review fails. A `--model` other than `sonnet` or `opus` is a usage error (2).
- **`draft`** (since 1.3): 0 when the proposal is accepted or rejected, or the owner stops waiting with Ctrl-C (the session stays open); 1 when a plan file is in the way (`goal`, `spec`, `import`), the plan is invalid (`extend`), FILE can't be read or parsed, the session is lost or skipped, the backend or project can't be used, inside a side session, or the review fails. A missing or unknown subcommand, a missing FILE, an extra argument, a `--model` or `--format` value not listed, or `--format` with a subcommand other than `import` is a usage error (2).
- **`init`**: 1 when it can't write its files (an unreadable `.claude/settings.local.json`, say). `--template` with `--example`, or an unknown or empty template name, is a usage error (2).
- **`update`** (since 1.2): 0 when igris updated, is up to date, is a development build, was installed from an archive (the steps are printed), or the owner declined; 1 when GitHub can't be reached or answers anything but a release, when the install command fails, or without a terminal and without `--yes`, or when stdin ends before an answer. An argument or an unknown flag is a usage error (2).
- **`completion`**: a missing or unknown shell is a usage error (2).
- **`help`, `-h`, `--help`**: 0, printed on stdout; `igris COMMAND -h` and `igris help COMMAND` print the command's synopsis and flags. `help` with an unknown command or more than one is a usage error (2). **`version`, `-v`, `--version`**: 0; with an argument, 2.
- The hidden **`hook`** and **`__complete`** always exit 0.

- **Bare `igris`** opens the home screen (§15.6) only when both stdin and stdout are terminals (character devices, `ModeCharDevice`), stdin is not `/dev/null` (which is also a character device), and `TERM` is not `dumb`. Otherwise — piped, scripted, under cron — it prints the help to stderr and exits 2, as before. Home exits 0, or 1 if the TUI fails or a run it held ended stuck or with an error (exit codes, above). It works in the project root found as for `arise` (below); outside a project it shows the get-started steps. Every subcommand is unchanged by home.
- **`igris arise` without a phase and with nothing to resume** (no `state.json`, or one without phases) opens the app in the start-run wizard (§15.6) when it runs on a terminal (as for bare `igris`) without `--no-tui` or `--dry-run`; `--mode`, `--through` and `--force-unlock` are prefilled, and `esc` leaves the wizard for home instead of exiting. In every other case it exits 1 and asks for a phase, as before. `igris arise PHASE` goes straight to the run view, as before.
- `arise`, `adapt`, `draft extend` and `notify test` work in the project root: the nearest directory, from the working directory up, that holds `igris.toml` or `.igris/`. Without one, the working directory is the root only if it holds the plan (`adapt --plan`, else `tasks.md`); otherwise they exit 1 with a hint to run `igris init` and create nothing. `draft goal`, `spec` and `import` (since 1.3) work in the project root found the same way, or without one in the working directory, where they create `.igris/` for the proposal once the plan path passed the empty-slot check (§9.1); a relative `--plan` is taken from the working directory, as for `adapt`.
- The plan file is `--plan`, else `plan` in `igris.toml` in the working directory, else `tasks.md`. `check`, `phases` and `status` stay working-directory-based (they don't search upward like `arise` and home do). `check` and `phases` read the plan only; `status` reads the plan plus the `.igris/` run state, read-only (§13), and never creates `.igris/`; `phases` and `status` refuse an invalid plan (exit 1, listing the problems) and report to stdout, errors to stderr. `--json` prints one JSON document instead of text.
- **`config migrate`** removes deprecated settings (§12), and settings a release removed (§19), from `igris.toml` in the project root, found as for `arise`. It prints each line it removes (`igris.toml:5: - command = "claude"`) with the key and why it goes, then asks `Apply these changes? [y/N]` on stdin (`--yes` skips the question). Applying, it checks that the file is unchanged since it was read, copies it to `.igris/config/igris.<timestamp>.bak.toml`, and replaces it atomically with the same bytes minus those lines: comments, layout, line endings and the file's mode are kept. A key it can't remove by dropping one line (an inline table, a multi-line value) is left to the owner, with its line number; each removal is checked by decoding the result. `check` and `doctor` name the command next to a deprecated setting.
- **`--json`** (§19): every `--json` output is one JSON object whose first key is `"v": 1`, described by a schema in [`docs/json/`](docs/json/): `check`, `phases`, `status`, `history`, `history TASK-ID` (`history-task`), `report`, `usage` (since 1.1) and `doctor`; `phases` and `status` on an invalid plan print `{"v", "issues", "plan", "valid": false}` (`invalid-plan`) and exit 1. Since 1.2 `check`, `phases` and `status` add the plan's `files` and each phase's `file` (§3.7). Fields may be added in 1.x; readers ignore fields they don't know.
- `check` prints each validation problem as `file:line: message` and each readiness drift (§5.2) and ignored dependency-like column (§3.2) as `warning: file:line: …`, and an `igris` in `PATH` that is missing or another binary (§11.4), each Claude Code or backend (herdr, tmux) version problem (§11.4), a set `ANTHROPIC_API_KEY` (§7.4) and an `igris.toml` in a parent directory (which `check`, `phases` and `status` don't read: they use the working directory's) as `warning: …` (in `--json`, a warning without `file` and `line`), and each deprecated config setting as `warning: igris.toml: …` (§12); warnings never fail the check, except under `--strict` (below). Without any `igris.toml`, `check` prints `note: no igris.toml here; using the defaults` (text only, not a warning). `phases` and `status` print the parent-directory hint as a `note:` on stderr. `phases` also reports each phase's **parallelism** (since 1.1), read-only, over the phase's unfinished tasks (not `done` or `skipped`) and the dependencies between them (a finished dependency, or one in another phase, is met as far as this view goes): a task's *level* is one more than the deepest dependency it waits on in the phase, the **critical path** is the longest chain of such tasks (the first on ties, in file order), the **width** is the size of the widest level and the **would-run-together sets** are the levels of two or more tasks, whose tasks depend on none of each other. Igris still runs one task at a time (§1); this shows what the plan would allow. In text it follows the table as one `ID: critical path A → B (n), width w` line per phase with work left, then a `together: …` line per set; in `--json` each phase gets a `parallelism` object (`critical_path`, `width`, `together`; empty and 0 for a finished phase). `arise --dry-run` prints the same for the phases and slice it walks, ahead of the steps. `status` shows, per phase, how many tasks are finished, the §5.1 outcome (`next`, `complete`, `stuck`) and, for each task, the dependencies it waits on. A VERIFY, TIMEOUT or CONTEXT column is shown only when some task of the plan has that cell set; `--json` adds `verify`, `timeout` and `context` (an array) to each task, omitted when empty. The current run (needs `.igris/` state, §13) is added to `status` once runs exist: phase range, the slice of a sliced run (§5.5), current task, mode, since when, session reference and lock state (running here, running elsewhere, stale, none), plus pending signals. In text it is a short `Run` block above the phase table, with a `Slice` row (`only M1-03, M1-05`, `from M1-03 until M2-02`) for a sliced run; in `--json` it is a `run` object, omitted when there is none, with a `selection` object (`only`, `from`, `until`) for a sliced run, the pending signals as `{"task", "action"}` objects and `signals_unreadable`, the number of signal files that couldn't be read (omitted when 0). A task's `rank` and `mode` are omitted when its cell has none; a phase's `counts` has the keys `ready`, `blocked`, `in_progress`, `done` and `skipped`. A state file that can't be read or doesn't have the expected shape is reported as "state unreadable", never a crash.
- `--no-tui` prints plain timestamped log lines and reads owner commands from stdin, one per line, for scripting or very small terminals:
  - `y` / `n` answer the question igris asked (commit? confirm a session's skip request?); `reset yes|no [ID]` answers a reset request (§6.2), the oldest one without an ID;
  - `done [note]` marks the current task done (an agent task is not verified, §6.4), `skip <reason>` skips it;
  - `retry [continue|fresh]` replaces the current session: continue its conversation or start fresh (`Resumed=true`); bare `retry` is `fresh`. This is also the answer when a session is lost;
  - `pause` toggles pause-after-task like `p` in the TUI (§15.3), `stop` stops igris and leaves the session open;
  - `mode <m>` sets the run mode for the next sessions, `mode <task> <m>` overrides one task's mode for its next session (§7.2; a user task has no session, so igris answers "<task> is a user task; it has no session"); `yolo` and `plan:yolo` then ask to type `skip permissions` (§7.3);
  - `help` lists them. Ctrl-C (SIGINT or SIGTERM) is `stop`; a second one exits igris at once, also while the run is still sending its last notifications (§10).
- **`check --strict`** adds lint hints to `check`'s warnings and exits 1 when any plan or config warning is reported: lint hints, readiness drift, a dependency-like column, a column ignored on a user task, a `[phases.<id>]` naming no phase, a deprecated setting. Machine warnings (the igris in `PATH`, Claude Code and backend versions, `ANTHROPIC_API_KEY`, an `igris.toml` in a parent directory) are still printed but never fail it, so it works in CI without Claude Code installed. Plain `check` never shows lint hints. Lint hints skip `done` and `skipped` tasks; each is printed as `warning: file:line: <message>`:

  | Lint | Reported when | Message |
  |---|---|---|
  | `title` | the Task cell has no `**bold**` span (not reported for a table without a Task column) | `M1-03: the Task cell has no **bold** title, so igris shows its first 80 characters; start the cell with **Title**`; for a cell of 80 characters or fewer, `… so igris shows the whole cell as its title; …`; for an empty cell, `M1-03: the Task cell is empty, so the task has no title; …` |
  | `long` | the Task cell is longer than 400 characters | `M1-03: the Task cell is N characters (over 400); keep the row short and point to a spec for the details` |
  | `owner-step` | an `agent + user` task whose row contains none of `owner`, `approv`, `decid`, `decision` (case-insensitive) | `M1-03: agent + user task, but its row never says what the owner does (no "owner", "approve" or "decide"); say what needs the owner's sign-off` |
  | `gate` | a task whose ID ends in `-G` (case-insensitive) doesn't depend, directly or through other deps, on every other task of its phase; tasks that themselves depend on the gate, directly or not (a release after it), are left out of both the list and the suggested range | `M1-G: the gate does not depend on M1-04, M1-05 of phase M1; add them to Deps (e.g. M1-01…M1-05)` |
  | `yolo` | the Mode cell is `yolo` | `M1-03: Mode yolo runs this task with --dangerously-skip-permissions; prefer auto unless it must run unattended` |
  | `yolo` | the Mode cell is `plan:yolo` | `M1-03: Mode plan:yolo runs this task without permission prompts once its plan is approved; prefer plan:auto unless it must run unattended` |
  | `fable` | a task of rank `fable` in a phase where no other task has rank `opus` or `fable` (the aliases as written) | `M1-03: the only heavy-rank task of phase M1 is fable; check that this task needs fable` |

  With warnings, the summary line is `tasks.md: N warning(s) under --strict; fix them and run igris check --strict again`. In `--json` the document gains `"strict": true` and each lint warning a `"lint"` field with its name; `valid` still means the plan validates, and the exit code carries the strict result.
- **`arise --only/--from/--until`** (§5.5): `--only` takes a comma-separated list. Combining it with `--from`/`--until`, an empty list or a malformed ID is a usage error (exit 2). A named ID that is not in the plan or not in the phase range, or a `--from` after the `--until`, exits 1 before anything is written, e.g. `arise: --until M3-01 is in phase M3, outside the run's phases M1…M2; widen --through or drop it`.
- **`reset ID [--force]`** puts a task back to `ready`/`blocked` by readiness (§5.2). It finds the project root like `done`/`skip` and needs a valid plan (otherwise exit 1, listing the problems).
  - An `in progress` task is reset. A `done` or `skipped` one needs `--force` (otherwise exit 1: `M1-03 is done; pass --force to reset it`). A `ready` or `blocked` one prints `nothing to reset` and exits 0 without writing. User tasks are reset the same way.
  - With no lock, or a stale one (§13): igris writes the Status cell directly (§4) and syncs readiness. With no lock it takes the run lock for the write, so an `arise` starting at that moment can't write the plan too; if one took it first, `reset` exits 1 saying to run it again. If `state.json` names the task as the interrupted one, `reset` adds that its session may still be open and should be closed by hand.
  - With a lock held by a live igris on this host: `reset` writes a `reset` signal, a request the owner confirms in that igris before it is applied (§6.2).
  - With a remote lock: exit 1, saying to run `reset` on that host or clear the lock with `arise --force-unlock`.
  - It prints what it did (`M1-03: in progress → ready`, or `M1-03: reset requested; confirm it in the running igris (if none is running, the next `igris arise` asks)`) and lists the task's dependents that are `in progress`, which it never changes. Exit 0, 1 on failure, 2 on a usage error: a missing or malformed task ID (`igris reset "bad id"`), checked before anything is read. A well-formed ID that is not in the plan exits 1.
- `init --example` also writes the canonical example plan (the one in `examples/tasks.md`) as the configured plan path when no plan exists; it never overwrites (`kept existing tasks.md`). Without `--example`, `init` behaves as before.
- **`init --template NAME`** (since 1.2) writes a starter plan instead, under the same rule (only when no plan exists, never overwriting): `go-cli`, `web-app` or `library`, the files in `examples/templates/<name>/tasks.md` (decisions.md V12-P3). It never changes `igris.toml`. Each template passes `check --strict` as written; its phases start with a `user` task to write `SPEC.md` and end in gates, and its ranks are suggestions, which the template's first paragraph and `init`'s step line (`… its ranks are suggestions: review the Model column, then run igris check`) say, with a hint to set `[run] verify`. Names are matched exactly; `--template` with `--example`, or an unknown name (`init: unknown template "rust"; templates: go-cli, library, web-app`), is a usage error (2). `igris help init` lists the templates.
- `doctor` is **read-only, now and later**: it never writes, creates or fixes anything (no `--fix`), and for every problem it prints the exact command that would fix it. It works outside a project (it reports "no igris.toml" and checks the machine). Checks, in this order:
  1. the `igris` in `PATH` is this one (check id `igris-path`, since 1.2; §11.4);
  2. `claude` found, with its version (§11.4);
  3. `ANTHROPIC_API_KEY` set in the environment (§7.4);
  4. the backend the config chooses (§11.3): its version (`herdr --version` or `tmux -V`), whether igris runs inside it and it is reachable (check id `backend`), and for herdr whether its Claude Code integration is installed (information only: igris reads the agent state from hooks without it, §6.3); not running inside herdr or tmux is a `warn` here, not a `fail`, because `doctor` is usually run from a plain shell;
  5. the project is a git repository, and whether its tree is dirty (§7.3);
  6. `igris.toml` is valid — every problem listed, not only the first (§12);
  7. the plan is valid (every problem) and has no readiness drift (§5.2);
  8. `.claude/settings.local.json` (or `.claude/settings.json`, which Claude Code also reads) holds the allow rules for `igris done` (`Bash(igris done:*)` / `Bash(igris done *)`, as `init` writes them); `warn` if a rule in either file allow-lists `igris skip` (that bypasses the skip confirmation, §6.2) or every shell command (`Bash`, `Bash(*)`, `Bash(:*)`: skip-permissions mode without the confirmation, §7.3), or if `permissions.defaultMode` there is `bypassPermissions`, `auto` or `dontAsk`;
  9. `.igris/` has mode 0700 and its files 0600 (§13, §16);
  10. no stale or foreign lock (§13);
  11. the project is not under `/mnt/` (WSL on a Windows filesystem; points to `docs/check-wsl.md`);
  12. notification channels are configured (§10) — **never sent to**; sending is `notify test`.

  Each check prints one line: a glyph, a level and a message; a problem is followed by its next command. Levels are `ok`, `warn` and `fail`. The exit code is 0 unless some check is `fail` (then 1); `--json` prints `{"v": 1, "checks": [...]}`, each check with `id`, `level` and `message`, and where they apply `next`, `confirm` (a run starts only after the owner confirms), `file` and `line`, and for a drifted task `task`, `from` and `to`. Text from the plan, config, lock or command output is cleaned before it is printed (§16).
- `history` reads `.igris/runs.jsonl` (§13) read-only and never creates `.igris/`. It lists the last N runs (default 10, newest first), each with its phases, tasks done and skipped, per-task duration, verify attempts, commits and how it ended; a run without a stop event is shown as interrupted (or `running` while a live igris holds the lock), and lines that can't be read are skipped with a note (§13). A known duration under a second is shown as 1s, never as unknown. A task's result is `done`, `skipped`, `running` (no result yet in the live run) or `unfinished` (no result when its run ended). With a task ID it lists every attempt of that task across runs. Each run shows its run ID when the log has one (§13; `"run"` in `--json`), for `report`.
- **`report [RUN] [--json]`** summarises one run from `.igris/runs.jsonl` (§13): read-only, no lock, never creates `.igris/`; it finds the project root like `history`. `RUN` is a positive integer of any size (1 = the newest run, the default) or a run ID; anything else is a usage error (exit 2). No runs (`no runs recorded yet`), an index past the end (`only N runs recorded`) or an unknown ID (`no run <id> in .igris/runs.jsonl; igris history lists them`) exit 1. The markdown, meant to be pasted into a PR description or a journal:
  ```
  # igris report · <project>

  Run 20261008-091500-3fa2 · 2026-10-08T09:15:00Z → 2026-10-08T11:02:13Z (1h47m) · completed
  Phases M1, M2 · only M1-03, M1-05
  5 done · 1 skipped · 0 unfinished · 3 commits · needs you 12m30s

  ## Phase M1

  | Task | Result | Duration | Attempts | Verify | Commit | Needs you |
  |---|---|---|---|---|---|---|
  | M1-03 Config loader | done | 14m02s | 2 | fast ✗ ✓ | 3fa29c1 | 2m10s |

  Notes
  - M1-03 done: tests added

  Resume
  - M1-03: `claude --resume <uuid>` (run it in the project root)
  ```
  Tasks are grouped by phase (`## Tasks` when the log doesn't say, as in v0 lines); Verify lists the profile and one ✗ per failure, ✓ per pass; Commit is the short SHA of the task's last commit (`—` when that commit's SHA is unknown, even if an earlier one's is known); Notes are the done notes and skip reasons; Resume lists one `claude --resume <uuid>` per agent task whose latest session's UUID is known (never an older session's), and is left out otherwise; `Errors` bullets follow when the run had errors. Needs-you time is how long igris waited on the owner (§13): from each `needs_you` to its `needs_you_clear`, or to the task's next `task_retried`, `task_done`, `task_skipped` or `task_reset`, or the run's end, overlaps counted once; user tasks have none (their time is their duration). Anything the log doesn't record (v0 lines, an interrupted run) is shown as `—`; a known duration under a second is 1s (also `duration_s` in JSON). A run without a stop is `interrupted`, or `running` while a live igris holds the lock. Text from the log (titles, notes, IDs, phases, errors, details) is cleaned and, in the markdown, has the characters that make markup (`\` `` ` `` `*` `_` `[` `]` `(` `)` `!` `<` `>` `#` `|`) escaped with a backslash, so it can't make links, images, emphasis or HTML; `--json` keeps it as cleaned. `--json` prints one object: `run`, `started_at`, `ended_at`, `duration_s`, `end`, `phases`, `selection`, `done`, `skipped`, `unfinished`, `needs_you_s`, `commits` (`task`, `sha`, `subject`), `errors`, and `tasks`, each with `id`, `phase`, `title`, `owner`, `rank`, `model`, `result`, `duration_s`, `attempts`, `verify` (`profile`, `passed`, `duration_s`), `commit`, `note`, `sessions`, `resume`, `needs_you_s`; unknown values are omitted.
  Since 1.1 the task table has a Tokens column (`2.3M in · 41k out`, `—` when unknown) and a task whose audit found a mismatch is marked `⚠ rank` (red on a terminal, §6.9); `--json` adds the same per task (`usage`, `sub`, `models`, `audit`).
- **`usage [RUN | --since D] [--json]`** (since 1.1) reports token usage and the rank audit from `.igris/runs.jsonl`'s `usage` and `rank_mismatch` lines (§6.9, §13), and from `.igris/quota.json`; it never reads transcripts. Read-only, no lock, never creates `.igris/`; it finds the project root like `history`. `RUN` is as for `report`. `--since` takes a duration (`7d`, `36h`; `d` is 24 h) or a date (`2026-10-01`, local midnight) and covers every run that started since then. It prints the runs covered; the totals (`in` is input + cache read + cache write; all four counts are shown); one table by task (attempts, in, out, subagents, model ids, audit), one by model and one by rank; the top 5 tasks by in + out; the audit result, naming each mismatch; and the subscription quota as last recorded, with its time and reset times, or `quota unavailable` (§6.3). An attempt without usage counts as `unavailable` and is left out of the totals, with a note. The figures are tokens recorded in the session transcripts, never a cost. An attempt that ran on several models is counted under each in the by-model table (its tokens can't be split, so the rows can add up to more than the totals, which the table says). Runs with no `usage` line at all (made before 1.1, or with `[usage] enabled = false`) print `No usage was recorded: …` with the reason instead of tables of zeros, and `--json` adds a `no_usage` string in that case only (since 1.2).
- `completion` prints a hand-written script per shell (no CLI framework, P0-01). It completes subcommands, each subcommand's flags, phase and task IDs, and after `report` run IDs; for `arise --only`, which takes a comma-separated list, it completes the ID after the last comma. IDs come from a hidden `igris __complete <kind>`, which is not listed in help and prints one candidate per line, never touching the network: phase and task IDs come from the plan only (never `.igris/`), and on a missing or invalid plan it prints nothing; run IDs (`runs`) come from `.igris/runs.jsonl` only, read-only and newest first, whatever state the plan is in, and with no log it prints nothing. It always exits 0.
- **`update [--check] [--yes]`** (since 1.2, decisions.md V12-P4) is the only command that contacts GitHub, and only when the owner runs it; igris never checks on its own (at start, on a timer, in a run) and never replaces its own binary: it hands over to the package manager that installed it. It sends `GET https://api.github.com/repos/drilonrecica/igris/releases/latest` with `Accept: application/vnd.github+json` and `User-Agent: igris/<version>`, a 10 s timeout and proxies from the environment, reads at most 1 MiB of the answer and uses only `tag_name`, which must match `^v\d+\.\d+\.\d+$` (the endpoint skips pre-releases); the release link is built by igris (`https://github.com/drilonrecica/igris/releases/tag/vX.Y.Z`) and no other text of the answer is shown. It compares the latest release with the running version (the ldflags version, else the build info's module version, §18): a development build (`dev`, `(devel)`, a pseudo-version, a dirty or `git describe` build) says so and names the latest release, offering nothing; an equal or newer version is up to date. The install method comes from the running binary's path (`os.Executable`, symlinks resolved) and the build info: **Homebrew** when the path contains `/Cellar/igris/` (`brew upgrade igris`); **go install** when no ldflags version is set and the build info has a release's module version (`go install github.com/drilonrecica/igris/cmd/igris@vX.Y.Z` with `GOBIN` set to the running binary's directory, so the same file is replaced; when its output says `unknown revision`, igris adds the hint to retry with `GOPROXY=direct GONOSUMDB=github.com/drilonrecica/igris`, since the checksum database lags a new tag by about 30 minutes); otherwise an **archive** (or a method igris can't tell): no command, but the release link and the release notes' download, verify (`ssh-keygen -Y verify` against `allowed_signers`) and install steps for this platform. `--check` prints the current and latest versions, the release link and the method with its command, and runs nothing. Without it igris prints the same, then asks `Update? [y/N]` on a terminal; `--yes` skips the question, and without a terminal and without `--yes` it exits 1 saying to pass `--yes`. The command runs through the command runner as argv, never a shell, with a 10-minute timeout and igris's environment minus the variables the notification secrets come from (as git, §6.5, §16; outside a project, or with an `igris.toml` that doesn't load, nothing more is left out). A failure prints the end of its output (the last 10 lines, cleaned) and the hint. After a success igris runs `<igris in PATH> version` (5 s timeout) and prints `Updated: igris in PATH is now X.Y.Z.`, or says that the igris in PATH is missing, didn't print its version or is another version, and to fix PATH. Runs going on keep going; their sessions run the new igris for `igris done`.
- `hook <event>` is hidden too: Claude Code runs it from the hooks file igris passes to each session (§6.3). It is not meant to be run by hand.
- `notify test` sends one sample message per event to every channel set up for it (the backend's toast is included when the backend is reachable) and prints `ok` or `FAILED: <reason>` per event and channel; secrets never appear in the output. It exits 1 if a delivery failed or no channel is set up. `--event` limits it to one event; when channels are set up but none sends that event it says `no channel sends <event>; add it to a channel's events in igris.toml` (exit 1).
- `--worktree` runs this run in a git worktree on a branch of its own, as `[run] worktree = true` does (§6.8).
- `--force-unlock` clears a stale `.igris/igris.lock` (its process is gone, the file is unreadable, or it comes from another host); a lock held by a live process on this host is always refused (§13). The CLI never asks about a stale lock: `arise` exits 1 saying to rerun with `--force-unlock`. The start-run wizard's **Clear the lock and start** dialog (**Cancel** is the default; asked per launch, never remembered) is the TUI equivalent of the flag, not a new prompt.
- `--dry-run` uses the fake backend: walks the phase (or the slice, §5.5), prints which task would launch with which model, mode (a plain `plan` with the mode it switches to after approval, `plan → auto`, §7.1) and verify profile (§6.4; `none` when the task's cell or phase turns verification off, `—` when nothing applies; the column is shown when some task has a profile or `none`), that task hooks would run (§6.7), and in worktree mode which worktree the run would work in (§6.8), writes nothing. It runs the real engine on a temporary copy of the plan, with every session finishing at once, user tasks done, verify and commits off. Drift and skip-permissions tasks are shown as warnings instead of asked about; a task already in progress is shown as resumed with a fresh session. Without a phase it walks the last run's phases. It never touches `.igris/`.
- On start, `arise` warns if `ANTHROPIC_API_KEY` is set in the environment (Claude Code would bill the API instead of the subscription) and asks for confirmation. It also warns, without asking, if the project is not a git repository or has uncommitted changes (§7.3), and about the igris in `PATH` and the Claude Code and backend versions (§11.4).
- Before its first write `arise` asks to confirm readiness drift (§5.2), and asks for the typed `skip permissions` confirmation when a task would run in `yolo` or `plan:yolo` mode (§7.3). Declining any of these exits 1 with nothing started. With `--no-tui` the answers come from stdin (`y` for the questions). With the TUI they are asked the same way, as plain prompts before the TUI takes over the terminal; in the start-run wizard (§15.6) they are dialogs with the same defaults and meaning. `--dry-run` prints the warnings and never asks.

---

## 15. TUI

Built with Bubble Tea / Lip Gloss. Runs in the igris pane; the Claude sessions live in their own herdr tabs or tmux windows.

**One program, a stack of screens.** Igris's TUI is one Bubble Tea program (one alt-screen session, one background-colour detection, stable mouse and focus modes) holding a stack of screens: home at the bottom (§15.6), with pages, the start-run wizard, the run view (§15.1–§15.5) and the adapt review pushed on top. `esc` closes an open dialog first, otherwise it pops the screen; `q` pops a pushed screen and quits on home; `ctrl+c` quits the whole app from anywhere (with a run going, it stops igris and leaves the session open, exactly like quitting `arise`; while that run winds down, another Ctrl-C exits at once, §13). SIGINT and SIGTERM sent to igris end the app the same way. A resize reaches every screen on the stack. While the run view is on the stack it owns the only engine; there is no background run. `igris arise PHASE` opens the run view on its own, with no home beneath it, and behaves as before.

### 15.1 Layout (≥ 100 columns)
```
┌ igris · sinjal ─────────────── phase M0 · mode: plan · herdr ┐
│ TASKS                          │ CURRENT                      │
│ ✓ M0-01  Go module      sonnet │ M0-03 Makefile               │
│ ✓ M0-02  Entrypoint     sonnet │ rank sonnet → model sonnet   │
│ ● M0-03  Makefile       sonnet │ mode plan · 4m12s            │
│ · M0-04  Config loader  sonnet │ state: NEEDS YOU (idle 45s)  │
│ ⨯ M0-13  Static assets  sonnet │ o opens the session          │
│   waits on P0-05               │ fmt, lint, test targets      │
│                                │ [o] Open session [t] Details │
├─ LOG ──────────────────────────┴──────────────────────────────┤
│ 09:41 M0-02 done · "subcommand dispatch + tests"              │
│ 09:41 M0-03 started (sonnet, plan)                            │
├───────────────────────────────────────────────────────────────┤
│ [›Open session‹] [Mode] [Pause] [Retry] [Skip] [Stop] [?]     │
└───────────────────────────────────────────────────────────────┘
```

**Progress** (since v0.5). The header shows the current phase's progress after the phase: `phase M0 · 7/12 · 58% ██████░░░░` — n/m as text, the percentage (rounded down) and a 10-cell bar (`█` done, `░` to do), so it never relies on colour. m counts the run's tasks of the phase: all of them, or for a sliced run (§5.5) the slice's tasks in that phase, then labelled `slice 2/3 · 66%`; n counts those `done` or `skipped` in the plan as last loaded. It is hidden when m is 0. With too little width the bar is dropped first, then the percentage.

**Usage** (since 1.1, §6.9). The current task's card shows `tokens 2.3M in · 41k out · claude-sonnet-5-5` (`in` is input + cache read + cache write, subagents included), refreshed with each read; the model id is red on a rank mismatch, and the line reads `usage unavailable` when igris can't read the transcript. With `[usage] statusline = true` and a recorded quota, the header shows `5h 4% · 7d 52%` (amber from 80%, red from 95%; the age, e.g. `· 25m ago`, once it is over 10 minutes old). It is hidden without one, and dropped before the progress bar when the width runs out.

**Side sessions** (since 1.2, §6.10). While any are open the header shows `side N`, dropped before the quota when the width runs out.

### 15.2 Narrow layout (< 100 columns, e.g. Termius on a phone)
Single column: header, current task card, compact task list (ID + status glyph + rank), last 3 log lines, action bar. The status bar shows the progress as `M1 · 7/12 · 58%` with a 6-cell bar, under the same rules. The card shows usage as `2.3M/41k`. Must stay usable at 50×20. The action bar wraps to a second row or folds its less common actions into a `More…` button; an open dialog (§15.5) takes the whole screen.

### 15.3 Actions
**The current-task card** shows the task's ID and title, its rank → model and mode (or "user task"), how long it has run, an ETA, and its state; then the live tail; then the task's text (the Task cell without its bold title, from the plan as last loaded; for a user task, the text igris sent with Your turn) and the card's buttons: `[o] Open session` while a session exists, `[d] Done…` and `[s] Skip…` for a user task, `[t] Details`, and `[Answer…]` while a question is pending. While the attempt runs past the task's Timeout (§6.3) the card adds a line `OVERDUE: running longer than its Timeout 45m` until the attempt ends. A user task's card adds, under YOUR TURN, `Yours to do outside igris (no session).` and `Then: d done · s skip`. The text gets only the rows the head and the buttons leave; when it doesn't fit it ends with `… t: details`. While the agent waits on background work (§6.3) the state reads `waiting on background work (4m)`, not as an alert. While the session needs the owner the state reads `NEEDS YOU (idle …) — o opens the session` (`NEEDS YOU (waiting …)` past `waiting_limit`, timed from the start of the wait) (the hint on its own line when the card is too narrow) and Open session is drawn in bold. Clicking the card's title opens the details too.

- **ETA** (agent tasks, since v0.5): the median duration of finished tasks of the same rank in `runs.jsonl` — `task_done` of tasks started (not resumed) in their run, verify and waits included, the 20 most recent per rank, loaded once when the run starts (for a run started from home, from the snapshot home last read). Shown only with at least 3 such tasks, always approximate: `4m12s · ≈ 14m left`, and `· over ≈ 18m typical` once the task runs longer; minutes resolution. There is no phase ETA.
- **Live tail** (since v0.5, `[tui] tail = true` by default): when the session's backend can tail it (§11.1), the last lines of the session above Claude Code's input box (each refresh reads the last 60 lines) — 6 in the wide layout, 3 in the narrow one, fewer when the card has no room (the task text gives way first) — refreshed every `poll_interval`. Each line is cleaned of escape sequences and control characters (§16), blank lines are dropped, a line wider than the card loses its leading spaces (Claude Code pads right-aligned text such as `◐ medium · /effort` to the pane's width, which would otherwise show as a blank row ending in `…`) and lines are then clipped at their end to the card width by display width, cut between grapheme clusters (an emoji with a variation selector, `⚠️`, is two cells and never split); every other clipped text in the TUI is cut the same way. Any error, an empty result, a lost session or a user task hides it without a warning. The tail is display only: never logged, notified or stored. `tail = false` turns it off.

Every action is reachable three ways: **clicking** its button (or tapping it, e.g. in Termius), **moving the focus** to it and pressing `enter`, and its **shortcut key**. The action bar shows only the actions that apply right now (e.g. `Done` only while a task is running, `Open session` only while a session exists); it never shows buttons that do nothing.

| Button | Key | Action |
|---|---|---|
| Open session | `o` | Focus the current session's pane (herdr tab, tmux window) |
| Side session | `S` | Since 1.2: open the Side sessions page, or the side-session dialog when none are open (§15.7); the bar's `Side…` |
| Details | `t` | The selected task's details (full text, deps, extra columns): the current task unless another is selected. Also the card's `[t] Details` and its title |
| Mode | `m` | Change run mode for upcoming sessions (choice list; `yolo` and `plan:yolo` need the typed confirmation, §15.5) |
| Task mode | `M` | Override mode for the selected agent task (same choice list); hidden for user tasks |
| Pause / Resume | `p` | Pause after the current task (toggle): the task finishes normally, then igris launches nothing and waits — the run stays alive — until pause is toggled off. The button label shows the current state |
| Done | `d` | Mark the current task done (a user task, or an agent task as the owner's decision, §6.4). For a user task it asks first, with an optional note (§8) |
| Skip | `s` | Skip the current task: asks for a reason; for agent tasks also closes the session after confirmation |
| Retry | `r` | Retry: close the current session, then choose **Continue conversation** or **Start fresh** (`Resumed=true`) |
| Stop | `x` | Stop now: leave the session open, stop igris after confirmation |
| — | `↑/↓`, `enter`, click | Browse tasks / show task details (full text, deps, extra columns); one click on a task row opens its details |
| Quit / Home | `q` | Quit the TUI; sessions keep running, `igris arise` resumes. When the run was started from home the button reads **Home**: igris stops, the session keeps running, and home comes back with fresh data |
| — | `y` | Copy the selected item to the clipboard with OSC 52, and say "copied" in the header: `claude --resume <uuid>` of the current session (current-task card), the log line at the bottom of the view (log focused), the proposal path (adapt review). Terminals without OSC 52 ignore it, so "copied" only means the sequence was written |
| ? | `?` | Help: every action, its key, and the focus keys |

When a run ends on its own (complete, stuck, error) the run view stays open with its end banner and **Home** focused, so the reason can be read; a Stop the owner confirmed (`x`) returns to home on its own once igris has stopped.

### 15.4 Visual rules
- Status glyphs: `✓` done, `●` running, `!` needs you, `·` ready, `⨯` blocked, `–` skipped. Never color alone: every state, badge and marker is also a glyph or a word, and color and weight only add emphasis (what runs or needs the owner stands out; what is done, skipped or blocked is dimmed).
- Colors follow the brand palette (`docs/brand/`): cyan is the working accent, red marks what needs the owner and the `[SKIP PERMISSIONS]` badge. Each color has a value for dark and for light terminals; `[tui] theme` picks one, and `auto` goes by the terminal's background (dark if it can't be detected).
- Rank colors are configurable (`[tui.rank_colors]`, §12) but each rank also shows its name. The stock ranks have built-in colors; other ranks are uncolored until given one.
- Respect `NO_COLOR`: no color is drawn. Bold, faint and reverse video are not colors and stay.
- The focused element is marked without relying on color (reverse video plus `›…‹` brackets on buttons, `›` on rows), so focus is visible under `NO_COLOR`.
- Buttons have four states: normal, focused, wanting attention (`Answer…` while a question is pending) and inactive (the bar under a dialog or page, dimmed). Hover is drawn over any of them (§15.5): the element under the pointer is underlined, which is no color, so it shows under `NO_COLOR` too, and it never looks like focus.

### 15.5 Interaction
Modelled on Claude Code's choice prompts and herdr's clickable UI.

- **Focus.** `tab` / `shift+tab` move between regions: task list, action bar, log (and the open dialog, which keeps the focus until it closes). Inside a region: `←/→` along the action bar, `↑/↓` (and `j/k`) in lists, dialogs and the log, `pgup/pgdn` to scroll. `enter` or `space` activates the focused element. Shortcut keys (§15.3) work from anywhere outside a text field.
- **Mouse.** Click or tap activates a button, a task row (selects it and opens its details) or a dialog option. The wheel scrolls the list or log under the pointer. While the pointer is over something a click would act on now (a button, a row, a dialog option or field, not the bar under an open dialog or page), its text is underlined (§15.4); hover never moves the focus or changes what `enter` does. Mouse reporting (button presses, wheel and all pointer motion, for hover) is on by default; `[tui] mouse = false` turns it off, hover with it, because while a TUI captures the mouse the terminal's own text selection usually needs `shift`+drag. A terminal that doesn't report motion without a button only loses the hover.
- **Dialogs.** When the engine asks the owner something, a modal choice list opens at once and is focused on the safe default:

  | Question | Options (default first) |
  |---|---|
  | Commit (§6.5, `commit = "ask"`) | **Commit** · Leave uncommitted |
  | Session asks to skip (§6.2) | **Keep working** (discards the request) · Skip the task |
  | Reset request (§6.2), over any other question | **Keep it as it is** (drops the request) · Reset ID |
  | Session lost (§6.3) | **Start fresh** · Continue conversation · Mark done · Skip · Stop igris |
  | `before_task` hook failed (§6.7) | **Retry** (runs the hook again) · Mark done · Skip · Stop igris |
  | Plan approval (§6.3), titled `ID presents a plan` | the plan (markdown as text, cleaned, line breaks and indentation kept, scrolls with `pgup`/`pgdn` and the wheel; the box may grow to 100 columns) · **Approve: the session switches to MODE** · Reject with feedback… (a text field; empty is refused, `esc` returns to the plan) · Answer in the pane (opens the session). It closes by itself when the plan is answered in the pane |
  | Stop (`x`) | **Keep running** · Stop igris |
  | Skip (`s`) | text field for the reason · **Cancel** · Skip (it says the session is closed only when one is open) |

  Options can be picked by click, by `↑/↓` + `enter`, or by their number (`1`…`9`). `esc` closes a dialog with its non-destructive choice (cancel / keep) where it has one; the session-lost, hook-failed, commit, reset and plan approval questions stay pending until answered, and the dialog reopens from `Answer…` (a reset request first). Answering a reset request brings back the question it covered. Destructive choices (skip, stop) are never the default.
- **Skip permissions** (§7.3) is never a single click or key: picking `yolo` or `plan:yolo` in a mode list opens a text field that only accepts the typed phrase `skip permissions`.
- `--no-tui` (§14) remains the plain stdin interface with the same actions as typed commands.

### 15.6 Home screen

Bare `igris` on a terminal (§14) opens home: the project at a glance and the way into everything else. Home itself never writes the plan or `igris.toml`; it starts runs, `init`, `adapt` and `draft`, opens files in the owner's editor, and runs the package manager's update when the owner asks for it (Check for updates, below).

**Layout.** A header (project, plan file, the backend's name — `herdr`, `tmux`, or `herdr/tmux` when `auto` finds neither — with `…` / `✓` / `⨯`, default mode, and the run state on the right), then **PHASES** (glyph, ID, title, progress bar, `done/total` counting done and skipped tasks, and the §5.1 outcome as a word: `done`, `next`, `wait` when the phase would complete once the tasks of other phases are done, else `stuck`), **NOW** (the card below), **HEALTH** (the check result and the doctor summary) and **RECENT** (the last 2–3 runs from `runs.jsonl`), a status line with the last action's result, and the action bar. Wide (≥ 100×13) puts PHASES left and NOW/HEALTH/RECENT right; narrow is one column (NOW, PHASES, HEALTH, RECENT) and must stay usable at 50×20, with the bar folding into `More…`; below 60 columns the bars are dropped and the numbers stay; below 40×12 only `igris — terminal too small (need 50×20, now W×H) · q quits` is shown.

**NOW card**, one state at a time. When several hold, the first in this order wins: no `igris.toml`; a live igris on this host holds the lock; `igris.toml` invalid; plan missing; plan invalid or unreadable; a stale or unreadable lock; a remote lock; interrupted; stopped; ready. Without a reachable backend the ready and plan-invalid cards say why Arise or Adapt is missing and where to run igris (a herdr pane, a tmux session).

| State | Card | Default action |
|---|---|---|
| No `igris.toml` | **GET STARTED**: numbered steps `igris.toml`, plan, check, preview, arise, each turning `✓` as data refreshes; after init, the plan step offers **Plan from template…** (`init --example` or `--template`), or **Check** / **Adapt** when a non-canonical plan exists | Init |
| `igris.toml` invalid | **CONFIG INVALID**: the first few problems, "… N more — Settings shows all"; igris uses the defaults meanwhile | Settings |
| Plan missing | **NO PLAN**: the path igris looked at | — |
| Plan invalid | **PLAN INVALID**: file, problem count, the first few `file:line: message`, "… N more — Check shows all"; without a backend, why Adapt is unavailable | Check |
| Ready | **READY**: the phase with work, `next` and `then` tasks with rank, tasks left and how many wait on what | Arise… |
| Interrupted | **INTERRUPTED**: last run's range, the interrupted task, its session and whether Resume reattaches; **STOPPED** when `state.json` has phases but no current task, and only while the last run's phases still have work; when every one of them is complete the card is READY (next phase with work) with a dim `last run P1 completed` line, and the bar says Arise… | Resume… |
| Running elsewhere | **RUNNING** in another igris (pid, host, since): current task, the last 3 run-log events, "This screen only watches. Use that terminal to control the run." Read-only (§13) | Open session |
| Remote / stale lock | `LOCKED by a run on host H …; igris can't tell if it is alive` / `LAST RUN DID NOT CLEAN UP (pid N gone)`; Arise stays available and its wizard asks before clearing (below) | Arise… |

**Pages** (pushed on home; each page's own key runs it again):

| Page | Key | Shows | Actions |
|---|---|---|---|
| Phase detail | `enter` / second click on a phase | that phase's task rows (as in the run view), the §5.1 outcome, counts | Arise this phase…, Preview this phase, Edit plan |
| Task detail | `enter` on a task | the run view's detail page plus the task's last attempts from history | Copy ID (`y`) |
| Check | `c` | `check`'s problems, then its warnings | Edit plan, Adapt (when invalid) |
| Doctor | `i` | `doctor`'s rows (§14); the selected row shows its next step | the row's safe home equivalent (Edit igris.toml, Init, Check, Adapt), Copy fix command (`y`) |
| History | `h` | `history`'s runs; `enter` drills into a run's tasks and a task's attempts | — (read-only) |
| Settings | `,` | problems first, then the effective config in TOML-shaped sections; defaults marked `· default` in words; secrets never shown (`token = set (env:NTFY_TOKEN)` / `set (hidden)`) | Edit igris.toml (`e`), Doctor, Init (no file) |
| Preview | `v` | the dry run (§14) as numbered steps: task, rank → model, mode, `[SKIP PERMISSIONS]`, resumed; warnings, totals | Arise with these settings |
| Notify test | `n` | after a confirm dialog, one row per event × channel filled in live (`· sending` → `✓ ok` / `⨯ FAILED: reason`), as `notify test` (§14) | Cancel while sending, Send again |
| Init | `I` | after a confirm dialog listing the files it touches (**Create files** · Cancel; never overwrites), each step's result as `init` prints it, then the backend hint | **Plan from template…** (`E`; a picker of Example and the templates, a confirm dialog, then `init --example` or `--template NAME`, never overwriting a plan), Check, Doctor |
| Adapt | `A` | after a confirm dialog (**Cancel** · Adapt with sonnet · Adapt with opus; the `ANTHROPIC_API_KEY` warning when it applies), `adapt`'s progress, then the adapt review (§9) | Open session, Cancel; then Accept / Reject as in §9 |
| Draft… | `D` | since 1.3: a picker of **From a goal**, **From a spec file…**, **Import an export…** and **Extend the plan…** (the last three ask for the path or the request; the model as for Adapt), the draft's progress, then the review (§9.1) | Open session, Cancel; then Accept / Reject as in §9 |

**Keys on home.** The run view's letters (`o m M p d s r x q ?`) never mean something else on home; `y` is copy and `j`/`k` navigate everywhere. Actions appear only when they apply (§15.3):

| Key | Action | Shown when |
|---|---|---|
| `a` | **Arise…** / **Resume…** (the label follows the state) | config and plan valid, the backend reachable, no live lock held by another igris |
| `v` | Preview | plan valid |
| `c` | Check | a plan file exists |
| `i` | Doctor | always |
| `h` | History | `runs.jsonl` exists |
| `e` | Edit the file this screen is about (the plan on home and phase detail, `igris.toml` on Settings) | the file or its directory exists |
| `,` | Settings | always |
| `n` | Notify test | a channel is set up |
| `A` | Adapt | the plan exists, is invalid, and the backend is reachable |
| `D` | Draft… (since 1.3) | the backend is reachable and no run holds the project; goal, spec and import when `igris.toml` is valid or missing and the plan file is missing, Extend when the plan is valid |
| `I` | Init | there is no `igris.toml` |
| `E` | Plan from template… (Example, go-cli, web-app, library) | `igris.toml` is valid and the plan file is missing |
| `o` | Open session | another igris runs here and `state.json` has a session ref |
| `S` | Side sessions (§15.7); home shows `SIDE N open` in its right column while any are, which opens the page | the backend is reachable, or a side session is open; also while another igris runs here |
| `u` | **Check for updates** (bar button **Updates**; below) | always; Update itself not while a run holds this project's lock |
| `?` `q` `esc` `ctrl+c` | help, back or quit, back, quit the app | always |

Focus regions in tab order: PHASES → action bar → HEALTH/RECENT (each line opens its page). The selected phase is the wizard's default. Mouse as in §15.5: a click selects a phase and a second click opens it; a click on a HEALTH or RECENT line opens its page.

**Start-run wizard** (`a`, or `igris arise` with nothing to resume, §14). A sequence of dialogs over home (full-screen when narrow), each with the CLI's default and meaning:
1. **What to run** — only when something can be resumed: **Resume last run (T first)** · Start a phase…. Resume continues the previous range with the interrupted task first (§13); the session-lost question then comes in the run view.
2. **Phase** — phases with work first, each with `n/m` and its outcome word; complete phases last, labelled `complete`. Default: the selected phase if it has work, else the first with work.
3. **Through** — **Only P** · P through Q … (later phases only).
4. **Mode** — **As planned** (Mode column, then `default_mode`) · default · accept · auto · plan · yolo `[SKIP PERMISSIONS]` · manual · plan:default · plan:accept · plan:auto · plan:yolo `[SKIP PERMISSIONS]`. yolo and plan:yolo open the typed-phrase field (§7.3, §15.5).
5. **Summary** — `Arise P through Q · mode M · N tasks · first T rank`, with the warnings `arise` prints (plan hints, the integration hint, not a git repository, uncommitted changes, the interrupted task first): **Arise** · Preview · Cancel.
6. **Confirmations** — every start warning that `arise` asks about becomes a dialog, Cancel first (e.g. `ANTHROPIC_API_KEY`: **Cancel** · Start anyway).
7. **Launch** — "starting… checking the backend, taking the lock" with Cancel. An error before the run starts becomes the matching dialog and the launch is retried: readiness drift → the list of changes, **Cancel** · Let igris fix them; a task needing yolo → the typed phrase (unless typed in step 4 of this launch); a stale or remote lock → **Cancel** · Clear the lock and start (the `--force-unlock` equivalent, §14). A live local lock, an unavailable backend or any other error → an error dialog with the next step and a link to Doctor; no retry. A failed launch leaves no engine behind.
8. **Started** — the run view is pushed (§15.3, Quit labelled **Home**).

Every answer belongs to that one launch and is thrown away afterwards; nothing — in particular no skip-permissions confirmation — carries over to the next run.

**Edit.** `e` suspends the TUI and runs `$VISUAL`, else `$EDITOR`, on the file's absolute path (§16). With neither set, a dialog shows the path: **Close** · Open with vi (only if `vi` is on `PATH`) · Copy path (`y`); igris never falls back to `vi` silently. On return igris reloads and re-validates (`✓ igris.toml valid` / `⨯ igris.toml: N problems — Settings`); a non-zero exit reads `editor exited with status N; file reloaded`; theme, mouse and rank colours apply at once. Under a live local lock, editing the plan asks first: **Cancel** · Edit anyway.

**Check for updates** (`u`, since 1.2; decisions.md V12-P4) is home's only network request, and only the owner starts it: never at start, on a timer, in the run view or in a `--no-tui` run. It does what `igris update` does (§14), off the loop, in a dialog: *checking…* (Cancel); *up to date*; *development build* with the latest release's link; a failed request with its error, which says to check the connection (**Try again** · Close). A newer release shows current → latest, the method and the exact command, the release link and the note that runs in other projects keep going and their sessions then run the new igris for `igris done`, with **Update** · Not now. While a run holds this project's lock (home's own run, or another igris's for a read-only home) there is no Update, only "Update after the run ends". An archive install shows the link and the download, verify and install steps, and runs nothing. Update runs the command off the loop (*updating…*; closing the dialog leaves it running, `u` shows it again, and quitting igris waits for it, since a package manager stopped halfway is worse); a success says `Updated: igris in PATH is now X.Y.Z. Quit and start igris again.` (**Quit igris** · Close), a failure shows the end of the output, cleaned, and the hint. igris never restarts itself.

**Refresh.** Every 2 s home compares size and modification time of `igris.toml`, the plan, `.igris/state.json`, `.igris/igris.lock` and `.igris/runs.jsonl`, and reloads only what changed (the plan is parsed and validated only when its file changed). It also refreshes on returning from the editor, when a run, review, init or adapt ends, and when the terminal regains focus. Slow checks (backend availability, doctor's process calls) run asynchronously when home opens and on demand, never on the poll. Lists draw only their visible rows.

**`NO_COLOR` and mouse off** change only the look and the input method (§15.4, §15.5): states are glyph plus word everywhere, bars are `█░` with the numbers beside them.

### 15.7 Side sessions

Since 1.2 (§6.10). `S` on home and in the run view, not in `--no-tui`.

- **Side sessions page.** One row per open side session: its ID, the state from its hooks (`working`, `idle`, `blocked`, `waiting`, `unknown`), age, subject, rank, mode and `worktree` when it has one. **New…** opens the dialog, **Focus** (`enter`, `o` or a click on the row) brings its pane to the front, **Close…** (`x` on a row, or *Close a session…*) asks **Keep it** · Close session and closes it (§6.10). With none open, `S` opens the dialog directly; the page is read again when it opens, and on home with every refresh.
- **Dialog**, a sequence like the start-run wizard: **Preset** (**Blank** · About this task · igris problem; *about this task* only with a task selected or current: in the run view the selected task, else the current one; on home the run's current or interrupted task, else the selected phase's next), **First prompt** (a text field holding the prefill, its whole text shown above it; `enter` goes on, and on an empty field types nothing; more than 8 KiB once cleaned is refused with a hint), **Model** (the `[models]` ranks, `[side] model` first), **Mode** (`default` · `manual` · `accept` · `auto` · `plan`, `[side] mode` first, `manual` first during a run in the shared tree; no `yolo`), and during a run **Where** (**Shared tree** · Own worktree) with the shared-tree warning; then **Open** · Cancel. Needs a reachable backend. A read-only home (a run in another igris, §13) may open, focus and close side sessions too.
- Quitting igris leaves side sessions open; the next home or run view lists them again.

---

## 16. Errors and safety

**Trust model.** The owner and `igris.toml` are trusted. A session is not: it can edit any file in the project without a prompt in `accept`, `auto` and `yolo` mode (and in `default` when Claude Code's own default is auto, §7.1) — the plan, `igris.toml`, `.igris/` — and it controls its done note and the output of `verify`. Igris is designed so that none of this lets a session change which model or permission mode a task runs with, make igris skip verification, or drive the owner's terminal. What a session can do by design: edit files, and therefore influence what `verify` and git hooks execute as the owner once they run (verify runs project code; this is inherent to verifying) — so the mode a task runs in is the real boundary.

[`docs/threat-model.md`](docs/threat-model.md) lists the assets, actors and boundaries, the threats at each boundary, the accepted risks, and the test that enforces each rule below; `tools/threatmodel` fails when a mapped test disappears. A change to this section updates it.

- The plan is never written while it fails validation.
- Sessions can't change igris's behavior mid-run: config is snapshotted (§13), `igris skip` from a session needs owner confirmation (§6.2), model/mode flags can't be smuggled in via `extra_args` (§7.4), values read back from `state.json` are checked before they become arguments (§7.4, §13), and plan edits igris didn't make hold the run until the owner looks (§5.4).
- Nor the next run's: an `igris.toml` edited during a run is used by a later run only after the owner confirms it (§13), and a session can't point igris at another pane through `state.json`: a backend reattaches only to the pane it started for the task, and the held task prompt is built again from the plan rather than read back (§13). `doctor` warns about Claude Code settings that would widen every later session (allow-all `Bash` rules, `igris skip` allowed, a bypassing `defaultMode`; §14).
- Every external command (herdr, tmux, claude, git, verify) has a timeout; failures are shown with the command and exit code. Captured output is bounded (§6.4). The one exception is the owner's editor opened from home (§15.6): it is interactive and owner-configured, so it has no timeout. `$VISUAL` / `$EDITOR` is split into argv with `strings.Fields` (arguments like `code --wait` work; quoted arguments don't), the file's absolute path is the last argument, no shell is involved, and with neither variable set igris asks instead of falling back to `vi`.
- Igris never runs commands from the plan's content, and never passes plan text through a shell — prompts go to herdr as argv or to tmux through a paste buffer on stdin, never interpolated into shell strings or tmux formats (§11.5).
- Text igris did not write — plan cells, done notes, command output, backend errors, and on home also run-log details, the lock's host, notify errors and config string values — is cleaned of escape sequences, control characters and the invisible characters that change what is read (the bidi controls U+202A–U+202E, U+2066–U+2069, U+200E, U+200F and U+061C, which can make a line read in another order than it is; the zero-width U+200B, U+2060 and U+FEFF; and, since 1.0, the soft hyphen U+00AD, the invisible operators U+2061–U+2064, the deprecated format controls U+206A–U+206F, the interlinear annotation characters U+FFF9–U+FFFB and the tag characters U+E0000–U+E007F, which draw nothing in a terminal yet carry text a model reads — a subdivision-flag emoji, which uses tags, loses its tags; the joiners U+200C and U+200D stay, emoji need them) before it is drawn in the TUI or the `--no-tui` log, sent as a notification, used in a commit message, or typed into a session's pane (§3.5, §6.2, §6.4, §9.5).
- Signal and state files are written atomically, with `0600` files and `0700` directories. Every file a session can replace is read only as a regular file of bounded size, opened without blocking, so a FIFO or a huge file in its place is an error igris reports, never a hang: signals (64 KiB, §6.2), agent state (4 KiB, §6.3), `state.json` (1 MiB) and the lock (4 KiB), never through a symlink, and the plan (16 MiB, all its files together, §3.7) and `igris.toml` (1 MiB), which may be symlinks of the owner's (`internal/fsafe`). An included file must stay inside the project root once symlinks are resolved and end in `.md`, so an include a session adds can't make igris read other files; it is a plan edit and holds the run (§5.4).
- Everything igris writes to stderr is cleaned the same way, since errors can carry text igris didn't write (a git hook's output, values read back from `.igris/`).
- Every user-facing error says what to do next (the command to run, the setting to change, the file to fix or delete).
- The commands igris runs (verify, task hooks, git, and since 1.2 the update command) never get the environment variables the notification secrets refer to through `env:` (§6.4); `[run] env` limits verify and task hooks further to an allowlist.
- `igris draft` (§9.1) treats a spec or an issue export as untrusted input to a session: the session runs in `manual`, writes only a proposal, and nothing reaches the plan without the owner's Accept after the review; an export is parsed by igris, bounded and cleaned before the session or the TUI sees it. A spec or an issue can still steer what the session proposes, an accepted risk: the review is the guard.
- The update check (`igris update`, home's `u`, §14, §15.6) runs only when the owner asks. GitHub's answer is untrusted: it is read with a 10 s timeout and at most 1 MiB, only a `tag_name` of the shape `vX.Y.Z` is used and none of its text is shown. The install command is built from that version only, as argv, never through a shell, and igris never replaces its own binary: the new binary's integrity stays with Homebrew (the formula's sha256) and Go (the checksum database), so a forged answer can at worst name a version the package manager then refuses or resolves. An archive install gets the signed release's verify steps, never a download by igris.
- Igris reads a session's transcript only through the path its hooks report, named after the session's UUID, and only the fields in §6.9; it never stores, logs or shows message content. A session can forge its own transcript or `quota.json`, and so its usage and audit: accepted, since usage is information and the audit catches configuration slip, not a hostile session (§6.9).
- Igris never reads or logs Claude Code credentials, and never sets `ANTHROPIC_API_KEY`. Notification secrets never enter the run log or error messages (§10), nor the config hash when they are `env:` references (a secret written in `igris.toml` itself is part of what the hash covers; accepted, docs/threat-model.md), and never appear on home's Settings, Doctor or Notify test pages (§15.6), which say only whether a secret is set and where from. An ntfy `token` is sent as a bearer header and a Gotify `token` as `X-Gotify-Key`, so use an `https://` server for them (a `server` URL with a `user:password@` part is a config error: it is not a secret field and would be shown); plain `http://` works (a server on the LAN), but `check`, `arise` and `doctor` warn about it for ntfy with a token, Gotify, and a webhook `url`, written in `igris.toml` or, once its value is known, from an `env:` reference (the payload and its signature go unencrypted; the warning names the variable, never the value).

---

## 17. Testing requirements

- **Parser:** table splitting (escaped pipes, padding, backticks), phase detection, column aliases, deps incl. ranges and cross-phase, every validation error. Golden tests with realistic plans: at least one large synthetic fixture (10+ phases, 150+ tasks, ranges, escaped pipes, cross-phase deps, user tasks). Never commit private plans as fixtures.
- **Writer:** byte-for-byte preservation except the target Status cell; CRLF files; a leading UTF-8 BOM; files without trailing newline; concurrent-edit detection.
- **Scheduler:** selection order, resume, stuck detection, readiness sync, `--through`.
- **Engine:** full phase runs on the fake backend covering done, verify failure + retry, verify limit, session lost, user tasks, skip, pause, resume after restart, stray signals.
- **herdr and tmux backends:** command construction and output parsing against recorded fixtures; the backend conformance suite (§11.1) on fake, herdr and tmux; a manual smoke checklist per backend (`docs/smoke-herdr.md`, `docs/smoke-tmux.md`).
- **Hooks:** `igris hook` against the recorded Claude Code payloads (`internal/hook/testdata/`); the agent-state reader fuzzed like the signal reader.
- **TUI:** model update tests (teatest) for key handling and both layouts. Home, its pages and the wizard (§15.6) have teatest goldens at 120×40, 80×24 and 50×20, run against a fake of the services seam; a test injects escape sequences into every untrusted text source and checks every screen draws them cleaned; the lock is released on every exit path of a run started from home (leave, stop, ctrl+c, a recovered panic, a failed launch), checked under the race detector; the existing run-view goldens stay unchanged. A benchmark on the large fixture keeps home's `View` under 2 ms at 120×40.
- **Notifications:** httptest servers for ntfy and Discord; secret redaction.
- **Fuzzing** (stdlib `testing.F`): the table tokenizer and parser (no panic, deterministic, validation never crashes), the Status writer (only the target Status cell changes, for any input), the config loader and the signal reader (arbitrary bytes give an error, never a panic). `make test` replays the seeds; `make fuzz` runs each target (10 minutes each before a release); a failing input is committed under `testdata/fuzz/` as a regression seed.
- **Compatibility suite** (§19): `testdata/compat/<tag>/` for every release, captured from the release itself by `make compat TAG=vX.Y.Z` (its `igris init` config and plan, its example configs and plans and its plan templates, the signal files its `done` and `skip` wrote); `testdata/compat/state/` and `runs/` hold the state files and run logs each release wrote, shaped after its structs. In `go test` (so in CI) every config loads (after `igris config migrate` where a key was removed), every plan passes `check` and a dry run, every `--json` output matches its schema, every signal, state file and run log is read whole. A release in the CHANGELOG without its directory fails the suite, the newest excepted until it is tagged.
- `go test -race ./...` clean.

---

## 18. Distribution

- Single static binary, `CGO_ENABLED=0`, for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.
- Built with **GoReleaser** (P0-04): `tar.gz` per target (binary + `LICENSE` + `README.md`) and a single `checksums.txt` (SHA-256), version injected with `-ldflags`. `make release-local` runs `goreleaser release --snapshot --clean` into `dist/`; nothing is published automatically — the owner uploads to a GitHub Release by hand (task M7-07).
- **Signed from v0.6.0** (V06-P1): `checksums.txt` is signed with an SSH key (`ssh-keygen -Y sign -n file`, giving `checksums.txt.sig`), and the release tag is signed with the same key. The owner tags, builds and signs every release with one command (`make sign VERSION=X.Y.Z`; the key has a passphrase igris's agent never sees); nothing is signed in CI. It signs only a clean, pushed `origin/master` with a CHANGELOG section for the version, never pushes the tag, and checks its own output; the agent checks again before it pushes the tag and publishes. The public key is in `allowed_signers` at the repository root, in the README and among the owner's GitHub signing keys. A download is checked with `ssh-keygen -Y verify -f allowed_signers -I igris -n file -s checksums.txt.sig < checksums.txt`, then `sha256sum -c --ignore-missing checksums.txt`. A rotated key stays in `allowed_signers` with `valid-before`. Releases before v0.6.0 have checksums only.
- Each archive has an SPDX JSON SBOM made by syft through GoReleaser's `sboms`, listed in `checksums.txt` (so covered by its signature).
- The Go toolchain is pinned in `go.mod` (`toolchain` line, V06-02). Releases, CI and govulncheck use that exact Go, and it is bumped for every Go patch release that fixes something igris uses.
- `go install github.com/drilonrecica/igris/cmd/igris@latest` works; such builds have no ldflags, so `igris version` falls back to the module version from the binary's build info (`v0.1.0` → `0.1.0`).
- CI (P0-07, V06-P1): GitHub Actions on push to `master` and on PRs — gofmt check, `go vet`, golangci-lint, `go test -race ./...` on Linux and macOS, a pinned `govulncheck` (also weekly on a schedule; a reachable vulnerability fails it), a reproducible-build check (the same commit built twice with the release flags must give identical binaries) and a short fuzz run per target. Read-only token, no secrets, never publishes releases or signs anything.
- Project page (P0-09): `https://drilonrecica.github.io/igris/`, a static page built from `site/` and deployed by `.github/workflows/pages.yml`. The docs pages under `/docs/` (V10-03) are rendered from markdown by `tools/sitedocs` (standard library only) in the same build: the pages written for the site in `site/docs/` (install, quick start, modes, notifications, adapt, several projects, FAQ, troubleshooting, upgrade) and, from their one copy in `docs/`, the plan format, the generated config reference and the compatibility page. The build fails on a link to a missing file or heading. That is the only workflow with write permissions (`pages`, `id-token`, for the deploy job only); it has no secrets and never builds or publishes releases.
- Homebrew tap (V02-P1): `brew install drilonrecica/tap/igris`, from the repository `drilonrecica/homebrew-tap`. The formula is generated by `make release-local` from a repo template (`tools/genformula`, output `dist/igris.rb`; GoReleaser's `brews` is deprecated in 2.x), from `dist/checksums.txt`; the owner pushes it to `Formula/igris.rb` in the tap by hand (`docs/homebrew-tap.md`). Nothing is published automatically, as for the archives.
- MIT license.

---

## 19. Compatibility

What igris 1.x promises not to break (V07-P1). Until v2.0, every surface below keeps working unchanged. Additions are allowed; removals and changed meanings are not. [`docs/compatibility.md`](docs/compatibility.md) maps each frozen surface to the tests that keep it, and `tools/contract` fails when a surface has no row or a named test is gone.

**Frozen:**
- **The plan format, format 1**: §3–4, also written out on its own in `docs/plan-format.md`. A plan may declare `<!-- igris:format 1 -->` on a line of its own before the first phase heading; without the marker it is format 1, forever. A plan declaring a format igris doesn't know is refused with "plan format N needs a newer igris". **Format 2** (since 1.2) is format 1 plus includes (§3.7), declared with `<!-- igris:format 2 -->`, and frozen the same way; igris 1.0 and 1.1 refuse it rather than run the main file's phases only, so a split plan needs igris 1.2 or later wherever it runs. Format 1 includes the leniency the parser has always had: Mode and Verify values and phase IDs are case-insensitive (§3.1–3.2), header cells are trimmed of spaces, tabs, `*` and backticks. Model ranks are case-sensitive.
- **`igris.toml`**: the keys, defaults and validation in §12. An unknown key is an error.
- **The CLI**: commands, flags, arguments and exit codes (§14), including `igris update` with `--check` and `--yes` (since 1.2, additive) and `igris draft` with its subcommands and flags (since 1.3, additive). Exit codes are `0` success, `1` failure, `2` usage error; `3`–`9` are reserved, and a 1.x release may give a case that exits `1` today its own code from that range, so scripts test for non-zero (or for `2`), never for exactly `1`. Text output is for people and may change; `--json` is the machine interface.
- **`--json` output** of `check`, `phases`, `status`, `history`, `report`, `usage` (since 1.1) and `doctor`: each is an object with `"v": 1`, described by a JSON schema in `docs/json/`.
- **The session contract**: `igris done`, `skip` and `reset` (§6.2, §14) and the signal files they write (§13).
- **`.igris/`**: the paths in §13; `state.json` (below); the run log `runs.jsonl` (`"v": 1`, `docs/runlog.md`).
- **Template variables** of `prompt_template` (§6.1), `commit_message` (§6.5) and notification `template`s (§10).
- **The generic webhook** (§10): the payload (`"v": 1`), the `X-Igris-Event` and `X-Igris-Signature` headers and the signature.
- **The task-hook environment** (§6.7): `IGRIS_TASK_ID`, `IGRIS_PHASE`, `IGRIS_RANK`, `IGRIS_MODEL`, `IGRIS_RESULT`.

**Not frozen:** the TUI (§15); the default task prompt's wording; text output; the hidden `hook` and `__complete` commands; the contents of `.igris/agent-state/`, `.igris/hooks/`, `.igris/plan-approval/`, `.igris/prompts/`, `.igris/quota.json`, `.igris/side/` (since 1.2), `.igris/draft/` (since 1.3) and `igris.lock`; the status line igris shows in task panes; the messages sent to ntfy, Discord, Slack, Gotify and the backend; the Go packages under `internal/`.

**Allowed in 1.x:** new optional plan columns (an older igris treats them as extra columns), new Mode values (an older igris reports them as an unknown Mode, like a config key it doesn't know), new config keys with defaults, new commands and flags, new JSON fields, new run-log event types, new template variables. Readers of `--json`, the run log and the webhook must ignore fields and event types they don't know. A config using a key added in a later 1.x fails on an older igris as an unknown key; the fix is to upgrade. A feature deprecated in 1.x warns until 2.0 and is removed only then.

**`state.json` versions.** igris 1.0 writes `"version": 3` and reads 1, 2 and 3, migrating older state in memory; the next save writes 3. A higher version is refused with "written by a newer igris; upgrade igris, or delete the file to start a new run". A damaged file (bad JSON, a value of the wrong shape) is reported as damaged, separately from an old one. A new field gets a new version only when an igris that ignored it would do something wrong; every 1.x igris reads every older version. A config that doesn't set a key added later keeps its snapshot hash, so adding keys never shows an interrupted run the "config changed" notice.

**Removed in v0.7:** `claude.command` (deprecated in v0.1.2, never read). An `igris.toml` that still has it, as every one `igris init` wrote before v0.7 does, fails to load with `claude.command was removed in 0.7 …; run igris config migrate to delete it` (`doctor`'s next step is the same), and `igris config migrate` deletes it. The config hash still reads it as `"claude"`, so configs keep their hashes.
