# Re-verifying Claude Code and herdr

igris depends on Claude Code flags and herdr JSON shapes that were checked by hand in P0-02 and P0-03 ([`decisions.md`](decisions.md)). Both tools change often. Run this checklist before every minor release, and whenever `igris check` warns about a newer major version or something breaks after an upgrade. Afterwards, update the verified versions (last section).

Every probe uses `sonnet` (never `haiku`), runs in a scratch git repository outside any real project, and touches only tabs it creates itself. No credentials are read or printed.

## Prerequisites

- [ ] You are inside a herdr pane (`echo $HERDR_WORKSPACE_ID` prints something), with `claude` logged in and `jq` and `uuidgen` installed.
- [ ] Record the versions: igris `____`, herdr `____` (`herdr --version`), Claude Code `____` (`claude --version`).
- [ ] Set up the scratch repo. The first `claude` there asks you to trust the folder; answer it once in a herdr tab, or run every headless probe below once to get past it:

  ```sh
  export S=$(mktemp -d) && cd "$S" && git init -q && echo hi > a.txt && git add -A && git commit -qm init
  ```

## Claude Code (P0-02)

Headless probes print JSON; `jq` picks out the field that matters. `< /dev/null` stops `claude -p` waiting 3 s for stdin.

- [ ] **Model aliases resolve.** For each of `fable opus sonnet`, the model key is a full model ID such as `claude-sonnet-5-5`:
  ```sh
  for m in fable opus sonnet; do claude -p --model $m --output-format json "Reply with ok" < /dev/null | jq -r '.modelUsage | keys[]'; done
  ```
- [ ] **Permission modes.** `claude --help` lists `--permission-mode` with `acceptEdits`, `auto`, `bypassPermissions`, `manual`, `dontAsk` and `plan`, and no `default`. It also lists `--dangerously-skip-permissions`, `--session-id`, `--resume`, `--append-system-prompt` and `--fallback-model`.
- [ ] **`--append-system-prompt-file` works**, even though `--help` may not list it. The reply contains `PERSIMMON`:
  ```sh
  echo "The secret word is PERSIMMON." > rules.md
  claude -p --model sonnet --append-system-prompt-file rules.md "What is the secret word? One word." < /dev/null
  ```
- [ ] **`--session-id` and `--resume`.** The second reply knows the word, and both report the same `session_id`:
  ```sh
  id=$(uuidgen); claude -p --model sonnet --session-id $id --output-format json "Remember the word KUMQUAT. Reply ok." < /dev/null | jq -r .session_id
  claude -p --model sonnet --resume $id --output-format json "Which word did I ask you to remember?" < /dev/null | jq -r '.session_id, .result'
  ```
- [ ] **`--model` beats the settings `model`.** The model key is a sonnet ID, not opus:
  ```sh
  mkdir -p .claude && echo '{"model":"opus"}' > .claude/settings.local.json
  claude -p --model sonnet --output-format json "Reply with ok" < /dev/null | jq -r '.modelUsage | keys[]'
  ```
- [ ] **Allow-rule syntax.** With `igris` on `PATH`, `Bash(igris done:*)` lets a headless session run `igris done` without a prompt. The reply shows the command ran and printed `igris: done signal recorded for T-01` (or a not-in-plan error from igris, which still proves the command ran); a permission denial means the rule syntax changed:
  ```sh
  echo '{"model":"sonnet","permissions":{"allow":["Bash(igris done:*)"]}}' > .claude/settings.local.json
  printf '## T\n\n| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n| T-01 | x | — | ready | sonnet | agent |\n' > tasks.md && touch igris.toml
  claude -p --model sonnet --output-format json "Run exactly this shell command and tell me its output: igris done T-01" < /dev/null | jq -r '.result, .permission_denials'
  ```
- [ ] **`ANTHROPIC_API_KEY` takes precedence.** With a bogus key, the request fails (a warning that the key takes precedence over your claude.ai login, or an authentication error). This is why `igris arise` warns about the variable:
  ```sh
  ANTHROPIC_API_KEY=bogus claude -p --model sonnet "Reply with ok" < /dev/null; echo "exit $?"
  ```
- [ ] **Folder trust.** Covered by the herdr `agent start` check below: a new folder starts with the trust prompt, which herdr reports as `agent_not_ready` and status `blocked`.

## herdr (P0-03)

Each command prints JSON. Compare its **shape** with the fixture in `internal/backend/herdr/testdata/`, ignoring IDs, paths and numbers. This prints the key paths, so two shapes can be diffed:

```sh
shape() { jq -c '[paths(scalars) | map(if type == "number" then "#" else . end) | join(".")] | unique'; }
fx=<path to the igris checkout>/internal/backend/herdr/testdata
```

New keys are fine. A missing key or a renamed code needs a fixture refresh (last section).

