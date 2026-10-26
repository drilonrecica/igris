# igris in the editor — design

Owner's direction 2026-10-26, after v1.8 was scheduled. Scheduled as phase V19 (v1.9) in the task plan; decision task V19-P1 settles the open points below before anything is built.

## Intent

The TUI stays igris's home, and the owner likes it. But there are moments when a GUI is simply easier, and they all happen next to the code:

1. **Reading what a task changed while developing.** Today this means switching between the TUI and the editor to read a task's diff or the plan.
2. **Watching a run while coding.** Run state should be visible without switching to the herdr or tmux window.
3. **Winning over other developers.** People who would try igris bounce off herdr/tmux and a TUI before they ever see a run.

The owner works in VS Code and its forks (Kiro, Antigravity, Cursor) on different machines. So igris goes where those developers already are: into the editor. It works the way the Claude Code and Codex extensions do (a thin layer over a CLI, native diffs, a sidebar), but what it shows is a run control room (phases, tasks, several sessions, Needs you, the changes each task made), not a chat.

## Decision

- **Now: one VS Code extension**, published on the VS Code Marketplace and on Open VSX. One build reaches VS Code, Cursor, Kiro, Antigravity and Windsurf. It is the only option that fixes moment 1, because the changes are read in the editor the owner is already in.
- **Later, only if the extension shows demand: a dedicated igris app**, a "mission control" window around the run (the plan, a grid of sessions, diff review), as Conductor and Crystal do for parallel Claude Code agents. Not designed or scheduled now. It needs its own decision on hosting terminals (xterm.js plus a native PTY module, or attaching to the tmux backend as Part B does) and on installers and updates for each operating system.
- **Never: a VS Code fork.** It means merging upstream VS Code every month, forever, plus installers and signing for three operating systems (self-upgrade is a non-goal). It would compete with the editors igris should run inside. Editors get forked when the AI has to be inside the editing loop; igris works a level above that (one session per task, tasks in a plan) and needs no editor internals.

## Goals

- The extension is a **client of the igris binary**. The engine, the plan writer, verify and commits stay in Go. The TUI and the web UI keep working alongside it.
- Editor-native: a sidebar tree, the status bar, VS Code's own prompts and notifications, the native diff editor, CodeLens on plan rows.
- A new developer goes from installing the extension to a first run without learning tmux or herdr.

## Non-goals

- No engine or SPEC changes made just for the editor. Protocol additions, if any, are additive and editor-neutral, usable by any client.
- No skip-permissions mode from the editor: never offered or accepted (SPEC §7.3), as on the web UI. It stays in the terminal with its typed phrase.
- No editor other than the VS Code family in this phase (no JetBrains, Neovim or Zed).
- No native Windows: Windows works through VS Code's Remote-WSL, as igris already does.

## Anti-goals

What would make this a failure even if it works (owner's words):

- **It feels like a cheap wrapper.** The web UI glued into a webview tab. Every surface is the editor's own.
- **Igris bends to the editor.** The binary and its contract change to suit VS Code.
- **Others still hit setup walls.** New users still have to install and understand tmux, herdr and Claude Code by hand.

## Shape

### Part A — the viewer (the owner's daily use)

- Talks to `igris serve` over the local socket, protocol 1 (SPEC §13/§14). The socket is owner-only and uid-checked, so no token is needed.
- **Tree** (sidebar): phases and tasks with status, rank and mode; the live run on top.
- **Status bar:** the status word (Working, Needs you, Paused, …) and what waits on the owner; a click focuses it.
- **Answering:** questions through QuickPick/InputBox and notification actions. Pause, resume, stop, retry, done and skip take the same confirmations as the web UI.
- **Reading changes:** the native diff editor on a task's commits (`<ID>: …`) or its worktree branch (`igris/<run-id>-<ID>`), found through git, from the tree and from Needs you.
- **CodeLens** on `tasks.md` rows: status, run this task (`--only`), open its diff. Plan text is cleaned as `textsafe` does before it is drawn.

### Part B — sessions in the editor (for other developers)

- `serve` started headless on the **existing tmux backend** (SPEC §11.5, since 1.4) opens a detached `igris-<project>` session. Each active task's window appears as a VS Code terminal tab attached to it, for example through a grouped tmux session per tab that selects the task's window.
- Prompt, state, tail and submit are the existing, tested tmux paths. Users never see or learn tmux; it is only a dependency underneath, which the onboarding installs.
- **Rejected: a true "VS Code terminal backend".** Extensions can't reliably read what a terminal shows through the stable API, and the tail and stuck-prompt detection need it. Owning the terminal would also need a native PTY module, and it would bend igris to the editor.

### Onboarding (the setup wall)

- A first-run checklist built from `igris doctor --json`. It checks for `igris`, `claude` and `tmux` and gives the install command for each one missing; the owner runs it.
- `igris init` from the extension, then a run started with one button.

### Keeping the later app cheap

All behavior stays in the binary (`internal/report`, `internal/look`, the engine). The extension only renders what `serve` sends and sends protocol commands, so a later app is just another client, like the TUI and the web UI. Nothing should live only in the extension.

## Open decisions (V19-P1)

- **Where it lives.** Proposed: this repo, as `editors/vscode/`, so protocol changes, compat tests and releases move together, trunk-based.
- **The TypeScript toolchain and its dependencies** need a P0-01-style approval: TypeScript, esbuild, `@vscode/vsce`, `ovsx`, `@vscode/test-electron`.
- **Protocol 1 as a contract.** Whether it is frozen for the extension (SPEC §19), and how a version mismatch between the extension and igris is refused (as `compat` does today).
- **The first release's scope.** Proposed: Part A plus onboarding first, Part B second, if the phase gets long.
- **Publishing.** Marketplace and Open VSX publisher accounts, and an owner-only release step like `make sign`.
- **Verification first (V19-P2):** the socket from the extension host locally, over Remote-WSL and over Remote-SSH; a terminal tab attached to one task's tmux window; installing from Open VSX in a fork.

## Testing

- Extension tests run against a fake `igris serve` socket replaying recorded protocol-1 transcripts. No real igris and no real Claude Code: tests never start the real igris binary (AGENTS §5).
- A gate in the V18-G style: a scratch project, the extension in `@vscode/test-electron`, a nested run on a private tmux server (never yolo), synthetic plans only. Installing it in Kiro, Antigravity and Cursor by hand is listed as an owner check.
