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
- `github.com/aymanbagabas/go-osc52/v2` (added by V02-P1, 2026-10-07: promoted from indirect to direct; already in the module graph through Lip Gloss)

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

Re-verified on Claude Code 2.1.292 (2026-10-07) with `docs/reverify.md`: unchanged.

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

Re-verified on herdr 0.9.1 (2026-10-07) with `docs/reverify.md`: shapes unchanged except new, additive `agent_session` objects; `agent_not_ready` now exits 1 (igris reads only the error code). The integration is installed on this machine now (`claude: current (v10)`, fixture `integration_status_current.txt`).

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

## P0-07 — CI policy

**Approved by owner: yes, Linux + macOS** (2026-10-06). M0-07 implements it.

- Trigger: `push` to `master` and `pull_request`; `concurrency` cancels superseded runs on the same ref.
- Jobs:
  - `lint` (ubuntu-latest): `gofmt -l` must print nothing, `go vet ./...`, golangci-lint with the M0-04 config.
  - `test` (matrix `ubuntu-latest`, `macos-latest`): `go test -race ./...`. macOS is included because darwin is a release target (SPEC §18).
- Go version from `go.mod` (`actions/setup-go` with `go-version-file`), module cache enabled.
- Hardening: `permissions: contents: read`; third-party actions pinned by full commit SHA; no secrets; no publishing, tagging, release or artifact upload steps (releases stay manual per P0-04).
- `make test` never needs herdr or Claude (AGENTS §5), so CI needs no extra tooling.

## P0-08 — TUI interaction model

**Approved by owner** (2026-10-06), after the M3-07 smoke run: answering igris by typing `done`, `y` or `retry fresh` works but is not what the owner wants day to day. The TUI should offer every option as something you can click (or tap in Termius) or reach by moving a highlight with the keyboard, like Claude Code's choice prompts and herdr's clickable UI. SPEC §15.3–15.5 and §12 are amended; the M4 rows are reworded to match.

- **Three ways to every action:** a button in a contextual action bar, focus navigation (`tab` between regions, arrows inside, `enter`/`space`), and the existing shortcut key. The bar only shows actions that apply right now.
- **Questions become dialogs:** commit, a session's skip request, session lost, stop and skip open a modal choice list focused on the safe default; options also take `1`…`9`. Destructive choices are never the default, and `esc` only ever picks the non-destructive one.
- **Mouse:** click/tap, wheel scrolling, hover highlight, on by default. `[tui] mouse = false` turns it off, because a TUI that captures the mouse makes the terminal's own text selection need `shift`+drag.
- **No new dependency:** Bubble Tea's mouse messages plus a small hand-rolled hit-region layer (each rendered button, row and option records its rectangle). `bubblezone` would do this but isn't on the P0-01 list and the layer is small.
- **Safety unchanged:** skip-permissions (§7.3) still needs the typed phrase `skip permissions`; no click or key alone can select it. Focus and hover are marked without color, so they work under `NO_COLOR`.
- `--no-tui` keeps the plain stdin commands.
- **Amended** (2026-10-06, M4-01): mouse reporting uses cell motion (presses, wheel, drags), so there is no hover highlight; SPEC §15.4–15.5 updated.
- **Amended** (2026-10-06, M4-07, approved by owner): the visual design pass. Rank colors are set in a new `[tui.rank_colors]` table, with built-in colors for the stock ranks. The default colors are the brand palette (`docs/brand/`). A new `[tui] theme = "auto" | "dark" | "light"` key overrides the background detection, which can fail in a multiplexer or over SSH. Hover stays out, as amended in M4-01. `NO_COLOR` removes color but keeps bold, faint and reverse video. No new dependency: colors go through Lip Gloss, the attributes are plain SGR codes. SPEC §12 and §15.4 updated.

## P0-09 — Project site

**Approved by owner** (2026-10-07), after v0.1.0. The project gets a one-page landing site at `https://drilonrecica.github.io/igris/`; the README stays the full documentation. Task M7-11 builds it.

| Option | Pros | Cons |
|---|---|---|
| **Hand-written HTML/CSS in `site/`** | No toolchain, nothing to keep updated, fast page | Copy is kept in step with the README by hand |
| Static site generator (Hugo, Astro, …) | Templates, docs pages later | A toolchain and its updates for a single page |

| Deploy | Pros | Cons |
|---|---|---|
| **Pages workflow (`pages.yml`)** | Deploys on every change to the site, no manual step | First workflow with write scopes |
| `gh-pages` branch, pushed by hand | CI stays fully read-only | A manual step that drifts |
| Branch source `/docs` | No workflow | Would also publish `docs/decisions.md`, the smoke checklist and so on |

