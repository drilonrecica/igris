# Smoke test: igris on a real herdr

`make test` never needs herdr (the backend tests replay recorded fixtures). This checklist is the manual end-to-end check that igris, herdr and Claude Code work together. Run it after changing `internal/backend/herdr/`, the engine's session handling, or when upgrading herdr or Claude Code.

It uses a synthetic 3-task plan: one agent task, one user task, and one agent task whose first attempt fails verification. Nothing in it comes from a real project.

## Prerequisites

- [ ] herdr is running and you are in a herdr pane (`echo $HERDR_WORKSPACE_ID` prints a value)
- [ ] `herdr status server` shows `status: running`
- [ ] Optional: `herdr integration install claude` (`herdr integration status` shows `claude: installed`). Without it igris uses Claude Code hooks for the agent state; run the checklist both ways after changing `internal/hook/`
- [ ] `claude` is logged in with your subscription and `ANTHROPIC_API_KEY` is **not** set
- [ ] `igris version` prints the build you want to test (`make install`)

Record versions: igris `____`, herdr `____`, Claude Code `____`.

## Set up the scratch project

```sh
dir=$(mktemp -d) && cd "$dir" && git init -q
igris init
```

- [ ] `igris init` created `igris.toml`, `.igris/`, a `.gitignore` entry and `.claude/settings.local.json` with `Bash(igris done:*)` (and no `skip` rule); running it again changes nothing

Edit `igris.toml` so it contains:

```toml
plan = "tasks.md"
needs_input_after = "20s"

[run]
verify = "test -f verify-ok || { touch verify-ok; echo 'smoke: first verification always fails'; exit 1; }"
verify_max_attempts = 3
commit = "never"
```

Create `tasks.md`:

```markdown
## S0 — Smoke

| ID | Task | Deps | Status | Model | Owner |
|----|------|------|--------|-------|-------|
| S0-01 | Create `hello.txt` containing the word `hello`. | — | ready | sonnet | agent |
| S0-02 | Look at `hello.txt` and confirm it says hello. | S0-01 | blocked | — | user |
| S0-03 | Create `done.txt` containing `done`. | S0-02 | blocked | sonnet | agent |
```

The `verify` command fails the first time it ever runs (it creates `verify-ok` and exits 1) and passes afterwards, so the first agent task goes through the failure loop exactly once.

Commit the scaffolding so igris doesn't warn about uncommitted changes: `git add -A && git -c user.name=smoke -c user.email=smoke@example.invalid commit -qm scratch`.

- [ ] `igris check` passes and `igris arise S0 --dry-run` lists the three tasks

## Run

```sh
igris arise S0 --no-tui
```

### Start and S0-01

- [ ] Igris prints no integration warning (or prints the hint if you skipped the integration)
- [ ] A new herdr tab labelled `S0-01 · sonnet` opens without stealing focus, in the scratch directory
- [ ] Claude Code starts in it on the sonnet model; if a folder-trust prompt appears, igris reports Needs you, and the task prompt is delivered only after you accept the prompt
- [ ] The task prompt arrives as one message (multi-line intact)
- [ ] The session creates `hello.txt` and runs `igris done S0-01 --note …` **without** a permission prompt

### Verify failure loop (S0-01)

- [ ] Igris reports that verification failed (`smoke: first verification always fails`) and sends the failure text into the **same** session, which is told to run `igris done S0-01` again
- [ ] The session runs `igris done` again, verification passes and the task is marked `done`
- [ ] The tab closes once the session has settled

### Toast and user task (S0-02)

- [ ] A herdr toast appears when igris needs you (user task or Needs you), with the `request` sound, naming project, phase and task, with no file contents
- [ ] For S0-02 igris prints the task and waits; answer `done` on stdin
- [ ] S0-02 becomes `done` in `tasks.md` and S0-03 starts

### Needs you

- [ ] In S0-03, leave the session idle without finishing for longer than `needs_input_after`: igris reports Needs you once, plus one toast
- [ ] Typing in the session clears the flag

### Session lost and reattach

- [ ] While S0-03 runs, press Ctrl-C in the igris pane: igris stops, and the Claude tab **stays open**
- [ ] Run `igris arise` again: it reports `reattached to its session` and keeps watching (this exercises `Attach`)
- [ ] Close the S0-03 tab by hand: igris reports Session lost and offers `retry [continue|fresh]`, `done`, `skip`, `stop`
- [ ] `retry fresh` opens a new tab and the prompt mentions it is resumed
- [ ] Stop igris with Ctrl-C, close the tab, run `igris arise` once more: it offers the lost-session choice instead of crashing

### Finish

- [ ] The run ends with the phase complete; `tasks.md` shows S0-01…S0-03 `done`
- [ ] `.igris/runs.jsonl` has the task, verify and notification events and no notification secrets or command output
- [ ] No Claude tabs are left open except any you kept on purpose

## Without herdr

- [ ] Outside a herdr pane and outside tmux (`env -u HERDR_WORKSPACE_ID -u TMUX igris arise S0 --no-tui`) igris exits 1, saying it must run inside a herdr pane or a tmux session, and writes nothing
- [ ] With `backend = "herdr"` in `igris.toml`, outside a herdr pane, the message says to run igris inside a herdr pane, or to use tmux
- [ ] `igris check`, `status`, `done` and `skip` still work

## Result

Date `____`, tester `____`, result **pass / fail**, notes (anything surprising, with the herdr error code if one showed up):

```
```
