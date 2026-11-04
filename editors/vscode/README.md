# Igris for VS Code

Watch and steer an [igris](https://github.com/drilonrecica/igris) run from the editor: the plan's phases and tasks,
what needs you, and what each task changed. It is a client of `igris serve`; the engine stays in the `igris` binary.

Work in progress for v1.9. What it does so far:

- **The run view.** The Igris icon in the activity bar opens a tree of the plan's phases and tasks, each with its
  status, rank and mode, and the live run on top: the status word, the active tasks and what waits on you (a task's
  question, a reset request, a budget reached, the commit batch). Without a live run the tree comes from
  `igris status --json` and follows the plan's files as they change.
- **The status bar** shows the same status word as the TUI and the web UI (`● Working`, `! Needs you · 1 task needs
  you`, `Paused · 5h limit 96% · resumes 09:00 (≈ 3h12m)`, `Done · ✓ completed`). Click it to go to what needs you.
- **Reconnecting.** It attaches to the run's `igris serve` over `.igris/igris.sock` as soon as one starts. A dropped
  connection shows `▲ reconnecting…` and comes back with a fresh snapshot, as `igris attach` does; when the serve has
  gone, the view goes back to the plan and the status bar keeps how the run ended.
- **Answering.** Whatever waits on you arrives as a notification whose buttons are its first answers (`Commit`,
  `Leave uncommitted`, `Answer…`); a button for a question already answered elsewhere sends nothing. Click a row that
  needs you, or run **Igris: Answer what needs you**, for every answer in a QuickPick; a reason, a note, feedback on a
  plan or a new budget is typed in an InputBox. The answers are the web UI's: commit, skip requests, lost sessions,
  failed hooks, tasks that didn't integrate (Resolve, Rerun), reset requests, plans to approve (read the plan in an
  editor tab), a budget reached (Raise, Continue) and the commit batch (Keep, Undo).
- **Steering.** The run's row and the view's title bar have Pause after this task / Resume, Stop, Start anyway while a
  usage hold holds, and the mode of the next sessions; an active task's row has Mark done, Skip and Retry (fresh or
  continuing), a task still to do its next session's mode. Stop, Skip, Mark done and Undo ask a second time, as in the
  web UI.
- **Reading a task's changes.** A task's row (and a row that needs you) has **Read this task's changes**, also in the
  command palette: its commits (`<ID>: …`, from the run log) or, in a parallel phase, its worktree branch
  `igris/<run-id>-<ID>`, then its files, each opened in the editor's own diff. It reads with `git` in the project and
  changes nothing.
- **Plan CodeLens.** On the rows of the plan file (`tasks.md`) each task igris knows shows its status, **Run this task**
  (after one confirmation: `igris serve --only <ID>` in the project's own mode, never skip-permissions; not offered
  while a run is attached) and **Open its changes**.
- **Sessions as terminal tabs.** A run started from the editor runs headless on tmux (`backend = "auto"`), and each
  active task's Claude Code session opens as a terminal tab of its own, named `<ID> · <rank>`, attached to the run's
  tmux session and showing that task's window. The tab's first line names the task and says that a folder-trust
  question, if Claude Code asks one, is Claude Code's: answer it there. Closing the tab leaves nothing behind in tmux
  and the task runs on; **Igris: Open the task's session** (on an active task's row, or in the command palette) brings
  the tab back. A task's end closes its tab. You never handle tmux. On herdr, or with an `igris serve` older than 1.9,
  the command says where the session is instead. Side sessions stay in the terminal.
- **Onboarding.** **Igris: Set up** (also the button in the empty run view) is a first-run checklist: igris's own
  install check and each check of `igris doctor --json`, what you can act on first. A tool not found in PATH shows its
  install command (fixed text); choosing it puts the command in a terminal for you to read and run, the extension
  never runs it. In a folder with no project it offers `igris init`; in a project with no plan, **Create a starter
  plan…** (one of igris's templates or its example, `igris init --template` / `--example`, never overwriting a plan),
  from the checklist and from the run view. When nothing fails, **Start a run…** (the play button in the view's title,
  or the empty state) asks for a phase and a mode, confirms once and starts `igris serve`. It never offers `yolo` or
  `plan:yolo`.
- **Never skip permissions.** The mode pickers leave out `yolo` and `plan:yolo`, the extension never sends one nor
  says you typed the phrase: a skip-permissions run is started and changed in the terminal only (SPEC §7.3).

A snapshot of another protocol than 1 is refused with one line in the tree's place and nothing else runs, as is an
`igris` below 1.8.0 at activation.

Needs `igris` 1.8.0 or newer in `PATH` (SPEC §15.9). The extension runs where the workspace's files are
(`extensionKind: ["workspace"]`), so it works under Remote-WSL and Remote-SSH.
