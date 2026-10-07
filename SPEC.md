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
6. **Works from anywhere.** Runs inside a persistent multiplexer (herdr in v1), so the owner can detach, reconnect over SSH (e.g. Termius), and find everything as it was.
7. **Bring your own plan.** Works on the owner's existing `tasks.md`, with an AI-assisted `adapt` step for plans that don't match the canonical format.

### Non-goals (v1)
- Parallel task execution or multiple agents per task.
- Running agents other than Claude Code (the backend interface leaves room; not implemented).
- Multiplexers other than herdr (tmux etc. come later via the backend interface, §11).
- Hosted service, web UI, accounts, telemetry. Igris makes no network calls except to the notification endpoints the user configures.
- Estimating or tracking token cost. Igris enforces the model; it does not meter usage.
- Native Windows. Windows is supported through WSL2, with herdr and Claude Code inside WSL as well; release builds are for Linux and macOS only.

---

## 2. Concepts

| Term | Meaning |
|---|---|
| **Plan** | The markdown file containing phases and task tables (default `tasks.md`). Single source of truth. |
| **Phase** | A `##` section of the plan containing one task table, identified by the first token of its heading (e.g. `P0`, `M0`). |
| **Task** | One row of a task table. |
| **Rank** | The model alias in the task's Model column (`sonnet`, `opus`, `fable`, …), resolved to a Claude Code `--model` value through config. |
| **Agent task** | Owner is `agent` or `agent + user`. Igris launches a session. |
| **User task** | Owner is `user` (Model is `—`). Igris pauses and waits for the owner to finish it. |
| **Session** | One Claude Code process in one multiplexer pane, bound to exactly one task. |
| **Backend** | The adapter that creates panes, starts Claude Code, reads agent state and closes panes. v1: herdr. |
| **Signal** | A file written by `igris done` / `igris skip` that tells the running igris a task is finished. |
| **Run mode** | Claude Code permission mode used to launch a session: `default`, `acceptEdits`, `auto`, `plan`, `bypassPermissions`. |

---

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

- In `Model` and `Mode`, `—`, `-`, `none` (case-insensitive) and an empty cell all mean "no model" / "no override", as in `Deps`. `Owner` is case-insensitive (`Agent + User` = `agent+user` = `agent + user`); an empty cell means `agent`.
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

---

## 4. Plan writing rules

Igris edits the plan file in place, so writes must be surgical:

1. **Only Status cells change.** Every other byte of the file — whitespace, other cells, other sections, line endings, trailing newline, a leading UTF-8 byte order mark — is preserved exactly.
2. Within a Status cell, the original padding and backtick style are preserved (`` `ready` `` → `` `done` ``). Any text after the old keyword is dropped (`blocked (waits on vendor)` → `ready`), since it described the old status.
3. **Re-read before write.** The file is re-read and re-parsed immediately before every write, because the owner or a session may have edited it. If the target row no longer exists, igris stops with an error rather than writing.
4. **No lost updates.** Igris hashes the bytes it re-read and, immediately before the rename, hashes the file again. If it changed in between, the write is discarded and retried from step 3 (up to 3 times, then stop with an error).
5. **Atomic write:** write to a temp file in the same directory, fsync, rename. The file replaced is the one the plan path resolves to, so a plan that is a symlink stays a symlink.
6. Igris never reorders, adds or deletes rows, and never edits the plan outside Status cells — except `igris adapt` after explicit owner approval (§9).

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
Igris remembers every row as it last read or wrote it (Status, Model, Mode, Owner, Deps, Task text; rows added or removed). Whenever it re-reads the plan to select a task and finds a difference it did not write itself — a session editing a later task's Mode or Model, marking tasks done, or the owner's own edit; igris can't tell them apart — it reports the changed cells (`M1-03 Mode — → auto`), sends `needs_input`, and **holds**: pause-after-task goes on and nothing is selected, not even "phase complete", until the owner resumes (`p`, or `pause` with `--no-tui`). The current task is not interrupted. Ready/blocked cells igris recomputes itself (§5.2) are never reported.

---

## 6. Session lifecycle (agent tasks)

For each selected agent task:

