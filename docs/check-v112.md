# v1.12 gate

The v1.12 gate (V112-G) checks the three changes of 1.12 live: the mode on a task row (V112-01), the run view's Trim layout (V112-04) and the old cookie's retirement (V112-02). It covers:

- a scratch plan with Mode cells `plan`, `yolo` and none, run in `auto` with `--only` on the tasks that don't skip permissions: the rows at 120×40, 80×24 and 50×20, `M` on a row, and home's phase page;
- the Trim layout through `tmux resize-window` at 80×24, 90×28, 100×24 and 160×50: a parallel phase with one task needing you, a quiet log, the button row;
- the web UI with a browser that holds only the old `igris_token` cookie, then pairing through `igris serve token`;
- `make fmt lint test`, the race detector and govulncheck.

It ran unattended on Linux (Fedora 44) on 2026-11-12, 00:13–00:35, from the V112-G task session. The build was this tree (`master` at `36aa4e9`, then with the fix below), built with `-X main.version=1.12.0` into a scratch `GOBIN` first in `PATH`. Both projects were synthetic scratch git repos under one `mktemp -d` directory, with `verify = "true"`, `commit = "ask"`, the backend's toast off, and every `[models]` key mapped to sonnet. The run mode was `auto` both times. P-02, the `yolo` row, was never selected, so no session ran in a skip-permissions mode and no confirmation was asked.

**Where it ran.** A private tmux server, `tmux -L gate -f /dev/null`, started from `env -i` inside a `systemd-run --user --scope` (MemoryMax 6G, TasksMax 400), with only `HOME`, `USER`, `LANG`, `TERM`, `COLORTERM`, the D-Bus address, `SHELL=/bin/bash`, the gate's `PATH` (scratch `GOBIN`, then Claude Code's directory and the system's, not `~/go/bin`), and `XDG_RUNTIME_DIR`, `XDG_STATE_HOME` and `TMUX_TMPDIR` set to directories of the scratch tree. No `HERDR_*` or `CLAUDE*` variable reached it. The first window said `igris is /tmp/igris-gate-v112.…/bin/igris` and `igris 1.12.0`. Each new worktree asked Claude Code's folder-trust question once (Down, Enter in its pane), as at earlier gates. The browser was the Playwright MCP's Chromium.

**Versions:** Claude Code 2.1.322, tmux 3.8, Go 1.27.4, govulncheck 1.8.0.

**Projects:** trim, a format-3 plan with one parallel phase P (`[parallel] max = 4`, `exclusive_owner_tasks = false`):

| Task | Mode | Owner | What it does |
|---|---|---|---|
| P-01 | `plan` | sonnet | plans a one-line `hello.txt`, plan only |
| P-02 | `yolo` | sonnet | never selected |
| P-03 | — | sonnet | runs `./ticks.sh` (a tick every 12 s, 96 s) eight times, then `igris done` |
| P-04 | — | user | the owner's turn |
| P-05 | — | sonnet | as P-03 |

Run with `igris arise P --mode auto --only P-01,P-03,P-04,P-05`. The second project, quiet, had one sonnet task Q-01 (`./ticks.sh` four times) and `[serve] http = "127.0.0.1:18112"`, a port outside Linux's ephemeral range (the v1.11 gate's pitfall).

## The mode on a task row

**Home's phase page** (`igris`, then `t` on phase P), before the run, `default_mode = "auto"`:

```
›· P-01  Plan a greeting                                                         sonnet plan with P-02, P-03, P-04, P-05
 · P-02  Never runs                                                              sonnet yolo with P-01, P-03, P-04, P-05
 · P-03  Tick A                                                                  sonnet      with P-01, P-02, P-04, P-05
 · P-04  Water the plant                                                         user        with P-01, P-02, P-03, P-05
 · P-05  Tick B                                                                  sonnet      with P-01, P-02, P-03, P-04
```

