# Remote check: start on the PC, finish from a phone (M4-G)

A real phase starts on the PC and is finished from a phone over SSH (e.g. Termius). It checks the narrow layout (SPEC §15.2), mouse and keys over SSH, colors, dialogs, and reattaching from another terminal.

## Setup

- [ ] The PC runs herdr; you can SSH into it from the phone and attach to the herdr session (`herdr` → the igris workspace)
- [ ] A real project (or the smoke project from `docs/smoke-herdr.md`) with a phase of 3+ agent tasks and at least one user task, `commit = "ask"`
- [ ] At least one notification channel set up, so the phone hears when igris needs you (`docs/check-notify.md`)

Record: phone and app (e.g. iPhone 16 / Termius 9.x), terminal size it reports (`tput cols; tput lines`), theme (dark/light), PC terminal.

## On the PC

```sh
igris arise PHASE
```

- [ ] The wide layout (≥ 100 columns) looks like SPEC §15.1; the first task starts
- [ ] Leave it running and walk away

## On the phone

Attach to the herdr session and switch to the igris pane.

**Layout**
- [ ] Below 100 columns igris shows the single-column layout: header, current task card, compact task list, last 3 log lines, action bar
- [ ] Nothing is cut off at the phone's size; rotate the phone (portrait ↔ landscape) and the layout follows
- [ ] Shrink to 50×20 if the app allows (font size): still usable
- [ ] The action bar wraps or folds into `More…`; no button is unreachable

**Input**
- [ ] Tapping buttons works (Pause, Mode, More…, `?`)
- [ ] Tapping a task row selects it; a second tap opens details; scrolling the list and log with a swipe works
- [ ] Keys from the phone keyboard work: `tab`, arrows, `enter`, `esc`, `?`, number keys in dialogs
- [ ] `o` (Open session) switches herdr to the Claude tab, and you can type into the session from the phone and come back

**Dialogs and the owner's part**
- [ ] When a session asks a question (Needs you), the phone is notified and the TUI marks it (`!`, red, `Answer…`)
- [ ] The commit dialog opens focused on **Commit**; answer from the phone
- [ ] A user task: mark it **Done** from the phone
- [ ] Skip dialog: type a reason with the phone keyboard; cancel it once, then skip a task you don't need
- [ ] Mode → `yolo` asks for the typed `skip permissions`; cancel it

**Colors and focus**
- [ ] Glyphs and words show every state without relying on color; the focused button is visible (`›…‹`)
- [ ] Colors are readable in the app's theme; if not, try `[tui] theme = "dark"`/`"light"` and note which works
- [ ] With `NO_COLOR=1 igris arise` (restart from the phone) everything is still readable

**Reattach**
- [ ] Quit the TUI with `q` on the phone, run `igris arise` again from the phone: it reattaches to the running session
- [ ] Let the phase finish from the phone: phase done is shown and notified

## Report

One line per finding: what you did, what you saw, what you expected, app and size. Screenshots help (crop out private text).

## Result

Date `____`, device/app `____`, result **pass / findings**. When there are no open findings, mark M4-G `done` in `imp-docs/tasks.md` and V011-02 `done` in `imp-docs/ROADMAP.md`.
