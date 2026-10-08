## 3. Canonical plan format

Igris's parser is deterministic and strict. Plans that don't match this format are rejected by `igris check` and can be converted with `igris adapt` (§9).

### 3.1 Phases
- A phase is a level-2 heading (`## `) whose section contains a task table.
- **Phase ID** = the first whitespace-delimited token of the heading text (`## M0 — Repository foundation` → `M0`). Matching on the command line is case-insensitive.
- If the first token is alphabetic and the second is a number (`## Phase 2 — API`), the phase ID is the two tokens joined with a hyphen: `Phase-2`. The second token must be digits only (`## Phase 2: API` has phase ID `Phase`).
- Only level-2 headings start a phase; `###` and deeper headings stay inside the current phase. Headings and tables inside fenced code blocks (```` ``` ```` / `~~~`) are ignored.
- Phase IDs must be unique within a plan.
- `##` sections without a task table (Legend, Working rules, traceability tables) are ignored. A plan needs at least one task table; a plan without any is a validation error (it is most likely in another format, §9).
- **Phase order** is file order.

### 3.2 Task tables
A task table is a GitHub-flavored markdown table whose header row contains at least the columns **ID**, **Status** and **Model** (case-insensitive, surrounding `*`/backticks ignored). Recognized columns:

| Column | Required | Purpose |
|---|---|---|
| `ID` | yes | Unique task ID across the whole plan. Pattern: `[A-Za-z0-9][A-Za-z0-9._-]*`. |
| `Task` | recommended | Description. The first `**bold**` span is used as the task title; otherwise the first 80 characters. |
| `Deps` | no | Dependencies (§3.4). Missing column = no deps. |
| `Status` | yes | §3.3. |
| `Model` | yes | Rank alias or `—` for no model. |
| `Owner` | no | `agent`, `user`, or `agent + user`. Missing column = `agent`. |
| `Mode` | no | Per-task run-mode override (§7.2): `default`, `accept`, `auto`, `plan`, `yolo`, or `—`. |
| `Verify` | no | Verify profile from config (§6.4), or `none` to skip verification for this task. Never a command. |
| `Timeout` | no | Go duration (`45m`, `1h30m`) after which a running task is reported overdue (§6.3). Never stops a session. |
| `Context` | no | Comma-separated repo-relative paths (files or directories) the task prompt names as required reading (§6.1). |

- In `Model` and `Mode`, `—`, `-`, `none` (case-insensitive) and an empty cell all mean "no model" / "no override", as in `Deps`. `Owner` is case-insensitive (`Agent + User` = `agent+user` = `agent + user`); an empty cell means `agent`.
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

Validation reports **every** problem at once, each as `file:line: message` saying what to fix, sorted by line; a dependency cycle is reported once with its path (`a → b → a`).

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
