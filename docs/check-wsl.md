# WSL2 check: igris on Windows through WSL2 (V013-01)

Windows is supported through WSL2: herdr, Claude Code and igris all run inside the Linux distribution. Native Windows is not supported (SPEC §1). This checklist runs `docs/smoke-herdr.md` under the conditions Windows users actually have: a project on the Windows drive, files saved by Windows editors, and paths with spaces.

## Setup

- [ ] WSL2 with Ubuntu (`wsl -l -v` in PowerShell shows `VERSION 2`), opened in Windows Terminal
- [ ] Inside WSL: herdr, Claude Code (logged in, `ANTHROPIC_API_KEY` not set) and igris installed; the smoke prerequisites hold

Record: Windows build `____`, distribution `____`, Windows Terminal version `____`, igris `____`, herdr `____`, Claude Code `____`.

## Runs

1. **Linux filesystem** (the recommended setup): run `docs/smoke-herdr.md` in a project under `~`.
   - [ ] The whole checklist passes
   - [ ] The TUI (`igris arise S0` without `--no-tui`) has readable colors in Windows Terminal, and mouse clicks on dialog options work
2. **Windows drive** — a project under `/mnt/c/Users/<you>/…`, ideally in a folder whose name has a space:
   - [ ] Save `tasks.md` and `igris.toml` with a Windows editor (Notepad: UTF-8 with BOM if offered, CRLF line endings); `igris check` passes
   - [ ] `git config core.autocrlf true`; the smoke run passes and `tasks.md` keeps its BOM and CRLF (`head -c 3 tasks.md | od -An -tx1` shows `ef bb bf`; `file tasks.md` says `CRLF`)
   - [ ] Claude Code opens the tab in the right folder (paths with spaces) and the rules file is found
   - [ ] Note how slow it is compared with run 1 (`/mnt/c` goes through the 9P file server)
3. **Plan on the other filesystem:** in the run-1 project, make `tasks.md` a symlink to a plan under `/mnt/c`.
   - [ ] Status writes land in the Windows file, and the symlink stays a symlink

## Result

Date `____`, tester `____`, result **pass / fail**, notes:

```
```

## Simulated on Linux (2026-10-07, v0.1.3)

No WSL2 machine was available for v0.1.3, so the WSL-specific risks were reproduced on Linux (Fedora 44; igris v0.1.3-dev, herdr 0.9.1, Claude Code 2.1.292), with one smoke project combining all of them:

- project path with spaces on a tmpfs (`…/John Doe/my project`);
- `tasks.md` a symlink into `/dev/shm`, a different filesystem (different `st_dev`), standing in for `/mnt/c`;
- `tasks.md` and `igris.toml` with a UTF-8 BOM and CRLF line endings, the plan starting directly with its `## S0` heading;
- `git config core.autocrlf true`.

Result: **pass** after one fix. The run went through the trust prompt (Needs you), the verify failure loop, the user task and S0-03; afterwards the plan was byte-for-byte the expected file (BOM and CRLF kept, only Status cells changed), the symlink was intact and no temp files were left in either directory. herdr quoted the rules-file path with spaces correctly.

Finding, fixed in v0.1.3: a plan starting with a UTF-8 BOM directly followed by a `##` phase heading (or a table) was invalid ("task table outside a phase"), because the BOM hid the first heading. igris now skips a leading BOM while parsing and keeps it in the file.

Not covered by the simulation: Windows Terminal rendering and mouse, `/mnt/c` speed, drvfs permission semantics (`chmod` is a no-op there; igris only sets modes and never checks them, so it should not fail), and running herdr inside WSL. Runs 1–3 above on a real machine close those.
