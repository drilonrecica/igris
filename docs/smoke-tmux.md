# Smoke test: igris on a real tmux

`make test` never needs tmux (the backend tests replay recorded outputs, and the conformance suite runs on a simulated server). This checklist is the manual end-to-end check that igris, tmux and Claude Code work together. Run it after changing `internal/backend/tmux/`, `internal/hook/`, the engine's session handling, or when upgrading tmux or Claude Code.

It uses the synthetic plan of [`smoke-herdr.md`](smoke-herdr.md) plus one phase whose task waits for an answer. Nothing in it comes from a real project.

## Prerequisites

- [ ] tmux 3.2 or newer (`tmux -V`), and you are **inside** tmux (`echo $TMUX` prints a value) but **not** inside a herdr pane (`echo $HERDR_WORKSPACE_ID` prints nothing; inside herdr, `backend = "auto"` picks herdr)
- [ ] `claude` is logged in with your subscription and `ANTHROPIC_API_KEY` is **not** set
- [ ] `igris version` prints the build you want to test (`make install`), and `which igris` is that build

Record versions: igris `____`, tmux `____`, Claude Code `____`.

## Set up the scratch project

```sh
dir=$(mktemp -d) && cd "$dir" && git init -q
igris init
```

- [ ] `igris.toml` says `backend = "auto"`; `igris doctor` shows `tmux 3.x` and `tmux is running and igris is inside it`, and no herdr lines

Edit `igris.toml` and create `tasks.md` exactly as in [`smoke-herdr.md`](smoke-herdr.md#set-up-the-scratch-project) (`needs_input_after = "20s"`, the failing-once `verify`, `commit = "never"`). Add a second phase:

```markdown

## S1 — Waits

| ID | Task | Deps | Status | Model | Owner |
|----|------|------|--------|-------|-------|
| S1-01 | Ask the owner in the chat (plain text, not a tool) which colour they prefer, then stop and wait for the answer. Write the answer to `colour.txt` only after they reply. | — | ready | sonnet | agent |
```

Commit the scaffolding (`git add -A && git -c user.name=smoke -c user.email=smoke@example.invalid commit -qm scratch`).

- [ ] `igris check` passes and `igris arise S0 --dry-run` lists the three tasks

## Run S0

```sh
igris arise S0 --no-tui
```

- [ ] A new tmux window named `S0-01 · sonnet` opens in the background, in the scratch directory, running `claude`
- [ ] A fresh directory shows Claude Code's folder-trust question: no hook fires, so after ~15 s the log says `session open`, and after `needs_input_after` igris reports **Needs you** (`blocked`)
- [ ] Accept the trust question in the window: the task prompt arrives by itself, as one message (multi-line intact), and igris logs `is working again`
- [ ] `.igris/hooks/S0-01.settings.json` exists (0600) and holds only `hooks`; `.igris/agent-state/` gets a file named by the session UUID
- [ ] The session runs `igris done S0-01` without a permission prompt; the failing verify output is pasted into the **same** window; the second `igris done` passes and the window closes
- [ ] S0-02 asks you on stdin; `done looks good` finishes it; S0-03 opens a window within a few seconds (the folder is trusted now) and completes
- [ ] The run ends with `phase S0 complete`

## Run S1: needs you, reattach, session lost

```sh
igris arise S1 --no-tui
```

- [ ] The session asks for a colour and stops; after `needs_input_after` igris reports **Needs you** (`idle`), once
- [ ] Ctrl-C igris: the session's window **stays open**. `igris arise` again says `reattached to its session`
- [ ] Kill the session's window (`tmux kill-window -t <its window>`): igris reports **Session lost** and offers `retry continue`, `retry fresh`, `done`, `skip`, `stop`
- [ ] `retry continue` opens a new window resuming the conversation (it still waits for your colour)
- [ ] Answer `blue` in that window: the agent writes `colour.txt` and runs `igris done`; the phase completes

## TUI and toasts

- [ ] `igris` (home) shows `tmux ✓` in the header
- [ ] Run a phase from home; `o` switches tmux to the session's window
- [ ] A toast (`igris: …`) shows in tmux's status line when igris needs you
- [ ] A permission prompt in a `default`-mode session (e.g. ask the session to `touch` a file outside the allow list) raises **Needs you** (`blocked`) through the `PermissionRequest` hook

## Without a multiplexer

- [ ] Outside tmux and herdr (`env -u TMUX -u HERDR_WORKSPACE_ID igris arise S0 --no-tui`), igris exits 1 saying it must run inside a herdr pane or a tmux session, and writes nothing
- [ ] With `backend = "tmux"` in `igris.toml` but outside tmux, the message says what tmux needs

## Result

Date `____`, tester `____`, platform `____`, result **pass / fail**, notes:

```
```
