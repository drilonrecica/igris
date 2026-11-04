# Changelog

The Igris extension is released with igris, under igris's version numbers. The full history is igris's
[CHANGELOG](https://github.com/drilonrecica/igris/blob/master/CHANGELOG.md).

## 1.9.0

First release. A client of `igris serve` for VS Code and its forks (Cursor, Kiro, Antigravity, Windsurf):

- the run view (the plan's phases and tasks, the live run, what needs you) and the status bar, with reconnecting;
- answering and steering from notifications, QuickPick and InputBox, never in skip-permissions mode;
- a task's commits or branch in the editor's diff, and a CodeLens on the plan's rows;
- each active task's session as a terminal tab on the run's tmux session;
- a first-run checklist, `igris init`, a starter plan and a run started with one button.

Needs igris 1.8.0 or newer in `PATH`. Works under Remote-WSL and Remote-SSH.
