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
- `##` sections without a task table (Legend, Working rules, traceability tables) are ignored.
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
- Column names can be aliased in config (`[columns]`, §12), e.g. `Depends on` → `Deps`.
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
- Validation errors: unknown ID, self-dependency, dependency cycle, malformed range.

### 3.5 Owners and models
- `user` tasks must have Model `—`. `agent` and `agent + user` tasks must have a Model that resolves through `[models]` config. Violations are validation errors (igris never guesses a model).
- An unknown Owner or Mode value is a validation error.

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

1. **Only Status cells change.** Every other byte of the file — whitespace, other cells, other sections, line endings, trailing newline — is preserved exactly.
2. Within a Status cell, the original padding and backtick style are preserved (`` `ready` `` → `` `done` ``). Any text after the old keyword is dropped (`blocked (waits on vendor)` → `ready`), since it described the old status.
3. **Re-read before write.** The file is re-read and re-parsed immediately before every write, because the owner or a session may have edited it. If the target row no longer exists, igris stops with an error rather than writing.
4. **No lost updates.** Igris hashes the bytes it re-read and, immediately before the rename, hashes the file again. If it changed in between, the write is discarded and retried from step 3 (up to 3 times, then stop with an error).
5. **Atomic write:** write to a temp file in the same directory, fsync, rename.
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

---

## 6. Session lifecycle (agent tasks)

For each selected agent task:

1. **Mark** the task `in progress` (§4).
2. **Resolve** rank → model (`[models]` config) and run mode (§7.2). Unknown rank → stop with error.
3. **Open a pane** via the backend in the project directory, labelled `<ID> · <rank>`.
4. **Start Claude Code** in that pane: `claude --model <model> --session-id <uuid>` + run-mode flags + `--append-system-prompt-file <igris rules file>` + `claude.extra_args` from config. Neither prompt travels as argv: herdr rejects control characters (newlines) in agent arguments (§11.2). Igris writes the rules to `.igris/prompts/<ID>.rules.md` first.
5. **Submit the task prompt (§6.1)** with the backend's prompt call once the session is `idle` (the backend's start call only returns when Claude Code is ready for input, so this cannot race with startup).
6. **Record** the Claude session UUID (generated by igris) in `state.json` (§13).
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
- `igris done <ID> [--note TEXT]` and `igris skip <ID> --reason TEXT` locate the project root (walk up from cwd to the nearest `igris.toml` or `.igris/`), validate that `<ID>` exists, and write a signal file `.igris/signals/<ID>.json` (`{"id","action":"done|skip","note","at"}`) atomically. They print a one-line confirmation and exit 0.
- They do **not** edit the plan themselves; only the running igris consumes signals. This keeps a single writer.
- A signal for a task that isn't the current one is kept and reported in the TUI; it is never applied silently.
- **Skip needs the owner.** A `skip` signal for an **agent** task is treated as a request: igris marks the task **Needs you**, notifies, and applies the skip only after the owner confirms in the TUI (or via `--no-tui` stdin). Pressing `s` in the TUI applies directly. For **user** tasks a skip signal applies directly, since only the owner acts on them.
- `igris init` adds the allow rule `Bash(igris done:*)` to `.claude/settings.local.json` (merging, never overwriting) so the done command doesn't trigger a permission prompt. `igris skip` is deliberately **not** allow-listed: a session that wants to skip has to go through a permission prompt and the confirmation above.

### 6.3 Watching a session
Igris polls the backend every 2 s (configurable) for the pane's agent state and checks for signals.

| Observation | Action |
|---|---|
| Signal present | Proceed to verification. |
| Agent `working` | Clear any "needs you" flag. |
| Agent `blocked`, `idle` or `done` without a signal for ≥ `needs_input_after` (default 30 s) | Mark the task **Needs you** in the TUI; send a `needs_input` notification once per idle episode. Possible causes: a question, a permission prompt, a plan awaiting approval, a usage limit, or a stall — igris doesn't try to tell them apart. |
| Pane gone or Claude Code exited without a signal | Mark **Session lost**, notify, and offer: continue the conversation (`claude --resume <uuid>`), retry fresh (`Resumed=true`), mark done, skip, or stop. |

