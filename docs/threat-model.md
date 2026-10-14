# Threat model

What igris protects, from whom, where the boundaries are, and which test enforces each safety rule. SPEC §16 is normative; this document explains it and keeps its rules tied to tests. It is updated with each security review (V06-07, then V10-05, V2R-04, V2X-03) and with every change to SPEC §16. Written for v0.6 (task V06-06).

## Trust model

- **Trusted:** the owner, and `igris.toml` as the owner wrote it when the run started (the config snapshot, SPEC §13).
- **Not trusted:**
  - a Claude Code session. In `accept`, `auto` and `yolo` mode it can write any file in the project without a prompt, including the plan, `igris.toml` and `.igris/`. It controls its done note, what `verify` prints, and the hook payloads Claude Code sends.
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
| Mode or model smuggled into the session | `claude.extra_args` rejects model, mode, settings, session and resume flags (SPEC §7.4); `--model` beats a settings model. Skip-permissions mode needs a typed confirmation every run, including to reattach an interrupted yolo task (SPEC §7.3). |
| The `--settings` file widens permissions | It holds hooks only; in worktree mode also the `igris done` rules the project's own settings already have, and `Read` of the plan when its path has no pattern characters (SPEC §6.8, §7.4). |

### B2 — files a session can write

| Threat | Mitigation |
|---|---|
| The session edits the plan mid-run | Rows igris didn't write hold the run until the owner looks (SPEC §5.4); an invalid plan is never written. |
| The session edits `igris.toml` mid-run | The run uses its snapshot; a change is reported, never applied (SPEC §13). |
| The session rewrites `state.json` to smuggle flags | Session ref IDs, the Claude session UUID and the selection are shape-checked when read back; a bad UUID offers only a fresh session (SPEC §7.4, §13). |
| A FIFO or a huge file in place of a signal, agent state or run log | Read only as regular files of bounded size (64 KiB signals, 4 KiB agent state, last 16 MiB of the run log); anything else is reported, never blocks. |
| Concurrent runs, or a lock race with `--force-unlock` | The lock is created complete (temp file, hard link); a live lock is refused (SPEC §13). |
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
| Escape sequences or bidi/zero-width characters in plan cells, notes, command output, backend errors, run-log details, the lock's host, notify errors or config strings | Cleaned before they are drawn, logged, sent as a notification, used in a commit message or typed into a pane (SPEC §16). |
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
| A replaced download | `checksums.txt` and the tag are signed with the owner's SSH release key, kept with a passphrase only the owner types; users verify with `ssh-keygen -Y verify` and `allowed_signers` (SPEC §18). |
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

## Rule → test map

One row per safety rule. The tests are named `package.TestName`; `tools/threatmodel` fails when one of them no longer exists, so the map can't silently lose a test. A rule only partly covered says what is missing.