| Size | Result |
|---|---|
| 120×40 | as above: `plan` and `yolo`, nothing on P-03, P-04 and P-05, the column aligned after the rank; `plan` dim (SGR 2), `yolo` in Ember (`38;2;255;176;0`, #FFB000) |
| 80×24 | the same words; the title column narrower |
| 50×20 | the `with …` marker gone, the mode words kept: `sonnet plan`, `sonnet yolo` (§15.4: the marker goes first) |

**The run view** (wide TASKS rows, 120×40), the same three words: `sonnet plan`, `sonnet yolo` and none. At 100×24 the same. At 80×24 and 50×20 (the narrow TASKS cells) only the skip-permissions mode shows, `· P-02 Never runs yolo`, and no cell has the parallel marker. Done rows show no mode: after P-01 finished its row read `✓ P-01  Plan a greeting sonnet`.

**`M` on a row.** With the focus on TASKS (Tab from the log, or Shift-Tab from the bar) and P-03 selected, `M` opened `Mode for P-03's next session` with `auto … (current)` focused. `3` (accept) gave the feed line `P-03's next session in mode accept`, and the row became `›● P-03  Tick A        sonnet accept with P-01, P-02, P-05`: the column widened to `accept` and the titles were cut to `Plan a greet…` and `Water the pl…` to make room. The session went on in auto, as §7.2 says.

## The Trim layout

**A parallel phase with one task needing you.** P-01's session presented its plan (Needs you, `a plan to approve`); P-04's turn was marked done with `igris done P-04` from another window, so P-01 was the one task needing you while P-03 and P-05 worked.

| Size | Result |
|---|---|
| 120×40 | the wide layout; P-01's card boxed with its whole body (state, model, tokens, tail, task text, `[Answer…] [o Open session] [t Details]`); P-03 and P-05 one line each, `● P-03 Tick A  sonnet  1m3s  · Germinating… (44s · ↓ 227 t…`, `›` before the selected one; the banner `! P-01 needs you · a plan to approve [Answer…]`; the bar `[›Answer…‹] [o Open session] [p Pause] [x Stop…] [? More]` |
| 100×24 | the same arrangement in less room: P-01's card full, the other two one line each; the same one-row bar |
| 90×28, 80×24 | the narrow layout: the switcher `‹ ! P-01 · 1 of 3 › ● P-03 ● P-05` over P-01's card (its body whole), TASKS two rows (as tall as its five cells), the log the rest; the bar `[›Answer…‹] [o Open session] [d Done] [r Retry…] [p Pause] [x Stop…] [? More]`; the header drops the backend first (`· 25% ██░░░░ · 3 active · mode auto` without `tmux` at 80) |
| 50×20 | the card keeps its need, the log its three lines, TASKS gets the two rows left (P-05's cell scrolled out of view, reachable with the arrows); the bar's labels shortened to keys `[Answer…] [o] [d] [r] [p] [x] [?]`; the header `P · slice 1/4 · 3 act…` |
| 160×50 | as before: every card a box (P-01, P-03, P-05), the full bar `[›Answer…‹] [o Open session] [m Mode] [M Task mode] [p Pause] [d Done] [r Retry…] [s Skip…] [x Stop…] [S Side…] [?] [q Quit]` |

With no one needing you (after the plan was approved), at 120×40 P-03 and P-05 were both boxes again, as there was room for both. After P-03 and P-05 finished, **Commit all** made two commits (`P-03: Tick A`, `P-05: Tick B`) and the run ended `Done ✓ completed` (4 done, wall 14m00s).

**A quiet log: the tail fills** (quiet, Q-01 working, four log lines):

| Size | Result |
|---|---|
| 80×24 | the log exactly its four lines; the card's tail six lines (three in the narrow layout plus the three the log lent) |
| 90×28 | the tail ten lines |
| 100×24 | the wide layout; the card short of rows puts state and model on one line, `● Working · 44s · sonnet · mode auto`; the tail six lines |
| 160×50 | the tail every line the session had (22); the log keeps the rows the tail can't use; the full bar, as before |

The working bar in all four was `[›o Open session‹] [p Pause] [x Stop…] [? More]` below 160 columns.

## The old cookie

The quiet project served on `127.0.0.1:18112`. The browser's cookies were cleared, then one cookie set by hand: `igris_token`, holding the project's *valid* token (`.igris/serve-token`), host `127.0.0.1`, path `/`, HttpOnly, SameSite Lax.

| Step | Result |
|---|---|
| `/` with only `igris_token` | `401` · `Not paired · Run igris serve token on the machine and open its URL on this device.`; `/api/v1/events` `401`; no other cookie set (nothing migrated) |
| `/` again | `401`, still only `igris_token` |
| `igris serve token`, its URL opened | `/pair?token=…` → `200`, redirected to `/`; cookie `igris_token_a2b051a17959` (HttpOnly, Lax, path `/`) |
| `/` | `200` · `igris · quiet · Working · nothing needs you · Q-01: working`; `/api/v1/events` `200` |

The hand-set `igris_token` stayed in the browser beside the new cookie, unused (observation below).

## The suite

- **Lint and tests:** `make fmt lint test` with the fix: `0 issues.`, every Go package ok (36), the extension's 178 unit tests passing.
- **Race:** `go test -race -count=1 ./...` with the fix: every package ok (36).
- **govulncheck** (1.8.0): `Your code is affected by 0 vulnerabilities.` It names one module-level advisory the code doesn't call: GO-2026-5024, `golang.org/x/sys` v0.36.0, `NewNTUnicodeString` in `x/sys/windows`, Windows only, fixed in v0.44.0 (below).

Every Go test run went through a `systemd-run --user --scope` fence (MemoryMax 6G, TasksMax 400), one at a time, with `pgrep` before and after; no test process was left.

## Findings

### Fixed (with a regression test)

1. **Start anyway was refused from the terminal UI.** A stray `a` in the run view (no hold was on) logged `▲ the run refused a command: limit takes text start`. That is serve's wire check, not the engine's answer: the run view reaches the run through `igris serve`, `actLimitStart` sends `engine.Command{Kind: CmdLimitStart}` with no text, and the client's `requestOf` copied that empty text into the `limit` request, which serve refuses unless its text is `start`. So during a real usage hold or outside working hours, **Start anyway** from the TUI (the key and the button) never reached the engine. The web UI (`ui.go`) and the extension send `start` and were unaffected. Since 1.8 (V18-03).

   **Fixed** in `internal/serve/client.go`: `requestOf` gives a `limit` request its one text, `start`. Test: `TestClientSendsStartAnyway` (`internal/serve/client_test.go`), which failed before (`the run got no command`). Live after the rebuild (the quiet run, no hold): `▲ ignored start anyway: nothing holds new starts: no usage limit, and within working hours`, the engine's own answer, so the command now crosses the socket. CHANGELOG and the release notes have it under Fixed.

### Needs a decision

None.

### Observations (no change)

- **A plan question's tail is two lines.** While P-01 presented its plan, its card's tail was `⎿ /plan to preview` and a row of `▔`, whatever the room: Claude Code's plan dialog draws two `─` rules, and `backend.InputBoxStart` takes the first of them for the input box (the heuristic its comment describes). The card, its banner and Answer… were right; only the live tail was short. Not new in 1.12.
- **The old cookie stays in the browser after pairing.** igris no longer reads `igris_token`, so it neither uses nor clears it; it is harmless and goes away with the browser's cookies.
- **`M` acts on the selected card until TASKS has the focus.** From the bar, `M` asked about P-01 (the selected card); Tab goes bar → log → TASKS, where the arrows pick a row and `M` acts on it. As designed (§15.3: the selected task is the current one unless you picked another).
- **At 50×20 with a full card TASKS scrolls.** The card keeps its need and the log its three lines, so five cells in two columns got two of their three rows. As §15.2 says (the card first, TASKS at least one row).
- **The header shows `slice 1/4` with a bar** at every width where the bar fits; the count form (`1 of 4 done`) comes only once the bar drops, as §15.1 says.
- **govulncheck's module advisory** (GO-2026-5024, `golang.org/x/sys` < v0.44.0, Windows only, not called). Raising `golang.org/x/sys` is a dependency change, outside this gate.
- Answering folder trust recorded the scratch worktrees in Claude Code's own config, and the sessions left transcripts under `~/.claude/projects/` and P-01's plan under `~/.claude/plans/`, as at earlier gates.

**Cleanup.** The trim run completed and the quiet run was stopped (`Done – stopped`); the private tmux server was killed, which ended Q-01's open session; the `systemd-run` scope was inactive; the browser's cookies were cleared; the scratch tree (projects, `GOBIN`, runtime, state and tmux directories) was removed, and the Playwright MCP's console log, written into the repository's `.playwright-mcp/`, was deleted. No igris, Claude Code or test process of the gate was left, and nothing listened on 18112. The owner's own runs (`igris arise B8` and `igris arise V112`, this run, each with its serve) were not touched.

## Owner checks left

1. **The Trim layout on the MacBook's own terminal.** 90×28 is a guess at it (V112-04); a look at a real run there, ideally a parallel phase with one task needing you, confirms the guess.
2. **A phone that kept only the old cookie.** If one exists, opening its saved page should say Not paired; `igris serve token` pairs it again.
