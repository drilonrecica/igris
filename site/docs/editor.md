# igris in your editor

Since 1.9 there is an **Igris** extension for VS Code and the editors built on it (Cursor, Kiro, Antigravity, Windsurf). It lets you read what a task changed, watch a run and answer what needs you without leaving the editor, and it gives a first run to someone who would rather not start with tmux and a TUI.

It is a client of `igris serve`, like `igris attach` and the [web UI](control-from-your-phone.md): the engine, the plan writer, verify and every safety rule stay in the `igris` binary. Every surface is the editor's own (a sidebar tree, the status bar, QuickPick, InputBox, notifications, the diff editor and terminal tabs); there is no webview.

## Install

Install **igris 1.8.0 or newer** first ([install](install.md)), so `igris` is in the editor's `PATH`, then the extension, `drilonrecica.igris`, from the VS Code Marketplace, or from Open VSX in an editor that uses it. A `.vsix` is also attached to each GitHub release (`code --install-extension igris-1.9.0.vsix`).

Without igris, or with one older than 1.8.0, the extension says so in one line and does nothing else. A run whose `igris serve` speaks another protocol than 1 is refused the same way, never guessed at. A newer igris always works.

It runs where the workspace's files are, so under Remote-WSL and Remote-SSH it runs on the remote, next to the socket. Windows works through WSL, as igris does.

## First run

**Igris: Set up** (also the button in the empty run view) is a checklist: igris's own install check, then each check of `igris doctor`, what you can act on first. For a tool that isn't found it shows its install command; choosing it puts the command in a terminal for you to read and run, and the extension never runs it. In a folder with no project it offers `igris init`; in a project with no plan, **Create a starter plan…** (one of igris's templates, or its example; it never overwrites a plan). When nothing fails, **Start a run…** asks for a phase and a mode, confirms once and starts `igris serve`.

A run started from the editor has no herdr or tmux around it, so igris runs its sessions in a detached tmux session, `igris-<project>`. You never handle it: each active task's Claude Code session opens as a terminal tab named `<ID> · <rank>`. tmux itself needs to be installed; the checklist says so.

## Watching a run

- **The run view** in the activity bar: the plan's phases and tasks with status, rank and mode, the live run on top, and what waits on you (a task's question, a reset request, a budget reached, the commit batch). With no live run it comes from `igris status --json` and follows the plan's files.
- **The status bar** shows the same word as the TUI and the web UI (`● Working`, `! Needs you · 1 task needs you`, `Paused · 5h limit 96% · resumes 09:00`, `Done · ✓ completed`); a click goes to what needs you.
- A dropped connection shows `▲ reconnecting…` and comes back with a fresh snapshot, as `igris attach` does.

## Answering and steering

Whatever waits on you arrives as a notification whose buttons are its first answers (`Commit`, `Leave uncommitted`, `Answer…`). **Igris: Answer what needs you**, or a click on a row that needs you, lists every answer: commit, skip requests, lost sessions, failed hooks, tasks that didn't integrate, reset requests, plans to approve (read in an editor tab), a budget reached and the commit batch. A reason, a note or a new budget is typed in an input box. A button for a question already answered elsewhere sends nothing.

The run's row and the view's title have pause, resume, stop, **Start anyway** while a usage limit holds, and the mode of the next sessions; a task's row has Mark done, Skip and Retry. Stop, Skip, Mark done and Undo ask a second time, as on the web UI.

**The extension never offers skip-permissions mode.** `yolo` and `plan:yolo` are not in its pickers and it never types the confirmation for you; start such a run in a terminal ([modes](modes.md)).

## Reading a task's changes

**Read this task's changes** (on a task's row and in the command palette) lists the task's commits (`<ID>: …`, from the run log) or, in a parallel phase, its branch `igris/<run-id>-<ID>`, then its files, each opened in the editor's own diff. It reads with `git` in the project and changes nothing.

## On the plan

In the plan file, each task igris knows shows a CodeLens: its status, **Run this task** (one confirmation, then `igris serve --only <ID>` in the project's own mode, never skip-permissions; not offered while a run is attached) and **Open its changes**.

## Sessions as terminal tabs

Each active task's session opens as a terminal tab attached to the run's tmux session, showing that task's window. Closing the tab leaves nothing behind and the task runs on; **Igris: Open the task's session** brings it back, and a task's end closes its tab. If Claude Code asks whether you trust the folder, that question is Claude Code's: answer it in the tab, as the tab's first line says. With herdr, or an `igris serve` older than 1.9, the command says where the session is instead. [Side sessions](side-sessions.md) stay in the terminal.

## What it relies on

The extension uses protocol 1, `igris doctor --json` and a few commands (`igris status --json`, `phases --json`, `init`, `serve`), all of which 1.x keeps ([compatibility](../../docs/compatibility.md)). Its own views, commands and settings are not frozen. Its safety rules, including how the `.vsix` is built and checked before it is published, are in the [threat model](../../docs/threat-model.md) (B10).
