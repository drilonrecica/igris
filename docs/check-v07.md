# v0.7 gate: the 1.0 contract

The v0.7 gate (V07-G) checks what v1.0.0-rc.1 promises: that every surface SPEC §19 freezes has a test, that what every earlier release wrote still works, and, in real runs, that a project and an interrupted run from v0.6.0 carry over to the candidate the way the CHANGELOG says.

It ran on Linux (Fedora 44) on 2026-10-14, from the owner's Claude Code session, which drove igris in a herdr tab it created itself (`herdr tab create`, `pane run`, `pane read`) and in a private tmux server (`tmux -L igrisgate`). All projects and plans were synthetic scratch repos (`git init`), every rank on sonnet. Publishing (V07-R) is the owner's.

**Versions:**
- igris v0.6.0, built from its tag, and the candidate built from `11db7e2` (`igris 1.0.0-rc.1-gate`); the gate's own changes (below) are tests, docs and one doctor message;
- herdr 0.9.1, Claude Code 2.1.300, tmux 3.7c, Go 1.27.2.

## Every frozen surface has a test

[`docs/compatibility.md`](compatibility.md) now maps each surface of SPEC §19 to the tests that keep it (C1–C13), and `tools/contract` fails when a surface has no row, a row names no test, or a named test no longer exists (tried: renaming `internal/notify.TestWebhookSend` fails it). Building the map found three surfaces without a test that pinned them, now added:

| Surface | Missing | Added |
|---|---|---|
| C10 `prompt_template` variables | no test used every SPEC §6.1 variable, so dropping one would pass | `internal/prompt.TestPromptTemplateEveryVariable` |
| C10 `commit_message` variables | `Note` was never used in a test template | `internal/engine.TestCommitMessageEveryVariable` |
| C8 `.igris/` layout | the paths were defined in several places and asserted nowhere together | `internal/state.TestLayout` (every §13 path and both signal slots) |

The threat model's map checker moved to `tools/testmap`, shared by both maps; `tools/threatmodel` behaves as before.

## Phase A: an interrupted v0.6.0 run, upgraded

Scratch project `gateA`, set up by **v0.6.0's own `igris init`** (so `igris.toml` has `  command = "claude"`), `default_mode = "auto"`, `commit = "auto"`, three tasks: G-1 (agent) writes `one.txt`, G-2 is the owner's, G-3 (agent) writes `three.txt`. `.claude/settings.json` puts a `gbin/` directory first on the sessions' `PATH`, whose `igris` is a symlink switched from v0.6.0 to the candidate between the two halves, so a session's `igris done` is always the igris that runs.

| Step | Result |
|---|---|
| v0.6.0: `igris arise G --no-tui`, `stop` at G-2's YOUR TURN | `G-1 committed`, `G-1 done`, `G-2 YOUR TURN`, `run stopped: stopped`. `state.json` `"version": 1`, current task G-2, `config_hash` `bcf00101…`; `runs.jsonl` 7 lines, all `"v":1` |
| Candidate: `igris check` | exit 1: `igris.toml: claude.command was removed in 0.7 (igris always starts claude from PATH); run igris config migrate to delete it` |
| `igris doctor` | `⨯ fail … claude.command was removed in 0.7 …`, `next: igris config migrate` |
| `igris config migrate --yes` | `igris.toml:16: -   command = "claude"`, backup in `.igris/config/`; `git diff --stat`: one line removed; `check` OK |
| `igris status --json` before resuming | exit 0, the v0.6.0 run shown (current task G-2), valid against the schema |
| Bare `igris arise --no-tui` | `G-2 resumed: user task` and **no config-changed notice**; `done one.txt says one`, then G-3 in a new session: committed, done, `phase G complete`, `run stopped: completed` |
| After the run | `state.json` `"version": 3`, `config_hash` still `bcf00101…`; `runs.jsonl` 16 lines (both runs, `task_resumed` for G-2); `history` lists the stopped v0.6.0 run (G-1 done, G-2 unfinished) and the resumed one (G-2, G-3 done) |
| A pending signal (`igris done G-3` with no run) | `status --json` shows `"signals": [{"task": "G-3", "action": "done"}]` |

## Phase B: the candidate on tmux

Scratch project `gateB` from the candidate's `igris init` (no `command` line written), `backend = "auto"`, two tasks: B-1 (agent) writes `b.txt`, B-2 is the owner's. `igris arise B --no-tui` in the private tmux server.

