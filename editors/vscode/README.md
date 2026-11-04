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

A snapshot of another protocol than 1 is refused with one line in the tree's place and nothing else runs, as is an
`igris` below 1.8.0 at activation.

Needs `igris` 1.8.0 or newer in `PATH` (SPEC §15.9). The extension runs where the workspace's files are
(`extensionKind: ["workspace"]`), so it works under Remote-WSL and Remote-SSH.
