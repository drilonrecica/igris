# v0.3 gate: tmux and hooks

The v0.3 gate (V03-G): a real phase on tmux, and herdr with the new hook state, on the build that became v0.3.0. Run on Linux (Fedora 44) on 2026-10-07/08 by the owner's Claude Code session, driving igris in a scratch tmux server and a herdr tab. The project, the plan and the answers were synthetic ([`smoke-tmux.md`](smoke-tmux.md)).

Versions: igris v0.3.0 (pre-release build), tmux 3.7c, herdr 0.9.1, Claude Code 2.1.293 (sessions on haiku through `[models] sonnet = "haiku"`).

## tmux (`backend = "auto"`, inside tmux, outside herdr)

| Check | Result |
|---|---|
| `igris init` writes `backend = "auto"`; `igris doctor` shows `tmux 3.7.0` and `tmux is running and igris is inside it`, no herdr lines | pass |
| S0-01 window `S0-01 · sonnet` opens in the background running `claude` | pass |
| Folder-trust question: no hook, `session open` after ~15 s, **Needs you** (`blocked`) after `needs_input_after` (20 s) | pass |
| Trust accepted in the window: the held task prompt goes out by itself; `is working again` from the hooks | pass |
| `igris done` without a permission prompt; failing verify output pasted into the same window; second `done` passes; window closed | pass |
| User task S0-02 via stdin `done looks good`; S0-03 opens in 3 s (trusted folder: `SessionStart` + settle) and completes; `phase S0 complete` | pass |
| `.igris/hooks/<ID>.settings.json` 0600 with only `hooks`, `hooks/` and `agent-state/` 0700 | pass |
| S1-01 asks a question and stops: **Needs you** (`idle`, from `Stop`) once | pass |
| Ctrl-C igris: the window stays; `igris arise` → `reattached to its session` | pass |
| `tmux kill-window` on the session: **Session lost** with the five choices | pass |
| `retry continue`: new window resuming the conversation (`--resume`, old agent state cleared); answer `blue` in it → `colour.txt`, `igris done`, phase complete | pass |
| Home (`igris`) header `tmux ✓`, NOW `COMPLETE` | pass |
| Notifications `needs_input`, `phase_done` delivered `via backend` (`display-message`) | pass (server detached, so no client showed them) |

## herdr (`backend = "auto"`, inside a herdr pane, integration installed)

| Check | Result |
|---|---|
| Same build, same project: `auto` picks herdr; S2-01 runs in a herdr tab, verify passes, phase complete | pass |
| The herdr session also gets `--settings`; its hooks write `.igris/agent-state/<uuid>.json` (ending `exited` on `SessionEnd`) | pass |

## Not run

- **herdr without its integration, live.** Uninstalling the owner's herdr integration was not done; the fill-in (herdr `unknown` → hook state) is covered by `TestStateHookFillIn` and the conformance suite.
- **macOS.** The tmux smoke on macOS is open; CI runs the tests (including the tmux simulation and conformance suite) on macOS.
- **A visible tmux toast** on an attached client, and `o` from the TUI in tmux: the gate ran in a detached scratch server.

Findings fixed before the release: the herdr availability errors still said "tmux support is planned"; hooks named the versioned binary path (now the stable PATH link when it is the same file, so a `brew upgrade` doesn't break running sessions' hooks).