Igris never advances on agent state alone — only on a signal or an explicit owner action.

### 6.4 Verification
- Optional `verify` command in config (e.g. `make fmt lint test`), run with `sh -c` in the project root, with a timeout (default 15 min).
- The verify command (like every other config value) comes from the config snapshot taken at `arise` start (§13), never from a re-read of `igris.toml` mid-run.
- **Pass** → continue. **Fail** → delete the signal, wait until the agent is idle (it ran `igris done` as its last action, so it may still be finishing its turn; up to 30 s, then send anyway), and send the last 60 lines of output into the same session: "igris verification `<cmd>` failed: … Fix the problem, then run `igris done <ID>` again." The task stays in progress.
- After `verify_max_attempts` (default 3) consecutive failures, igris stops sending failures back, marks **Needs you**, and notifies.
- No verify command → the signal is accepted as is.

### 6.5 Commits
- `commit = "ask"` (default) — the TUI (or `--no-tui` stdin) asks y/n after each verified task.
- `commit = "never"` — igris never touches git. Uncommitted changes then carry over into the next task's session; the task prompt says so, and `Resumed` sessions can't tell whose changes they're looking at. Use `never` only if you commit by hand between tasks.
- `commit = "auto"` — after verification passes: `git add -A && git commit -m "<template>"`. Default message template: `{{.ID}}: {{.Title}}` plus the done note as body. If there's nothing to commit, continue silently. A commit failure stops the run and notifies.

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

Exact flag spellings are verified against the installed Claude Code (P0-02, 2.1.291) and kept in one table in code. `--permission-mode` accepts `acceptEdits`, `auto`, `bypassPermissions`, `manual`, `dontAsk`, `plan`; there is no `default` value, so Default mode passes **no** permission flag (`manual` behaves the same but is not used).

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
- `claude.extra_args` must not contain `--model`, `--fallback-model`, `--permission-mode`, `--dangerously-skip-permissions`, `--allow-dangerously-skip-permissions`, `--session-id`, `--resume`, `--continue`, `--append-system-prompt` or `--append-system-prompt-file`. Igris sets these itself; `check` and `arise` reject a config that contains them. `--fallback-model` in particular would let a task silently run on a different model.
- `--model` on the command line takes precedence over a `model` in Claude Code's settings (verified: settings `haiku` + `--model sonnet` ran `claude-sonnet-5-5`), so the plan's rank always wins. This also holds on `--resume`: a resumed session's new turns run on the `--model` given at resume.
- Model aliases `fable`, `opus`, `sonnet`, `haiku` all resolve (2.1.291: `claude-fable-5-1`, `claude-opus-5-5`, `claude-sonnet-5-5`, `claude-haiku-4-5-20251001`). Igris passes the alias and never pins a full model name.
- If `ANTHROPIC_API_KEY` is set in the environment, Claude Code uses it instead of the subscription login (it warns that it "takes precedence over your claude.ai login"). Igris never sets it; `check` warns when it is present in igris's environment, because tasks would then bill the API.

---

## 8. User tasks

For a task with Owner `user`:
1. Igris shows the full task text in the TUI, marks it **Your turn**, and sends a `needs_input` notification.
2. No pane, no Claude session.
3. The owner completes it outside igris, then presses `d` (done) or `s` (skip, with a reason) in the TUI, or runs `igris done <ID>` / `igris skip <ID> --reason …` from any terminal.
4. Igris marks the status and continues.

---

## 9. `igris adapt` — AI-assisted plan conversion

For plans that fail `igris check` or use a different format.

