# Threat model

What igris protects, from whom, where the boundaries are, and which test enforces each safety rule. SPEC §16 is normative; this document explains it and keeps its rules tied to tests. It is updated with each security review (V06-07, then V10-05, V2R-04, V2X-03) and with every change to SPEC §16. Written for v0.6 (task V06-06).

## Trust model

- **Trusted:** the owner, and `igris.toml` as the owner wrote it when the run started (the config snapshot, SPEC §13).
- **Not trusted:**
  - a Claude Code session. In `accept`, `auto`, `yolo` and `plan:yolo` mode, and in a plan mode that continues in `accept` or `auto` once its plan is approved, it can write any file in the project without a prompt, including the plan, `igris.toml` and `.igris/`. It controls its done note, what `verify` prints, and the hook payloads Claude Code sends.
  - the plan's text (a plan someone else wrote, or one `igris adapt` produced);
  - the output of any command;
  - the network;
  - other local users.
- **The boundary that matters is the permission mode.** A session can't change the mode or model a task runs with, make igris skip verification, or drive the owner's terminal. What it can do by design is edit files, and therefore what `verify` and git hooks execute as the owner. Worktree mode (SPEC §6.8) keeps those edits on a branch of their own.

## Assets

| Asset | Why it matters |
|---|---|
| The owner's repository and checkout | Sessions change it; igris commits to it. Worktree mode isolates a run's changes on `igris/<run-id>`. |
| Notification secrets (ntfy and Gotify tokens, Discord, Slack and webhook URLs, the webhook signing secret) | They let anyone post to the owner's channels or forge signed webhooks. |
| Claude Code credentials | igris never reads, logs or sets them (`ANTHROPIC_API_KEY` included). |
| The plan's integrity | Only igris writes Status, and only to a valid plan. A wrong Status skips or repeats work. |
| igris's state in `.igris/` | The lock, `state.json`, signals, agent state, hook settings and run log decide what igris does next and what it reports. |
| Each task's model and permission mode | The real safety boundary (above). |
| The owner's terminal | Escape sequences in drawn text could rewrite the screen, set the clipboard or fake prompts. |
| The owner's attention | Needs you and notifications: a forged or suppressed one costs the owner time or hides a problem. |
| Release artifacts | Users run the binary; a replaced one runs as them. |

## Actors

| Actor | Can | Can't (by design) |
|---|---|---|
| Owner | Everything; confirms skips, resets, skip-permissions mode and drift | — |
| Claude Code session (main threat) | Write project files, `.igris/`, the plan, `igris.toml`; run `igris done`; shape its done note, verify output and hook payloads | Change its model or mode, skip verification, apply a skip or reset without the owner, inject terminal escapes, read notification secrets through verify, hooks or git |
| Plan author | Put any text in cells | Make igris run it as a command or pass it through a shell |
| Project code (verify, task hooks, git hooks) | Run as the owner with the project's files | See the variables the notification secrets come from; run without a timeout |
| Notification endpoint, network | Read what is sent to it; delay or drop it | Make igris block (requests have timeouts); read a secret over plain http without a warning |
| Another local user | Read world-readable files | Read `.igris/` (`0700`/`0600`) |
| Supply chain | — | Replace a release unnoticed (signed `checksums.txt` and tags, SBOM, govulncheck, reproducible build) |

## Boundaries

```
                         ┌──────────────────────────── owner ────────────────────────────┐
                         │  terminal (TUI, --no-tui log)      igris.toml (snapshot)        │
                         └──────────────▲──────────────────────────────┬──────────────────┘
                                 B4 cleaned text                       │ trusted at start
┌─────────── session pane (herdr / tmux) ───────────┐      ┌───────────▼───────────────┐
│ Claude Code session (untrusted)                    │ B1   │ igris                     │ B5  notification
│  · writes project files, plan, igris.toml, .igris/ │◄────►│  engine, plan writer,     │◄──► endpoints
│  · igris done / skip → .igris/signals/             │      │  state, notifier          │     (network)
│  · Claude Code hooks → igris hook → agent-state/   │      └───┬───────────────┬───────┘
└────────────────────────────────────────────────────┘    B3 │ argv, env,    │ B2 files sessions
                                                              │ timeouts      │ can write
                                         ┌────────────────────▼──┐   ┌────────▼──────────────────┐
                                         │ verify, task hooks,   │   │ plan, igris.toml, .igris/ │
                                         │ git (+ git hooks)     │   │ (lock, state, signals,    │
                                         │ project code          │   │  agent state, run log)    │
                                         └───────────────────────┘   └───────────────────────────┘
   B6 project root ↔ run worktree (.igris/worktrees/<run-id>)      B7 release build ↔ user (signatures)
```

## Threats by boundary

### B1 — igris ↔ session