| Check | Result |
|---|---|
| Backend | tmux (auto); B-1's session in its own window |
| Folder trust at the first session | igris held the task prompt; once the folder was trusted it was delivered, and B-1 was committed and done |
| `igris done B-2 --note …` from another shell, during the run | `done signal recorded for B-2`; the run took it at its next poll: `B-2 done · b.txt says bee`, `phase B complete`, `run stopped: completed`; the session window closed |

## Phase C: real `--json` against the schemas

`check`, `phases`, `status` (paused run, finished run, pending signal), `history`, `report` and `doctor` from phase A, and `status`, `history`, `report` from phase B: 11 documents, all valid against `docs/json/*.schema.json`, strictly (no field a schema doesn't list). Checked with `IGRIS_SCHEMA_CHECK_DIR=<dir> go test ./cmd/igris -run TestSchemaCheckDir`, which the gates use from now on.

## Phase D: exit codes from a shell

On the candidate, in `gateB`: `check` 0; `bogus` 2; `check --nope` 2; `done 'B 2'` 2; `done B-9` 1; `notify test --event bogus` 2; `help check` 0; `help nope` 2; `-v` 0; `version x` 2; `report 99` 1; `report x` 2; `history -n 0` 2; `config migrate` with nothing to do 0. All as SPEC §14's table says.

## Phase E: the suite

On the gate's tree: `make fmt lint test` (0 lint issues; includes the compatibility suite over all 11 releases and the contract map), `go test -race ./...`, `make fuzz FUZZTIME=30s` (7 targets, no new crashers), govulncheck v1.8.0: **0 vulnerabilities affecting igris**. It lists GO-2026-5024 in `golang.org/x/sys` v0.36.0 (an indirect dependency, fixed in v0.44.0), in code igris doesn't reach.

## Publishing v1.0.0-rc.1

- **The release commit:** `b683911` (`V07-R: release 1.0.0-rc.1`): the CHANGELOG section `[1.0.0-rc.1] - 2026-10-14` and its compare links, the README status line, and `tools/sign.sh` accepting `X.Y.Z-rc.N` (and saying that a candidate is a pre-release that doesn't go to the tap). Pushed with the ten v0.7 commits; CI (tests on Linux and macOS, lint, fuzz, the reproducible-build check on both) and govulncheck passed on it.
- **Signing:** the owner ran `make sign VERSION=1.0.0-rc.1`. Checked again before publishing: the tag points at `b683911`, `Good "git" signature for igris` and `Good "file" signature for igris` with the key in `allowed_signers` (`SHA256:75VRO2deETEI4kic8DzVMJYOKjFIWGjlvXNRiS5BtgM`), all 8 archives and SBOMs `OK`, `igris version` → `igris 1.0.0-rc.1`.
- **Published** as a pre-release (`gh release create v1.0.0-rc.1 --prerelease --verify-tag`, 10 assets). The tap's formula was left alone: Homebrew stays on 0.6.0 (owner's decision).

| Check, from GitHub | Result |
|---|---|
| The release notes' commands on files from the release URLs, `allowed_signers` from `raw.githubusercontent.com/…/v1.0.0-rc.1/` | `Good "file" signature for igris`; `igris_1.0.0-rc.1_linux_amd64.tar.gz: OK`; `igris 1.0.0-rc.1` |
| The release | `prerelease=true`, 10 assets; the latest release is still `v0.6.0` |
| The tag | `verified=true reason=valid` (GitHub shows it as Verified) |
| `go install …@v1.0.0-rc.1` | Right after the tag was pushed, `sum.golang.org` answered `unknown revision` (it had looked the version up before the tag existed, and caches the miss for a while); fetched directly (`GOPROXY=direct`) it builds `igris 1.0.0-rc.1` |


1. **doctor printed the config's absolute path in front of a config error** (`⨯ fail /tmp/…/igris.toml: claude.command …`): `config.Load` names the file by its path, and doctor trimmed only `igris.toml:`. Fixed (`internal/checks/doctor.go`): the result already names the file; `cmd/igris.TestDoctorRemovedClaudeCommand` now fails on the path. Text output only, not part of the contract.
2. **Not fixed here:** `golang.org/x/sys` v0.36.0 has an advisory igris doesn't reach (Phase E). Bumping it is a dependency change for after the candidate.

With finding 1 fixed, the gate passed.
