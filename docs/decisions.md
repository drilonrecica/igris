# Decisions log

Each P0 task records its evidence and recommendation here. The owner approves; `SPEC.md` is amended where affected.

## P0-01 — Dependency approval list

Versions and release dates are from the Go module proxy (checked 2026-10-06). Licenses are the upstream repository licenses.

| Candidate | Purpose | Latest (date) | License | Stdlib alternative | Verdict |
|---|---|---|---|---|---|
| `charmbracelet/bubbletea` | TUI runtime (SPEC §15) | v1.3.10 (2025-09) | MIT | none practical (raw termios + redraw loop) | **Approve** |
| `charmbracelet/bubbles` | Ready-made table/viewport/textinput widgets | v1.0.0 (2026-02) | MIT | hand-roll widgets | **Approve** — only the components we use |
| `charmbracelet/lipgloss` | Styling/layout, narrow vs wide layout (SPEC §15.2) | v1.1.0 (2025-03) | MIT | raw ANSI strings | **Approve** |
| `charmbracelet/x/exp/teatest` | Drive Bubble Tea models in tests (AGENTS §5) | pseudo-version (2026-10-04) | MIT | hand-driven `Update` calls | **Approve, test-only** — pre-release (`x/exp`), so pin the exact version and keep it out of the shipped binary |
| `BurntSushi/toml` | `igris.toml` loader (SPEC §12) | v1.6.0 (2025-12) | MIT | none (no TOML in stdlib) | **Approve** — `MetaData.Undecoded()` gives the strict unknown-key errors M0-06 needs directly |
| `pelletier/go-toml/v2` | alternative TOML lib | v2.4.3 (2026-07) | MIT | — | Reject — faster, but strict mode is less ergonomic for our error messages; speed is irrelevant for one small file |
| `spf13/cobra` | CLI parsing | v1.10.2 (2025-12) | Apache-2.0 | stdlib `flag` | Reject — ~10 subcommands with few flags; `flag.FlagSet` per subcommand in `cmd/igris/` is enough and avoids a large transitive tree |
| `sergi/go-diff` / `pmezard/go-difflib` | diff rendering for `igris adapt` | v1.4.0 / v1.0.0 (2016, unmaintained) | MIT / BSD-3 | hand-rolled | Reject — `adapt` shows a line diff of a table; a small LCS line differ (~60 lines, fully tested) avoids a dependency |

### Approved list (approved by owner 2026-10-06)

- `github.com/charmbracelet/bubbletea`
- `github.com/charmbracelet/bubbles`
- `github.com/charmbracelet/lipgloss`
- `github.com/charmbracelet/x/exp/teatest` (tests only)
- `github.com/BurntSushi/toml`

Anything else (including their transitive dependencies being swapped, or adding cobra/a diff library later) needs a new decision task first (AGENTS §4). No CGO.

## P0-02 — Claude Code CLI verification

Verified on Claude Code 2.1.291 (2026-10-06) in a scratch git repo outside this project, with `haiku` for probes (headless `-p` plus one interactive session inside herdr). No credentials were read or printed.