| Threat | Mitigation |
|---|---|
| The session asks to skip its task (`igris skip`) | A skip signal for an agent task is a request: Needs you, applied only after the owner confirms (SPEC §6.2). `igris init` never allow-lists `igris skip`, and doctor warns about a rule that does. |
| The session resets a task (`igris reset` writes a signal) | A request the owner confirms, never mid-verify or mid-commit (SPEC §6.2). |
| A stale or other task's done signal | Applied only for the current task and only if written after it started; others are kept and reported (SPEC §6.2). |
| Forged agent state (hook files) | It can at most raise or clear Needs you, or delay it by up to `waiting_limit`; it never changes Status, mode or verification (accepted, below). |
| Hook payload too large or malformed | `igris hook` reads at most 64 KiB, keeps only states and counts, never commands or descriptions, and reads leniently. |
| The prompt or rules carry injected text | Plan text and notes are cleaned before they are typed into a pane; prompts travel as argv (herdr) or through a paste buffer on stdin (tmux), never through a shell or a tmux format (SPEC §11.5). |
| Mode or model smuggled into the session | `claude.extra_args` rejects model, mode, settings, session, resume, permission, tool, directory and system-prompt flags, and control characters (SPEC §7.4); `--model` beats a settings model. Skip-permissions mode needs a typed confirmation every run, including to reattach an interrupted yolo task (SPEC §7.3). |
| Claude Code's project settings widened for every later session (`.claude/settings*.json`: an allow-all `Bash` rule, a bypassing `defaultMode`) | Claude Code itself prompts for writes under `.claude/` in every mode but skip-permissions, and ignores `auto`/`bypassPermissions` as a project `defaultMode`; `doctor` warns about such rules in either file (SPEC §14). |
| The `--settings` file widens permissions | It holds hooks only; in worktree mode also the `igris done` rules the project's own settings already have, and `Read` of the plan when its path has no pattern characters (SPEC §6.8, §7.4). |

### B2 — files a session can write