Decided:
- Static, hand-written site in `site/`. `site/build.sh` assembles `_site/` from it plus the brand files in `docs/brand/` and the demo GIF in `docs/demo/`, so those assets live in one place.
- `.github/workflows/pages.yml` is the **only** workflow with write scopes: the deploy job alone gets `pages: write` and `id-token: write`, everything else stays `contents: read`. No secrets; no build, tag, release or artifact steps for igris itself (releases stay manual per P0-04). Actions pinned by full commit SHA, as in P0-07.
- Fonts: Chakra Petch 600/700 (the wordmark's typeface), self-hosted as woff2 from Fontsource under the SIL Open Font License 1.1, with `OFL.txt` next to them. It is a static asset, not a Go dependency, so AGENTS §4 doesn't apply.
- No third-party requests from the page: no web-font CDN, analytics or cookies.

## Plans without a task table

**Approved by owner** (2026-10-06), raised during M6-02. `igris check` used to accept a plan without any task table ("OK, 0 tasks"), although such a plan is almost always in another format: exactly what `igris adapt` is for. It is now a validation error, reported only when nothing else explains it (a misplaced table is reported as misplaced). SPEC §3.1 and §9.2 amended; `igris adapt` drops its own special case for it.

## V02-P1 — Onboarding scope

**Approved by owner** (2026-10-07), for v0.2. Records the onboarding decisions of the v0.2 plan (2–5) and the details of `doctor`, completions and clipboard copy. SPEC §3.4, §9, §14 and §18 are amended. Tasks V02-01…V02-08, V02-27…V02-29 build on it.

| Option | Pros | Cons |
|---|---|---|
| **`doctor` read-only, prints the fix command** | Safe to run anywhere, any time; one mental model (`check`-like) | The owner types the fix |
| `doctor --fix` | One step | Writes behind the owner's back; a second way to do what `init` and `adapt` already do |

| Option | Pros | Cons |
|---|---|---|
| **Blocker = a `user` task the waiting task depends on** | No plan-format change; igris already notifies on user tasks and waits | A row per blocker |
| New `Blocked by` / `Waits` column or status | Reads naturally | Format change, parser, adapt and docs changes; `blocked` already means "deps unmet" |

| Option | Pros | Cons |
|---|---|---|
| **Hand-written completions + hidden `igris __complete`** | No dependency (cobra stays rejected, P0-01); IDs always match the plan | Three templates to keep in step with the flags |
| cobra's generator | Free scripts | Rewrites the CLI for a feature |

Decided:
- **`igris doctor` is read-only forever.** It never writes and has no `--fix`; every problem comes with the exact command that fixes it. It reports, outside a project, that there is no `igris.toml`, and does not fail for that alone.
- **Check list, in order:** `claude` found + version; `ANTHROPIC_API_KEY`; herdr reachable / inside a pane / integration installed; git repository and dirty tree; `igris.toml` valid, all problems; plan valid, all problems, and drift; `.claude/settings.local.json` has the `igris done` allow rule (as `init` writes it) and warns if any rule allow-lists `igris skip` (SPEC §6.2: skip is deliberately never allow-listed, so a skip always goes through a permission prompt); `.igris/` modes 0700 / 0600; stale or foreign lock; project under `/mnt/` (WSL, points to `docs/check-wsl.md`); notification channels configured — never sent to.
- **Levels** `ok` / `warn` / `fail`; exit 0 unless any `fail` (exit 1); `--json` prints the results.
- **External blockers** (an owner input, a date, a deploy) are a `user` task that names the blocker, with the waiting task depending on it. No plan-format change; `igris adapt` converts prose blockers the same way and says so in `## Adapt notes`.
- **Homebrew:** the tap repository `drilonrecica/homebrew-tap` exists. The formula is generated by `make release-local` from a repo template (`tools/genformula/igris.rb.tmpl`, rendered by a small Go program from `dist/checksums.txt` into `dist/igris.rb`; GoReleaser `brews` is deprecated in 2.x and `goreleaser check` must stay clean). The owner pushes it to `Formula/igris.rb` by hand (`docs/homebrew-tap.md`). Nothing publishes automatically (P0-04 stays true).
- **Column-aware adapt review** and **hiding Task mode on user tasks** are in v0.2.
- **Completions** are hand-written per shell (bash, zsh, fish), embedded as templates, no cobra. A hidden `igris __complete <kind>` supplies phase and task IDs; it reads the plan only (no `.igris/`, no network), prints one candidate per line, and prints nothing and exits 0 on an invalid plan. It is not listed in help.
- **OSC 52 copy** on `y`: new direct dependency `github.com/aymanbagabas/go-osc52/v2` (v2.0.1, MIT), already in the module graph through Lip Gloss, so nothing new is downloaded; it is promoted from indirect to direct and added to the P0-01 approved list. Terminals without OSC 52 ignore the sequence.
- **SPEC amended:** §14 (`doctor`, `history`, `completion`, `init --example`, current run in `status`), §18 (Homebrew), §3.4 and §9 (blocker convention: "a task blocked on something outside the plan depends on a `user` task that names it").

## V02-P2 — Home screen

**Approved by owner** (2026-10-07), for v0.2. The home-screen design (private design note, accepted as written) is transcribed into SPEC §13, §14, §15 (new §15.6), §16 and §17; AGENTS §3 gains `internal/checks`, `internal/report` and `internal/project`. Tasks V02-10…V02-26 build on it.

| Option | Pros | Cons |
|---|---|---|
| **One Bubble Tea program with a screen stack** | One alt-screen session and one background detection (no flicker or misdetection over SSH); home keeps its selection across runs; `tea.Exec` for the editor just works | The run view must be embeddable (a `Leave` hook; Quit becomes **Home**) |
| One program after another, driven from `cmd/igris` | Run view untouched | A driver loop, state lost on every switch, the alt screen re-entered each time |

| Option | Pros | Cons |
|---|---|---|
| **`check` / `phases` / `status` stay working-directory-based** | No silent behaviour change in v0.2 | They differ from `arise` and home, which search upward for the root |
| Search upward like `arise` | One rule everywhere | Changes what an existing command reads without the owner asking |

Decided:
- **Entry.** Bare `igris` opens home only when stdin and stdout are character devices (`ModeCharDevice`), stdin is not `/dev/null`, and `TERM` is not `dumb`; otherwise help to stderr and exit 2, as today. No isatty dependency; the detection is a test seam. `igris arise` with no phase and nothing to resume opens the start-run wizard on a terminal (not with `--no-tui` or `--dry-run`); `igris arise PHASE` and every subcommand are unchanged.
- **One app.** Starting a run from home pushes the existing run view; leaving it (the session keeps running), a confirmed Stop, or a run ending brings the owner back to home with fresh data. At most one engine, only while the run view is on the stack; no background run in v0.2. The adapt review is embedded the same way.
- **Home keymap** `a v c i h e , n A I o`, plus `? q esc ctrl+c`. The run view's letters (`o m M p d s r x q ?`) keep their meaning; `y` is OSC 52 copy everywhere (V02-P1); `j`/`k` navigate.
- **Read-only under a foreign lock.** A new `state.PeekLock` classifies the lock without taking it (sharing the classification with `Lock`) and never creates `.igris/`. While a live igris on this host holds the lock, home offers no Arise, Init or Adapt, and asks before editing the plan.
- **Stale locks.** Today the CLI has no stale-lock prompt: `StaleLockError` (and a remote `LockedError`) say to rerun with `--force-unlock`. The wizard's **Clear the lock and start** dialog — **Cancel** default, asked per launch, never remembered — is the TUI equivalent of that flag, not a new behaviour. Home never clears a lock outside the wizard.
- **Wizard confirmations** mirror `arise`: same defaults (Cancel), same meaning; yolo always needs the typed phrase; all answers belong to one launch and are thrown away (SPEC §7.3 holds).
- **Settings is read-only; editing goes through the owner's editor.** Igris never rewrites `igris.toml`, and writes the plan only in Status cells, as before. `$VISUAL`, else `$EDITOR`, split with `strings.Fields`, absolute path last, no shell; with neither set igris asks (no silent `vi`). The editor is the one external process without a timeout (SPEC §16).
- **Secrets** never appear on Settings, Doctor or Notify test; they show only whether a secret is set and from where.
- **Plan commands stay working-directory-based** in v0.2 (`check`, `phases`, `status`, `--plan` as today).
- **Tests:** teatest goldens for home, pages and wizard at 120×40, 80×24 and 50×20 against a fake services seam; escape-sequence injection into every untrusted source; existing run-view goldens unchanged.
- **Layout:** everything stays in `internal/tui` (new `app_*`, `home_*`, `page_*` files); a subpackage would export internals for no gain. New packages `internal/checks`, `internal/report`, `internal/project` (AGENTS §3).
- **SPEC amended:** §13 (reading state without a run, `PeekLock`, read-only home), §14 (bare `igris`, `arise` wizard fallback, stale lock vs `--force-unlock`, plan commands stay cwd-based), §15 (screen stack, **Home** label, `y`, new §15.6), §16 (editor exception, secrets on home), §17 (home goldens).

## V03-P1 — Hook-based agent state

**Approved by owner** (2026-10-07), for v0.3. Verified on Claude Code 2.1.293 in a scratch repository, sessions driven through a scratch tmux server; sanitized payloads are in `internal/hook/testdata/`. SPEC §6.3, §7.4, §11, §13 and §14 are amended. Tasks V03-01…V03-03 build on it.

| Option | Pros | Cons |
|---|---|---|
| **Per-session `--settings` file, written by igris, containing only `hooks`** | Nothing persistent; the owner's own sessions never run `igris hook`; nothing for `init`/`doctor` to repair; the hook command names the exact igris binary | igris passes `--settings` itself (still forbidden in `extra_args`) |
| Merge hooks into `.claude/settings.local.json` in `igris init` | Visible, one place | Every Claude session in the project runs the hooks; existing projects need `init` again; the binary path goes stale after an upgrade |

| Option | Pros | Cons |
|---|---|---|
| **On herdr, hook state only fills in when herdr says `unknown`** | herdr's integration (screen-based) stays authoritative where installed; no behavior change for current users | Two sources to reason about |
| Hook state always wins | One source everywhere | Changes working herdr setups for no gain |

Verified behavior:
- A hooks-only file passed with `--settings` merges with the project's settings: the `Bash(…)` allow rules in `.claude/settings.local.json` still apply.
- Hooks run through `/bin/sh -c` with the event JSON on stdin. A failing hook command (exit ≠ 0 and ≠ 2) is non-blocking: Claude Code shows "Failed with non-blocking status code" and carries on. Exit 2 would block a `UserPromptSubmit`, so `igris hook` always exits 0.
- No hook fires while the folder-trust question is shown; `SessionStart` (`source` `startup`, `resume`, `clear`, `compact`) fires once the input box is up.
- `session_id` is the UUID igris passes with `--session-id`.
- Event order for a turn: `UserPromptSubmit` → (`PreToolUse` → `PostToolUse`)… → `Stop`. A permission prompt: `PreToolUse` → `PermissionRequest` → `Notification` (`notification_type` `permission_prompt`, ~6 s later). An `AskUserQuestion` question: `PreToolUse` (`tool_name` `AskUserQuestion`) → `PermissionRequest` → `Notification` `permission_prompt`. After ~60 s idle: `Notification` `idle_prompt`. `/exit`: `SessionEnd` (`reason` `prompt_input_exit`).

Decided:
- **Event → state:** `SessionStart` (startup, resume, clear) → `idle` (`compact` is ignored: it fires mid-turn); `UserPromptSubmit`, `PreToolUse`, `PostToolUse` → `working`; `PreToolUse` for `AskUserQuestion` or `ExitPlanMode`, `PermissionRequest`, `Notification` other than `idle_prompt` → `blocked`; `Notification` `idle_prompt` and `Stop` → `idle`; `SessionEnd` → `exited`. Other events are ignored.
- igris writes `.igris/hooks/<ID>.settings.json` (0600) before each session (fresh, continue and adapt) and adds `--settings <file>` to the argv. The hook command is the absolute path of the running igris plus `hook --root <project root> <Event>`, both paths single-quoted, timeout 5 s.
- `igris hook <event>` (hidden): reads at most 64 KiB of stdin, needs a UUID-shaped `session_id`, takes the project root from `--root` (falling back to walking up from its working directory), writes `.igris/agent-state/<uuid>.json` atomically (`{"state","event","at"}`, 0600, directory 0700), prints nothing, does no network I/O, gives up after 2 s and always exits 0.
- Hook state is untrusted (a session can write the files): read only as a regular file of at most 4 KiB, shape-checked, and keyed by the UUID igris generated. It can at worst raise or clear **Needs you**; only a signal or an owner action advances a task (invariant 3).
- On herdr, herdr's `agent_status` wins; hook state is used only when herdr reports `unknown` (no integration). On tmux, hook state is the state source.

## V03-P2 — tmux behavior

**Approved by owner** (2026-10-07), for v0.3. Verified on tmux 3.7c in a scratch server (`tmux -L … -f /dev/null`); outputs are in `internal/backend/tmux/testdata/`. SPEC §11 is amended. Tasks V03-04…V03-06 build on it.

Verified behavior:
- `new-window -d -P -F '#{window_id} #{pane_id}' -c DIR -n NAME -- prog arg…` prints `@N %N` and execs a multi-argument command **directly, without a shell**: arguments with spaces, `;` and `$HOME` arrive unchanged.
- **`-n` is format-expanded**: `#{pane_id}` expands and `#(cmd)` runs a shell command. `-c` is not expanded. `display-message` text is a format too. So igris escapes every `#` as `##` in window names and toasts (renders as a literal `#`).
- `load-buffer -b NAME -` reads the buffer from stdin (no argv, no expansion); `paste-buffer -p -d -b NAME -t PANE` pastes it with bracketed paste (if the application asked for it, as Claude Code does) and deletes the buffer; `send-keys -t PANE Enter` submits. A multi-line prompt arrives as one prompt.
- `list-panes -t %N -F '#{pane_id}\t#{pane_dead}\t#{pane_dead_status}\t#{pane_current_command}'` describes a pane; a missing one gives `can't find pane: %N` on stderr and exit 1. `display-message -p -t %N` on a missing pane prints an empty line and **exits 0**, so it is not used to detect a gone pane.
- Without `remain-on-exit` a window disappears when its command exits; with `set-option -w -t @N remain-on-exit on` the dead pane stays (`pane_dead` 1, `pane_dead_status` the exit code) until `kill-window`.
- `kill-window -t @N` again → `can't find window: @N`, exit 1. `select-window` on a missing window: same message.
- Outside a reachable server: `error connecting to <socket> (No such file or directory)`, exit 1.
- `tmux -V` prints `tmux 3.7c`; `display-message -p '#{version}'` prints `3.7c`.

Decided:
- The tmux backend needs `$TMUX` (igris runs inside a tmux client) and a reachable server; minimum version **3.2** (older warns via the version table, like herdr; igris never refuses for a version).
- One window per task, named `<ID> · <rank>` (escaped), started detached in the project root with `claude <args…>` as the window command, `remain-on-exit on` set on it right after creation. Window and pane IDs (`@N`, `%N`) are the session ref, shape-checked on attach.
- Prompts go `load-buffer` (stdin) → `paste-buffer -p -d` → `send-keys Enter`; the text is cleaned of control characters except newlines (as for herdr).
- `exited` when the pane is missing or dead, or the hook state says so; otherwise the hook state; `unknown` until the first hook.
- Startup: the session waits for the first hook (`SessionStart`); without one after 15 s (the folder-trust question, or a slow start) it holds the task prompt and reports `blocked`, like herdr's startup handling, and delivers it once the hook state turns `idle`.
- Errors are classified from stderr: `can't find pane`/`can't find window` → session gone.

## V04-P1 — plan format additions

**Approved by owner** (2026-10-08), for v0.4. The v0.4 plan's design, with every open point decided here. Everything is additive: a v0.3 plan and a v0.3 `igris.toml` mean what they meant before (qualified by the amendments below: a plan column that already used one of the new names is read as the new column, a config without `default_mode` runs in `auto`, and an `igris init`-written config lists its notification events explicitly). SPEC §3.2, §5, §6.1–§6.4, §6.7 (new), §10, §12, §13 and §14 are amended. Tasks V04-01…V04-07 build on it.

| Option | Pros | Cons |
|---|---|---|
| **Lint hints only under `check --strict`** | Plain `check` output stays as it is for existing plans; CI opts in | Hints are invisible until asked for |
| Lint hints always shown as `warning:` (the draft plan) | Discoverable | Every existing plan with a long cell or a `yolo` row gets new warnings on every `check` |

| Option | Pros | Cons |
|---|---|---|
| **`before_task` after the task is marked `in progress`, before its session opens** | A failure is the known "in progress, no session" state: retry, done, skip, stop already exist; a crash mid-hook resumes like any other | The plan says `in progress` while only the hook ran |
| Before marking | Plan untouched on failure | A new state with no recovery path; the hook could run twice after a crash with nothing recorded |

| Option | Pros | Cons |
|---|---|---|
| **Resetting the current task pauses the run** | The owner reset it for a reason; nothing relaunches it behind their back | One more `p` to continue |
| Reselect at once | No extra key | The reset task is usually the first ready one again, so it would just restart |

Decided:
- **Columns.** `Verify`, `Timeout` and `Context` become optional canonical columns: matched case-insensitively like the others, aliasable through `[columns]`, task fields instead of `Extra` entries (so they no longer reach the prompt as extra columns). An empty cell, `—` or `-` means "not set" in all three. They are checked only on tasks that are not `done`/`skipped` (like Model, §3.5): igris never runs a finished task, and a finished task's Context may name files that were since moved. On a `user` task each set cell is ignored with a plan warning (`check`, `arise`), like the dependency-like column hint: `M1-02: user tasks have no session, so its Verify (Timeout, Context) is ignored; clear the cell`. `status` shows a VERIFY, TIMEOUT or CONTEXT column only when some task of the plan has that cell set; `status --json` adds `verify`, `timeout` and `context` (an array) per task, omitted when empty.
- **Verify profiles.** `[verify]` maps a profile name to a shell command (`sh -c`, as `run.verify` today), in the owner's config only, never in a plan. Names match `[a-z0-9_-]+`; values must be non-empty. `run.verify` is the profile `default`; setting both `run.verify` and `[verify] default` is a config error, as is a profile named `none`. `run.verify` stays supported (not deprecated). A Verify cell names a profile (case-insensitive) or `none`, which skips verification for that task. Note that in Verify, unlike Model/Mode/Deps, `none` is not "not set": `—` falls back, `none` turns verification off. `[phases.<id>] verify = "<profile>"` (or `none`) is the phase's default; `<id>` matches phase IDs case-insensitively and `verify` is its only key. Resolution per task: the Verify cell → `[phases.<id>] verify` → `default` → no verification. An unknown profile in a cell is a plan validation error (`check`, `arise`, `status`/`phases`, which validate with the config's profile names as they do with `[models]`): `M1-03: unknown verify profile "fsat"; define it under [verify] in igris.toml or use one of: default, fast, none`. An unknown profile under `[phases.<id>]` is a config error; a `[phases.<id>]` naming no phase of the plan is a plan warning (config validation does not see the plan). `verify_timeout` and `verify_max_attempts` apply to every profile. Verify still runs only after the agent's `done` signal and is still skipped when the owner marks the task done. The failure message sent into the session, the run log's `verify_passed`/`verify_failed` detail and the dry run name the profile.
- **Timeout.** A Go duration (`time.ParseDuration`), greater than zero; anything else is a validation error (`M1-03: Timeout "45" is not a duration; write e.g. 45m or 1h30m`). The clock starts when igris sends a session's first prompt: the task prompt for a fresh session, the continue prompt for `retry continue`, and on resume the moment igris reattaches to a live session (elapsed time is not persisted). Every session igris opens or reattaches for the task is one attempt. Once the attempt runs longer than the Timeout, checked while igris watches the session: a `TaskOverdue` UI event, the task card shows **overdue** until the attempt ends, **Needs you** is raised once, the run log gets `task_overdue`, and the `task_overdue` notification goes out once per attempt. igris never closes, stops or kills the session for it. A retry starts a new attempt and a new clock. On a user task Timeout is ignored (above).
- **`task_overdue`** is a new notification event, in the default event list of every channel, urgent like `needs_input` (ntfy priority high, toast sound `request`). `config` accepts it in `events`.
- **Context.** A comma-separated list of repo-relative paths (files or directories), each entry trimmed, surrounding backticks stripped, empty entries and duplicates dropped, `/` as separator, at most 20 entries. Each entry must: not be absolute; have no `..` element; contain no control character (already true of every cell, §3.5); exist; and, with symlinks resolved (`filepath.EvalSymlinks` on the root and on the joined path), stay inside the project root. Violations are plan validation errors in `check` and `arise` (`M1-03: Context "../secrets": paths must stay inside the project; use a repo-relative path`). The root is the project root for `arise` and home, and the working directory for `check`, `phases` and `status` (the directory they read `igris.toml` from). Since every re-read of the plan validates it, a Context path deleted mid-run makes the re-read fail like any other invalid plan. The template variable `.Context` is the list as written (`[]string`); the default task prompt, when it is non-empty, lists the paths as required reading before changing anything. igris never reads or sends the files' contents.
- **Task hooks.** `[hooks] before_task = [argv…]`, `after_task = [argv…]`, `timeout = "2m"` (default 2 m, > 0). An empty or missing list means no hook; a non-empty list needs a non-empty first element, and no element may contain a control character or NUL (config errors). They come from the config snapshot (§13) and run through the command runner as argv, never a shell, in the project root, with igris's environment plus `IGRIS_TASK_ID`, `IGRIS_PHASE`, `IGRIS_RANK` (the alias), `IGRIS_MODEL` (the resolved `--model` value) and, for `after_task` only, `IGRIS_RESULT` (`done` or `skipped`). They run for agent tasks only, never for user tasks or `adapt`, and never in `--dry-run` (which says they would run). `before_task` runs before every session igris opens for the task (fresh, `retry fresh`, `retry continue`), after the task is marked `in progress` and before the pane opens; not when igris reattaches to a live session. On a non-zero exit, a timeout or a hook that can't start: no session is opened, the task stays `in progress`, **Needs you** with the choices of a lost session that has nothing to continue (retry, which runs the hook again; mark done; skip; stop), the run log gets an `error` with the task and a short reason (`before_task hook failed: exit status 1`), and `run_error` is notified. `after_task` runs once after the task is marked `done` or `skipped` (by signal or by the owner) and its session closed; a failure is a warning in the feed, an `error` in the run log and a `run_error` notification, and the run continues. On a failure the last 20 lines of the hook's output (stdout and stderr interleaved), cleaned with `textsafe`, go to the feed only; the run log and notifications get the short reason, never the output.
- **Selection.** `arise --only ID[,ID…]`, `--from ID`, `--until ID`. `--only` cannot be combined with `--from`/`--until`; an empty list or a malformed ID is a usage error (exit 2). The phase range is the positional phase and `--through` as today; with no phase named it is derived from the flags: from the phase of the earliest named task (file order) through the phase of the latest (`--from` alone or `--until` alone: that task's phase). Up front, before anything is written, `arise` exits 1 when a named ID is not in the plan, is outside the phase range, or `--from` comes after `--until` in file order (`arise: --until M3-01 is in phase M3, outside the run's phases M1…M2; widen --through or drop it`). The **slice** is the tasks of the range listed in `--only`, or from `--from` (default: the first) to `--until` (default: the last) in file order. §5.1 runs on the slice: the first slice task `in progress`, else the first unsatisfied slice task whose deps are satisfied. When unsatisfied slice tasks remain but none qualifies, each is reported (UI event and final summary: `not run: M1-05 waits on M1-04 (ready, phase M1)`), never marked, and the run moves to the next phase of the range; this is not a stuck phase (no `phase_stuck`). The run ends when the slice has nothing left to run. `phase_done` is sent only for a phase whose tasks are all satisfied. Tasks outside the slice change only through readiness sync. The interrupted task of an earlier run is still picked up first (§13), also outside the slice, and `arise` says so. `state.json` records the selection with the phases, and a bare `arise` resumes with the same slice; `run_started` names it (`phase M1; only M1-03, M1-05`). The dry run walks the slice.
- **`igris reset ID [--force]`.** Puts a task back to `ready`/`blocked` by readiness (§5.2), finds the project root like `done`/`skip`, and needs a valid plan (else exit 1 listing the problems). `in progress` → reset; `done`/`skipped` need `--force` (else exit 1, `M1-03 is done; pass --force to reset it`); `ready`/`blocked` → `nothing to reset`, exit 0, nothing written. User tasks are reset the same way. With no lock or a stale one: a direct, surgical Status write (§4) plus readiness sync; if `state.json` names the task as the interrupted one, `reset` adds that its session may still be open (close it by hand). With a lock held by a live igris on this host: a signal `.igris/signals/<ID>.json` with action `reset` and `"force": true|false`, replacing any pending signal for that task; the running igris applies it at its next poll (never in the middle of a verify or a commit), re-checking the status: if it is the current task, it closes the session without the idle wait, clears it from `state.json`, rewrites the Status, syncs readiness and turns pause-after-task on, so nothing is selected until the owner continues; any other task gets the Status write and sync. A `reset` signal still pending when the next `arise` starts is applied before selection. A remote lock (another host) → exit 1: run it there, or clear the lock with `arise --force-unlock`. The run log gets `task_reset`. Every path prints what it did (`M1-03: in progress → ready`, or `M1-03: reset sent to the running igris`) and lists dependents that are `in progress`, which are never changed.
- **`check --strict`.** Adds the lint hints below to `check`'s warnings and exits 1 when any **plan or config** warning is reported (lint hints, readiness drift, the dependency-like column, columns ignored on user tasks, an unknown `[phases.<id>]`, deprecated config). Machine warnings (tool versions, `ANTHROPIC_API_KEY`, an `igris.toml` in a parent directory) are still printed but never fail it, so `--strict` works in CI without Claude Code installed. Plain `check` never shows lint hints and its exit codes are unchanged. Lint hints skip `done`/`skipped` tasks; each is `warning: file:line: <message>`:
  - `title`: `M1-03: the Task cell has no **bold** title, so igris shows its first 80 characters; start the cell with **Title**`
  - `long`: `M1-03: the Task cell is N characters (over 400); keep the row short and point to a spec for the details`
  - `owner-step`: an `agent + user` task whose row contains none of `owner`, `approv`, `decid`, `decision` (case-insensitive): `M1-03: agent + user task, but its row never says what the owner does (no "owner", "approve" or "decide"); say what needs the owner's sign-off`
  - `gate`: a task whose ID ends in `-G` (case-insensitive) that does not depend, directly or through other deps, on every other task of its phase: `M1-G: the gate does not depend on M1-04, M1-05 of phase M1; add them to Deps (e.g. M1-01…M1-05)`
  - `yolo`: `M1-03: Mode yolo runs this task with --dangerously-skip-permissions; prefer auto unless it must run unattended`
  - `fable`: a task of rank `fable` in a phase where no other task has rank `opus` or `fable` (the aliases as written): `M1-03: the only heavy-rank task of phase M1 is fable; check that this task needs fable`
  The summary line under `--strict` with warnings is `tasks.md: N warning(s) under --strict; fix them and run igris check --strict again`. `--json` gains `"strict": true` and each lint warning a `"lint": "<name>"` field (names above); `valid` still means the plan validates; the exit code carries the strict result.
- **Status values and writes are unchanged:** `reset` writes only Status cells; no new status value.
- **SPEC amended:** §3.2 (columns), §5.5 (new, selection), §6.1 (`.Context`), §6.2 (`reset` signal), §6.3 (Timeout), §6.4 (profiles), §6.7 (new, task hooks), §10 (`task_overdue`), §12 (`[verify]`, `[phases.<id>]`, `[hooks]`), §13 (`state.json` selection, run log types), §14 (`arise` flags, `reset`, `check --strict`, `status` columns).

**Amended 2026-10-08 after review: resets are requests in their own slot.** Approved by owner, for v0.4.0. A reset signal goes to `.igris/signals/<ID>.reset.json`, separate from the done/skip slot `<ID>.json`, so a session's `igris done`, a verify failure or the end of a task can never replace or delete a pending reset. While igris runs, a reset signal is a request the owner confirms (TUI dialog, `--no-tui` `reset yes|no`), exactly like a session's skip request: sessions can write `.igris/signals/`, so the source can't be trusted. `igris reset` says `reset requested; confirm it in the running igris`. Confirmed resets are applied at a poll, never in the middle of a verify or a commit; before the current task is accepted (after its verify, after the commit question) a pending request for it is answered and applied first, so it is deferred, never dropped. A request pending at `arise` start is asked about before anything is selected. With no live lock, `igris reset` still writes the Status cell directly, now under the run lock. Plan-change detection (§5.4) also watches the Verify, Timeout and Context cells, so a session can't turn a later task's verification off unnoticed. `state.json` of a sliced run is version 2, so v0.3 refuses it rather than resuming the whole phases. SPEC §5.4, §6.2, §13, §14 and §15.5 amended. The `gate` lint of `check --strict` leaves out tasks that depend on the gate, directly or not (a release after it): they are neither reported missing nor part of the suggested range (SPEC §14). `config.Write` (used by `igris init`) leaves a channel's `events` out while it equals the default list, so default events added later (like `task_overdue`) reach those configs; configs written by v0.3 `igris init` list their events and don't get `task_overdue` on ntfy or Discord until it is added or the line removed (SPEC §12, CHANGELOG Migration).

**Amended 2026-10-08: `default_mode` is `auto`.** Approved by owner, for v0.4.0. `default_mode` defaults to `auto` (`--permission-mode auto`, verified on Claude Code 2.1.294) instead of `default`; `igris init` and `examples/igris.toml` write it, and `ResolveMode`'s last resort is `auto` too. Reason: the owner wants sessions to approve routine actions themselves; risky actions still ask, and `yolo` still needs the typed confirmation every run. This is the one exception to "a v0.3 `igris.toml` means what it meant before": a config without `default_mode` now runs sessions in `auto`; `default_mode = "default"` restores every prompt. SPEC §7.1, §7.2 and §12 amended.

## V05-P1 — observability

**Approved by owner** (2026-10-08), for v0.5. The v0.5 plan's design, with every open point decided here; the owner's fixed decisions are: Gotify only (no Pushover); quiet hours hold everything but the break-through events and send what they held as one digest per channel when the window ends; the live tail is on by default. Everything is additive: a v0.4 `igris.toml` means what it meant before (the new channels, quiet hours and digests are off until configured; only the tail is new by default), and a v0.4 `runs.jsonl` still reads. SPEC §10, §11.1, §11.2, §11.5, §12, §13, §14 and §15 are amended. Tasks V05-01…V05-07 build on it.

| Option | Pros | Cons |
|---|---|---|
| **`task_overdue` breaks through quiet hours by default** | It is urgent like `needs_input` (SPEC §10): a task running past its Timeout usually needs the owner | A slow task can wake the owner; `break_through` takes it out |
| Only `needs_input` and `session_lost` (the owner's list as written) | Quieter nights | An overdue task waits silently until morning, although every other urgent event gets through |

| Option | Pros | Cons |
|---|---|---|
| **Claude session UUID on `task_started`, `task_resumed` and a new `task_retried`** | Every session igris opens or reattaches is logged with its UUID and attempt number, so `report` can print the right `claude --resume` for each | One new event type |
| UUID on `task_started` only | No new type | A retry opens a new conversation with a new UUID that the log never sees, so the resume command would point at a dead one |

| Option | Pros | Cons |
|---|---|---|
| **Progress counts the run's tasks of the current phase (the slice of a sliced run)** | Reaches 100% when the run is done with the phase; matches what the run will actually do | Differs from `status`'s per-phase counts for a sliced run (labelled `slice` so it is clear) |
| All tasks of the phase | Same numbers as `status` | A slice of 2 tasks in a phase of 12 never gets past 2/12 |

| Option | Pros | Cons |
|---|---|---|
| **Quiet hours hold remote channels only; the backend toast is never held** | The toast shows on the machine running igris, not a phone; holding it and toasting a digest later is noise | Toasts still appear at night on a desktop |
| Hold every channel | One rule | A digest toast in herdr or tmux the morning after is useless |

Decided:
- **Run log schema v1** (`.igris/runs.jsonl`, SPEC §13). Every line igris v0.5 writes has `"v":1`. Fields (all but `v`, `at`, `type` omitted when empty or zero):
  - `v` (1), `at` (RFC 3339 UTC, as before), `type`, `task`, `rank`, `model`, `detail` (as before, same meaning, so v0.4 readers and `history` keep working).
  - `run`: the run ID, on every line written by a run (`arise`), `run_started` and `run_stopped` included. Format `YYYYMMDD-HHMMSS-xxxx`: the run's start in UTC plus 4 lowercase hex characters from `crypto/rand` (e.g. `20261008-091500-3fa2`), created once when `run_started` is logged; it contains dashes, so it never looks like a `report` index. Lines written outside a run (`igris reset` with no igris running) have no `run`. Readers check the shape `^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$` and treat a line whose `run` doesn't match as having none.
  - `attempt`: the task's attempt number within this run: 1 for the session of `task_started` or the one reattached by `task_resumed`, +1 for each `task_retried`. On every event of an agent task once it has an attempt (`task_started`, `task_resumed`, `task_retried`, `task_overdue`, `verify_*`, `committed`, `needs_you*`, `task_done`, `task_skipped`). User tasks have none.
  - `session`: the Claude session UUID, on `task_started` (agent tasks: the UUID is generated before the task is marked, `prepareSession`), `task_resumed` (the UUID recorded in `state.json`, if any) and `task_retried` (the new session's UUID; for `retry continue` the same one). Readers shape-check it with `backend.ValidClaudeSession` and treat a bad one as unknown, because `report` prints it into a command.
  - `phase`, `title`, `owner` on `task_started` and `task_resumed`: the task's phase ID, its title (markdown stripped like notification titles, `textsafe.Line`, cut to 80 characters) and owner (`agent`, `user`, `agent + user`).
  - `profile`: the verify profile on `verify_passed`/`verify_failed` (the `detail` keeps its v0.4 text).
  - `commit`: the full commit SHA on `committed` (`git rev-parse HEAD` after the commit; omitted if that fails, with a warning).
  - `duration_ms`: on `task_done`/`task_skipped`, the time from this run's `task_started` or `task_resumed` of the task to the event (wall time by igris's clock: watching, verify, questions, everything); on `verify_*`, the verify command's run time; on `run_stopped`, the run's length.
  - `reason`: on `needs_you`, why igris waits (below).
  - New types: `task_retried` (detail `fresh` or `continue`), `needs_you`, `needs_you_clear`. Everything else is unchanged.
- **Needs you in the log.** `needs_you` is logged every time the engine raises Needs you (the `NeedsYou` UI event) and when a session is lost, with `reason` one of `idle` (settled for `needs_input_after`), `blocked` (waiting for a permission or an answer), `skip_request`, `session_lost`, `task_overdue`, `verify_limit`, `verify_not_sent`, `hook_failed`, `commit` (the commit question under `commit = "ask"`), `reset_request` and `plan_changed` (§5.4); `detail` keeps the few words the notification says. `needs_you_clear` (same `reason`) is logged when that wait ends without the task ending: the agent works again, the owner answers the question, a retry. Readers treat an unknown `reason` as `other`. The config-changed notice (§13) is not a wait (the run goes on) and is not logged as `needs_you`.
- **Compatibility.** A line without `v` is v0 (the v0.2–v0.4 shape, SPEC §13 before this decision); readers accept v0 and v1 lines mixed in one file, ignore unknown types and fields, and read a line with a larger `v` best effort (the known fields), with `history`/`report` adding the note `runs.jsonl has lines from a newer igris (vN); some details may be missing`. Writers never remove or rename a field and never change a field's meaning within v1; adding fields or types is allowed in v1; anything else is v2. Runs are grouped by `run` where lines have it, and by `run_started`…`run_stopped` (or the next `run_started`) pairing for v0 lines, as today.
- **`docs/runlog.md`** (written by V05-01): the file's location, append-only and one JSON object per line; the field table above with types and on which events each appears; every type with its `detail`; the `reason` list; the compatibility rules above; one example line per type; and the v0 shape for reference. SPEC §13 keeps a summary and points to it.
- **`history`** (V05-01) shows the run ID per run when known (text `Run <started> <id> …`, JSON `"run"`), so it can be passed to `report`; otherwise unchanged.
- **`igris report [RUN] [--json]`** (SPEC §14). Finds the project root like `history` (upward), reads `runs.jsonl` with `state.PeekEvents`, read-only, no lock, never creates `.igris/`. `RUN` is a positive integer (1 = the newest run, the default) or a run ID; anything else is a usage error (exit 2). No log or no runs: `no runs recorded yet`, exit 1; an index past the end: `only N runs recorded`, exit 1; an unknown ID: `no run <id> in .igris/runs.jsonl; igris history lists them`, exit 1. A run without a stop is `interrupted`, or `running` while a live igris holds the lock (as `history`). Markdown (stdout, times RFC 3339 UTC, durations like `history`):
  ```
  # igris report · <project>

  Run 20261008-091500-3fa2 · 2026-10-08T09:15:00Z → 2026-10-08T11:02:13Z (1h47m) · completed
  Phases M1, M2 · only M1-03, M1-05
  5 done · 1 skipped · 0 unfinished · 3 commits · needs you 12m30s

  ## Phase M1

  | Task | Result | Duration | Attempts | Verify | Commit | Needs you |
  |---|---|---|---|---|---|---|
  | M1-03 Config loader | done | 14m02s | 2 | fast ✗ ✓ | 3fa29c1 | 2m10s |
  | M1-05 Docs | skipped | 3m40s | 1 | — | — | — |

  Notes
  - M1-03 done: tests added
  - M1-05 skipped: covered by M1-03

  Resume
  - M1-03: `claude --resume 0f6c…` (run it in the project root)
  ```
  Tasks are grouped by `phase` in log order; tasks with no known phase (v0 lines) go under `## Tasks`. Verify is the profile with one ✗ per failure and ✓ per pass in order; Commit is the first 7 characters of `commit` (the SHA of the task's last `committed`); Attempts counts attempts in this run (v0: `task_started` + `task_resumed` events); every unknown value is `—`. Notes are the `task_done` note or `task_skipped` reason, `textsafe.Line`d, one bullet each, left out when none. Resume lists agent tasks whose last session UUID is known and valid, one `claude --resume <uuid>` each; the section is left out when none. Table cells escape `|`; untrusted text (titles, notes, details) is cleaned with `textsafe.Line`. Run-level errors (the `error` events) follow as `Errors` bullets. `--json` prints one object: `{"run","started_at","ended_at","duration_s","end","phases","selection","done","skipped","unfinished","needs_you_s","commits":[{"task","sha","subject"}],"errors":[…],"tasks":[{"id","phase","title","owner","rank","model","result","duration_s","attempts","verify":[{"profile","passed","duration_s"}],"commit","note","sessions":[uuid…],"resume","needs_you_s"}]}`; unknown strings are omitted, unknown numbers are omitted (never 0 for "unknown"), `resume` is the full command.
- **Needs-you time.** An interval starts at `needs_you` and ends at the first later event of the same run that is `needs_you_clear` with the same task and reason, or for that task `task_retried`, `task_done`, `task_skipped`, `task_reset`, or the run's `run_stopped` (or its last event when it has none). A task's needs-you time is the union of its intervals (overlaps count once); the run's is the union over all its intervals, task-less ones included. User tasks get none: their time is the task's duration. v0 runs have no `needs_you` events: shown `—`, omitted in JSON.
- **TUI progress** (SPEC §15). `n/m · p%` plus a bar, never colour-only: m = the run's tasks of the current phase (all of them, or the slice's tasks in that phase for a sliced run, then labelled `slice`), n = those of them `done` or `skipped` in the plan as last loaded, p = ⌊100·n/m⌋. Wide header: `phase M1 · 7/12 · 58% ██████░░░░` (bar 10 cells, `█` filled, `░` empty); narrow status bar: `M1 · 7/12 · 58%` and a 6-cell bar; with too little width the bar goes first, then the percentage. Hidden when m = 0. Under `NO_COLOR` the bar stays (it is glyphs).
- **Elapsed and ETA.** The card's elapsed time stays what it is (since the task started or was picked up in this run, the 1 s tick). The ETA is the median of the rank's data points: `task_done` durations of agent tasks of the same rank alias, from runs where the task was started in that run (`task_started`, not `task_resumed`, so partial times never count), `duration_ms` when present and the `task_started`→`task_done` timestamps for v0 lines, the most recent 20 per rank, verify and owner waits included (the median absorbs the odd long wait). Skipped tasks and user tasks don't count. Shown only with ≥ 3 data points, always with `≈`, minutes resolution (≥ 1m): `· ≈ 14m left` while elapsed < median, `· over ≈ 18m typical` once past it. No phase ETA. The engine loads the data points once when the run starts (a `report` helper, e.g. `report.RankDurations(events)`) and passes them to the TUI through `tui.Options`; tasks finished during the run are not added.
- **Live tail** (SPEC §11.1, §15). An optional interface, like `PromptHolder`:
  ```go
  type Tailer interface {
      // Tail returns up to n of the last lines the session shows, oldest
      // first, trailing blank lines dropped. A gone session yields an error
      // wrapping ErrSessionGone.
      Tail(ctx context.Context, n int) ([]string, error)
  }
  ```
  herdr: `herdr agent read <name> --source recent-unwrapped --lines n`; when that is empty (or only blank lines, e.g. a dialog on the alternate screen), `--source visible --lines n`. tmux: `tmux capture-pane -p -J -t %N -S -<n>` (joined wrapped lines; the output is the history tail plus the visible screen, so igris drops trailing blank lines and keeps the last n). Both through the command runner with the backend's 10 s timeout; tmux gets a recorded fixture. The fake implements it with scripted lines. The run-view card shows the tail under the state line, before the task text, when `[tui] tail = true` (default) and the current agent session implements `Tailer`: 6 lines in the wide layout, 3 in the narrow one, fewer when the card has no room (the task text then gives way first, ending in `… t: details`). Each line is `textsafe.Line`d, tabs expanded, blank lines dropped, and clipped to the card width by display width. Refreshed every `poll_interval` by a TUI command calling a `Tail func(ctx, n) ([]string, error)` from `tui.Options` (the engine/project side resolves the current session), one call in flight at a time, each bounded by a 2 s context. Any error, an empty result, no session, a lost session, a user task or `tail = false` hides the block silently (no warning, no feed line). The tail is never logged, never in `runs.jsonl`, notifications, `state.json` or `report`; not in `--no-tui`. The conformance suite gains a case that runs only for a `Tailer`: after a prompt, `Tail(ctx, 5)` returns at most 5 lines without error; on a gone session it returns an error wrapping `ErrSessionGone`.
- **Webhook** (SPEC §10, §12). `[notify.webhook] url, secret, events, template`. On when `url` is set and `events` non-empty. `url` and `secret` accept `env:VAR`; the resolved URL must be `http` or `https` with a host (config error otherwise); a `secret` without a `url` is a config error. POST, `Content-Type: application/json`, `User-Agent: igris`, `X-Igris-Event: <event>`; with a secret, `X-Igris-Signature: sha256=<lowercase hex HMAC-SHA256(secret, raw body)>`. Body, every key always present (`""` when unknown): `{"v":1,"event","project","phase","task","title","what","run","at","urgent","text"}`; `at` RFC 3339 UTC; `urgent` is `Event.Urgent()`; `text` is the rendered template or the default text (the message body). A digest (below) has `"event":"digest"`, `X-Igris-Event: digest`, `task`/`title`/`what` empty, `text` the digest text, and adds `"messages":[…]` with one payload object (without `text`/`messages`) per held message. Timeout and retry as every channel (10 s, one retry); errors redacted (the URL and secret are secrets).
- **Slack.** `[notify.slack] webhook_url, events, template`; `webhook_url` accepts `env:` and is a secret (like Discord's). On when set and `events` non-empty. POST `{"text": …}`, default text `*igris · <project>* · <body> (<event>)`. The whole text has `&`, `<`, `>` escaped (`&amp;`, `&lt;`, `&gt;`), so a plan title can't mention `<!channel>` or make links; cut to 4000 characters.
- **Gotify.** `[notify.gotify] server, token, events, template`; `token` accepts `env:` and is a secret. On when `server` and `token` are both set and `events` non-empty; only one of them set is a config error. POST `<server>/message` (`url.JoinPath`), header `X-Gotify-Key: <token>` (never in the URL), JSON `{"title":"igris · <project>","message":<text>,"priority":p}`, text cut to 4000 characters. Priority: 8 for urgent events (`needs_input`, `session_lost`, `task_overdue`), 5 for `verify_failed_limit`, `phase_stuck`, `run_error`, `phase_done` and digests, 3 for `task_done` (and a digest holding only `task_done`).
- **Templates.** `template` on ntfy, Discord, webhook, Slack and Gotify (not the backend toast, whose text stays short). Go `text/template`, no extra functions, `Option("missingkey=error")`, over a struct with exactly `Event`, `Project`, `Phase`, `TaskID`, `Title` (markdown stripped), `What`, `RunID` (strings) and `At` (`time.Time`, local time, so `{{.At.Format "15:04"}}` works); `notify.Message` gains `RunID` and `At` for it. Never file contents or command output. Empty means the channel's default text (today's output, unchanged). It is parsed and executed once against a sample message at config validation, so a bad template or an unknown field is a config error naming the channel (`notify.slack.template: template: …: can't evaluate field Foo; use Event, Project, Phase, TaskID, Title, What, RunID or At`); a template is at most 1000 characters. The rendered text is what the channel sends (ntfy body, Discord `content`, Slack `text` before escaping, Gotify `message`, webhook `text`); titles stay `igris · <project>`. It is cleaned (`textsafe.Clean`: control characters out, newlines kept), trimmed, and cut per channel (ntfy 4096 bytes, Discord 2000 characters, Slack, Gotify and webhook 4000); an empty result at run time falls back to the default text.
- **Quiet hours.** `[notify] quiet = "HH:MM-HH:MM"` (24 h, local time; `""` = off, the default); start = end is a config error; the window is `[start, end)` and crosses midnight when start > end; it is evaluated on the wall clock each time, so DST shifts move it with the clock. `[notify] break_through` lists events never held (default `["needs_input", "session_lost", "task_overdue"]`; `[]` holds everything; names validated like `events`; written out by `config.Write` only when it differs from the default, like `events`). Inside the window a message for ntfy, Discord, webhook, Slack or Gotify whose event is not in `break_through` is held per channel (after that channel's `events` filter) instead of sent; the backend toast is never held. The router takes an injectable clock (`Now`) and gets `Flush(ctx)`: the engine calls it from its poll loop and from every other place it waits on the clock (questions included), at most once per `poll_interval`; when the clock is outside the window and a channel holds messages, that channel gets one digest. Held messages are also flushed as a digest at run stop (igris can't hold them after it exits). Digest text: `quiet hours 22:00–07:00: N held` then one line per held message, `HH:MM <event>: <default body>` (local time), at most 20 lines, then `… and K more`; templates are not applied to digests; Discord/Slack frame it like a message (`**igris · <project>**` / `*…*`). The run log gets `notification` events `<event> held for <channel> (quiet hours)` and `digest of N via <channel>`. `notify test` bypasses quiet hours and `task_done_digest`.
- **`task_done_digest`.** `[notify] task_done_digest = 0` (default: one message per task), an integer N ≥ 2, or `"phase"`; 1 means 0; anything else is a config error. With N: `task_done` messages are collected per channel and sent as one `task_done` message when N are collected, when the phase ends (before its `phase_done`/`phase_stuck`, or when the run moves to the next phase) and at run stop. With `"phase"`: at phase end and run stop only. The grouped message is a `task_done` `Message` with empty `TaskID`/`Title` and `What` `3 tasks done: M1-01, M1-02, M1-03` (IDs cut after 10 with `…`), so templates apply; during quiet hours it is then held like any `task_done`.
- **Config** (SPEC §12). `Secrets` gains `WebhookURL`, `WebhookSecret`, `SlackWebhook`, `GotifyToken`, all resolved by `Resolve` and scrubbed from router errors. `[notify]` gains `quiet`, `break_through`, `task_done_digest` (a `toml.Unmarshaler` type accepting an integer or `"phase"`). `[tui]` gains `tail` (default true). `igris init` writes none of the new channel blocks; `doctor` lists the new channels in its notification check (never sending); `notify test` covers them.
- **SPEC amended:** §10 (channels, templates, quiet hours, digests), §11.1 (`Tailer`), §11.2/§11.5 (tail calls), §12 (keys and example), §13 (run log v1), §14 (`report`, `history` run ID), §15 (progress, ETA, tail). `internal/prompt/format.md` mirrors only SPEC §3 and needs no change.

**Amended 2026-10-08 after review: notification hardening.** For v0.5.0, before its release. SPEC §10, §12 and §16 amended.
- **No redirects.** Every channel's HTTP client returns the redirect response instead of following it (`CheckRedirect` → `http.ErrUseLastResponse`, also on a client passed in): following would carry `X-Gotify-Key`, `X-Igris-Signature` or a secret URL (as `Referer`) to whatever host the `Location` names, and a `200` there counted as delivered. Any `3xx` is a failure, `server answered 307 (redirect); set the final URL in igris.toml`; the `Location` is never shown.
- **Fixed error texts.** A transport failure is one of `can't resolve host`, `connection refused`, `TLS failed`, `timed out`, `cancelled`, `connection failed`; a status is `server answered <code> <Go's status text>` (not the server's reason phrase); a URL that doesn't parse is `<key> is not a valid URL …`. net/http's error text (URL, host, `url.Parse` quoting parts of the URL) never reaches a result, so the router's redaction is the safety net only.
- **Redaction order.** `Redact` replaces the longest secret form first (escaped forms included), through a marker, so a secret that contains another never shows in part and `[redacted]` is never searched again.
- **Discord and Slack URLs** must be `https` with a host: a literal one is a config error at load, an `env:` one an error from `Resolve`; the text names the key, never the value. Gotify, ntfy and the webhook keep `http` (a LAN server); `Warnings` (shown by `check`, `arise`, `doctor`) flags `http://` for ntfy with a token, Gotify (the token goes in the clear) and a literal webhook `url`.
- **Templates can't loop.** A parsed template whose tree has `range` or `template` (anywhere, inside `if`/`with` too) or that defines templates (`define`, `block`) is a config error naming the channel: `notify.<channel>.template uses {{range}}; a notification template may use only variables, if/else and with`. A message has no lists, and a loop could spin the CPU on every message; the 64 KiB output cap stays for what is left (`printf` widths).
- **Output limits.** ntfy's default body is cut to 4096 bytes like a template's. A webhook digest lists at most 50 held messages (the oldest) in `messages` and adds `truncated`, the number left out (`0` when none); only digests have the two keys.
- **Discord masked links.** Discord content, the default and a template's, has `\`, `[` and `]` escaped with a backslash, so a plan title can't make `[text](url)` show one address and open another; mentions stay off through `allowed_mentions`.