- [ ] **`status server`** still prints `version:` and `status: running` lines like `status_server.txt`: `herdr status server`.
- [ ] **`tab create`** returns `.result.tab.tab_id` and `.result.root_pane.pane_id`, like `tab_create.json`:
  ```sh
  herdr tab create --workspace "$HERDR_WORKSPACE_ID" --cwd "$S" --label "reverify" --no-focus > tab.json
  T=$(jq -r .result.tab.tab_id tab.json); P=$(jq -r .result.root_pane.pane_id tab.json)
  diff <(shape < tab.json) <(shape < "$fx/tab_create.json")
  ```
- [ ] **`agent start` in a new folder** (do this before the folder is trusted) fails with `agent_not_ready` and a non-zero exit, like `agent_start_not_ready.json`, and `pane get` reports `blocked` (the trust dialog defaults to "No, exit"). Then answer the trust prompt in the tab and close it. In a folder already trusted, start a fresh one in `mktemp -d` for this check:
  ```sh
  herdr agent start rv-trust --kind claude --pane "$P" --timeout 15000 -- --model sonnet; echo "exit $?"
  ```
- [ ] **`agent start` in a trusted folder** returns `agent_status` `idle` and `interactive_ready: true` with an `argv`, like `agent_start_ok.json`. Use a new tab: `herdr tab create …` as above, then:
  ```sh
  herdr agent start rv --kind claude --pane "$P" --timeout 60000 -- --model sonnet > start.json; diff <(shape < start.json) <(shape < "$fx/agent_start_ok.json")
  ```
- [ ] **Control characters in arguments are rejected** with `invalid_agent_argument`: `herdr agent start rv2 --kind claude --pane "$P" --timeout 15000 -- "$(printf 'a\nb')"`.
- [ ] **`agent prompt` without `--wait`** returns at once with the pre-turn status, like `agent_prompt_nowait.json`. A multi-line text arrives intact in the tab:
  ```sh
  herdr agent prompt rv "$(printf 'Reply with ok.\nThis is line two.')" | shape
  ```
- [ ] **`pane get`** reports `.result.pane.agent_status`, one of `unknown`, `idle`, `working`, `blocked` or `done`, like `pane_get_*.json`: `herdr pane get "$P" | shape`.
- [ ] **`agent wait`** returns at the first settled state, like `agent_wait_settled.json`. `--until working --timeout 2000` on an idle agent fails with `timeout`, like `error_wait_timeout.json`:
  ```sh
  herdr agent wait rv --timeout 60000 | shape; herdr agent wait rv --until working --timeout 2000; echo "exit $?"
  ```
- [ ] **`agent prompt` while blocked.** Prompt the agent to run a command that needs approval (`Run the shell command: touch b.txt`). While the approval dialog shows, `pane get` reports `blocked`, and a second `agent prompt` fails with `agent_blocked`, like `error_agent_blocked.json`. Answer the dialog with Esc afterwards.
- [ ] **`agent read`** prints the transcript as plain text, like `agent_read_recent_unwrapped.txt`: `herdr agent read rv --source recent-unwrapped --lines 20`.
- [ ] **`tab focus`** returns `.result.tab` with `focused: true`, like `tab_focus.json`: `herdr tab focus "$T" | shape`.
- [ ] **`notification show`** returns `{"result":{"shown":true,…}}`, like `notification_show.json`, and a toast appears: `herdr notification show "igris reverify" --body "test" --sound done`.
- [ ] **`tab close`** returns `{"result":{"type":"ok"}}`. Closing again fails with `tab_not_found`, and `pane get` on the closed pane fails with `pane_not_found` (`error_tab_close_not_found.json`, `error_pane_not_found.json`). Errors are JSON on stderr: `{"error":{"code","message"},"id"}`, exit 1:
  ```sh
  herdr tab close "$T"; herdr tab close "$T"; echo "exit $?"; herdr pane get "$P"; echo "exit $?"
  ```
- [ ] **`integration status`** still prints a `claude:` line like `integration_status.txt`: `herdr integration status`.
- [ ] **End to end:** `make install`, then run `docs/smoke-herdr.md`. The checklist above verifies the parts, and the smoke run verifies how igris uses them.

## Claude Code hooks (V03-P1)

igris passes each session a hooks-only `--settings` file (`.igris/hooks/<ID>.settings.json`) and reads the state the hooks record. Check that the events still fire as recorded in `internal/hook/testdata/`:

- [ ] Write a hooks-only settings file whose hooks append their stdin to a log (one command per event: `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PermissionRequest`, `Notification`, `Stop`, `SessionEnd`), start `claude --model sonnet --session-id $(uuidgen) --permission-mode manual --settings <file>` in a tmux window or herdr tab, and drive it:
  - after the trust question: `SessionStart` (`source` `startup`), with the `session_id` you passed;
  - a prompt running an allow-listed command: `UserPromptSubmit` → `PreToolUse` → `PostToolUse` → `Stop`; the allow rule in `.claude/settings.local.json` still applies (no `PermissionRequest`);
  - a command that needs approval: `PreToolUse` → `PermissionRequest` → `Notification` with `notification_type` `permission_prompt`;
  - asking for an `AskUserQuestion` question: `PreToolUse` with `tool_name` `AskUserQuestion` → `PermissionRequest`;
  - ~60 s idle: `Notification` `idle_prompt`; `/exit`: `SessionEnd`.