| Threat | Mitigation |
|---|---|
| The session edits the plan mid-run | Rows igris didn't write hold the run until the owner looks (SPEC §5.4); an invalid plan is never written. |
| The session edits `igris.toml` mid-run | The run uses its snapshot; a change is reported, never applied, and recorded (`config_changed`): the next run asks the owner before using the edited file (SPEC §13). |
| The session rewrites `state.json` to smuggle flags | Session ref IDs, the Claude session UUID and the selection are shape-checked when read back; a bad UUID offers only a fresh session (SPEC §7.4, §13). |
| The session rewrites `state.json` to name another pane as its own (the owner's shell), or a prompt to type there | A backend reattaches only to the pane it started for the task: tmux checks the pane's start command (`claude`, the recorded `--session-id`, the task's rules file), herdr the task's agent name, to which prompts go; anything else is a gone session, with a warning. The held task prompt is built again from the plan, never read back (SPEC §13). |
| A FIFO or a huge file in place of a signal, agent state, run log, `state.json`, the lock, the plan or `igris.toml` | Read only as regular files of bounded size, opened without blocking (`internal/fsafe`; 64 KiB signals, 4 KiB agent state and lock, 1 MiB `state.json` and `igris.toml`, 16 MiB plan, last 16 MiB of the run log); anything else is reported, never blocks. |
| The session points igris at another file as its transcript, or puts a FIFO, a symlink or a huge file there (SPEC §6.9) | Only the path its hooks report is read, and only when it is absolute, clean and named `<the session's uuid>.jsonl`, checked when the hook writes it and again when igris reads it back. It is read through `internal/fsafe` (a regular file, no symlink, never blocking), streaming, a bounded amount per read, lines over 1 MiB skipped unparsed; of each line only the type, timestamp, ids, model and token counts are decoded, and model ids are shape-checked. Message content and the path never reach the run log. |
| Concurrent runs, or a lock race with `--force-unlock` | The lock is created complete (temp file, hard link); a live lock is refused (SPEC §13). |
| A session rewrites a side session's record (`.igris/side/<id>.json`, since 1.2) to name another pane, or puts a symlink, a FIFO or a huge file there | Records are read as regular files of at most 4 KiB, never through a symlink (nor a symlinked `side/`), and every value that becomes a file name, an argument or a path is shape-checked: the side ID, the backend, the session ref, the Claude session UUID, model, rank, mode (never `yolo` or `plan:<mode>`) and the worktree (only `.igris/worktrees/side-<id>` on `igris/side-<id>`). `AttachSide` binds the pane to its side session as `Attach` does a task's: tmux checks the start command for the recorded `--session-id` and `.igris/side/<id>.rules.md`, herdr the agent `igside-<id>`, which no task's agent can be; any other pane ends the record with a warning and is left alone. A damaged record is forgotten with a warning (SPEC §6.10, §13). |
| A side session finishes, skips or resets a task | Its environment holds `IGRIS_SIDE`, and `igris done`, `skip` and `reset` refuse while it is set (SPEC §6.2). It stops a mistake; a session can unset the variable (accepted, below), and then its signals are what any session's are: a done for the current task is verified, a skip or reset needs the owner. |
| A session in a worktree signals the wrong root | `igris done` from under `.igris/worktrees/<name>` finds the project, not the worktree's copy of `igris.toml` (SPEC §6.8). |

### B3 — igris ↔ child processes (verify, task hooks, git)

| Threat | Mitigation |
|---|---|
| Plan text run as a command | Never: verify and hooks come from the config snapshot; plans name profiles, not commands (SPEC §6.4). |
| A command hangs | Every external command has a timeout (SPEC §16). |
| Unbounded output | Captured output keeps the last 8 MiB; verify sends the last 60 lines back (SPEC §6.4). |
| Session-written code prints a notification secret into verify output | Verify, task hooks and git never get the variables the secrets' `env:` references name; `[run] env` limits verify and hooks to an allowlist (SPEC §6.4). |
| Verify and git hooks run session-written code as the owner | Inherent (accepted, below): the permission mode, and worktree mode, are the boundary. |

### B4 — igris ↔ the owner's terminal

| Threat | Mitigation |
|---|---|
| Escape sequences, bidi, zero-width, tag and other invisible format characters in plan cells, notes, command output, backend errors, run-log details, the lock's host, notify errors, config strings or `[models]` values | Cleaned before they are drawn, logged, sent as a notification, used in a commit message or typed into a pane (SPEC §16); a control character in a `[models]` value or an `extra_args` argument is a config error. |
| The owner's editor started through a shell | `$VISUAL`/`$EDITOR` is split into argv with no shell; with neither set igris asks (SPEC §16). |

### B5 — igris ↔ the network

| Threat | Mitigation |
|---|---|
| A secret leaks through the config hash, run log, errors or home's pages | Never written there; the pages say only whether a secret is set and where from (SPEC §16). |
| A token or webhook payload over plain http | Works (LAN servers) but `check`, `arise` and `doctor` warn (SPEC §16). |
| Forged webhooks | With a `secret`, every webhook body is signed (`X-Igris-Signature`, HMAC-SHA256) (SPEC §10). |

### B6 — project root ↔ worktree

| Threat | Mitigation |
|---|---|
| The run's changes reach the owner's checkout | In worktree mode sessions, verify, hooks and commits run in `.igris/worktrees/<run-id>`; igris never merges (SPEC §6.8). |
| Worktree mode widens what a session may do | Only the project's own `igris done` rules and reading the plan (B1). |

### B7 — release ↔ user

| Threat | Mitigation |
|---|---|
| A replaced download | `checksums.txt` and the tag are signed with the owner's SSH release key, kept with a passphrase only the owner types; users verify with `ssh-keygen -Y verify` and `allowed_signers` (SPEC §18). The trust root is the repository: `allowed_signers` and the README's copy of the key live there, so write access to it could replace key, tag and signature together (accepted, below). |
| A vulnerable dependency or toolchain | Pinned Go toolchain, govulncheck on push and weekly, an SBOM per archive (SPEC §18). |
| A build that isn't what the source says | The reproducible-build check builds the same commit on Linux and macOS and compares (SPEC §18). |

## Accepted risks

1. **Verify and git hooks run session-written code as the owner.** That is what verifying means. The permission mode the owner chooses limits what a session may write; worktree mode keeps it on a branch; the env rules keep the notification secrets out.
2. **Skip-permissions mode lets a session do anything the owner can.** It needs a typed confirmation every run and shows a red badge; use it in a container or with worktree mode.
3. **A session can forge hook state** (`.igris/agent-state/`): at worst it raises or clears Needs you, or delays it by up to `waiting_limit`. It never changes Status, mode or verification.
4. **A session can read `igris.toml`**, including the names of the variables the secrets come from, but not their values (they never reach its commands).
5. **The sessions' own environment** comes from the herdr or tmux server, not from igris, so the env rules don't apply to Claude Code itself.
6. **Plain-http notification servers** are allowed with a warning, for servers on the owner's LAN.
7. **A session can write files igris reads** (signals, agent state, `state.json`); each is checked as above, but a session can always make the run stop or ask the owner.
8. **The ntfy topic is shown and isn't a secret** (V06-07, owner's decision). On a public server without a token, the topic is what keeps others from reading and sending your notifications; the README says to pick a long random one or set a token.
9. **A secret written in `igris.toml` itself is part of the config hash** in `state.json` (V06-07, owner's decision): a weak one could be guessed offline from the hash. A session can read `igris.toml` anyway (risk 4); `env:` references keep secrets out of both.
10. **A skip-permissions session reattached after a stop** asks for the confirmation again only when `state.json` records it as one, or the plan or config give the task that mode (V06-07). A session started under a run-wide `--mode yolo` can rewrite its recorded mode; it already runs with full permissions (risk 2).
11. **A rewritten `state.json` can still name another igris-started pane** (V10-05): on tmux a pane whose start command carries a `--session-id` and a rules file for the same task ID (another project's run on the same server, say), on herdr the agent of the same task ID. igris would then type the task's prompts into that Claude session, never into a shell. Binding the pane to the task and session is what the check does; binding it to the project would need a marker a session can't forge, which tmux has none of.
12. **The version checks (`claude --version`, `herdr`, `tmux`) and the owner's editor run with igris's whole environment**, secrets included (V10-05): they are the owner's tools, not project code. Task hooks get the task's validated plan cells as `IGRIS_*` variables by design.
13. **`prompt_template` is read at every launch, not snapshotted** (V10-05): a session can rewrite it mid-run, as it can rewrite `CLAUDE.md`; both reach the next session's prompt, not igris's behaviour.
14. **The signed webhook has no replay protection** (V10-05): the signature covers the body, which has no nonce. Receivers are told to key on `run` + `at` and to ignore stale payloads (SPEC §10); a timestamp header may come in 1.x.
15. **The release trust root is the repository** (V10-05, B7): the signature defeats a replaced release asset, which is what it claims; a party able to rewrite the repository controls the source anyway.
16. **A session can forge a plan approval** (V11-P1, V11-01): it can write `.igris/plan-approval/` too, so it could answer its own `ExitPlanMode` request with an allow and implement a plan the owner never saw. Approval from igris is a convenience, not a guard: plan mode never was one, since an approved session runs with the owner's rights. What a forged decision can't do is pick the mode: the mode an approval switches to and the wait are arguments in the session's settings file, which Claude Code read at start: only a `plan:yolo` session, which the owner confirmed for this run (SPEC §7.3), has `bypassPermissions` there, and only it starts with `--allow-dangerously-skip-permissions`, without which Claude Code ignores that switch (V11-02). Such a session is not held by plan mode's prompts before approval either (Claude Code says so in the pane), so forging its approval gains it little. The exchange files are read bounded and shape-checked, never through a symlink, and a request whose hook is gone is ignored.
17. **A session can forge its own token usage and the quota** (V11-P2, V11-03, V11-06): it can write its transcript, so it can inflate, shrink or hide the usage igris logs, and the models the rank audit sees (V11-04); it can write `.igris/quota.json`, so it can fake the subscription quota the header and `igris usage` show. `quota.json` is read as a regular file of at most 4 KiB, without following a symlink, and shape-checked (percentages 0 to 100), and nothing decides on it. Usage is information, not a guard, and the audit catches configuration slip, not a hostile session, which runs with the owner's rights anyway. Pointing igris at another file gains nothing: the file must be named after the session's UUID, and only numbers and model ids are read from it.
18. **A side session can unset `IGRIS_SIDE`** (V12-P1, V12-02): the refusal of `igris done`, `skip` and `reset` inside a side session stops an honest mistake, not a session set on getting around it. Without the variable its signals are any session's: a done signal counts only for the current task and is verified, a skip or reset is a request the owner confirms (R3, R4). Side sessions are not exempt from the plan hold or the config snapshot either: a plan edit from one holds the run and an `igris.toml` edit raises `config_changed`, since an exemption would let any session claim to be a side session.

## Reviews

### V06-07 (2026-10-14, v0.6)

Four reviewers on the Fable model read the code against this document, one area each (session-facing inputs, commands igris runs, backends, notifications). Each finding was checked against the code before it was fixed or accepted.

| # | Area | Finding | Severity | Outcome |
|---|---|---|---|---|
| 1 | state.json, worktree | A rewritten `worktree` path or branch was used as the run's work dir and in git calls: a session could point the next run's sessions, verify and commits at the project root or another repository, or have igris remove another worktree. A symlink in place of the worktree was followed too. | medium | Fixed: only `.igris/worktrees/<run-id>` on `igris/<run-id>` reads back; a symlink is not a worktree (R6, R22) |
| 2 | state.json, mode | The reattach confirmation trusted the recorded mode, which a session can rewrite. | medium | Partly fixed: the mode must be a run mode, and the plan or config giving the task skip-permissions mode still asks; the rest accepted (risk 10) |
| 3 | start-up checks | The start-up `git rev-parse`/`git status` got igris's whole environment; `git status` runs programs a repository's config names (`core.fsmonitor`), which a session can write. | medium | Fixed: the secrets' variables are left out (R15) |
| 4 | terminal | Errors printed to stderr were not cleaned; they can carry a git hook's output or values from `.igris/`. | low | Fixed: all of stderr is cleaned (R12) |
| 5 | tmux | A process naming itself with a tab or newline could make a live pane read as gone (a false "session lost"). | low | Fixed (R31) |
| 6 | verify | The excerpt sent back after a failed verify was bounded in lines, not bytes; one long line could exceed herdr's argument limit and turn the retry into a Needs you. | low | Fixed: at most 16 KiB (R9) |
| 7 | signals | Every poll read every file in `signals/`; a session filling it could slow igris to a stop. | low | Fixed: at most 256 per poll, the rest reported (R14) |
| 8 | state.json | A negative `verify_attempts` deferred the verify limit; the task ID wasn't checked. | low | Fixed (R6) |
| 9 | notifications | A webhook URL from an `env:` reference that resolves to plain http wasn't warned about. | low | Fixed (R18) |
| 10 | config | A secret reading `env:PATH` or `env:HOME` would strip a variable every command needs. | low | Fixed: a config error |
| 11 | notifications | The ntfy topic, the only protection on a tokenless public server, is shown and not treated as a secret. | low | Accepted (risk 8) |
| 12 | config hash | A secret written in `igris.toml` is part of the config hash. | info | Accepted (risk 9) |

Checked and holding, among others: redirects never carry tokens or signatures to another host; response bodies are never echoed; templates can't loop or call out and are bounded; tmux names and messages can't expand formats; prompts reach panes as argv or stdin, cleaned; Context paths stay inside the project; plan text never becomes a command.

### V10-05 (2026-10-20, v1.0.0-rc.1)

The final review before 1.0. Four reviewers on the Fable model read the whole tree against this document, one area each (session-facing inputs and files sessions write; the commands igris runs and their environment; backends and the owner's terminal; notifications, the site generator and the release chain). Every finding was checked against the code before it was fixed or accepted. Baseline: lint, the race detector, govulncheck and `go mod verify` clean.

| # | Area | Finding | Severity | Outcome |
|---|---|---|---|---|
| 1 | state.json, backends | On resume, `Attach` took any live pane the recorded ref named and typed the recorded `pending_prompt` into it: a session could rewrite `state.json` to point igris at the owner's shell (tmux) or another agent's pane (herdr) and choose the text. | high (tmux), medium (herdr) | Fixed: `Attach` takes the task ID and binds the pane to it (tmux `pane_start_command`, herdr agent name); the held prompt is rebuilt from the plan (R32) |
| 2 | config | An `igris.toml` edited during a run was adopted by the next run silently; `commit = "auto"` could even commit the edit with the task. | medium | Fixed: recorded in `state.json` and the run log; the next run asks, `check`/`doctor` warn (R33) |
| 3 | doctor | The allow-rule check missed a bare `Bash` rule and never read `.claude/settings.json`. Claude Code prompts for writes under `.claude/` in every mode but skip-permissions, so defence in depth. | low | Fixed: both files, allow-all rules and `defaultMode` warned about (R21) |
| 4 | extra_args | Flags that change the session's permissions, tools, directory or system prompt (`--allowedTools`, `--add-dir`, `--bare`, `--setting-sources`, …) passed; the no-control-characters rule was enforced only by herdr at launch. | low | Fixed: list extended, control characters a config error (R5) |
| 5 | terminal | A `[models]` value reached the run view and the `--no-tui` log uncleaned, and TOML accepts `\u001b`. | low | Fixed: a config error, and `emit` cleans Model, Rank, Mode and Phase (R12) |
| 6 | terminal | The standalone adapt review screen kept bidi and zero-width characters. | low | Fixed (R12) |
| 7 | files | The plan, `igris.toml`, `state.json` and the lock were read with plain `ReadFile`: a FIFO in their place hung igris silently. | low | Fixed: one bounded non-blocking reader for every file a session can replace (R14) |
| 8 | notifications | Webhook receivers were not told a valid signature can be a replayed payload. | low | Docs; accepted (risk 14) |
| 9 | terminal | Tag characters U+E0000–E007F and other invisible format characters passed cleaning: text the owner never sees but the session's prompt carries. | low | Fixed: stripped, at the cost of subdivision-flag emoji in plan text (R12) |
| 10 | site | The site generator put link targets into attributes unescaped and passed any URL scheme. Trusted input only. | info | Fixed |
| 11 | config | `user:password@` in a `server` URL was shown on Settings and in doctor. | info | Fixed: a config error |
| 12 | signals | The once-per-key report map grew with every rewrite of a stray signal. | info | Fixed: once per task and action |
| 13 | config migrate | A symlinked `igris.toml` was replaced by a regular file. | info | Fixed: the target is rewritten |
| 14 | release | The verification instructions fetch `allowed_signers` from the repository being verified. | info | Accepted (risk 15) |
| 15 | commands | Version checks and the editor get igris's whole environment; task hooks get plan cells as env values. | info | Accepted (risk 12) |
| 16 | prompts | `prompt_template` is not part of the config snapshot. | info | Accepted (risk 13) |

Checked and holding, among others: no plan text reaches a shell; verify, hooks and git never see the secrets' variables; every command has a timeout and bounded output; the yolo confirmation on every path; redirects never followed; secrets never in errors, hashes or pages; every workflow step pinned and read-only; the reproducible build; signed checksums and formula checksums derived from them.

## Rule → test map

One row per safety rule. The tests are named `package.TestName`; `tools/threatmodel` fails when one of them no longer exists, so the map can't silently lose a test. A rule only partly covered says what is missing.

| ID | Rule | SPEC | Enforced by |
|---|---|---|---|
| R1 | The plan is never written while it fails validation | §16, §4 | `internal/plan.TestWriterRefusesInvalidPlan` |
| R2 | The run uses its config snapshot; an `igris.toml` edit mid-run is reported, never applied | §13, §16 | `internal/engine.TestConfigChangeIsReportedOncePerChange`, `internal/engine.TestConfigRemovedCountsAsChange`, `internal/engine.TestVerifyPasses` |
| R3 | A session's `igris skip` is a request the owner confirms | §6.2 | `internal/engine.TestSkipSignalNeedsConfirmation` |
| R4 | A reset signal is a request the owner confirms | §6.2 | `internal/engine.TestResetNeedsTheOwner` |
| R5 | `claude.extra_args` can't carry model, mode, settings, session, resume, permission, tool, directory or system-prompt flags, nor control characters | §7.4 | `internal/config.TestForbiddenExtraArgs`, `internal/config.TestForbiddenExtraArg`, `internal/config.TestExtraArgsRejectControlCharacters`, `internal/engine.TestClaudeArgsErrors` |
| R6 | Values read back from `state.json` are shape-checked before they become arguments | §7.4, §13 | `internal/engine.TestRetryContinueRefusesATamperedSessionID`, `internal/backend/tmux.TestAttach`, `internal/backend/herdr.TestAttachChecksClaudeSession`, `internal/state.TestLoadRunErrors`, `internal/state.TestLoadRunAcceptsItsOwn`, `internal/state.TestRunConfigChangedRoundTrip` |
| R7 | Plan edits igris didn't make hold the run until the owner looks | §5.4 | `internal/engine.TestPlanEditedBySessionHoldsTheRun`, `internal/engine.TestOwnWritesAreNotPlanChanges` |
| R8 | Every external command has a timeout | §16 | `internal/runner.TestExecInvalidCommands`, `internal/runner.TestFakeValidatesLikeExec`, `internal/runner.TestExecTimeoutKillsProcessGroup`, `internal/engine.TestHooksRunAroundEachSession` |
| R9 | Captured output is bounded (last 8 MiB; verify sends back the last 60 lines, at most 16 KiB) | §6.4 | `internal/engine.TestTailIsBoundedInBytes`, `internal/runner.TestExecOutputIsBounded`, `internal/runner.TestTailBufferKeepsTheEnd`, `internal/engine.TestVerifyFailureGoesBackToTheSession` |
| R10 | The owner's editor runs as argv, never through a shell, and isn't guessed | §16, §15.6 | `internal/project.TestEditorArgv`, `internal/tui.TestEditWithoutEditorAsks` |
| R11 | Plan text never passes through a shell; prompts go as argv (herdr) or via stdin (tmux), never into a tmux format | §16, §11.5 | `internal/runner.TestExecArgvIsNotShellInterpreted`, `internal/backend/tmux.TestLoadBufferUsesStdin`, `internal/backend/tmux.TestLiteral`, `internal/backend/herdr.TestPrompt` |
| R12 | Untrusted text is cleaned before it is drawn, logged, sent, committed or typed into a pane | §16 | `internal/textsafe.TestClean`, `internal/tui.TestHomeCleansEveryTextSource`, `internal/engine.TestUntrustedTextIsCleaned`, `internal/backend/herdr.TestPromptIsCleaned`, `internal/checks.TestResultsAreSafe`, `internal/tui.TestNotifyCleansErrors`, `cmd/igris.TestStderrIsCleaned`, `internal/engine.TestEmitCleansModelRankModePhase`, `internal/config.TestModelsRejectControlCharacters`, `internal/tui.TestReviewCleansHiddenCharacters` (partial: the `--no-tui` log is covered through the engine's cleaned events, not by a test of its own) |
| R13 | State and signal files are written atomically, `0600` files in `0700` directories | §16, §13 | `internal/state.TestWriteFileAtomic`, `internal/state.TestOpenCreatesPrivateDirs`, `internal/state.TestSignalRoundTrip`, `internal/state.TestAgentStateRoundTrip` |
| R14 | Files sessions can write or replace are read only as regular files of bounded size, without blocking | §6.2, §6.3, §13, §16 | `internal/fsafe.TestReadFile`, `internal/state.TestSignalsFromSessionsAreGuarded`, `internal/state.TestReadAgentStateUntrusted`, `internal/state.TestPeekLogNotRegular`, `internal/state.TestPeekLogSizeCap`, `internal/state.TestSignalScanIsBounded`, `internal/state.TestReadBackRefusesNonRegularFiles`, `internal/plan.TestLoadRefusesNonRegularFiles`, `internal/config.TestLoadRefusesNonRegularFiles`, `internal/engine.TestStraySignalsAreReportedOnce` |
| R15 | Verify, task hooks and git never get the secrets' `env:` variables; `run.env` limits verify and hooks | §6.4, §16 | `internal/engine.TestCommandEnv`, `internal/engine.TestChildEnv`, `internal/config.TestRunEnv`, `internal/config.TestSecretEnvNames`, `internal/checks.TestGitWithoutSecrets` |
| R16 | igris never sets `ANTHROPIC_API_KEY`; `check` warns and `arise` asks when it is set | §7.4, §16 | `tools/threatmodel.TestNeverSetsAPIKey`, `cmd/igris.TestCheckWarnsAboutAPIKey`, `cmd/igris.TestAriseStartUpConfirmations` |
| R17 | Notification secrets never reach the config hash, the run log, errors or home's pages | §10, §16 | `internal/config.TestHash`, `internal/config.TestSecretsAreRedacted`, `internal/notify.TestSecretsAreScrubbedFromErrors`, `internal/engine.TestNotificationsReachEveryChannel`, `internal/tui.TestSettingsPageHidesSecrets`, `internal/project.TestNotifyTestRedactsSecrets`, `internal/checks.TestNotifyChannels`, `internal/config.TestServerURLRejectsUserinfo` |
| R18 | Tokens and webhook URLs over plain http are warned about, also a webhook URL from an `env:` reference | §16 | `internal/config.TestHTTPWarnings`, `internal/config.TestWarningsEnv` |
| R19 | Skip-permissions mode needs a typed confirmation every run, also to reattach an interrupted task; never one click | §7.3 | `internal/engine.TestSkipPermissionsNeedsConfirmation`, `internal/engine.TestSkipPermissionsIsCheckedAgainAtLaunch`, `internal/engine.TestResumeYoloSessionNeedsConfirmation`, `internal/tui.TestWizardYoloNeedsThePhrasePerLaunch`, `internal/tui.TestYoloNeverOneClick` |
| R20 | The `--settings` file holds hooks only (in worktree mode also the rules of R22) | §6.3, §7.4 | `internal/hook.TestSettings`, `internal/hook.TestSettingsAllow`, `internal/engine.TestNoWorktree` |
| R21 | `igris done` is allow-listed by `init`, `igris skip` never; doctor warns about a rule that allows it, an allow-all `Bash` rule or a bypassing `defaultMode`, in either Claude Code settings file | §6.2, §7.1, §14 | `cmd/igris.TestInitFresh`, `internal/checks.TestDoctorAllowRules`, `internal/checks.TestAllowsSkip`, `internal/checks.TestAllowsAllBash` |
| R22 | In a worktree, `igris done` finds the project; the session gets only the project's `igris done` rules and reading the plan | §6.8 | `internal/state.TestFindRoot`, `internal/engine.TestWorktreeRun`, `internal/engine.TestWorktreeAllow`, `internal/engine.TestWorktreeSymlinkIsNotReused` |
| R23 | One run per project: a live lock is refused, a stale one needs `--force-unlock`, the lock appears complete | §13 | `internal/state.TestLock`, `internal/state.TestLockFileAppearsComplete`, `internal/engine.TestRunRefusesWhenLocked` |
| R24 | Webhooks are signed with the secret, which never appears in errors or the run log | §10 | `internal/notify.TestWebhookSend`, `internal/notify.TestWebhookErrorsHideSecrets`, `internal/engine.TestNotificationsReachEveryChannel` |
| R25 | Releases verify against the published key; `allowed_signers` and the README agree | §18 | `tools/signers.TestAllowedSigners`, `tools/signers.TestReadmeQuotesTheKey` |
| R26 | Commit messages and notifications built from untrusted text are cleaned | §6.5, §10 | `internal/engine.TestUntrustedTextIsCleaned`, `internal/notify.TestTemplatedCleansCutsAndFallsBack` |
| R27 | A hook payload is read bounded, and only a state is kept from it | §6.3 | `internal/hook.TestRunRejects`, `internal/hook.TestStopPending`, `internal/hook.TestRunStoresOnlyTheState` |
| R28 | User-facing errors say what to do next | §16 | `internal/state.TestLoadRunErrors`, `internal/state.TestStaleLockNamesTheCommand`, `internal/checks.TestDoctorAllowRules` (partial: checked for state, lock and doctor errors, not for every error) |
| R31 | What a pane's process calls itself can't forge the pane's state (tmux `pane_current_command`) | §11.5 | `internal/backend/tmux.TestPane` |
| R32 | A backend reattaches only to the pane it started for the task and Claude session; the held task prompt is rebuilt from the plan, never read back | §13, §16 | `internal/backend/tmux.TestAttachBindsThePaneToTheTask`, `internal/backend/herdr.TestAttachBindsTheAgentToTheTask`, `internal/backend/fake.TestAttachRefusesAnotherTask`, `internal/engine.TestResumeRefusesAnotherTasksPane`, `internal/engine.TestResumeRebuildsAHeldPrompt`, `internal/state.TestRulesPathMatchesBackendMarker` |
| R33 | An `igris.toml` edited during a run is used by a later run only after the owner confirms; `check` and `doctor` warn meanwhile | §13, §16 | `internal/engine.TestConfigChangedDuringRunNeedsConfirmationNextRun`, `cmd/igris.TestAriseStartUpConfirmations`, `internal/tui.TestWizardParity`, `internal/checks.TestConfigChangedDuringLastRun` |
| R34 | The plan approval exchange is read bounded and shape-checked, never through a symlink; a request whose hook is gone is ignored; the hook answers only a decision naming its own request and only for an `ExitPlanMode` request with an igris UUID | §6.3 | `internal/state.TestPlanExchangeUntrusted`, `internal/hook.TestApprovePlanRefuses`, `internal/hook.TestApprovePlanLeavesItToThePane`, `internal/engine.TestPlanApprovalOnlyForLivePlanSessions` |
| R35 | The mode an approval switches to comes from the settings file's arguments, never from a file a session can write; only `default`, `acceptEdits` and `auto`; no plan-approve entry without a TUI, for other modes or with `approve_timeout = "0"` | §6.3, §7.1 | `internal/hook.TestSettingsPlanApproval`, `internal/hook.TestApprovePlanRefuses`, `internal/engine.TestPlanSettingsEntries`, `internal/engine.TestPlanApprovalOffWithoutTUI` |
| R36 | Plan text and feedback never reach the run log; feedback and the shown plan are cleaned of escape sequences | §6.3, §13 | `internal/engine.TestPlanApprovedFromIgris`, `internal/engine.TestFeedback` |
| R37 | A session can forge a plan approval | §6.3 | accepted (see Accepted risks 16) |
| R38 | A transcript is read only from the path the hooks report, named after the session's UUID, as a regular file without following a symlink and never blocking, lines over 1 MiB skipped; only counts and shape-checked model ids are kept, never content or the path in the run log | §6.9, §13 | `internal/hook.TestRunTranscript`, `internal/state.TestReadAgentStateTranscript`, `internal/usage.TestReadUnavailable`, `internal/usage.TestReadSkips`, `internal/engine.TestUsageLoggedWhenTheTaskEnds` |
| R39 | A session can forge its own token usage | §6.9 | accepted (see Accepted risks 17) |
| R40 | A session can forge the subscription quota in `.igris/quota.json`, or its status line payload carries escape sequences | §6.3 | accepted (see Accepted risks 17); `internal/state.TestQuotaNeverCreatesStateAndDropsBadWindows`, `internal/hook.TestStatusLineHostileInput` |
| R41 | Side session records are read bounded and shape-checked, never through a symlink; a side session reattaches only to the pane started for it, a task only to its own, and a record naming another pane ends without touching it | §6.10, §13 | `internal/state.TestSideRecordShape`, `internal/state.TestReadSideRecordsUntrusted`, `internal/state.TestClaimSideRecord`, `internal/backend/tmux.TestSideSessionBinding`, `internal/backend/herdr.TestOpenSideSession`, `internal/side.TestRefreshForeignPane`, `internal/side.TestRefreshProblemRecords` |
| R42 | A side session never skips permissions: only `default`, `accept`, `auto` and `plan`, and `claude.extra_args` checked as for a task; the dialog offers no other mode and `[side] mode` can't name one | §6.10, §7.4, §12 | `internal/side.TestOpenRefuses`, `internal/state.TestSideRecordShape`, `internal/tui.TestSideDialogDuringRun`, `internal/config.TestSide` |
| R43 | `igris done`, `skip` and `reset` refuse inside a side session | §6.2, §6.10 | `cmd/igris.TestSideSessionRefusesSignals` (partial: a session can unset `IGRIS_SIDE`, see Accepted risks 18) |
| R44 | A side session's first prompt (plan text, run-log details, what the owner typed or pasted) is cleaned of escape sequences and control characters and at most 8 KiB before it is drawn or typed into the pane; the igris problem prompt holds no config values or secrets (doctor's checks are named, not quoted) | §6.10, §16 | `internal/side.TestTaskPrefill`, `internal/side.TestIgrisPrefill`, `internal/tui.TestSidePromptCleanedAndBounded` |
| R45 | A side session's transcript is read as a task's is (R38) when it ends; its `side_usage` line holds counts and model ids only, the side ID in it is shape-checked when read, and it is listed apart from the attempts, so it can't change a task's totals or audit | §6.9, §6.10, §13, §14 | `internal/side.TestSideRunLog`, `internal/state.TestEventChecked`, `internal/report.TestUsageSide` |
| R29 | Verify and git hooks run session-written code as the owner | §16 | accepted (see Accepted risks 1) |
| R30 | A session can forge hook state, moving Needs you within `waiting_limit` | §6.3 | accepted (see Accepted risks 3) |