| Item | Result |
|---|---|
| `--model` aliases | `fable`, `opus`, `sonnet`, `haiku` all work → `claude-fable-5-1`, `claude-opus-5-5`, `claude-sonnet-5-5`, `claude-haiku-4-5-20251001` |
| `--permission-mode` values | `acceptEdits`, `auto`, `bypassPermissions`, `manual`, `dontAsk`, `plan` (no `default`; omit the flag for default) |
| `--dangerously-skip-permissions` | Runs Bash with no prompt. `--permission-mode bypassPermissions` also starts fine |
| `auto` mode | Does **not** approve an unfamiliar command like `igris done` (headless: denied; interactive: approval prompt, agent status `blocked`). Needs the allow rule |
| Plan mode vs Bash | Blocked, even with the allow rule present. `igris done` is only possible after plan approval (approval step itself *not* exercised — needs the owner) |
| Allow-rule syntax | `Bash(igris done:*)` and `Bash(igris done *)` both work in `.claude/settings.local.json` |
| `--model` vs settings `model` | `--model` wins (settings `haiku`, flag `sonnet` → sonnet ran) |
| `--session-id <uuid>` + `--resume <uuid>` | Works; same session id returned. `--resume` with a different `--model` runs the new turn on that model |
| `--append-system-prompt` | Works; on `--resume` without re-passing it, the session still knew the appended secret word. Claude Code records the system prompt per conversation (`--system-prompt-snapshot`), so igris need not re-pass it on resume (re-passing does no harm) |
| `--fallback-model` | Documented as a fallback list "when the default model is overloaded or not available" (also retried per turn). Not exercised (can't force an overload); kept forbidden in `extra_args` (SPEC §7.4) |
| `ANTHROPIC_API_KEY` | Takes precedence over the subscription login (bogus key → warning "takes precedence over your claude.ai login", request fails). Igris must never set it and `check` should warn if present |
| Initial prompt as positional arg, interactive | Works for a single-line prompt, but **herdr rejects multi-line args**, so igris can't use it for the task prompt — superseded by P0-03 (prompt is sent with `herdr agent prompt`) |
| `--append-system-prompt-file <path>` | Works on 2.1.291 (file contents appended to the system prompt; verified with a marker word) although `--help` lists only `--append-system-prompt`. Used for the multi-line igris rules |
| Folder-trust prompt | New directories show "Quick safety check… trust this folder" and block startup; herdr reports `agent_not_ready` and status `blocked` (default selection is "No, exit") |

SPEC changes: §7.1 (flag notes, `auto`/plan/allow-rule findings), §7.4 (alias list, resume model precedence, API-key warning).

Follow-ups for dependent tasks: M2-05 builds argv from these flags; the "needs trust" and "needs approval" states both surface as herdr `blocked` and must map to **Needs you**.

## P0-03 — herdr CLI verification

Verified on herdr 0.9.1 (server running, `private_protocol` 22) from inside a herdr pane, using only tabs created for the probe (all closed afterwards). Fixtures (paths and home dir scrubbed to `/work/demo` and `/home/user`) are in `internal/backend/herdr/testdata/`.

| Call | Result | Fixture |
|---|---|---|
| `tab create --workspace --cwd --label --no-focus` | `.result.tab.tab_id`, `.result.root_pane.pane_id` (as SPEC) | `tab_create.json` |
| `agent start <name> --kind claude --pane … -- --model haiku` | Returns once Claude is ready: `.result.agent.agent_status` `idle`, `interactive_ready: true`, plus `argv` | `agent_start_ok.json` |
| `agent start` on an untrusted folder | `agent_not_ready` ("blocked during startup"), exit 2; agent name stays valid; pane status `blocked` | `agent_start_not_ready.json` |
| `agent start … -- <arg containing newline/tab/CR>` | **`invalid_agent_argument`** — control characters can't be encoded. Quotes, backticks, `$`, unicode and 5000-char args are fine. Timeout must be >3000 and ≤300000 ms (`invalid_agent_timeout`) | — |
| `agent prompt` multi-line text (`--wait`) | Delivered intact as a multi-line message (bracketed paste). `--wait` returned at `blocked` when Claude asked for approval | `agent_prompt_wait_blocked.json` |
| `agent prompt` while blocked | `agent_blocked` error | `error_agent_blocked.json` |
| `agent prompt` without `--wait` | Returns immediately with the *pre-turn* status | `agent_prompt_nowait.json` |
| `pane get` | `.result.pane.agent_status` seen: `unknown` (plain shell), `idle`, `working`, `blocked`, `done` | `pane_get_*.json` |
| `agent wait` | Default: returns at first settled state (`idle`/`done`/**`blocked`**), instantly if already settled. `--until working --timeout 2000` → `timeout` error. It **cannot replace polling for Needs-you** (blocked is returned as success), but is good for "wait until ready for verify feedback" | `agent_wait_settled.json`, `error_wait_timeout.json` |
| `agent read --source recent-unwrapped --lines N` | Plain text transcript. Empty while a permission dialog is drawn; `visible` works then | `agent_read_recent_unwrapped.txt` |
| `tab focus` | `.result.tab` with `focused: true` | `tab_focus.json` |
| `tab close` | `{"result":{"type":"ok"}}`; closing again → `tab_not_found` | `tab_close.json`, `error_tab_*.json` |
| `notification show … --sound done` | `{"result":{"shown":true,"reason":"shown"}}` | `notification_show.json` |
| Errors | JSON on stderr `{"error":{"code","message"},"id"}`; **not-found codes are specific** (`pane_not_found`, `agent_not_found`, `tab_not_found`), not `not_found` | `error_*.json` |
| `herdr integration status` | Read-only. On this machine `claude: not installed` (would install `~/.claude/hooks/herdr-agent-state.sh`). I did **not** run `install` (it edits the owner's Claude config). Agent states were still detected correctly without it, so it is a "nice to have", not a requirement | `integration_status.txt` |

### Design consequences (SPEC amended)
1. **Task prompt goes through `agent prompt`, not argv** (§6 steps 4–5, §11.2). Igris rules go via `--append-system-prompt-file` (P0-02).
2. **`blocked` covers both "folder trust" and "permission prompt"**; both map to **Needs you**.
3. Not-found handling uses `pane_not_found` for "session lost".
4. `herdr integration install claude` stays a recommendation in `init`/`check` (optional), matching §11.2.

Open for the owner: whether to run `herdr integration install claude` on this machine.

## P0-04 — Release tooling

Requirements (SPEC §18): static `CGO_ENABLED=0` binaries for linux/darwin × amd64/arm64, checksums, `make release-local`, never publish automatically, `go install …/cmd/igris@latest` keeps working.

| Option | Pros | Cons |
|---|---|---|
| **GoReleaser** (already installed on the owner's machine) | One declarative `.goreleaser.yaml`; cross-compiles all four targets, archives, `checksums.txt`, `--snapshot` for local dry runs, changelog; can add Homebrew tap/signing later with a few lines | One more dev tool (not a Go dependency, so AGENTS §4 is unaffected); config schema changes between majors |
| Plain release script | No tooling | ~40 lines of bash to re-implement archive naming, checksums, ldflags; more room for platform bugs |

**Recommendation: GoReleaser.**
- Config: `builds` with `CGO_ENABLED=0`, `goos: [linux, darwin]`, `goarch: [amd64, arm64]`, `-s -w` plus `-X main.version={{.Version}}`; `archives` tar.gz with LICENSE/README; `checksum: checksums.txt` (sha256). `release.disable: true` / run with `--skip=publish` so tooling never publishes; the owner creates the GitHub Release and uploads `dist/*` (M7-07).
- `make release-local` = `goreleaser release --snapshot --clean`; run `goreleaser check` as part of it.
- **Signing: no for v0.1.0.** Checksums protect against corruption, and `go install` provides module-proxy/checksum-db verification. Revisit with keyless cosign if/when CI publishes (P0-07).
- **Homebrew tap: later** (post-v1); GoReleaser's `brews` section can be added without changing the pipeline.

SPEC §18 amended accordingly. **Decided 2026-10-06** (owner delegated the call): GoReleaser, checksums only, no signing, Homebrew tap later. Task M7-03 implements it.

## P0-05 — GitHub repository

Checked read-only on 2026-10-06 (unauthenticated GitHub API, `git ls-remote`, Go module proxy). The owner had already created the repo; the agent made no changes to GitHub (`gh` currently reports an invalid token).

| Check | Result |
|---|---|
| `github.com/drilonrecica/igris` | Exists, **public**, default branch `master`, not archived |
| Module path `github.com/drilonrecica/igris` | Owned by the same account; Go proxy lists no versions yet (path unclaimed by anyone else) |
| Remote state | `origin/master` is at `a2ec189`; local commits since then (including P0-01…P0-05) are **not pushed** |
| Description | `Arise. Runs your tasks.md one fresh Claude Code session at a time, each with the model rank your plan assigns.` — differs from the planned text |
| Topics | none set |
| License | none detected on GitHub yet (M0-01 adds the MIT `LICENSE`) |

Owner to do (needs a valid `gh` login, e.g. `! gh auth login -h github.com`), if the planned description and topics are wanted:

```bash
gh repo edit drilonrecica/igris \
  --description "Run your task plan one Claude Code session at a time, with the right model for each task." \
  --add-topic claude-code --add-topic ai-agents --add-topic task-runner --add-topic golang --add-topic tui --add-topic herdr
```

The current description is also fine; this is a naming/branding call for the owner.

## P0-06 — Default task prompt

**Draft, committed for owner review of the wording** (2026-10-06). Files:

- `internal/prompt/rules.md` — the igris rules, written to `.igris/prompts/<ID>.rules.md` and passed with `--append-system-prompt-file` (P0-02/P0-03). Static on purpose: no template variables, so they stay correct even after the first message is compacted away. They state that they override plan/AGENTS/CLAUDE rules (notably "agent updates Status").
- `internal/prompt/task.md.tmpl` — `text/template` with exactly the SPEC §6.1 variables; sent as the first user message via `herdr agent prompt`. Conditional blocks:
  - `Resumed` → inspect `git status`/`git diff` and continue; with `commit = never` it adds that the diff may contain earlier tasks' changes.
  - `CommitPolicy` `never` → don't commit and leave earlier uncommitted changes alone; otherwise "igris commits after verification".
  - `Owner == "agent + user"` → present recommendation/result and wait for explicit decision/sign-off before finishing.
  - `Deps` and `Extra` are listed only when present.

Design choices:
- The non-negotiable parts (one task, no Status edits, no commits, ask-and-wait, `igris done` last, no self-initiated `igris skip`, fix-the-cause on verify feedback) are in the rules *and* briefly restated in the task prompt where they depend on per-run values.
- No template functions are used, so a custom `prompt_template` override needs nothing beyond stock `text/template`.
- Rendered with a scratch program for fresh/resumed × `ask`/`never` × `agent`/`agent + user`: no `<no value>`, no double blank lines. M2-04 turns this into golden tests.