- [ ] A hook command that fails (a missing binary) is reported by Claude Code as non-blocking and the session carries on.
- [ ] The fields igris reads (`session_id`, `hook_event_name`, `source`, `tool_name`, `notification_type`) are still there; save new payloads over the fixtures (scrubbed to `/work/proj`, a synthetic UUID, no prompt or reply text) and run `go test ./internal/hook/`.

## tmux (V03-P2)

In a scratch server, so your own sessions are untouched: `tmux -L igris-verify -f /dev/null new-session -d`, and `T() { tmux -L igris-verify -f /dev/null "$@"; }`.

- [ ] `T new-window -d -P -F '#{window_id} #{pane_id}' -n x -- sh -c 'printf "%s|" "$@" > argv.txt; sleep 30' x 'a b' 'c;d' '$HOME'` prints `@N %N`, and `argv.txt` holds `a b|c;d|$HOME|` (argv, no shell), like `new_window.txt`.
- [ ] `T new-window -d -n '#(touch pwn) ##x' -- sleep 5` **creates `pwn`** and names the window ` #x`: `-n` is still a format, so igris's `##` escaping is still needed. `-c DIR` with a `#` in DIR is taken literally.
- [ ] `printf 'a\nb' | T load-buffer -b b -` then `T paste-buffer -p -d -b b -t %N` and `T send-keys -t %N Enter` deliver the text; the buffer is gone afterwards.
- [ ] `T list-panes -t %N -F '#{pane_id}\t#{pane_dead}\t#{pane_dead_status}\t#{pane_current_command}'` matches `list_panes_alive.txt`; with `remain-on-exit on` and the command exited, `list_panes_dead.txt`; a missing pane gives `can't find pane: %N`, exit 1 (`err_pane.txt`).
- [ ] `T kill-window -t @N` twice: the second gives `can't find window: @N` (`err_window.txt`). Outside a server: `error connecting to …` (`err_no_server.txt`).
- [ ] `tmux -V` prints the version (`version.txt`).
- [ ] **End to end:** run `docs/smoke-tmux.md`.

## After the run

- **Shapes unchanged:** nothing to refresh.
- **A shape changed:** save the new output over the fixture, scrubbing paths and the home directory to `/work/demo` and `/home/user`. Then run `go test ./internal/backend/herdr/`. Fix the client and its tests in the same change, and note the change in `decisions.md` under P0-03. A changed Claude Code flag goes into `internal/engine/argv.go` / `mode.go` and SPEC §7.
- **Raise the verified versions:** after a clean run, set `Tested` for the tool (Claude Code, herdr, tmux) in `internal/checks/tools.go` (`Tools`) and in SPEC §11.4 to the version you ran. Change `Min` only when igris starts to need something newer.
- Record the run below.

## Runs

| Date | igris | Claude Code | herdr | Result |
|---|---|---|---|---|
| 2026-10-07 | v0.1.1 + v0.1.2 work | 2.1.292 | 0.9.1 (`private_protocol` 22) | Clean. Every P0-02 item as recorded (aliases resolve to the same model IDs; the API-key warning now reads "claude.ai connectors are disabled because ANTHROPIC_API_KEY … takes precedence over your claude.ai login"). herdr shapes unchanged except new `agent_session` objects in agent and pane results (additive); `agent_not_ready` exits 1 instead of 2 (igris reads the code only). New fixture `integration_status_current.txt` for an installed integration (`claude: current (v10)`). `Tested` for Claude Code raised to 2.1.292. |
| 2026-10-08 | v0.4.0 (`b7535ea`) | 2.1.294 | 0.9.1 (`private_protocol` 22), tmux 3.7c | Clean, with `sonnet` in place of `haiku` in every probe (owner rule: no haiku), so the `haiku` alias was not resolved; `fable`/`opus`/`sonnet` → `claude-fable-5-1`/`claude-opus-5-5`/`claude-sonnet-5-5`. `--model sonnet` beat a settings `"model":"opus"`. herdr shapes unchanged except the additive `agent_session` (already noted); `agent start` right after `tab create` can give `agent_pane_busy` while the shell starts (igris retries it). Claude Code hooks fire as in `internal/hook/testdata/` (`idle_prompt` only after a finished turn, not after an Esc); fixtures not refreshed. tmux probes as recorded; smoke S0 run on herdr and on tmux. `Tested` for Claude Code raised to 2.1.294. |
