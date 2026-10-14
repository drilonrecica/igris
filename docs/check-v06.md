# v0.6 gate: worktree mode and a signed download

The v0.6 gate (V06-G) covers two things: a real run in worktree mode, and a download of the first signed release verified end to end, the way a user would. Each v0.6 feature also had a real run when it was built (worktree mode, the env allowlist, the waiting state, the signing dry run with a throwaway key). The gate re-checked them on the build that became v0.6.0.

It ran on Linux (Fedora 44) on 2026-10-14, from the owner's Claude Code session, which drove igris in a herdr tab it created itself (`herdr tab create`, `pane send-text`, `pane read`) and in a private tmux server. All projects and plans were synthetic scratch repos (`git init`). The owner made the one step only the owner can: `make sign VERSION=0.6.0`, with the release key's passphrase.

**Versions:**
- igris built from `ce99328` for phase A (`0.5.2-0.20261014154936-ce993289f666`; the release commit `acfc29d` changed only the CHANGELOG and the README) and the signed `v0.6.0` build for phases B and C;
- herdr 0.9.1, Claude Code 2.1.299 to 2.1.300, tmux 3.7c;
- GoReleaser v2.17.1, OpenSSH 10.2p1;
- podman with `docker.io/homebrew/brew`.

## Phase A: a real run in worktree mode

Scratch project `p`: three agent tasks.
- G-1 creates `ok.txt`.
- G-2 reads the plan (outside the worktree) and lists its IDs.
- G-3 runs `sleep 40` in the background, ends its turn and runs `igris done` once it is finished.

```toml
backend = "herdr"            # "tmux" for the second run
default_mode = "auto"
[models]                     # every rank on sonnet
[run]
commit = "auto"
verify = "test -f ok.txt && env | cut -d= -f1 | sort > <scratch>/verify-env.txt"
worktree = true
env = ["LANG"]
[notify.ntfy]
token = "env:IGRIS_GATE_TOKEN"   # exported as gate-s3cret; events = [], so nothing is sent
```

- **The build under test on PATH.** A committed `.claude/settings.json` holds only `env.PATH`, with the build under test first, so the sessions' `igris done` is that build. The `igris done` allow rules are only in the untracked `.claude/settings.local.json`, as `igris init` writes them. So the worktree sessions get them from igris (the rule mirror of V06-07), not from a copy in the worktree.
- **Folder trust** was answered once for the project. The worktree inherits it.

| Check | Result |
|---|---|
| A new worktree on a new branch | `sessions, verify and commits run in new worktree .igris/worktrees/20261014-155802-f5a4 on branch igris/20261014-155802-f5a4` |
| Stop mid-task (`x`, **Stop igris**), then `q` and a bare `igris arise` | Closing line: `the run's work so far is on branch …, in the worktree …; igris arise continues there`. The resume said `sessions, verify and commits run in the worktree …` (no second worktree) and `G-2 resumed: reattached to its session`. |
| G-3 waiting on its background command | `G-3 waits on background work; Claude Code resumes it when that is done`; the card said `waiting on background work (7s … 31s)` for about 35 s, never NEEDS YOU, then verified and committed by itself. |
| No permission prompts | `runs.jsonl` has no `needs_you` event. G-2 read the plan outside the worktree without asking. |
| End of the run | `the run's work is on branch igris/20261014-155802-f5a4; igris doesn't merge it: git merge igris/20261014-155802-f5a4`; the worktree removed (`git worktree list` shows the main checkout only), the branch kept with the three task commits (`ok.txt`, `ids.txt`, `bg.txt`). |
| The main checkout | `git status`: only ` M tasks.md`, the Status cells igris wrote. |
| What verify saw | `HOME LANG PATH`, plus `PWD SHLVL _` from `sh`. Not `IGRIS_GATE_TOKEN`, and `gate-s3cret` appears nowhere in `runs.jsonl`. |
| Run log, `history`, `report` | Both `run_started` lines carry `"branch":"igris/20261014-155802-f5a4"`. `history` shows the stopped run (G-1 done, G-2 unfinished) and the resumed one. `report` lists G-2 and G-3 with `default ✓`, their commits and `claude --resume` lines. |
| `claude --resume <uuid>` from the project root, for a session that ran in the (now removed) worktree | Works: Claude Code finds the session by its ID. |
| tmux, `igris arise P1 --only G-1 --worktree --no-tui`, `worktree = false` in the config and the plan's Status edits uncommitted | The start-up warning reads `the working tree has uncommitted changes: the run's new worktree starts from HEAD without them.`; a second worktree and branch `igris/20261014-160025-0d64`; G-1 committed there; worktree removed, merge hint printed. |

## Phase B: release commit and signing

- **The release commit:** `acfc29d` (`V06-R: release 0.6.0`) renames the CHANGELOG section to `[0.6.0] - 2026-10-14`, adds the compare links and sets the README status line. It was pushed, and CI and govulncheck passed on it.
- **Signing:** the owner ran `make sign VERSION=0.6.0`. It made the signed tag `v0.6.0` on `acfc29d`, built `dist/`, signed `dist/checksums.txt` and checked both.

## Phase C: the download, as a user

**Before publishing,** in a clean folder with only `igris_0.6.0_linux_amd64.tar.gz`, `checksums.txt`, `checksums.txt.sig` and `allowed_signers` from the tag:

| Check | Result |
|---|---|
| `git -c gpg.ssh.allowedSignersFile=allowed_signers verify-tag v0.6.0` | `Good "git" signature for igris with ED25519 key SHA256:75VRO2deETEI4kic8DzVMJYOKjFIWGjlvXNRiS5BtgM`; the tag object points at `acfc29d` |
| `ssh-keygen -Y verify … < checksums.txt` | `Good "file" signature for igris` |
| `sha256sum -c --ignore-missing checksums.txt` | `igris_0.6.0_linux_amd64.tar.gz: OK` |
| The binary | `igris version` → `igris 0.6.0`; the archive holds `igris`, `LICENSE`, `README.md` |
| One byte of `checksums.txt` changed | the signature check fails (exit 255) |
| A byte appended to the archive | `sha256sum: WARNING: 1 computed checksum did NOT match` |
| SBOMs | 4 archives and 4 `.sbom.json` files, all listed in the signed `checksums.txt` |
| Reproducible | the tag rebuilt in a fresh clone with its own `GOCACHE` (`goreleaser build --clean`): all four binaries identical to the signed ones, byte for byte (CI's reproducible-build hashes come from snapshot builds, whose version string differs, so they aren't comparable) |
| Homebrew formula | `dist/igris.rb`: `version "0.6.0"`, the four v0.6.0 release URLs and their sha256s |

**After publishing** (`gh release create v0.6.0 … --verify-tag`, 10 assets), from GitHub:

| Check | Result |
|---|---|
| The README's commands on files downloaded from the release URLs, `allowed_signers` from `raw.githubusercontent.com/…/v0.6.0/` | it holds the README's key; `Good "file" signature for igris`; `OK`; `igris 0.6.0` |
| The tag on GitHub | `verified=true reason=valid` (the git tags API): GitHub shows it as Verified |
| Latest release | `v0.6.0` |
| Homebrew | the tap's formula updated (`igris 0.6.0`); in `docker.io/homebrew/brew`, `brew install drilonrecica/tap/igris` gives `igris 0.6.0` and `brew test igris` passes |

## Findings

None. The gate passed.