1. `igris adapt [--model sonnet|opus] [--plan PATH]` (default model from `adapt.model`, default `sonnet`). Only `sonnet` and `opus` are offered.
2. Igris runs `igris check` and captures every validation error.
3. It opens a session (same backend, mode `default`) with the adapt prompt: the canonical format (§3, embedded), the validation errors, the config's `[models]` aliases, and the job — **write a converted copy to `.igris/adapt/<plan-name>.proposed.md`**, preserving every task, ID, description, dependency, status and model. Allowed: restructure headings and tables, rename columns, normalize statuses, convert prose dependencies into IDs. Not allowed: inventing or changing models, dropping tasks, rewriting descriptions. Tasks with missing or unknown models are listed in a `## Adapt notes` section at the end of the proposal and given Model `?` — which `check` rejects, so the owner must fill them in.
4. The session can ask the owner questions like any other session. It finishes with `igris done ADAPT`.
5. Igris validates the proposal with the normal parser, then shows a **diff view** (original vs proposal) in the TUI with the validation result.
6. Owner accepts → the original is backed up to `.igris/adapt/<plan-name>.<timestamp>.bak.md` and replaced. Owner rejects → nothing changes.
7. `igris check` remains the gate: `igris arise` refuses to run on a plan that doesn't validate.

---

## 10. Notifications

Events: `needs_input`, `session_lost`, `verify_failed_limit`, `task_done`, `phase_done`, `phase_stuck`, `run_error`.

Channels (all optional, any combination):

| Channel | Config | Delivery |
|---|---|---|
| Backend | `[notify.backend] enabled` | herdr toast via `herdr notification show` (sound `request` for needs-input events, `done` for completions). |
| ntfy | `[notify.ntfy] server`, `topic`, optional `token` | HTTP POST, title + body, priority high for `needs_input`/`session_lost`. |
| Discord | `[notify.discord] webhook_url` | Webhook POST with a short message (`content`), no embeds needed. |

- Each channel has an `events` list; default: `needs_input`, `session_lost`, `phase_done`, `phase_stuck`, `run_error`, `verify_failed_limit`.
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

`SessionSpec` holds: working directory, label, Claude Code argv (model, mode flags, extra args), environment additions.

v1 ships `herdr` and `fake` (in-process, used by tests and `--dry-run`). `tmux` is planned and must fit this interface without changes to the engine.

### 11.2 herdr backend (v1)
Igris itself runs in a herdr pane. It uses the herdr CLI (JSON output), never the raw socket in v1:

| Step | herdr call |
|---|---|
| Availability | `herdr status server`; require `HERDR_WORKSPACE_ID` (igris must run inside a herdr pane) |
| Open pane | `herdr tab create --workspace $HERDR_WORKSPACE_ID --cwd <root> --label "<ID> · <rank>" --no-focus` → `.result.tab.tab_id`, `.result.root_pane.pane_id` |
| Start Claude | `herdr agent start <name> --kind claude --pane <pane_id> --timeout 120000 -- <claude args>`; `<name>` = `igris-<id>` lowercased, sanitized to `[a-z][a-z0-9_-]{0,31}`. Timeout must be > 3000 and ≤ 300000 ms. Returns `.result.agent` (`agent_status`, `interactive_ready`, `name`, `pane_id`) and `argv` |
| Startup blocked (e.g. folder-trust prompt) | `agent_not_ready` (exit 2, message "blocked during startup"; the name stays usable) → mark **Needs you**, poll state until the agent is past the prompt, then continue |
| Wait for state | `herdr agent wait <name> [--until STATUS]… [--timeout MS]`. Without `--until` it returns at the first settled state (`idle`, `done` **or `blocked`**), so check `.result.agent.agent_status` on return; it returns at once if already settled. `timeout` error code on expiry. Replaces tight polling for "is it ready for feedback"; `Needs you` still needs `pane get` polling |
| Send follow-up / task prompt | `herdr agent prompt <name> <text> [--wait --timeout MS]`. Multi-line text works (bracketed paste). Rejected with `agent_blocked` if the agent is at an approval/question UI. Without `--wait` the returned status is the pre-turn one |
| Read state | `herdr pane get <pane_id>` → `.result.pane.agent_status` ∈ `unknown` (plain shell / unclassified), `idle`, `working`, `blocked`, `done`; `pane_not_found` → session lost. `herdr agent read <name> --source recent-unwrapped --lines N` returns plain text (empty while a blocking prompt is drawn on the alternate screen — use `--source visible`) |
| Focus | `herdr tab focus <tab_id>` |
| Close | `herdr tab close <tab_id>` → `{"result":{"type":"ok"}}`; closing again gives `tab_not_found` |
| Notify | `herdr notification show <title> --body <body> --sound request|done|none` → `.result.shown` |

