# Threat model

What igris protects, from whom, where the boundaries are, and which test enforces each safety rule. SPEC §16 is normative; this document explains it and keeps its rules tied to tests. It is updated with each security review (V06-07, then V10-05, V2R-04, V2X-03, V16-05) and with every change to SPEC §16. Written for v0.6 (task V06-06).

## Trust model

- **Trusted:** the owner, and `igris.toml` as the owner wrote it when the run started (the config snapshot, SPEC §13).
- **Not trusted:**
  - a Claude Code session. In `accept`, `auto`, `yolo` and `plan:yolo` mode (and `default` when Claude Code's own default is auto), and in a plan mode that continues in `accept` or `auto` once its plan is approved, it can write any file in the project without a prompt, including the plan, `igris.toml` and `.igris/`. It controls its done note, what `verify` prints, and the hook payloads Claude Code sends.
  - the plan's text (a plan someone else wrote, or one `igris adapt` produced);
  - the output of any command;
  - the network;
  - since 1.6, callers of `igris serve`'s HTTP listener until they present the serve token, and even then they never reach skip-permissions mode (B9);
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
| GitHub's releases API, or whoever answers in its place (since 1.2; only when the owner checks for updates) | Answer with any status, body or tag; delay or drop it | Show the owner text (only a `vX.Y.Z` tag is used), make igris read more than 1 MiB or wait more than 10 s, put anything but a version into the update command, or replace the binary (the package manager installs it) |
| Remote caller of serve's HTTP listener (since 1.6; only when the owner turns it on) | Reach the address it binds (loopback through `ssh -L`, the tailnet, or with `--listen-public` whatever routes to it); with the serve token, do what an attached client does | Reach a listener the owner didn't turn on, guess the token (rate-limited, 256 bits), forge a cookie-authenticated request from another site, or set skip-permissions mode |
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
   B6 project root ↔ run worktree (.igris/worktrees/<run-id>[-<ID>])   B7 release build ↔ user (signatures)
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
| A session adds an include to the plan (`<!-- igris:include PATH -->`, since 1.2) to pull a file in, or points one at a file outside the project, at a symlink that leaves it, a FIFO, a device or a huge file | An include is a plan edit: the planwatch re-reads every file of the plan at each poll and an added or removed include shows as rows added or removed, so it holds the run until the owner confirms (SPEC §5.4). The path must be relative, without `..`, not under `.igris/`, and inside the project root once symlinks are resolved; the file is read as a regular file of bounded size, never blocking; at most 32 files and 16 MiB in all, one level, each file once (SPEC §3.7). |
| An included file is replaced between igris's read and its write | A Status update re-reads and hashes every file of the plan, writes each changed file to a temp file, re-hashes all of them and only then renames; a change in between retries from the read, three times, and then gives up without writing (SPEC §4). Symlinks stay symlinks and a file's mode is kept. |
| `igris adapt` pulls in files the owner never reviewed through an include in its proposal | `adapt` refuses a plan that has includes, and a proposal with an include line fails its review's validation (SPEC §9). |
| A plan template overwrites the owner's plan, or writes the configuration | `igris init --template` creates the plan only when there is none (`O_EXCL`), never touches an existing `igris.toml`, and the template's text is embedded in the binary, not read from the project (SPEC §14). |
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
| igris contacts GitHub on its own (a check at start or on a timer tells GitHub when and from where igris runs) | Never: only `igris update` and home's `u`, which the owner starts; not in the run view or a `--no-tui` run, and no setting turns a check on (SPEC §1, §14, §15.6). |
| A forged, huge or slow answer to the update check (a proxy from the environment, an intercepting middlebox) | Read with a 10 s timeout and at most 1 MiB; only `tag_name` is decoded and used, and only as `vX.Y.Z`; none of the answer's text is shown, the release link is built by igris, and errors name the status code only (SPEC §14, §16). |
| The answer smuggles a command or flags into the update | The command is fixed (`brew upgrade igris`, or `go install github.com/drilonrecica/igris/cmd/igris@vX.Y.Z` with `GOBIN`), only the shape-checked version goes into it, as argv through the command runner with a timeout, never a shell; it runs after the owner says yes (or passes `--yes`), never while a run holds the project on home, and without the notification secrets' variables (SPEC §14, §16). |
| A forged answer gets a malicious binary installed | igris never downloads or replaces its own binary: Homebrew checks the formula's sha256 and Go the checksum database, so a forged version is at worst one the package manager refuses or resolves to the real release; an archive install only gets the signed release's verify steps (B7; accepted, below). |

### B6 — project root ↔ worktree

| Threat | Mitigation |
|---|---|
| The run's changes reach the owner's checkout | In worktree mode sessions, verify, hooks and commits run in `.igris/worktrees/<run-id>`; igris never merges (SPEC §6.8). |
| Worktree mode widens what a session may do | Only the project's own `igris done` rules and reading the plan (B1). |

Since 1.5 the same boundary holds for the task worktrees of a parallel phase (`.igris/worktrees/<run-id>-<ID>` on `igris/<run-id>-<ID>`, SPEC §6.11).

| Threat | Mitigation |
|---|---|
| A session rewrites `state.json`'s `active` list to point igris at a worktree or branch that isn't the task's own (another project, another task's, the project root), so igris runs sessions, verify, commits or removals there | Each active task's worktree must be exactly `.igris/worktrees/<run-id>-<ID>` on `igris/<run-id>-<ID>` for its own task and this run; anything else is a damaged state file, refused with the file to delete (R59). |
| One task's session reads or edits another's work | It can't be prevented: the worktrees are directories of the owner's user, and in `accept`, `auto` and `yolo` mode a session can write any file. A session is told nothing of the other tasks, and each task is rebased and verified on the integrated tree before it is `done`, so changes meet in the open at integration, not silently (accepted risk 23). |
| A session writes the plan, `igris.toml` or `.igris/` through its worktree | The worktree holds none of them (the plan's copy is never written; Status lives in the main plan), and what a session writes to the main ones is as in B2 (R1, R2, R7). |
| Several sessions, verifies and git processes starve the machine or the plan writer | At most `[parallel] max` (8) tasks are active; the engine is the only writer of the plan and `state.json`; commits and integration run one at a time in completion order, as every external command runs with a timeout (R7). |
| Concurrent git operations corrupt the run branch | Integration is serial: rebase in the task's worktree, then a fast-forward only (`git merge --ff-only`); a conflict is aborted (`git rebase --abort`) and the task rerun or put to the owner, never merged by guess (R60). |
| A task's `IGRIS_SLOT` is used to reach another task's resources | A convenience, not a boundary: it names a number the project may offset ports or databases by. It carries no secret. |
| Skip-permissions mode starts N sessions at once with one confirmation | The typed phrase is still asked once per run and covers each session the run starts in that mode, as a run of one task does (R19); nothing starts parallel work in that mode without it. |

### B7 — release ↔ user

| Threat | Mitigation |
|---|---|
| A replaced download | `checksums.txt` and the tag are signed with the owner's SSH release key, kept with a passphrase only the owner types; users verify with `ssh-keygen -Y verify` and `allowed_signers` (SPEC §18). The trust root is the repository: `allowed_signers` and the README's copy of the key live there, so write access to it could replace key, tag and signature together (accepted, below). |
| A vulnerable dependency or toolchain | Pinned Go toolchain, govulncheck on push and weekly, an SBOM per archive (SPEC §18). |
| A build that isn't what the source says | The reproducible-build check builds the same commit on Linux and macOS and compares (SPEC §18). |

### B8 — `igris serve` ↔ attached clients (since 1.4)

| Threat | Mitigation |
|---|---|
| Another user drives the run through its socket | The socket is 0600 in the 0700 `.igris/`, or in a 0700 directory of the user's own (not a symlink) when the path is too long; on Linux serve checks each peer's uid (`SO_PEERCRED`) and closes any other user's connection (SPEC §13, §16). |
| A client sends huge, malformed or unexpected messages | Lines over 1 MiB, non-JSON, another protocol version, anything before `hello` and a bad command id end the connection; command text is bounded to 16 KiB and cleaned, task IDs and choices are shape-checked, and a command passes the engine's own checks, as the TUI's do (SPEC §13, §16). |
| A client that stops reading stalls the run | Each client has a bounded queue (4 MiB, 4096 messages) written by its own goroutine; a full queue drops the client, never blocks the engine (SPEC §13). |
| A session points clients at a socket of its own through `.igris/igris.sock.path` | The file is read bounded, never through a symlink, and used only when it names this root's fallback path (SPEC §13). |
| A client switches the run to skip-permissions mode | Only with the phrase typed in that client (the `mode` command's `yes`), checked by the engine as for the TUI (SPEC §7.3). |

### B9 — `igris serve`'s HTTP listener ↔ remote callers (since 1.6)

Off unless the owner sets an address (`[serve] http`, `serve --http`). A remote caller is untrusted until it authenticates; an authenticated one is an attached client short of skip-permissions mode (SPEC §14, §16, decisions.md V16-P1). V16-05 reviewed it (below).

| Threat | Mitigation |
|---|---|
| The listener is reachable from the internet or the LAN | Only loopback (`ssh -L`) and Tailscale addresses (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`) bind as they are; any other address needs `--listen-public`, serve's flag only, and TLS from the owner's certificate, and serve warns at start. Host names are refused: the rule goes by the address. A refusal starts nothing (SPEC §14). |
| Guessing or brute-forcing the token | 32 random bytes; compared in constant time; failed authentications (a wrong pairing token, cookie or bearer) are limited to 10 a minute per address (an IPv6 /64), in a bounded table whose overflow fails closed. |
| The token leaks through a URL, a log or a header | The pairing URL redirects to `/` once the cookie is set; `Referrer-Policy: no-referrer`; serve never prints the token (its output goes to `serve.log`), only `igris serve token` does, on its own terminal. The cookie is `HttpOnly`, `SameSite=Lax` (Strict is withheld by Android Chrome on a navigation another app started, which is how a phone pairs), `Secure` on TLS; a cross-site `POST` and a subresource request still go without it, and every change needs the CSRF token. |
| Cross-site request forgery, clickjacking, script injection | A cookie-authenticated `POST` needs the CSRF token (an HMAC of the owner token); a non-`GET` with a foreign `Origin` or `Sec-Fetch-Site: cross-site` is refused; commands are `application/json` only; a CSP without inline script and `frame-ancestors 'none'`, `X-Frame-Options: DENY`, `nosniff`. |
| A remote caller switches the run to skip-permissions mode | `mode` and `task_mode` naming `yolo` or `plan:yolo` are refused before the engine, also on a run whose owner typed the phrase at start (where the engine would take it without `yes`); an HTTP client never says hello, so it can't be the starter whose `yolo` a run takes (SPEC §7.3, §14). |
| A remote caller floods or stalls serve | Request headers in 10 s and 16 KiB, every body in 30 s and 1 MiB (bounded before any handler reads it, a form as much as a command), at most 16 event streams and 8 tail reads at once (`503` beyond; a socket client reads one tail at a time, and a tail is a backend read); each stream is a client with the socket's bounded queue, dropped when slow; commands are shape-checked as on the socket, and the web UI shape-checks a form before its confirmation page names anything from it. |
| A paired device is lost, or the token was seen | `igris serve token --rotate` replaces it; a live serve reads the token on every request, so the old one stops working at once and open event streams end within 15 s. |
| DNS rebinding: a site the owner visits points a name at the listener's address and reads its answers | The cookie is set for the host the pairing URL named (an address), so a request under a rebound name carries no cookie and no token: it gets only the unpaired answers (`401`, the page or text saying how to pair), which say nothing about the run; a change needs the CSRF token and a same-origin request besides, and `Origin: null` is foreign. |
| Another web service on the listener's host receives the cookie | Browsers scope cookies by host, not port: a service on another port of the same address (a dev server the project starts; a local app on the laptop behind `ssh -L`) gets `igris_token` from a paired browser and can set one in its place (a nuisance: a wrong cookie is a wrong token, `401`). On the serve machine that is risk 24; through `ssh -L` the services are the laptop's own. A bearer client sends no cookie; a loopback address of its own (`127.0.0.2`) keeps the cookie to igris (accepted, risk 25). |
| An ISP's CGNAT address binds like a Tailscale one | `100.64.0.0/10` is Tailscale's range and the carrier-grade NAT range: an interface of the machine that holds such an address from its ISP binds without `--listen-public` or TLS (accepted, risk 26). The token guards every request either way. |
| The pairing URL stays in the browser's history, or a messaging app previews it | Whoever sees the pairing URL pairs (risk 24); `igris serve token --rotate` unpairs everyone. A preview's `HEAD` is refused (`405`) and its `GET` pairs a server that never uses the cookie. |

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
19. **A split plan needs igris 1.2 everywhere** (V12-P2): an include line is an HTML comment to igris 1.0 and 1.1, which would run the main file's phases only. The format-2 marker a plan with includes must declare makes them refuse it (`plan format 2 needs a newer igris`), so a machine with an older igris stops instead of running part of the plan. A session can still write an include into a plan that declares format 2; that is a plan edit and holds the run (B2).
20. **A session can rewrite an included file as it can rewrite the plan**: the Status writer only ever changes Status cells, but a session in `accept`, `auto` or `plan`-continued mode can edit any file of the plan, included or not. The planwatch compares every row of every file and holds the run (R48), so a change is reported, not applied; the owner decides.
21. **The update trusts the package manager that installed igris** (V12-P4): `igris update` runs `brew upgrade igris` or `go install …@vX.Y.Z` as the owner, with igris's environment minus the notification secrets' variables, and the new binary is as good as the tap's formula or the Go module proxy and checksum database. A forged answer to the check can only name a version: a higher one that doesn't exist makes `go install` fail (or Homebrew upgrade to what its tap has), and it never makes igris download anything. A machine that reaches GitHub through a proxy it doesn't trust should not use `igris update`.

22. **Any process of the owner's user can drive a run over the socket** (V14-P1): it can attach, answer questions, skip, stop and switch modes, skip-permissions included with the `yes` a client sends after its owner typed the phrase. Such a process can already edit the plan, `igris.toml` and `.igris/`, and run `claude` itself; the socket is guarded against other users, not against the owner's own programs.

23. **Parallel tasks share the machine and a mode** (V15-P1): the sessions of a parallel phase run as the owner at the same time, each able to read or write the other tasks' worktrees and everything else the permission mode lets it. Isolation is for the code (one branch and tree per task, a rebase and verify before integration), not for a hostile session. Verify and git hooks of N worktrees run session-written code as the owner (risk 1). Run a phase in parallel only where you would run one task unattended; worktree mode (`--worktree`) keeps the whole run off your branch.

24. **Whoever holds the serve token controls the run** (V16-P1): a paired phone, someone who saw the pairing URL or its QR code, or a process of the owner's user (a session included) that reads `.igris/serve-token` can do what an attached client does, short of skip-permissions mode, until the owner rotates the token. A session can also replace the file with a token of its own, as it can drive the socket (risk 22). The listener is off by default and binds only loopback and Tailscale addresses without `--listen-public`.

25. **The cookie reaches every port of the listener's host** (V16-05): browsers scope cookies by host, not port, so another web service on the same address, a dev server the project starts or a local app on the laptop that `ssh -L` runs on, receives `igris_token` from a paired browser; `HttpOnly`, `SameSite=Lax` and `Secure` don't change that, and `__Host-` prefixes need TLS. On the serve machine a local process can read `.igris/serve-token` anyway (risk 24). Rotate the token when such a service isn't yours; a bearer client (`Authorization: Bearer`) sends no cookie; a loopback address of its own (`ssh -L 127.0.0.2:7777:127.0.0.1:7777`, with `[serve] http = "127.0.0.2:7777"` or the URL edited) keeps the cookie to igris.

26. **Tailscale's IPv4 range is the CGNAT range** (V16-05): `100.64.0.0/10` is what Tailscale assigns and what ISPs assign behind carrier-grade NAT. A machine whose own interface holds an ISP's CGNAT address (a tethered or cellular machine; a router's WAN address never reaches igris's host) binds it as a Tailscale address: plain HTTP, no `--listen-public`, on a wire that is the ISP's. The token guards every request; telling the interface apart would need the `tailscale` command or its socket, which igris doesn't call.

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

### V16-05 (2026-10-22, v1.6.0)

The remote boundary (B9) read on the Fable model against SPEC §10, §14, §15.8 and §16 and decision V16-P1: authentication and the token file, pairing, CSRF and origin checks, the headers, the bind rules and TLS, the ntfy action tokens, the rate limit, the bounds, skip-permissions mode and the web UI's output. Baseline: the serve package's tests under the race detector.

| # | Area | Finding | Severity | Outcome |
|---|---|---|---|---|
| 1 | bounds | A form posted to the web UI with the CSRF token in the header was parsed by the UI under Go's own 10 MiB limit, not the listener's 1 MiB: the listener bounded only the bodies it read itself. | low | Fixed: every body is bounded at 1 MiB before any handler reads it (R68) |
| 2 | bounds | Tail reads over HTTP had no bound: a socket client reads one at a time, an HTTP client could start a backend read (`tmux capture-pane`, a herdr call) per request. | low | Fixed: at most 8 at once, `503` beyond (R68) |
| 3 | web UI | The confirmation page named the form's task before the command was shape-checked: escaped by `html/template`, but of any length and any text. | info | Fixed: the shape check comes first (R72) |
| 4 | protocol | The error for an unknown message type quoted the type whole and uncleaned, on the socket and over HTTP; a 1 MiB type made the socket's reply too long to send. | info | Fixed: cut to 64 characters and cleaned (R55) |
| 5 | cookie | Browsers scope cookies by host, not port: other web services on the listener's address receive the cookie. | low | Accepted (risk 25); V16-06 says so on the site page |
| 6 | bind rules | `100.64.0.0/10` is also the ISPs' CGNAT range. | info | Accepted (risk 26) |
| 7 | pairing | The pairing URL stays in a browser's history and is fetched by link previews. | info | Risk 24 covers it: a preview's `HEAD` is refused, its `GET` pairs a server that never uses the cookie |

Checked and holding: the token is compared in constant time and read from its file on every request, so a rotation ends every session at once and every stream within a ping; it is never logged, never in a notification and never in a URL after the `303`; a wrong pairing token, cookie or bearer counts against its address and a missing one doesn't; the limiter's table is bounded and fails closed; a cookie-authenticated `POST` needs the CSRF token (an HMAC of the owner token, so a rotation changes it, and nothing can be derived from it), and a non-`GET` with a foreign `Origin`, `Origin: null` or `Sec-Fetch-Site: cross-site` is refused whatever its credentials, while a bearer request carries nothing ambient and a cross-site request with a custom header never leaves the browser's preflight; the headers go on every response, errors and static files included; the `mode` and `task_mode` refusal lowercases and trims as the engine does and comes before the engine, so a run whose owner typed the phrase takes nothing from HTTP, their `yes` is dropped, the web UI's select offers no such mode, and `hello` is refused; action tokens are 32 random bytes kept as their SHA-256, one answer of one question each, bounded in number, refused once the question was answered by any client or settled by the run, and a `GET` on one touches nothing; the SSE stream is a bounded socket client dropped when slow, 16 at once; headers and bodies have deadlines; TLS is 1.2 or later from the owner's files, `Secure` and HSTS only then; `RemoteAddr` is the rate limit's key, never `X-Forwarded-For`; the UI is `html/template` under a CSP without inline script or style, tails reach the page as text nodes, the static server serves two files, and the only redirect is to `/`; the token file is 0600, read bounded and never through a symlink, and created with a link so two igris can't race.

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
| R42 | A side session never skips permissions: only `default`, `manual`, `accept`, `auto` and `plan`, and `claude.extra_args` checked as for a task; the dialog offers no other mode and `[side] mode` can't name one | §6.10, §7.4, §12 | `internal/side.TestOpenRefuses`, `internal/state.TestSideRecordShape`, `internal/tui.TestSideDialogDuringRun`, `internal/config.TestSide` |
| R43 | `igris done`, `skip` and `reset` refuse inside a side session | §6.2, §6.10 | `cmd/igris.TestSideSessionRefusesSignals` (partial: a session can unset `IGRIS_SIDE`, see Accepted risks 18) |
| R44 | A side session's first prompt (plan text, run-log details, what the owner typed or pasted) is cleaned of escape sequences and control characters and at most 8 KiB before it is drawn or typed into the pane; the igris problem prompt holds no config values or secrets (doctor's checks are named, not quoted) | §6.10, §16 | `internal/side.TestTaskPrefill`, `internal/side.TestIgrisPrefill`, `internal/tui.TestSidePromptCleanedAndBounded` |
| R45 | A side session's transcript is read as a task's is (R38) when it ends; its `side_usage` line holds counts and model ids only, the side ID in it is shape-checked when read, and it is listed apart from the attempts, so it can't change a task's totals or audit | §6.9, §6.10, §13, §14 | `internal/side.TestSideRunLog`, `internal/state.TestEventChecked`, `internal/report.TestUsageSide` |
| R46 | An include can't leave the project (absolute, `~`, `..`, a symlink resolving outside, `.igris/`), can't be a FIFO or device, and is bounded in size and number | §3.7, §16 | `internal/plan.TestIncludeOutsideRoot`, `internal/plan.TestIncludeSymlinkInside`, `internal/plan.TestIncludeNotRegular`, `internal/plan.TestIncludeSizeLimits`, `internal/plan.TestIncludeTooManyFiles`, `internal/plan.TestIncludeProblems` |
| R47 | The Status writer changes only Status cells in the file the row is in, keeps every other byte and symlink, and gives up rather than write over an included file that changed under it | §4 | `internal/plan.TestSplitWriterPreservesBytes`, `internal/plan.TestSplitWriterEveryCellIsolated`, `internal/plan.TestSplitWriterKeepsSymlinkAndMode`, `internal/plan.TestSplitWriterConcurrentEditRetried`, `internal/plan.TestSplitWriterIncludeRetargeted`, `internal/plan.TestSplitWriterGivesUpAfterThreeAttempts` |
| R48 | An edit to any file of a split plan, or an include added, removed or moved, holds the run | §5.4 | `internal/engine.TestSplitPlanIncludedEditHoldsTheRun`, `internal/engine.TestPlanWatchSplitPlan` |
| R49 | `adapt` refuses a plan with includes before opening a session | §9 | `internal/adapt.TestRunRefusesSplitPlan` |
| R50 | `init --template` never overwrites a plan and writes only the plan | §14 | `cmd/igris.TestInitTemplateNeverOverwrites`, `cmd/igris.TestInitTemplateUsageErrors` |
| R51 | The update check runs only when the owner asks; GitHub's answer is read with a timeout and at most 1 MiB, and only a `vX.Y.Z` tag is used, none of its text shown | §1, §14, §16 | `internal/update.TestLatest`, `internal/update.TestLatestNetworkErrors`, `cmd/igris.TestUpdateCheck`, `internal/tui.TestUpdateNotInTheRunView` |
| R52 | The update command is built from the version only, runs as argv with a timeout after the owner confirms, never during a run on home, and without the notification secrets' variables; igris never replaces its own binary | §14, §15.6, §16 | `internal/update.TestDetectAndFor`, `internal/update.TestRun`, `cmd/igris.TestUpdateAsks`, `cmd/igris.TestUpdateYes`, `internal/project.TestServicesUpdate`, `internal/tui.TestUpdateNotDuringARun` |
| R53 | The update trusts the package manager that installed igris | §14 | accepted (see Accepted risks 21) |
| R54 | Only the owner's user reaches the run's socket: 0600 in a 0700 directory of the user's own, never a symlink; on Linux the peer's uid is checked | §13, §16 | `internal/serve.TestRealSocket`, `internal/serve.TestServeRefusesAnotherUser`, `internal/serve.TestPeerUID`, `internal/serve.TestListenFallback`, `internal/serve.TestListenFallbackDirSymlink`, `internal/serve.TestListenStaleLiveAndInTheWay` |
| R55 | Every socket message is bounded and shape-checked before it reaches the engine; malformed input ends the connection; an error names a client's text cut and cleaned | §13, §16 | `internal/serve.TestMalformedEndsConnection`, `internal/serve.TestCommandsRefused`, `internal/serve.TestCommandsMirrorEngine`, `internal/serve.TestEventLinesAreBounded`, `internal/serve.TestHTTPUnknownTypeIsCut` |
| R56 | A slow client is dropped, never waited for; the engine is never blocked by a client | §13 | `internal/serve.TestSlowClientDropped`, `internal/serve.TestSlowClientDroppedByBytes` |
| R57 | `.igris/igris.sock.path` is used only when it names this root's fallback socket | §13 | `internal/serve.TestSocketPathIgnoresForeignPathFile` |
| R58 | Any process of the owner's user can drive a run over the socket | §16 | accepted (see Accepted risks 22) |
| R59 | A task worktree named in `state.json` is the task's own, `.igris/worktrees/<run-id>-<ID>` on `igris/<run-id>-<ID>`, and the active list is checked (IDs, slots, modes, attempts) | §6.11, §13, §16 | `internal/state.TestLoadRunVersions`, `internal/state.TestLoadRunErrors` |
| R60 | A task is integrated one at a time, by rebase and fast-forward only; a conflict is aborted, never merged, and verify runs on the integrated tree before `done` | §6.11 | `internal/engine.TestIntegrationInCompletionOrder`, `internal/engine.TestIntegrationConflictRerunsOnce`, `internal/engine.TestIntegrationVerifyFailsOnIntegratedTree` |
| R61 | A parallel phase never runs more than `max` tasks, rank caps, `Group` and owner-task exclusivity hold, and skip-permissions mode still needs its typed phrase | §5.1, §7.3 | `internal/plan.TestSelectParallelProperties`, `internal/engine.TestParallelE2ECapsGroupsAndExclusivity` |
| R62 | A parallel phase that can't integrate (no git, `commit = "never"`) runs one task at a time | §6.11 | `internal/engine.TestParallelWithoutCommitRunsOneAtATime`, `internal/engine.TestParallelWithoutGitRunsOneAtATime` |
| R63 | The HTTP listener binds only loopback and Tailscale addresses, unless `--listen-public` and TLS are given; a refusal starts nothing | §14, §16 | `internal/serve.TestExposureOf`, `internal/serve.TestListenHTTPRules`, `internal/serve.TestListenHTTPServesTLS`, `cmd/igris.TestServeHTTPBindRules` |
| R64 | Every HTTP request but pairing needs the owner token (cookie or bearer), compared in constant time; a damaged or unreadable token authenticates nobody; a rotation takes effect at once | §13, §14 | `internal/serve.TestHTTPAuth`, `internal/serve.TestHTTPPairing`, `internal/serve.TestHTTPEventsEndOnRotation`, `internal/state.TestServeToken`, `internal/state.TestServeTokenUntrusted` |
| R65 | Failed authentication is rate-limited per address in a bounded table | §14 | `internal/serve.TestHTTPRateLimit`, `internal/serve.TestLimiterBounded`, `internal/serve.TestAddrKey` |
| R66 | A cookie-authenticated change needs the CSRF token; a cross-site non-GET is refused (but an ntfy answer, which nothing ambient authenticates); every response has the security headers | §14 | `internal/serve.TestHTTPCSRFAndOrigin`, `internal/serve.TestHTTPSecurityHeaders` |
| R67 | Skip-permissions mode is never set over HTTP, and an HTTP client is never the starter | §7.3, §14, §16 | `internal/serve.TestHTTPCommands`, `internal/serve.TestHTTPNeverYoloOnAConfirmedRun`, `internal/serve.TestHTTPEventsSnapshotThenEvents` |
| R68 | HTTP requests and event streams are bounded (every body 1 MiB, 16 streams, 8 tail reads); a slow stream is dropped, never waited for | §14 | `internal/serve.TestHTTPCommands`, `internal/serve.TestHTTPEventStreamsBounded`, `internal/serve.TestHTTPSlowStreamDropped`, `internal/serve.TestHTTPTailsBounded`, `internal/serve.TestUIFormBodyBounded` |
| R69 | Serve never prints the serve token | §14 | `cmd/igris.TestServeHTTPListener` |
| R70 | Whoever holds the serve token controls the run, short of skip-permissions mode | §16 | accepted (see Accepted risks 24) |
| R71 | An ntfy answer button answers one question of one task, once, within 10 minutes: its token is random, never the owner token, kept only as its SHA-256 in serve's memory; a used, expired, unknown or superseded token, or one whose question was answered elsewhere, is refused with 410 and the run is not touched; only POST answers; unknown tokens count as failed authentication; the tokens kept are bounded | §10, §14, §16 | `internal/serve.TestActionAnswersOnce`, `internal/serve.TestActionExpires`, `internal/serve.TestActionAnsweredElsewhere`, `internal/serve.TestActionRefusals`, `internal/serve.TestActionTokensBounded`, `internal/serve.TestActionButtonsOnlyForThePendingQuestion` |
| R72 | The web UI shape-checks a form before any of it is shown back; what it shows is escaped by `html/template` under a CSP without inline script, and it offers no skip-permissions mode | §15.8, §16 | `internal/serve.TestUIFormCheckedBeforeConfirm`, `internal/serve.TestUIPagesKeepTheCSPAndNoYolo`, `internal/serve.TestUIFormActions` |
| R73 | The cookie reaches every port of the listener's host | §16 | accepted (see Accepted risks 25) |
| R74 | Tailscale's IPv4 range is the CGNAT range | §14, §16 | accepted (see Accepted risks 26) |
| R29 | Verify and git hooks run session-written code as the owner | §16 | accepted (see Accepted risks 1) |
| R30 | A session can forge hook state, moving Needs you within `waiting_limit` | §6.3 | accepted (see Accepted risks 3) |