| ID | Rule | SPEC | Enforced by |
|---|---|---|---|
| R1 | The plan is never written while it fails validation | §16, §4 | `internal/plan.TestWriterRefusesInvalidPlan` |
| R2 | The run uses its config snapshot; an `igris.toml` edit mid-run is reported, never applied | §13, §16 | `internal/engine.TestConfigChangeIsReportedOncePerChange`, `internal/engine.TestConfigRemovedCountsAsChange` |
| R3 | A session's `igris skip` is a request the owner confirms | §6.2 | `internal/engine.TestSkipSignalNeedsConfirmation` |
| R4 | A reset signal is a request the owner confirms | §6.2 | `internal/engine.TestResetNeedsTheOwner` |
| R5 | `claude.extra_args` can't carry model, mode, settings, session or resume flags | §7.4 | `internal/config.TestForbiddenExtraArgs`, `internal/config.TestForbiddenExtraArg`, `internal/engine.TestClaudeArgsErrors` |
| R6 | Values read back from `state.json` are shape-checked before they become arguments | §7.4, §13 | `internal/engine.TestRetryContinueRefusesATamperedSessionID`, `internal/backend/tmux.TestAttach`, `internal/backend/herdr.TestAttachChecksClaudeSession`, `internal/state.TestLoadRunErrors`, `internal/state.TestLoadRunAcceptsItsOwn` |
| R7 | Plan edits igris didn't make hold the run until the owner looks | §5.4 | `internal/engine.TestPlanEditedBySessionHoldsTheRun`, `internal/engine.TestOwnWritesAreNotPlanChanges` |
| R8 | Every external command has a timeout | §16 | `internal/runner.TestExecInvalidCommands`, `internal/runner.TestFakeValidatesLikeExec`, `internal/runner.TestExecTimeoutKillsProcessGroup`, `internal/engine.TestHooksRunAroundEachSession` |
| R9 | Captured output is bounded (last 8 MiB; verify sends back the last 60 lines, at most 16 KiB) | §6.4 | `internal/engine.TestTailIsBoundedInBytes`, `internal/runner.TestExecOutputIsBounded`, `internal/runner.TestTailBufferKeepsTheEnd`, `internal/engine.TestVerifyFailureGoesBackToTheSession` |
| R10 | The owner's editor runs as argv, never through a shell, and isn't guessed | §16, §15.6 | `internal/project.TestEditorArgv`, `internal/tui.TestEditWithoutEditorAsks` |
| R11 | Plan text never passes through a shell; prompts go as argv (herdr) or via stdin (tmux), never into a tmux format | §16, §11.5 | `internal/runner.TestExecArgvIsNotShellInterpreted`, `internal/backend/tmux.TestLoadBufferUsesStdin`, `internal/backend/tmux.TestLiteral`, `internal/backend/herdr.TestPrompt` |
| R12 | Untrusted text is cleaned before it is drawn, logged, sent, committed or typed into a pane | §16 | `internal/textsafe.TestClean`, `internal/tui.TestHomeCleansEveryTextSource`, `internal/engine.TestUntrustedTextIsCleaned`, `internal/backend/herdr.TestPromptIsCleaned`, `internal/checks.TestResultsAreSafe`, `internal/tui.TestNotifyCleansErrors`, `cmd/igris.TestStderrIsCleaned` (partial: the `--no-tui` log is covered through the engine's cleaned events, not by a test of its own) |
| R13 | State and signal files are written atomically, `0600` files in `0700` directories | §16, §13 | `internal/state.TestWriteFileAtomic`, `internal/state.TestOpenCreatesPrivateDirs`, `internal/state.TestSignalRoundTrip`, `internal/state.TestAgentStateRoundTrip` |
| R14 | Files sessions can write are read only as regular files of bounded size | §6.2, §6.3, §13 | `internal/state.TestSignalsFromSessionsAreGuarded`, `internal/state.TestReadAgentStateUntrusted`, `internal/state.TestPeekLogNotRegular`, `internal/state.TestPeekLogSizeCap`, `internal/state.TestSignalScanIsBounded` |
| R15 | Verify, task hooks and git never get the secrets' `env:` variables; `run.env` limits verify and hooks | §6.4, §16 | `internal/engine.TestCommandEnv`, `internal/engine.TestChildEnv`, `internal/config.TestRunEnv`, `internal/config.TestSecretEnvNames`, `internal/checks.TestGitWithoutSecrets` |
| R16 | igris never sets `ANTHROPIC_API_KEY`; `check` warns and `arise` asks when it is set | §7.4, §16 | `tools/threatmodel.TestNeverSetsAPIKey`, `cmd/igris.TestCheckWarnsAboutAPIKey`, `cmd/igris.TestAriseStartUpConfirmations` |
| R17 | Notification secrets never reach the config hash, the run log, errors or home's pages | §10, §16 | `internal/config.TestHash`, `internal/config.TestSecretsAreRedacted`, `internal/notify.TestSecretsAreScrubbedFromErrors`, `internal/engine.TestNotificationsReachEveryChannel`, `internal/tui.TestSettingsPageHidesSecrets`, `internal/project.TestNotifyTestRedactsSecrets`, `internal/checks.TestNotifyChannels` |
| R18 | Tokens and webhook URLs over plain http are warned about, also a webhook URL from an `env:` reference | §16 | `internal/config.TestHTTPWarnings`, `internal/config.TestWarningsEnv` |
| R19 | Skip-permissions mode needs a typed confirmation every run, also to reattach an interrupted task; never one click | §7.3 | `internal/engine.TestSkipPermissionsNeedsConfirmation`, `internal/engine.TestSkipPermissionsIsCheckedAgainAtLaunch`, `internal/engine.TestResumeYoloSessionNeedsConfirmation`, `internal/tui.TestWizardYoloNeedsThePhrasePerLaunch`, `internal/tui.TestYoloNeverOneClick` |
| R20 | The `--settings` file holds hooks only (in worktree mode also the rules of R22) | §6.3, §7.4 | `internal/hook.TestSettings`, `internal/hook.TestSettingsAllow`, `internal/engine.TestNoWorktree` |
| R21 | `igris done` is allow-listed by `init`, `igris skip` never; doctor warns about a rule that allows it | §6.2, §7.1 | `cmd/igris.TestInitFresh`, `internal/checks.TestDoctorAllowRules`, `internal/checks.TestAllowsSkip` |
| R22 | In a worktree, `igris done` finds the project; the session gets only the project's `igris done` rules and reading the plan | §6.8 | `internal/state.TestFindRoot`, `internal/engine.TestWorktreeRun`, `internal/engine.TestWorktreeAllow`, `internal/engine.TestWorktreeSymlinkIsNotReused` |
| R23 | One run per project: a live lock is refused, a stale one needs `--force-unlock`, the lock appears complete | §13 | `internal/state.TestLock`, `internal/state.TestLockFileAppearsComplete`, `internal/engine.TestRunRefusesWhenLocked` |
| R24 | Webhooks are signed with the secret, which never appears in errors or the run log | §10 | `internal/notify.TestWebhookSend`, `internal/notify.TestWebhookErrorsHideSecrets`, `internal/engine.TestNotificationsReachEveryChannel` |
| R25 | Releases verify against the published key; `allowed_signers` and the README agree | §18 | `tools/signers.TestAllowedSigners`, `tools/signers.TestReadmeQuotesTheKey` |
| R26 | Commit messages and notifications built from untrusted text are cleaned | §6.5, §10 | `internal/engine.TestUntrustedTextIsCleaned`, `internal/notify.TestTemplatedCleansCutsAndFallsBack` |
| R27 | A hook payload is read bounded, and only a state is kept from it | §6.3 | `internal/hook.TestRunRejects`, `internal/hook.TestStopPending`, `internal/hook.TestRunStoresOnlyTheState` |
| R28 | User-facing errors say what to do next | §16 | `internal/state.TestLoadRunErrors`, `internal/state.TestStaleLockNamesTheCommand`, `internal/checks.TestDoctorAllowRules` (partial: checked for state, lock and doctor errors, not for every error) |
| R31 | What a pane's process calls itself can't forge the pane's state (tmux `pane_current_command`) | §11.5 | `internal/backend/tmux.TestPane` |
| R29 | Verify and git hooks run session-written code as the owner | §16 | accepted (see Accepted risks 1) |
| R30 | A session can forge hook state, moving Needs you within `waiting_limit` | §6.3 | accepted (see Accepted risks 3) |