- Exact JSON shapes and flags were verified against herdr 0.9.1 (P0-03); recorded responses live in `internal/backend/herdr/testdata/` and drive the fixture tests.
- **Arguments after `--` must not contain control characters** (newline, tab, CR): herdr rejects them with `invalid_agent_argument` ("cannot be encoded safely for the target shell"). So the multi-line task prompt can **not** be a positional argument of `agent start`. Igris starts Claude with its flags only (no prompt), waits for `idle`, then submits the task prompt with `agent prompt` (§6.1).
- Error shape: JSON on stderr `{"error":{"code","message"},"id"}`, exit 1 (syntax errors exit 2; `agent_not_ready` exits 2). Codes seen: `agent_not_ready`, `agent_blocked`, `agent_not_found`, `pane_not_found`, `tab_not_found`, `timeout`, `invalid_agent_argument`, `invalid_agent_timeout`.
- `igris init` recommends `herdr integration install claude` for accurate agent state, and `igris check` warns if it's missing (where detectable).
- All herdr calls have timeouts (10 s default; agent start uses its own). Command failures surface the herdr error code in the TUI.

### 11.3 Running without herdr (v1 behavior)
If herdr isn't available, `igris arise` exits with a clear message explaining that v1 requires herdr and that tmux support is planned. `check`, `status`, `phases`, `done`, `skip` and `adapt --check`-style validation work without any backend.

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
command = "claude"
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
| `igris.lock` | PID + host + start time. A second `igris arise` refuses to start while the PID is alive; a stale lock is reported and can be cleared with `--force-unlock`. |
| `state.json` | Current run: phases, current task ID, session ref (backend name, pane/tab IDs, agent name), Claude session UUID, mode, attempt counters, started-at, hash of the config snapshot. Written atomically on every change. |
| `signals/` | Pending signal files (§6.2). |
| `runs.jsonl` | Append-only log: one JSON line per event (task started/done/skipped, verify result, notifications, errors) with timestamps, task ID, rank and model. |
| `adapt/` | Adapt proposals and backups (§9). |

**Resume.** `igris arise` (any phase argument, or none to resume the last run) reads `state.json`:
- current task still `in progress` and its session reattachable → reattach and keep watching;
- session gone → offer (TUI, or `--no-tui` stdin; default fresh): **continue** the previous conversation (`claude --resume <uuid>`, same model) or start a **fresh** session with `Resumed=true`;
- pending signal for the current task → process it first.

**Config snapshot.** `arise` loads `igris.toml` once at start and uses that snapshot for the whole run. If the file changes during a run (a session could edit it, e.g. to weaken `verify`), igris marks **Needs you**, notifies, and keeps using the snapshot; the new config only takes effect when the owner restarts `arise`.

Quitting the TUI (`q`) never kills a running session; it saves state and exits. Stopping a session requires an explicit action.

---

## 14. CLI

```
igris init                                  create igris.toml, .igris/, .gitignore entry, Claude allow rules
igris check [--plan PATH] [--json]          validate the plan; exit 0 valid, 1 invalid, 2 usage error
igris phases                                list phases with task counts per status
igris status [PHASE]                        tasks with status/rank/owner, current run, unmet deps
igris arise [PHASE] [--through PHASE]       run (or resume) with the TUI
           [--mode default|accept|auto|plan|yolo] [--no-tui] [--dry-run]
igris done ID [--note TEXT]                 signal that a task is finished
igris skip ID --reason TEXT                 signal that a task is skipped
igris adapt [--model sonnet|opus]           AI-assisted conversion with diff review
igris version
```

- `--no-tui` prints plain timestamped log lines and reads owner commands from stdin (`done`, `skip <reason>`, `retry`, `pause`, `stop`, `mode <m>`), for scripting or very small terminals.
- `--dry-run` uses the fake backend: walks the phase, prints which task would launch with which model and mode, writes nothing.
- On start, `arise` warns if `ANTHROPIC_API_KEY` is set in the environment (Claude Code would bill the API instead of the subscription) and asks for confirmation.

---

## 15. TUI