1. **Resolve** rank → model (`[models]` config) and run mode (§7.2). Unknown rank → stop with error, before anything is written.
2. **Mark** the task `in progress` (§4).
3. **Open a pane** via the backend in the project directory, labelled `<ID> · <rank>`.
4. **Start Claude Code** in that pane: `claude --model <model> --session-id <uuid>` + run-mode flags + `--append-system-prompt-file <igris rules file>` + `claude.extra_args` from config. Neither prompt travels as argv: herdr rejects control characters (newlines) in agent arguments (§11.2). Igris writes the rules to `.igris/prompts/<ID>.rules.md` first.
5. **Submit the task prompt (§6.1)** with the backend's prompt call once the session is `idle` (the backend's start call only returns when Claude Code is ready for input, so this cannot race with startup).
6. **Record** the session in `state.json` (§13). The task and its Claude session UUID (generated by igris) are written before step 2 and the session ref right after step 4, so a crash at any point leaves enough to resume.
7. **Watch** (§6.3) until a completion signal arrives or the owner intervenes.
8. **Verify** (§6.4), optionally **commit** (§6.5).
9. **Mark** `done`, sync readiness, append to the run log.
10. **Close** the pane (the Claude Code session ends; its transcript stays in Claude Code's own history).
11. Select the next task.

### 6.1 Task prompt
Two parts (§6 steps 4–5):
- **Igris rules** — the fixed, non-negotiable part (exactly one task, don't edit Status, how and when to run `igris done`, ask the owner when blocked). Passed with `--append-system-prompt-file` (verified in P0-03; accepted by Claude Code 2.1.291 though absent from `--help`) so they survive context compaction in long sessions.
- **Task prompt** — the first user message (sent via the backend's prompt call, multi-line OK), built from a Go `text/template`. The default template ships embedded; the owner can override it with `prompt_template` in config. Variables: `ID`, `Title`, `Text` (full Task cell), `Phase`, `PhaseTitle`, `Rank`, `Model`, `Owner`, `Deps`, `Extra` (map of extra columns, e.g. `Spec`), `PlanFile`, `Resumed` (bool), `CommitPolicy`, `DoneCommand`.

Defaults live in `internal/prompt/rules.md` (static, no template variables, so the rules hold after compaction) and `internal/prompt/task.md.tmpl` (P0-06).

The default prompt must tell the session:
- It is working on **exactly one task** (`ID — Title`) as part of an automated sequential run; do nothing beyond it.
- Read the project's agent rules (`CLAUDE.md`/`AGENTS.md` if present), the task's row in the plan, and any referenced specs before changing anything.
- If anything needs the owner — a decision, approval, credentials, an unclear spec — **ask in the chat and wait**. For `agent + user` tasks: present the recommendation/result and get the owner's explicit decision or sign-off before finishing.
- **Do not edit the Status column**; igris owns it and unblocks dependents itself. (This overrides any plan rule that says the agent updates Status.)
- Commit policy: don't commit yourself; depending on `commit`, igris commits after verification (`auto`/`ask`) or not at all (`never`, in which case earlier tasks' changes may still be uncommitted in the tree).
- When the task meets the project's definition of done and nothing is waiting on the owner, run **`igris done <ID> --note "<one-line summary>"`** as the very last action. Never run it with anything unresolved.
- If `Resumed`: a previous session worked on this task and may have left partial changes; inspect `git status`/`git diff` first and continue from there.

### 6.2 Completion protocol
- `igris done <ID> [--note TEXT]` and `igris skip <ID> --reason TEXT` locate the project root (walk up from cwd to the nearest `igris.toml` or `.igris/`), validate that `<ID>` exists, and write a signal file `.igris/signals/<ID>.json` (`{"id","action":"done|skip","note","at"}`) atomically. They print a one-line confirmation and exit 0; when no igris run holds the project lock (§13), the confirmation adds that the next `igris arise` applies the signal.
- They do **not** edit the plan themselves; only the running igris consumes signals. This keeps a single writer.
- A signal for a task that isn't the current one is kept and reported in the TUI; it is never applied silently.
- Signal files are read only if they are regular files of at most 64 KiB (sessions can write into `signals/`; a FIFO or a huge file must not stall igris); anything else is reported as unreadable. The note is cleaned of control characters before it is shown, logged or used as a commit body.
- A signal counts for the current task only if it was written at or after the task started. An older file (e.g. `igris done` run before igris reached the task) is kept and reported, never applied; running the command again replaces it.
- **Skip needs the owner.** A `skip` signal for an **agent** task is treated as a request: igris marks the task **Needs you**, notifies, and applies the skip only after the owner confirms in the TUI (or via `--no-tui` stdin). Pressing `s` in the TUI applies directly. For **user** tasks a skip signal applies directly, since only the owner acts on them.
- `igris init` adds the allow rule `Bash(igris done:*)` to `.claude/settings.local.json` (merging, never overwriting) so the done command doesn't trigger a permission prompt. `igris skip` is deliberately **not** allow-listed: a session that wants to skip has to go through a permission prompt and the confirmation above.

### 6.3 Watching a session
Igris polls the backend every 2 s (configurable) for the pane's agent state and checks for signals.

| Observation | Action |
|---|---|
| Signal present | Proceed to verification. |
| Agent `working` | Clear any "needs you" flag. |
| Agent `blocked`, `idle` or `done` without a signal for ≥ `needs_input_after` (default 30 s) | Mark the task **Needs you** in the TUI; send a `needs_input` notification once per idle episode. Possible causes: a question, a permission prompt, a plan awaiting approval, a usage limit, or a stall — igris doesn't try to tell them apart. |
| Agent `unknown` | Nothing; it says nothing about the agent (e.g. herdr's integration is missing). |
| Pane gone or Claude Code exited without a signal | Mark **Session lost**, notify, and offer: continue the conversation (`claude --resume <uuid>`, with a short fixed prompt telling the session to pick the task up again), retry fresh (`Resumed=true`), mark done, skip, or stop. A done signal written before the session went away still counts. |

Igris never advances on agent state alone — only on a signal or an explicit owner action. A signal for another task, or one igris can't read, is reported once and kept. A skip signal from an agent session is asked about once; declining it deletes the signal and the session carries on.

### 6.4 Verification
- Optional `verify` command in config (e.g. `make fmt lint test`), run with `sh -c` in the project root, with a timeout (default 15 min).
- The verify command (like every other config value) comes from the config snapshot taken at `arise` start (§13), never from a re-read of `igris.toml` mid-run.
- **Pass** → continue. **Fail** → delete the signal, wait until the agent is idle (it ran `igris done` as its last action, so it may still be finishing its turn; up to 30 s, then send anyway), and send the last 60 lines of output into the same session: "igris verification `<cmd>` failed: … Fix the problem, then run `igris done <ID>` again." The task stays in progress.
- Output is stdout and stderr interleaved as written, with escape sequences and other control characters removed before it is sent (the text is pasted into the session's terminal and must stay text); only the last 8 MiB are kept. A timeout counts as a failure. A verify command that can't be started at all stops the run with an error. The run log records each result, never the output.
- After `verify_max_attempts` (default 3) consecutive failures, igris stops sending failures back, marks **Needs you**, and notifies (`verify_failed_limit`). A later `igris done` is verified again (failures still not sent back); retrying the session starts a new count.
- No verify command → the signal is accepted as is.
- When the **owner** marks an agent task done (TUI `d`, `--no-tui` `done`, or the session-lost choice), verify is skipped: that is the owner's explicit decision. The commit policy still applies.

### 6.5 Commits
- `commit = "ask"` (default) — the TUI (or `--no-tui` stdin) asks y/n after each verified task.
- `commit = "never"` — igris never touches git. Uncommitted changes then carry over into the next task's session; the task prompt says so, and `Resumed` sessions can't tell whose changes they're looking at. Use `never` only if you commit by hand between tasks.
- `commit = "auto"` — after verification passes: `git add -A && git commit -m "<template>"`. Default message template: `{{.ID}}: {{.Title}}` plus the done note as body. If there's nothing to commit, continue silently. A commit failure stops the run and notifies.
- Igris checks `git status --porcelain` first: with nothing to commit, `ask` doesn't ask. The commit happens before the task is marked `done` (§6 steps 8–9). Git runs as argv, never through a shell, with a timeout.
- `commit_message` is a Go `text/template` with `ID`, `Title`, `Phase`, `Rank`, `Model` and `Note`; a template that doesn't parse stops `arise` before anything starts. User tasks are never committed.

### 6.6 Closing
After a task is accepted, igris waits up to 30 s for the agent to become idle (so it can finish its final message), then closes the pane.

---

## 7. Run modes and permissions

### 7.1 Modes

| Mode | Plan `Mode` value | Claude Code flags | Notes |
|---|---|---|---|
| Default | `default` | none (user's normal settings) | Every permission prompt goes to the owner. |
| Accept edits | `accept` | `--permission-mode acceptEdits` | |
| Auto | `auto` | `--permission-mode auto` | Claude Code approves routine actions itself and asks for risky ones. Middle ground between `accept` and `yolo`. |
| Plan | `plan` | `--permission-mode plan` | Session plans first; owner approves the plan in Claude Code, then it implements. `igris done` only becomes possible after approval, since plan mode blocks commands. |
| Skip permissions | `yolo` | `--dangerously-skip-permissions` | Shown with a red badge everywhere; needs per-run confirmation (§7.3). |

Exact flag spellings are verified against the installed Claude Code (P0-02, 2.1.291; the verified versions are in §11.4) and kept in one table in code. `--permission-mode` accepts `acceptEdits`, `auto`, `bypassPermissions`, `manual`, `dontAsk`, `plan`; there is no `default` value, so Default mode passes **no** permission flag (`manual` behaves the same but is not used).

Verified behavior that the design relies on:
- **`auto` does not pre-approve `igris done`.** An unfamiliar shell command still prompts (headless: denied; interactive: "This command requires approval"). `igris done` must therefore be allow-listed (below) in every mode that is not `yolo`.
- **Allow rule:** `Bash(igris done:*)` and `Bash(igris done *)` in `.claude/settings.local.json` (`{"permissions":{"allow":[...]}}`) both let `igris done` run without a prompt in `default`, `acceptEdits` and `auto`.
- **Plan mode blocks Bash** (including allow-listed `igris done`) until the owner approves the plan.
- `--dangerously-skip-permissions` runs `igris done` with no prompt.

### 7.2 Resolution order (per task)
1. Owner override set in the TUI for that specific task.
2. The task's `Mode` column, if present and not `—`.
3. The run mode chosen in the TUI for this run.
4. `default_mode` in config.
5. `default`.

Mode changes in the TUI apply to the **next** session launched; a running session keeps its mode.

### 7.3 Skip-permissions guardrails
- Choosing `yolo` (for the run or any task) requires typing the confirmation shown in the TUI, every run. Config can't silently enable it: `default_mode = "yolo"` still asks once at start.
- The TUI header shows a persistent red `SKIP PERMISSIONS` badge while any session runs in this mode.
- Igris warns at start if the project is not a git repository or has uncommitted changes.

### 7.4 Model and mode enforcement
- `claude.extra_args` must not contain `--model`, `--fallback-model`, `--permission-mode`, `--dangerously-skip-permissions`, `--allow-dangerously-skip-permissions`, `--session-id`, `--resume`, `--continue`, `--append-system-prompt`, `--append-system-prompt-file` or `--settings`, nor the short forms `-c`/`-r` (alone or in a cluster such as `-rd`). Igris sets the first ones itself; `--settings` could set `permissions.defaultMode` and so change the mode of a default-mode task; `check` and `arise` reject a config that contains any of them. `--fallback-model` in particular would let a task silently run on a different model. Arguments may not contain control characters.
- The Claude session UUID that `--resume` gets on "continue" comes from `state.json`; it is only used if it has the form of a UUID (a session could rewrite the file so the value reads as another flag), otherwise only a fresh session is offered.
- `--model` on the command line takes precedence over a `model` in Claude Code's settings (verified: settings `haiku` + `--model sonnet` ran `claude-sonnet-5-5`), so the plan's rank always wins. This also holds on `--resume`: a resumed session's new turns run on the `--model` given at resume.
- Model aliases `fable`, `opus`, `sonnet`, `haiku` all resolve (2.1.291: `claude-fable-5-1`, `claude-opus-5-5`, `claude-sonnet-5-5`, `claude-haiku-4-5-20251001`). Igris passes the alias and never pins a full model name.
- If `ANTHROPIC_API_KEY` is set in the environment, Claude Code uses it instead of the subscription login (it warns that it "takes precedence over your claude.ai login"). Igris never sets it; `check`, `arise` and `adapt` warn when it is present in igris's environment (never showing the value), because tasks would then bill the API; `arise` also asks for confirmation.

---

## 8. User tasks

For a task with Owner `user`:
1. Igris shows the full task text in the TUI, marks it **Your turn**, and sends a `needs_input` notification.
2. No pane, no Claude session.
3. The owner completes it outside igris, then presses `d` (done) or `s` (skip, with a reason) in the TUI, or runs `igris done <ID>` / `igris skip <ID> --reason …` from any terminal.
4. Igris marks the status and continues. User tasks are never verified or committed; `state.json` records the task (without a session) so a restarted `arise` comes back to it.

---

## 9. `igris adapt` — AI-assisted plan conversion

For plans that fail `igris check` or use a different format.

1. `igris adapt [--model sonnet|opus] [--plan PATH]` (default model from `adapt.model`, default `sonnet`). Only `sonnet` and `opus` are offered.
2. Igris runs `igris check` and captures every validation error; a plan that passes has nothing to adapt (exit 0). `adapt` takes the run lock (§13), so it never runs alongside `arise`.
3. It opens a session (same backend, mode `default`) with the adapt prompt: the canonical format (§3, embedded), the validation errors, the config's `[models]` aliases, and the job — **write a converted copy to `.igris/adapt/<plan-name>.proposed.md`**, preserving every task, ID, description, dependency, status and model. Allowed: restructure headings and tables, rename columns, normalize statuses, convert prose dependencies into IDs; turn prose that waits on something outside the plan ("waits on", "after the client…", "blocked by deploy") into a `user` task naming the blocker plus a dependency on it (§3.4), mentioned in `## Adapt notes`. Not allowed: inventing or changing models, dropping tasks, rewriting descriptions. Tasks with missing or unknown models are listed in a `## Adapt notes` section at the end of the proposal and given Model `?` — which `check` rejects, so the owner must fill them in. The adapt session gets its own rules instead of the task rules (§6.1): write only the proposal file, never edit the original, never guess a model, ask when the original is ambiguous, finish with `igris done ADAPT`.
4. The session can ask the owner questions like any other session (`needs_input` and `session_lost` are notified as in §10). It finishes with `igris done ADAPT`, which `igris done` accepts without reading the plan (unless the plan has a task `ADAPT`). Interrupting `adapt` leaves the session open; a session that ends without `done` is an error and nothing changes.
5. Igris validates the proposal with the normal parser, then shows a **diff view** (original vs proposal) in the TUI with the validation result: a line diff (hand-rolled LCS, P0-01) with `-`/`+`/space markers on every row, unchanged stretches folded to 3 lines of context, the proposal's problems above it, and **Reject** / **Accept** buttons (keys `r`/`esc`/`q` and `a`; Reject has the focus first). Text from the plan or proposal is drawn with control characters replaced, so a proposal can't send escape sequences to the terminal.
6. Owner accepts → the original is backed up to `.igris/adapt/<plan-name>.<timestamp>.bak.md` (UTC, `20060102-150405`) and replaced atomically, keeping its file mode; if the plan changed on disk since it was read, nothing is replaced. Accepting a proposal that doesn't pass `check` (e.g. models left at `?`) needs a second confirmation, which starts on "Keep reviewing", and igris prints what is left to fix. Owner rejects (or quits the review) → nothing changes; the proposal stays in `.igris/adapt/`.
7. `igris check` remains the gate: `igris arise` refuses to run on a plan that doesn't validate.

---

## 10. Notifications

Events: `needs_input`, `session_lost`, `verify_failed_limit`, `task_done`, `phase_done`, `phase_stuck`, `run_error`.

Channels (all optional, any combination):

| Channel | Config | Delivery |
|---|---|---|
| Backend | `[notify.backend] enabled` | herdr toast via `herdr notification show` (sound `request` for needs-input events, `done` for completions). It has no `events` list and always gets the default events. |
| ntfy | `[notify.ntfy] server`, `topic`, optional `token` | HTTP POST, title + body, priority high for `needs_input`/`session_lost`. |
| Discord | `[notify.discord] webhook_url` | Webhook POST with a short message (`content`), no embeds needed. |

- Each channel has an `events` list; default: `needs_input`, `session_lost`, `phase_done`, `phase_stuck`, `run_error`, `verify_failed_limit`. `task_done` (opt-in) is sent when an `agent` or `agent + user` task is marked done.
- Messages contain project name, phase, task ID and title, and the event — never file contents, diffs or command output.
- Delivery is best-effort with a 10 s timeout and one retry; failures are logged and shown in the TUI, never fatal.
- Secrets (Discord webhook URL, ntfy token) may be given as `env:VAR_NAME` references so they stay out of the repo; igris never logs them.

---

## 11. Backends

### 11.1 Interface

```go
type Backend interface {
    Name() string
    Available(ctx context.Context) error                 // e.g. herdr server reachable, inside a herdr pane
    OpenSession(ctx context.Context, spec SessionSpec) (Session, error)
    Attach(ctx context.Context, ref SessionRef) (Session, error) // reattach after igris restart
    Notify(ctx context.Context, n Notification) error
}

type Session interface {
    Ref() SessionRef                                     // serializable (pane id, agent name, ...)
    Prompt(ctx context.Context, text string) error       // submit text to the agent
    State(ctx context.Context) (AgentState, error)       // working | idle | blocked | done | unknown | exited
    Focus(ctx context.Context) error                     // bring the session pane to front
    Close(ctx context.Context) error
}
```

`SessionSpec` holds: task ID (backends derive session names from it, e.g. herdr's `igris-<id>`), working directory, label, Claude Code argv (model, mode flags, extra args), environment additions.

`State` reports a vanished pane or exited Claude Code as `exited` rather than an error; `Prompt`, `Focus` and `Attach` fail with a "session gone" error for it, and `Close` on a gone session is a no-op. The engine treats both as **Session lost** (§6.3).

v1 ships `herdr` and `fake` (in-process, used by tests and `--dry-run`). `tmux` is planned and must fit this interface without changes to the engine.

### 11.2 herdr backend (v1)
Igris itself runs in a herdr pane. It uses the herdr CLI (JSON output), never the raw socket in v1:

| Step | herdr call |
|---|---|
| Availability | `herdr status server`; require `HERDR_WORKSPACE_ID` (igris must run inside a herdr pane) |
| Open pane | `herdr tab create --workspace $HERDR_WORKSPACE_ID --cwd <root> --label "<ID> · <rank>" --no-focus` → `.result.tab.tab_id`, `.result.root_pane.pane_id` |
| Start Claude | `herdr agent start <name> --kind claude --pane <pane_id> --timeout 120000 -- <claude args>`; `<name>` = `igris-<id>` lowercased, sanitized to `[a-z][a-z0-9_-]{0,31}`. Timeout must be > 3000 and ≤ 300000 ms. Returns `.result.agent` (`agent_status`, `interactive_ready`, `name`, `pane_id`) and `argv` |
| Shell not ready yet | `agent_pane_busy` ("is not an available shell"): the new tab's shell hasn't reached its prompt (slow rc file, fresh herdr server) → retry `agent start` every 250 ms for up to 15 s, then fail the start (and close the tab) |
| Startup blocked (e.g. folder-trust prompt) | `agent_not_ready` (non-zero exit, message "blocked during startup"; the name stays usable) → mark **Needs you**, hold the task prompt, poll state until the agent is past the prompt, then deliver it once the agent has stayed idle for 2 s (Claude Code shows its input box a moment before it takes input; a prompt sent in that moment is lost although herdr accepts it). The held prompt is also recorded in `state.json`, so a reattach after an igris restart still delivers it (§13) |
| Wait for state | `herdr agent wait <name> [--until STATUS]… [--timeout MS]`. Without `--until` it returns at the first settled state (`idle`, `done` **or `blocked`**), so check `.result.agent.agent_status` on return; it returns at once if already settled. `timeout` error code on expiry. Replaces tight polling for "is it ready for feedback"; `Needs you` still needs `pane get` polling |
| Send follow-up / task prompt | `herdr agent prompt <name> <text> [--wait --timeout MS]`. Multi-line text works (bracketed paste). Rejected with `agent_blocked` if the agent is at an approval/question UI. Without `--wait` the returned status is the pre-turn one |
| Read state | `herdr pane get <pane_id>` → `.result.pane.agent_status` ∈ `unknown` (plain shell / unclassified), `idle`, `working`, `blocked`, `done`; `pane_not_found` → session lost. `herdr agent read <name> --source recent-unwrapped --lines N` returns plain text (empty while a blocking prompt is drawn on the alternate screen — use `--source visible`) |
| Focus | `herdr tab focus <tab_id>` |
| Close | `herdr tab close <tab_id>` → `{"result":{"type":"ok"}}`; closing again gives `tab_not_found` |
| Notify | `herdr notification show <title> --body <body> --sound request|done|none` → `.result.shown` |

- Exact JSON shapes and flags were verified against herdr 0.9.1 (P0-03; the verified versions are in §11.4); recorded responses live in `internal/backend/herdr/testdata/` and drive the fixture tests.
- **Arguments after `--` must not contain control characters** (newline, tab, CR): herdr rejects them with `invalid_agent_argument` ("cannot be encoded safely for the target shell"). So the multi-line task prompt can **not** be a positional argument of `agent start`. Igris starts Claude with its flags only (no prompt), waits for `idle`, then submits the task prompt with `agent prompt` (§6.1).
- Error shape: JSON on stderr `{"error":{"code","message"},"id"}` and a non-zero exit (1 for errors, 2 for syntax errors; `agent_not_ready` exited 2 in P0-03 and 1 in the v0.1.2 re-verification). Igris acts on the error code, never on the exit status. Codes seen: `agent_not_ready`, `agent_pane_busy`, `agent_blocked`, `agent_not_found`, `pane_not_found`, `tab_not_found`, `timeout`, `invalid_agent_argument`, `invalid_agent_timeout`.
- `igris init` recommends `herdr integration install claude` for accurate agent state, and `igris check` warns if it's missing (where detectable).
- All herdr calls have timeouts (10 s default; agent start uses its own). Command failures surface the herdr error code in the TUI.

### 11.3 Running without herdr (v1 behavior)
If herdr isn't available, `igris arise` exits with a clear message explaining that v1 requires herdr and that tmux support is planned. `check`, `status`, `phases`, `done`, `skip` and `adapt --check`-style validation work without any backend.

### 11.4 Verified versions
Claude Code and herdr change often, and igris relies on their flags and output shapes. The versions igris was verified with live in one table in code (`checks.Tools`) and here:

| Tool | Oldest verified (`Min`) | Newest re-verified (`Tested`) | Verified in |
|---|---|---|---|
| Claude Code (`claude`) | 2.1.291 | 2.1.292 | P0-02; `docs/reverify.md` 2026-10-07 |
| herdr (`herdr`) | 0.9.1 | 0.9.1 | P0-03; `docs/reverify.md` 2026-10-07 |

- `igris check` and `igris arise` (including `--dry-run`) run `claude --version` and `herdr --version` through the command runner, 5 s timeout each, and print a `warning:` when a tool is not in `PATH`, its version can't be determined (no version in the output, a non-zero exit, a timeout), it is older than `Min`, or its major version is newer than `Tested`'s. Newer minor and patch versions don't warn.
- The version is the first `N.N` or `N.N.N` in the output; anything around it is ignored.
- These are warnings only: they never fail `check`, never ask for confirmation in `arise`, and igris never refuses to run because of a version.
- `docs/reverify.md` re-runs the P0-02/P0-03 checks. After a clean run against a newer version, `Tested` (code and this table) is raised to it; `Min` changes only when igris starts to depend on something newer.

---

## 12. Configuration

`igris.toml` in the project root (created by `igris init`). All keys optional.

```toml
plan = "tasks.md"                 # path to the plan, relative to the project root
backend = "herdr"
default_mode = "default"          # default | accept | auto | plan | yolo
needs_input_after = "30s"
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
command = "claude"                # deprecated and ignored: herdr always starts claude from PATH; any other value warns; removed in v0.2
extra_args = []                   # appended to every session launch; model/mode/session flags are rejected (§7.4)

[run]
verify = ""                       # e.g. "make fmt lint test"
verify_timeout = "15m"
verify_max_attempts = 3
commit = "ask"                    # ask | auto | never
commit_message = "{{.ID}}: {{.Title}}"
prompt_template = ""              # path to a custom task prompt template

[adapt]
model = "sonnet"                  # sonnet | opus

[tui]
mouse = true                      # click/tap and wheel in the TUI (§15.5); false keeps the terminal's own text selection
theme = "auto"                    # auto | dark | light: which colors to use (§15.4); auto goes by the terminal's background

[tui.rank_colors]                 # optional: rank -> color, "#rrggbb" or an ANSI color number "0"–"255" (§15.4)
# opus = "#B48CFF"
# fable = "220"

[notify.backend]
enabled = true

[notify.ntfy]
server = "https://ntfy.sh"
topic = ""
token = ""                        # or "env:NTFY_TOKEN"
events = ["needs_input", "session_lost", "phase_done", "phase_stuck", "run_error", "verify_failed_limit"]

[notify.discord]
webhook_url = ""                  # or "env:IGRIS_DISCORD_WEBHOOK"
events = ["needs_input", "session_lost", "phase_done", "phase_stuck", "run_error", "verify_failed_limit"]
```

- Unknown keys are errors (catches typos). Durations use Go syntax.
- Durations must be greater than zero. `env:VAR` references are accepted for `notify.ntfy.token` and `notify.discord.webhook_url`; an unset or empty variable is an error. Several problems are reported together, each saying what to fix.
- The config hash recorded in the run snapshot (§13) is a SHA-256 of the parsed config as written (defaults applied, `env:` references unresolved), so secret values never enter it.
- `igris.toml` holds no secrets by default and is safe to commit; `.igris/` is local state and is added to `.gitignore` by `igris init`.

---

## 13. State, resume and locking

`.igris/` in the project root:

| Path | Content |
|---|---|
| `igris.lock` | PID + host + start time, created complete (written to a temp file and hard-linked into place) so a concurrent `--force-unlock` never mistakes a fresh lock for a damaged one. A second `igris arise` refuses to start while the PID is alive; a stale lock is reported and can be cleared with `--force-unlock`. If the PID was reused by another program (after a reboot, say), the error says to delete the file. |
| `state.json` | Current run: phases, current task ID, session ref (backend name, pane/tab IDs, agent name), Claude session UUID, mode, attempt counters, started-at, hash of the config snapshot, and the task prompt while it is held at a startup prompt. Written atomically on every change. Values that become command arguments (session ref IDs, the session UUID) are checked for their expected shape when read back; a file that fails is reported as damaged. |
| `signals/` | Pending signal files (§6.2). |
| `runs.jsonl` | Append-only log: one JSON line per event (task started/done/skipped, verify result, notifications, errors) with timestamps, task ID, rank and model. |
| `adapt/` | Adapt proposals and backups (§9). |

**`runs.jsonl` record shape** — documented, unversioned until v0.5 (it may change before then; readers ignore unknown types and fields). One JSON object per line:

| Field | Content |
|---|---|
| `at` | RFC 3339 UTC timestamp |
| `type` | `run_started`, `run_stopped`, `task_started`, `task_resumed`, `task_done`, `task_skipped`, `verify_passed`, `verify_failed`, `committed`, `notification` or `error` |
| `task` | task ID; omitted for events outside a task (`run_started`, `run_stopped`, most `error`s) |
| `rank`, `model` | the task's rank and resolved model; omitted with `task` |
| `detail` | free text, never secrets: the phase scope (`phase A, B`) for `run_started`, the outcome (`completed`, `stuck`, `stopped`, `error`) for `run_stopped`, the note or reason for `task_done`/`task_skipped`, `attempt N of M: <why>` for `verify_failed`, the commit subject for `committed` |

A run is the events from a `run_started` to its `run_stopped`, or to the next `run_started` when there is none (the run was interrupted). `igris history` is the reader of this format.

**Resume.** `igris arise` (any phase argument, or none to resume the last run) reads `state.json`:
- current task still `in progress` and its session reattachable → reattach and keep watching; if its task prompt was still held at a startup prompt (`pending_prompt` in `state.json`), it is handed back and delivered once Claude Code is ready;
- session gone → offer (TUI, or `--no-tui` stdin; default fresh): **continue** the previous conversation (`claude --resume <uuid>`, same model) or start a **fresh** session with `Resumed=true`;
- pending signal for the current task → process it first.

Details:
- With no phase argument the previous run's phase range (and `--through`) is used again; with no previous run, `arise` opens the home screen's start-run wizard on a terminal (§14, §15.6) and otherwise exits 1 asking for a phase. A resumed run starts in the phase of the interrupted task.
- The interrupted task is picked up before anything else, also when a different phase is named.
- If the plan no longer says `in progress` for it (the owner settled it while igris was down), igris warns, forgets it and goes on.
- A task the plan says is `in progress` without any record in `state.json` is treated like a lost session with nothing to continue: the owner can start a fresh session (`Resumed=true`), mark it done, skip it or stop.
- The new run uses the config as it is now; igris warns if it differs from the interrupted run's.

**Config snapshot.** `arise` loads `igris.toml` once at start and uses that snapshot for the whole run. If the file changes during a run (a session could edit it, e.g. to weaken `verify`), igris marks **Needs you**, notifies (once per change), and keeps using the snapshot; the new config only takes effect when the owner restarts `arise`.

Quitting the TUI (`q`) never kills a running session; it saves state and exits. Stopping a session requires an explicit action.

**Reading state without a run.** `doctor`, `status`, `history` and the home screen (§15.6) read `.igris/` without taking the lock and never create it: `state.PeekRun` for `state.json`, `state.PeekEvents` for `runs.jsonl` (a truncated or malformed last line is skipped), and `state.PeekLock` for `igris.lock`. `PeekLock` classifies the lock the same way `arise` does when it takes it (one shared classification): none, held by a live igris on this host, stale (its process is gone, or the file is unreadable) or remote (another host; igris can't tell if it is alive). While a live igris on this host holds the lock, the home screen is **read-only**: it shows that run (state, current task, the last `runs.jsonl` events) and offers to open its session, but offers no Arise, Init or Adapt, and editing the plan asks first, because an edit holds that run (§5.4). A stale or remote lock is only shown; clearing it is offered inside the start-run wizard (§15.6), never on its own.

---

## 14. CLI

```
igris                                       on a terminal: the home screen (§15.6); otherwise help, exit 2
igris init [--example]                      create igris.toml, .igris/, .gitignore entry, Claude allow rules;
                                            --example also writes the example plan when there is none
igris doctor [--json]                       read-only health check of this project and machine
igris check [--plan PATH] [--json]          validate the plan; exit 0 valid, 1 invalid, 2 usage error
igris phases [--plan PATH] [--json]         list phases with task counts per status
igris status [PHASE] [--plan PATH] [--json] tasks with status/rank/owner, current run, unmet deps
igris history [TASK-ID] [-n N] [--json]     past runs from .igris/runs.jsonl; with a task ID, its attempts
igris arise [PHASE] [--through PHASE]       run (or resume) with the TUI
           [--mode default|accept|auto|plan|yolo] [--no-tui] [--dry-run]
           [--force-unlock]
igris done ID [--note TEXT]                 signal that a task is finished
igris skip ID --reason TEXT                 signal that a task is skipped
igris notify test [--event NAME]            send a sample of each notification to the configured channels
igris adapt [--model sonnet|opus]           AI-assisted conversion with diff review
           [--plan PATH]
igris completion bash|zsh|fish              print a shell completion script
igris version
```

- **Bare `igris`** opens the home screen (§15.6) only when both stdin and stdout are terminals (character devices, `ModeCharDevice`), stdin is not `/dev/null` (which is also a character device), and `TERM` is not `dumb`. Otherwise — piped, scripted, under cron — it prints the help to stderr and exits 2, as before. Home exits 0, or 1 if the TUI fails. It works in the project root found as for `arise` (below); outside a project it shows the get-started steps. Every subcommand is unchanged by home.
- **`igris arise` without a phase and with nothing to resume** (no `state.json`, or one without phases) opens the app in the start-run wizard (§15.6) when it runs on a terminal (as for bare `igris`) without `--no-tui` or `--dry-run`; `--mode`, `--through` and `--force-unlock` are prefilled, and `esc` leaves the wizard for home instead of exiting. In every other case it exits 1 and asks for a phase, as before. `igris arise PHASE` goes straight to the run view, as before.
- `arise`, `adapt` and `notify test` work in the project root: the nearest directory, from the working directory up, that holds `igris.toml` or `.igris/`. Without one, the working directory is the root only if it holds the plan (`adapt --plan`, else `tasks.md`); otherwise they exit 1 with a hint to run `igris init` and create nothing.
- The plan file is `--plan`, else `plan` in `igris.toml` in the working directory, else `tasks.md`. `check`, `phases` and `status` stay working-directory-based (they don't search upward like `arise` and home do). `check` and `phases` read the plan only; `status` reads the plan plus the `.igris/` run state, read-only (§13), and never creates `.igris/`; `phases` and `status` refuse an invalid plan (exit 1, listing the problems) and report to stdout, errors to stderr. `--json` prints one JSON document instead of text.
- `check` prints each validation problem as `file:line: message` and each readiness drift (§5.2) and ignored dependency-like column (§3.2) as `warning: file:line: …`, and each Claude Code or herdr version problem (§11.4), a set `ANTHROPIC_API_KEY` (§7.4) and an `igris.toml` in a parent directory (which `check`, `phases` and `status` don't read: they use the working directory's) as `warning: …` (in `--json`, a warning without `file` and `line`), and each deprecated config setting as `warning: igris.toml: …` (§12); warnings never fail the check. Without any `igris.toml`, `check` prints `note: no igris.toml here; using the defaults` (text only, not a warning). `phases` and `status` print the parent-directory hint as a `note:` on stderr. `status` shows, per phase, how many tasks are finished, the §5.1 outcome (`next`, `complete`, `stuck`) and, for each task, the dependencies it waits on. The current run (needs `.igris/` state, §13) is added to `status` once runs exist: phase range, current task, mode, since when, session reference and lock state (running here, running elsewhere, stale, none), plus pending signals. In text it is a short `Run` block above the phase table; in `--json` it is a `run` object, omitted when there is none. A state file that can't be read or doesn't have the expected shape is reported as "state unreadable", never a crash.
- `--no-tui` prints plain timestamped log lines and reads owner commands from stdin, one per line, for scripting or very small terminals:
  - `y` / `n` answer the question igris asked (commit? confirm a session's skip request?);
  - `done [note]` marks the current task done (an agent task is not verified, §6.4), `skip <reason>` skips it;
  - `retry [continue|fresh]` replaces the current session: continue its conversation or start fresh (`Resumed=true`); bare `retry` is `fresh`. This is also the answer when a session is lost;
  - `pause` toggles pause-after-task like `p` in the TUI (§15.3), `stop` stops igris and leaves the session open;
  - `mode <m>` sets the run mode for the next sessions, `mode <task> <m>` overrides one task's mode for its next session (§7.2); `yolo` then asks to type `skip permissions` (§7.3);
  - `help` lists them. Ctrl-C is `stop`.
- `init --example` also writes the canonical example plan (the one in `examples/tasks.md`) as the configured plan path when no plan exists; it never overwrites (`kept existing tasks.md`). Without `--example`, `init` behaves as before.
- `doctor` is **read-only, now and later**: it never writes, creates or fixes anything (no `--fix`), and for every problem it prints the exact command that would fix it. It works outside a project (it reports "no igris.toml" and checks the machine). Checks, in this order:
  1. `claude` found, with its version (§11.4);
  2. `ANTHROPIC_API_KEY` set in the environment (§7.4);
  3. herdr reachable, whether igris runs inside a herdr pane, and whether the Claude Code integration is installed (§11); not running inside a herdr pane is a `warn` here, not a `fail`, because `doctor` is usually run from a plain shell;
  4. the project is a git repository, and whether its tree is dirty (§7.3);
  5. `igris.toml` is valid — every problem listed, not only the first (§12);
  6. the plan is valid (every problem) and has no readiness drift (§5.2);
  7. `.claude/settings.local.json` holds the allow rules for `igris done` (`Bash(igris done:*)` / `Bash(igris done *)`, as `init` writes them), and no rule allow-lists `igris skip`: `warn` if one does, because that bypasses the skip confirmation (§6.2);
  8. `.igris/` has mode 0700 and its files 0600 (§13, §16);
  9. no stale or foreign lock (§13);
  10. the project is not under `/mnt/` (WSL on a Windows filesystem; points to `docs/check-wsl.md`);
  11. notification channels are configured (§10) — **never sent to**; sending is `notify test`.

  Each check prints one line: a glyph, a level and a message; a problem is followed by its next command. Levels are `ok`, `warn` and `fail`. The exit code is 0 unless some check is `fail` (then 1); `--json` prints the results as a JSON array (`id`, `level`, `message`, `next`). Text from the plan, config, lock or command output is cleaned before it is printed (§16).
- `history` reads `.igris/runs.jsonl` (§13) read-only and never creates `.igris/`. It lists the last N runs (default 10, newest first), each with its phases, tasks done and skipped, per-task duration, verify attempts, commits and how it ended; a run without a stop event is shown as interrupted, and a truncated last line is ignored. With a task ID it lists every attempt of that task across runs.
- `completion` prints a hand-written script per shell (no CLI framework, P0-01). It completes subcommands, each subcommand's flags, and phase and task IDs. IDs come from a hidden `igris __complete <kind>`, which is not listed in help: it reads the plan only (never `.igris/`, never the network) and prints one candidate per line; on a missing or invalid plan it prints nothing and exits 0.
- `notify test` sends one sample message per event to every channel set up for it (the herdr toast is included when herdr is reachable) and prints `ok` or `FAILED: <reason>` per event and channel; secrets never appear in the output. It exits 1 if a delivery failed or no channel is set up. `--event` limits it to one event.
- `--force-unlock` clears a stale `.igris/igris.lock` (its process is gone, the file is unreadable, or it comes from another host); a lock held by a live process on this host is always refused (§13). The CLI never asks about a stale lock: `arise` exits 1 saying to rerun with `--force-unlock`. The start-run wizard's **Clear the lock and start** dialog (**Cancel** is the default; asked per launch, never remembered) is the TUI equivalent of the flag, not a new prompt.
- `--dry-run` uses the fake backend: walks the phase, prints which task would launch with which model and mode, writes nothing. It runs the real engine on a temporary copy of the plan, with every session finishing at once, user tasks done, verify and commits off. Drift and skip-permissions tasks are shown as warnings instead of asked about; a task already in progress is shown as resumed with a fresh session. Without a phase it walks the last run's phases. It never touches `.igris/`.
- On start, `arise` warns if `ANTHROPIC_API_KEY` is set in the environment (Claude Code would bill the API instead of the subscription) and asks for confirmation. It also warns, without asking, if the project is not a git repository or has uncommitted changes (§7.3), and about the Claude Code and herdr versions (§11.4).
- Before its first write `arise` asks to confirm readiness drift (§5.2), and asks for the typed `skip permissions` confirmation when a task would run in `yolo` mode (§7.3). Declining any of these exits 1 with nothing started. With `--no-tui` the answers come from stdin (`y` for the questions). With the TUI they are asked the same way, as plain prompts before the TUI takes over the terminal; in the start-run wizard (§15.6) they are dialogs with the same defaults and meaning. `--dry-run` prints the warnings and never asks.

---

## 15. TUI

Built with Bubble Tea / Lip Gloss. Runs in the igris pane; the Claude sessions live in their own herdr tabs.

**One program, a stack of screens.** Igris's TUI is one Bubble Tea program (one alt-screen session, one background-colour detection, stable mouse and focus modes) holding a stack of screens: home at the bottom (§15.6), with pages, the start-run wizard, the run view (§15.1–§15.5) and the adapt review pushed on top. `esc` closes an open dialog first, otherwise it pops the screen; `q` pops a pushed screen and quits on home; `ctrl+c` quits the whole app from anywhere (with a run going, it stops igris and leaves the session open, exactly like quitting `arise`). A resize reaches every screen on the stack. While the run view is on the stack it owns the only engine; there is no background run. `igris arise PHASE` opens the run view on its own, with no home beneath it, and behaves as before.

### 15.1 Layout (≥ 100 columns)
```
┌ igris · sinjal ─────────────── phase M0 · mode: plan · herdr ┐
│ TASKS                          │ CURRENT                      │
│ ✓ M0-01  Go module      sonnet │ M0-03 Makefile               │
│ ✓ M0-02  Entrypoint     sonnet │ rank sonnet → model sonnet   │
│ ● M0-03  Makefile       sonnet │ mode plan · 4m12s            │
│ · M0-04  Config loader  sonnet │ state: NEEDS YOU (idle 45s)  │
│ ⨯ M0-13  Static assets  sonnet │ [o] open session             │
│   waits on P0-05               │                              │
├─ LOG ──────────────────────────┴──────────────────────────────┤
│ 09:41 M0-02 done · "subcommand dispatch + tests"              │
│ 09:41 M0-03 started (sonnet, plan)                            │
├───────────────────────────────────────────────────────────────┤
│ [›Open session‹] [Mode] [Pause] [Retry] [Skip] [Stop] [?]     │
└───────────────────────────────────────────────────────────────┘
```

### 15.2 Narrow layout (< 100 columns, e.g. Termius on a phone)
Single column: header, current task card, compact task list (ID + status glyph + rank), last 3 log lines, action bar. Must stay usable at 50×20. The action bar wraps to a second row or folds its less common actions into a `More…` button; an open dialog (§15.5) takes the whole screen.

### 15.3 Actions
Every action is reachable three ways: **clicking** its button (or tapping it, e.g. in Termius), **moving the focus** to it and pressing `enter`, and its **shortcut key**. The action bar shows only the actions that apply right now (e.g. `Done` only while a task is running, `Open session` only while a session exists); it never shows buttons that do nothing.

| Button | Key | Action |
|---|---|---|
| Open session | `o` | Focus the current session's pane (herdr) |
| Mode | `m` | Change run mode for upcoming sessions (choice list; `yolo` needs the typed confirmation, §15.5) |
| Task mode | `M` | Override mode for the selected task (same choice list) |
| Pause / Resume | `p` | Pause after the current task (toggle): the task finishes normally, then igris launches nothing and waits — the run stays alive — until pause is toggled off. The button label shows the current state |
| Done | `d` | Mark the current task done (a user task, or an agent task as the owner's decision, §6.4) |
| Skip | `s` | Skip the current task: asks for a reason; for agent tasks also closes the session after confirmation |
| Retry | `r` | Retry: close the current session, then choose **Continue conversation** or **Start fresh** (`Resumed=true`) |
| Stop | `x` | Stop now: leave the session open, stop igris after confirmation |
| — | `↑/↓`, `enter`, click | Browse tasks / show task details (full text, deps, extra columns) |
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
- Buttons have four states: normal, focused, wanting attention (`Answer…` while a question is pending) and inactive (the bar under a dialog or page, dimmed). There is no hover state (§15.5).

### 15.5 Interaction
Modelled on Claude Code's choice prompts and herdr's clickable UI.

- **Focus.** `tab` / `shift+tab` move between regions: task list, action bar, log (and the open dialog, which keeps the focus until it closes). Inside a region: `←/→` along the action bar, `↑/↓` (and `j/k`) in lists, dialogs and the log, `pgup/pgdn` to scroll. `enter` or `space` activates the focused element. Shortcut keys (§15.3) work from anywhere outside a text field.
- **Mouse.** Click or tap activates a button, a task row (select; a second click or `enter` opens details) or a dialog option. The wheel scrolls the list or log under the pointer. Mouse reporting (button presses, wheel and drags; no hover) is on by default; `[tui] mouse = false` turns it off, because while a TUI captures the mouse the terminal's own text selection usually needs `shift`+drag.
- **Dialogs.** When the engine asks the owner something, a modal choice list opens at once and is focused on the safe default:

  | Question | Options (default first) |
  |---|---|
  | Commit (§6.5, `commit = "ask"`) | **Commit** · Leave uncommitted |
  | Session asks to skip (§6.2) | **Keep working** (discards the request) · Skip the task |
  | Session lost (§6.3) | **Start fresh** · Continue conversation · Mark done · Skip · Stop igris |
  | Stop (`x`) | **Keep running** · Stop igris |
  | Skip (`s`) | text field for the reason · **Cancel** · Skip |

  Options can be picked by click, by `↑/↓` + `enter`, or by their number (`1`…`9`). `esc` closes a dialog with its non-destructive choice (cancel / keep) where it has one; the session-lost and commit questions stay pending until answered, and the dialog reopens from the current-task card. Destructive choices (skip, stop) are never the default.
- **Skip permissions** (§7.3) is never a single click or key: picking `yolo` in a mode list opens a text field that only accepts the typed phrase `skip permissions`.
- `--no-tui` (§14) remains the plain stdin interface with the same actions as typed commands.

### 15.6 Home screen

Bare `igris` on a terminal (§14) opens home: the project at a glance and the way into everything else. Home itself never writes the plan or `igris.toml`; it starts runs, `init` and `adapt`, and opens files in the owner's editor.

**Layout.** A header (project, plan file, herdr `…` / `✓` / `⨯`, default mode, and the run state on the right), then **PHASES** (glyph, ID, title, progress bar, `done/total`, the §5.1 outcome word), **NOW** (the card below), **HEALTH** (the check result and the doctor summary) and **RECENT** (the last 2–3 runs from `runs.jsonl`), a status line with the last action's result, and the action bar. Wide (≥ 100×13) puts PHASES left and NOW/HEALTH/RECENT right; narrow is one column (NOW, PHASES, HEALTH, RECENT) and must stay usable at 50×20, with the bar folding into `More…`; below 60 columns the bars are dropped and the numbers stay; below 40×12 only `igris — terminal too small (need 50×20, now W×H) · q quits` is shown.

**NOW card**, one state at a time:

| State | Card | Default action |
|---|---|---|
| No `igris.toml` | **GET STARTED**: numbered steps `igris.toml`, plan, check, preview, arise, each turning `✓` as data refreshes; after init, the plan step offers **Example plan** (`init --example`), or **Check** / **Adapt** when a non-canonical plan exists | Init |
| Plan invalid | **PLAN INVALID**: file, problem count, the first few `file:line: message`, "… N more — Check shows all"; without herdr, why Adapt is unavailable | Check |
| Ready | **READY**: the phase with work, `next` and `then` tasks with rank, tasks left and how many wait on what | Arise… |
| Interrupted | **INTERRUPTED**: last run's range, the interrupted task, its session and whether Resume reattaches; **STOPPED** when `state.json` has phases but no current task | Resume… |
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
| Init | `I` | after a confirm dialog listing the files it touches (**Create files** · Cancel; never overwrites), each step's result as `init` prints it | — |
| Adapt | `A` | after a confirm dialog (**Cancel** · Adapt with sonnet · Adapt with opus; the `ANTHROPIC_API_KEY` warning when it applies), `adapt`'s progress, then the adapt review (§9) | Open session, Cancel; then Accept / Reject as in §9 |

**Keys on home.** The run view's letters (`o m M p d s r x q ?`) never mean something else on home; `y` is copy and `j`/`k` navigate everywhere. Actions appear only when they apply (§15.3):

| Key | Action | Shown when |
|---|---|---|
| `a` | **Arise…** / **Resume…** (the label follows the state) | config and plan valid, herdr reachable, no live lock held by another igris |
| `v` | Preview | plan valid |
| `c` | Check | a plan file exists |
| `i` | Doctor | always |
| `h` | History | `runs.jsonl` exists |
| `e` | Edit the file this screen is about (the plan on home and phase detail, `igris.toml` on Settings) | the file or its directory exists |
| `,` | Settings | always |
| `n` | Notify test | a channel is set up |
| `A` | Adapt | the plan exists, is invalid, and herdr is reachable |
| `I` | Init | there is no `igris.toml` |
| `o` | Open session | another igris runs here and `state.json` has a session ref |
| `?` `q` `esc` `ctrl+c` | help, back or quit, back, quit the app | always |

Focus regions in tab order: PHASES → action bar → HEALTH/RECENT (each line opens its page). The selected phase is the wizard's default. Mouse as in §15.5: a click selects a phase and a second click opens it; a click on a HEALTH or RECENT line opens its page.

**Start-run wizard** (`a`, or `igris arise` with nothing to resume, §14). A sequence of dialogs over home (full-screen when narrow), each with the CLI's default and meaning:
1. **What to run** — only when something can be resumed: **Resume last run (T first)** · Start a phase…. Resume continues the previous range with the interrupted task first (§13); the session-lost question then comes in the run view.
2. **Phase** — phases with work first, each with `n/m` and its outcome word; complete phases last, labelled `complete`. Default: the selected phase if it has work, else the first with work.
3. **Through** — **Only P** · P through Q … (later phases only).
4. **Mode** — **As planned** (Mode column, then `default_mode`) · default · accept · auto · plan · yolo `[SKIP PERMISSIONS]`. yolo opens the typed-phrase field (§7.3, §15.5).
5. **Summary** — `Arise P through Q · mode M · N tasks · first T rank`, with the warnings `arise` prints (plan hints, the integration hint, not a git repository, uncommitted changes, the interrupted task first): **Arise** · Preview · Cancel.
6. **Confirmations** — every start warning that `arise` asks about becomes a dialog, Cancel first (e.g. `ANTHROPIC_API_KEY`: **Cancel** · Start anyway).
7. **Launch** — "starting… checking herdr, taking the lock" with Cancel. An error before the run starts becomes the matching dialog and the launch is retried: readiness drift → the list of changes, **Cancel** · Let igris fix them; a task needing yolo → the typed phrase (unless typed in step 4 of this launch); a stale or remote lock → **Cancel** · Clear the lock and start (the `--force-unlock` equivalent, §14). A live local lock, an unavailable backend or any other error → an error dialog with the next step and a link to Doctor; no retry. A failed launch leaves no engine behind.
8. **Started** — the run view is pushed (§15.3, Quit labelled **Home**).

Every answer belongs to that one launch and is thrown away afterwards; nothing — in particular no skip-permissions confirmation — carries over to the next run.

**Edit.** `e` suspends the TUI and runs `$VISUAL`, else `$EDITOR`, on the file's absolute path (§16). With neither set, a dialog shows the path: **Close** · Open with vi (only if `vi` is on `PATH`) · Copy path (`y`); igris never falls back to `vi` silently. On return igris reloads and re-validates (`✓ igris.toml valid` / `⨯ igris.toml: N problems — Settings`); a non-zero exit reads `editor exited with status N; file reloaded`; theme, mouse and rank colours apply at once. Under a live local lock, editing the plan asks first: **Cancel** · Edit anyway.

**Refresh.** Every 2 s home compares size and modification time of `igris.toml`, the plan, `.igris/state.json`, `.igris/igris.lock` and `.igris/runs.jsonl`, and reloads only what changed (the plan is parsed and validated only when its file changed). It also refreshes on returning from the editor, when a run, review, init or adapt ends, and when the terminal regains focus. Slow checks (herdr availability, doctor's process calls) run asynchronously when home opens and on demand, never on the poll. Lists draw only their visible rows.

**`NO_COLOR` and mouse off** change only the look and the input method (§15.4, §15.5): states are glyph plus word everywhere, bars are `█░` with the numbers beside them.

---

## 16. Errors and safety

**Trust model.** The owner and `igris.toml` are trusted. A session is not: it can edit any file in the project without a prompt in `accept`, `auto` and `yolo` mode — the plan, `igris.toml`, `.igris/` — and it controls its done note and the output of `verify`. Igris is designed so that none of this lets a session change which model or permission mode a task runs with, make igris skip verification, or drive the owner's terminal. What a session can do by design: edit files, and therefore influence what `verify` and git hooks execute as the owner once they run (verify runs project code; this is inherent to verifying) — so the mode a task runs in is the real boundary.

- The plan is never written while it fails validation.
- Sessions can't change igris's behavior mid-run: config is snapshotted (§13), `igris skip` from a session needs owner confirmation (§6.2), model/mode flags can't be smuggled in via `extra_args` (§7.4), values read back from `state.json` are checked before they become arguments (§7.4, §13), and plan edits igris didn't make hold the run until the owner looks (§5.4).
- Every external command (herdr, claude, git, verify) has a timeout; failures are shown with the command and exit code. Captured output is bounded (§6.4). The one exception is the owner's editor opened from home (§15.6): it is interactive and owner-configured, so it has no timeout. `$VISUAL` / `$EDITOR` is split into argv with `strings.Fields` (arguments like `code --wait` work; quoted arguments don't), the file's absolute path is the last argument, no shell is involved, and with neither variable set igris asks instead of falling back to `vi`.
- Igris never runs commands from the plan's content, and never passes plan text through a shell — prompts go to herdr as argv, not interpolated into shell strings.
- Text igris did not write — plan cells, done notes, command output, backend errors, and on home also run-log details, the lock's host, notify errors and config string values — is cleaned of escape sequences and control characters before it is drawn in the TUI or the `--no-tui` log, sent as a notification, used in a commit message, or typed into a session's pane (§3.5, §6.2, §6.4, §9.5).
- Signal and state files are written atomically, with `0600` files and `0700` directories; signals are read only as regular files of bounded size (§6.2).
- Every user-facing error says what to do next (the command to run, the setting to change, the file to fix or delete).
- Igris never reads or logs Claude Code credentials, and never sets `ANTHROPIC_API_KEY`. Notification secrets never enter the config hash, the run log or error messages (§10), and never appear on home's Settings, Doctor or Notify test pages (§15.6), which say only whether a secret is set and where from. An ntfy `token` is sent as a bearer header, so use an `https://` server for it.

---

## 17. Testing requirements

- **Parser:** table splitting (escaped pipes, padding, backticks), phase detection, column aliases, deps incl. ranges and cross-phase, every validation error. Golden tests with realistic plans: at least one large synthetic fixture (10+ phases, 150+ tasks, ranges, escaped pipes, cross-phase deps, user tasks). Never commit private plans as fixtures.
- **Writer:** byte-for-byte preservation except the target Status cell; CRLF files; a leading UTF-8 BOM; files without trailing newline; concurrent-edit detection.
- **Scheduler:** selection order, resume, stuck detection, readiness sync, `--through`.
- **Engine:** full phase runs on the fake backend covering done, verify failure + retry, verify limit, session lost, user tasks, skip, pause, resume after restart, stray signals.
- **herdr backend:** command construction + JSON parsing against recorded fixtures; a manual smoke checklist on a real herdr install.
- **TUI:** model update tests (teatest) for key handling and both layouts. Home, its pages and the wizard (§15.6) have teatest goldens at 120×40, 80×24 and 50×20, run against a fake of the services seam; a golden test injects escape sequences into every untrusted text source and checks they are cleaned; the existing run-view goldens stay unchanged. A benchmark on the large fixture keeps home's `View` under 2 ms at 120×40.
- **Notifications:** httptest servers for ntfy and Discord; secret redaction.
- **Fuzzing** (stdlib `testing.F`): the table tokenizer and parser (no panic, deterministic, validation never crashes), the Status writer (only the target Status cell changes, for any input), the config loader and the signal reader (arbitrary bytes give an error, never a panic). `make test` replays the seeds; `make fuzz` runs each target (10 minutes each before a release); a failing input is committed under `testdata/fuzz/` as a regression seed.
- `go test -race ./...` clean.

---

## 18. Distribution

- Single static binary, `CGO_ENABLED=0`, for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.
- Built with **GoReleaser** (P0-04): `tar.gz` per target (binary + `LICENSE` + `README.md`) and a single `checksums.txt` (SHA-256), version injected with `-ldflags`. `make release-local` runs `goreleaser release --snapshot --clean` into `dist/`; nothing is published automatically — the owner uploads to a GitHub Release by hand (task M7-07).
- Artifacts are **not signed** in v0.1.0 (checksums only); cosign/minisign signing is a post-v1 item.
- `go install github.com/drilonrecica/igris/cmd/igris@latest` works; such builds have no ldflags, so `igris version` falls back to the module version from the binary's build info (`v0.1.0` → `0.1.0`).
- CI (P0-07): GitHub Actions on push to `master` and on PRs — gofmt check, `go vet`, golangci-lint, `go test -race ./...` on Linux and macOS. Read-only token, no secrets, never builds or publishes releases.
- Project page (P0-09): `https://drilonrecica.github.io/igris/`, a static page built from `site/` and deployed by `.github/workflows/pages.yml`. That is the only workflow with write permissions (`pages`, `id-token`, for the deploy job only); it has no secrets and never builds or publishes releases.
- Homebrew tap (V02-P1): `brew install drilonrecica/tap/igris`, from the repository `drilonrecica/homebrew-tap`. GoReleaser's `brews` section writes the formula into `dist/` during `make release-local` (`skip_upload: true`); the owner copies it into the tap and pushes it by hand. Nothing is published automatically, as for the archives.
- MIT license.