Built with Bubble Tea / Lip Gloss. Runs in the igris pane; the Claude sessions live in their own herdr tabs.

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
├────────────────────────────────┴──────────────────────────────┤
│ 09:41 M0-02 done · "subcommand dispatch + tests"              │
│ 09:41 M0-03 started (sonnet, plan)                            │
└ o open · m mode · p pause after task · r retry · s skip · ? ──┘
```

### 15.2 Narrow layout (< 100 columns, e.g. Termius on a phone)
Single column: header, current task card, compact task list (ID + status glyph + rank), last 3 log lines, one-line key hint. Must stay usable at 50×20.

### 15.3 Keys
| Key | Action |
|---|---|
| `o` | Focus the current session's pane (herdr) |
| `m` | Change run mode for upcoming sessions (picker; `yolo` needs confirmation) |
| `M` | Override mode for the selected task |
| `p` | Pause after the current task (toggle) |
| `d` | Mark current user task done |
| `s` | Skip current task (asks for reason; for agent tasks also closes the session after confirmation) |
| `r` | Retry: close current session, then continue its conversation or start a fresh one (`Resumed=true`) |
| `x` | Stop now: leave the session open, stop igris after confirmation |
| `↑/↓`, `enter` | Browse tasks / show task details (full text, deps, extra columns) |
| `q` | Quit the TUI; sessions keep running, `igris arise` resumes |
| `?` | Help |

### 15.4 Visual rules
- Status glyphs: `✓` done, `●` running, `!` needs you, `·` ready, `⨯` blocked, `–` skipped. Never color alone.
- Rank colors are configurable but each rank also shows its name.
- Respect `NO_COLOR`.

---

## 16. Errors and safety

- The plan is never written while it fails validation.
- Sessions can't change igris's behavior mid-run: config is snapshotted (§13), `igris skip` from a session needs owner confirmation (§6.2), and model/mode flags can't be smuggled in via `extra_args` (§7.4).
- Every external command (herdr, claude, git, verify) has a timeout; failures are shown with the command and exit code.
- Igris never runs commands from the plan's content, and never passes plan text through a shell — prompts go to herdr as argv, not interpolated into shell strings.
- Signal and state files are written atomically, with `0600` files and `0700` directories.
- Igris never reads or logs Claude Code credentials, and never sets `ANTHROPIC_API_KEY`.

---

## 17. Testing requirements

- **Parser:** table splitting (escaped pipes, padding, backticks), phase detection, column aliases, deps incl. ranges and cross-phase, every validation error. Golden tests with realistic plans: at least one large synthetic fixture (10+ phases, 150+ tasks, ranges, escaped pipes, cross-phase deps, user tasks). Never commit private plans as fixtures.
- **Writer:** byte-for-byte preservation except the target Status cell; CRLF files; files without trailing newline; concurrent-edit detection.
- **Scheduler:** selection order, resume, stuck detection, readiness sync, `--through`.
- **Engine:** full phase runs on the fake backend covering done, verify failure + retry, verify limit, session lost, user tasks, skip, pause, resume after restart, stray signals.
- **herdr backend:** command construction + JSON parsing against recorded fixtures; a manual smoke checklist on a real herdr install.
- **TUI:** model update tests (teatest) for key handling and both layouts.
- **Notifications:** httptest servers for ntfy and Discord; secret redaction.
- `go test -race ./...` clean.

---

## 18. Distribution

- Single static binary, `CGO_ENABLED=0`, for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.
- Built with **GoReleaser** (P0-04): `tar.gz` per target (binary + `LICENSE` + `README.md`) and a single `checksums.txt` (SHA-256), version injected with `-ldflags`. `make release-local` runs `goreleaser release --snapshot --clean` into `dist/`; nothing is published automatically — the owner uploads to a GitHub Release by hand (task M7-07).
- Artifacts are **not signed** in v0.1.0 (checksums only); cosign/minisign signing is a post-v1 item.
- `go install github.com/drilonrecica/igris/cmd/igris@latest` works.
- CI (P0-07): GitHub Actions on push to `master` and on PRs — gofmt check, `go vet`, golangci-lint, `go test -race ./...` on Linux and macOS. Read-only token, no secrets, never builds or publishes releases.
- Homebrew tap: post-v1 nice-to-have.
- MIT license.
