# v1.11 gate

The v1.11 gate (V111-G) checks the hub and the cookie end to end, in a browser driven by Playwright. It covers:

- two projects served with their listeners on one address, both paired in one browser, both staying paired (two cookies);
- `igris hub --all` on loopback, paired once through the URL it prints; both runs on `/`, and a question answered in each through the one sheet across projects;
- a third serve started and seen joining an open page; a run that ended and its outcome; a run stopped from the hub;
- the wide layout at 1440×900: two panes, switching runs, answering in the side panel;
- ntfy's Open linking into the hub, against a local ntfy stand-in;
- `make fmt lint test` and the race detector.

It ran unattended on Linux (Fedora 44) on 2026-11-11, 02:55–03:30, from the V111-G task session. The build was this tree (`master` at `d094b6e`, then with the fixes below), built with `-X main.version=1.11.0` into a scratch `GOBIN` first in `PATH`. Every project was a synthetic scratch git repo under a `mktemp -d` directory with `verify = "true"` and `commit = "ask"`; every `[models]` key mapped to sonnet. Mode was `auto` throughout, never `yolo` or `plan:yolo`. Most questions came from user tasks (Owner `user`), which wait for the owner with no session; one sonnet task (B-01, writes `lemon.txt` and runs `igris done`) gave a real commit question.

**Where it ran.** A private tmux server, `tmux -L gate -f /dev/null`, started from `env -i` inside a `systemd-run --user --scope` (MemoryMax 6G, TasksMax 400), with only `HOME`, `USER`, `LANG`, `TERM`, `COLORTERM`, `SHELL=/bin/bash`, the gate's `PATH`, and these set to directories of the scratch tree: `XDG_RUNTIME_DIR` (so the run registry and `hub.json` held the gate's runs only, and `hub --all` could not see the owner's), `XDG_STATE_HOME` (the hub's token) and `TMUX_TMPDIR`. No `HERDR_*`, `TMUX` from outside or `CLAUDE*` variable reached it. The first window said `igris is /tmp/igris-gate-v111.…/bin/igris` and `igris 1.11.0`. B-01's session asked Claude Code's folder-trust question once (Down, Enter in its pane). The browser was headless Chromium from `playwright-core` 1.64.1 with one persistent profile in the scratch tree, so every step was the same browser: the iPhone 13 profile (390×844, touch) for the phone flow, a 1440×900 viewport for the wide layout. The ntfy stand-in was a small Python server on `127.0.0.1:47830` that logged each POST's path, title, priority, `Actions` header and body; each project's `[notify.ntfy]` had `server = "http://127.0.0.1:47830"` and `actions = true`.

**Versions:** Claude Code 2.1.322, tmux 3.8, Go 1.27.3, Node 24.21.0, playwright-core 1.64.1.

**Projects:** alpha (A-01, A-02 user tasks; listener `127.0.0.1:47811`), beta (B-01 sonnet, B-02 user; `:47812`), gamma (G-01, G-02 user; `:47813`), delta (D-01 user; `:47814`, for the Stop findings). The hub listened on `127.0.0.1:47820`, later `:47821`.

## Two projects on one address (alpha, beta)

| Step | Result |
|---|---|
| alpha unpaired | `401` · `Not paired · Run igris serve token on the machine and open its URL on this device.` |
| alpha paired | `/pair?token=…` → `200` alpha's page; one cookie `igris_token_333b12195f4f` (host `127.0.0.1`, path `/`, HttpOnly, SameSite Lax) |
| beta paired | `200` beta's page; two cookies, `igris_token_333b12195f4f` and `igris_token_21c8fc8a2b76`; the hashes are the first 12 hex characters of the registry files' (`333b12195f4fed7d.json`, `21c8fc8a2b76eebc.json`) |
| both stay paired | alpha, beta, alpha again: `200` each, each its own page |

Later, with the hub paired too, the browser had three cookies (the two and `igris_hub`) and both projects' pages still answered `200`.

## The hub on loopback

`igris hub --all --http 127.0.0.1:47820` printed `the hub's token is new (…/state/igris/hub-token): every device pairs once`, `hub on http://127.0.0.1:47820/ for paired devices; Ctrl-C ends the hub, never a run`, the pairing URL with its warning, the QR code (19 lines), then `02:57:45 run joined: beta` and `run joined: alpha`. The token file was 0600 in a 0700 directory; `hub.json` read `{"v":1,"url":"http://127.0.0.1:47820/","pid":…}`, 0600, next to `runs/`.

| Step (phone) | Result |
|---|---|
| unpaired | `401` · `Not paired · Run igris hub token on the machine and open its URL on this device.` |
| paired through the printed URL | `200` `/`, cookie `igris_hub` |
| `/` | `Runs · 2 live runs · 2 need you`; beta (`! Needs you · 1 question`), then alpha (`! Needs you · 1 task needs you`), each with its phase bar and `phase B · Fruit · 0 of 2 done · 1m27s`; the sheet `beta · B-01 · commit? · 1 of 2 ›` |
| beta's commit question, from the sheet | **Commit** → `Sent.`; beta's serve `✓ B-01 done`, `c7c2b26 B-01: Lemon` on `master`; the sheet then led with `beta · B-02 · your turn · 1 of 2 ›` |
| alpha's turn, from the sheet | `1 of 2 ›` stepped to `alpha · A-01 · your turn · 2 of 2 ›`; **Mark done** opened the note field, the next tap asked `Mark A-01 done? It's your decision, so igris doesn't verify it.`, **Mark A-01 done** → `Sent.`; alpha's serve `✓ A-01 done · watered by the gate` |

## A third serve joining; a run ending (phone)

With `/` open in one tab, gamma's serve started in a gate window: the hub logged `02:59:53 run joined: gamma`, and the open page showed gamma's row 2.1 s later without a reload. In another tab, alpha's hub page (from ntfy's Open, below) marked A-02 done: within 0.5 s the open `/` showed alpha as `Done · ✓ completed · A-01: done · A-02: done · 2 of 2 done`, below the live runs; the hub logged `run ended: alpha · completed`. After a reload `/` listed beta and gamma only, and `/p/alpha-333b12/` answered `410` · `Run ended · alpha ended: ✓ completed. Its pages closed with it. · Back to the runs`.

gamma ended from the terminal (`igris done G-02`): `run ended: gamma · completed`, its row `Done · ✓ completed · 2 of 2 done`. beta was stopped from its hub page: **Stop** asked `Stop igris? The session stays open; igris arise picks the run up again. · Stop igris · Cancel`; after **Stop igris** beta's serve said `run stopped · stopped`, the hub `run ended: beta · stopped`, `/` showed beta as `Done · – stopped` until the reload, then `no live run · No live run · A run started with igris arise in a project this hub follows joins here by itself.` Every serve's registry entry was gone once it ended.

## The wide layout at 1440×900 (beta and gamma live)

| Step | Result |
|---|---|
| `/` | the run list a 320 px column at the left, the selected run's page 656 px beside it, the questions in a 416 px side panel at the right; Plan · Pause after this task · Stop in the page's header bar; no horizontal scroll |
| the default run | beta, the first run that needs you (`aria-current` on its row) |
| switching runs | a click on gamma swapped the pane in the same document (a marker set on `window` survived) and the URL became `/?run=gamma-8b1e1e`; the panel stayed |
| keys | `k` on gamma moved the focus to beta, Enter opened it (`/?run=beta-21c8fc`), `j` back to gamma; the focus a 3 px solid outline; `?` showed `Keys · j k · next run, previous run · Enter · open the run · ? · these keys · Esc · close them · Close` |
| answering in the side panel | with gamma open the panel showed beta's B-02 first; **answer in the sheet** on gamma's card brought `gamma G-01 your turn 2 of 2 ›` into the panel; Mark done, a note, **Mark G-01 done** → `Sent.`, gamma's serve `✓ G-01 done · sorted in the side panel`, the pane moved on to G-02 |

## ntfy's Open (the stand-in)

| Message | Open |
|---|---|
| alpha A-01 your turn (02:56:33, before the hub) | `http://127.0.0.1:47811/?task=A-01` |
| beta B-01 commit question (02:56:59, before the hub) | `http://127.0.0.1:47812/?task=B-01`, with Commit and Leave uncommitted on `http://127.0.0.1:47812/api/v1/action/…` |
| beta B-02 your turn (02:58:11, the hub running) | `http://127.0.0.1:47820/p/beta-21c8fc/?task=B-02` |
| alpha A-02 your turn (02:59:17, the hub running) | `http://127.0.0.1:47820/p/alpha-333b12/?task=A-02` |

All at priority `high`. Opened on the phone, the A-02 link showed alpha's run page through the hub with `A-02 · YOUR TURN · Your turn: A-02 · Feed the cat` open in the sheet and Plan · Pause after this task · Stop below it.

## The hub's end

Ctrl-C with delta live: `the hub ended; the runs go on`, `hub.json` removed, delta still `! Needs you · D-01 · your turn` in `igris status`. The hub started again (the rebuilt one) took delta back (`run joined: delta`), and the browser paired before the restart was still paired.

## The suite

- **Lint and tests:** `make fmt lint test` gave `0 issues.`, every Go package ok (36) and the extension's 178 unit tests passing, with the fixes. Its first run with the fixes failed lint on the gate's own `TestMain` change (gosec G703 on removing a directory named by the environment); it now removes only the directory it made, and the run is green.
- **Race:** `go test -race -count=1 ./...` passed in every package with the fixes. Its first run failed once in `internal/serve`, `TestTailOfATask: a tail is already being read`, which is finding 4.

Every Go test run went through a `systemd-run --user --scope` fence (MemoryMax 6G, TasksMax 400), one at a time, with `pgrep` before and after; no igris test process was left. Fuzzing, govulncheck and `make test-vscode` were not part of this gate's row and were not run (the extension is unchanged in 1.11).

## Findings

### Fixed (each with a regression test)

1. **The page that confirmed Stop through the hub said Stopping for good.** On beta's hub page, after **Stop igris**, the page kept `Stopping · the sessions stay open` while every other page, the hub's and serve's own, said `Done · – stopped`. The reply to the stop sets Stopping; through the hub it comes back after `run_stopped` (the hub relays the command over the run's socket), so it overwrote the end that `ended()` had drawn. Reproduced on delta with three tabs (the page that confirmed, another hub page, serve's own page): only the first was wrong.

   **Fixed** in `internal/serve/ui/app.js`: `stopped()` does nothing once the run is over. Test: `TestUIStopReplyAfterTheEndKeepsDone` (`internal/serve/ui_test.go`, the script read as the other script tests do), which failed before. Live (delta again, rebuilt hub): all three tabs `Done · – stopped`. New in 1.11, so no CHANGELOG entry.

2. **`hub.json` stayed behind when the hub's output was piped.** `igris hub … | grep …` and Ctrl-C: the hub ended, `hub.json` stayed (its pid gone, so readers ignore it, but SPEC §13 says it is removed). The file was removed by a deferred call after the hub's last line, `the hub ended; the runs go on`; with stdout a pipe whose reader ended with the same Ctrl-C (`igris hub … | tee hub.log`), that write raises SIGPIPE and ends the process first.

   **Fixed** in `cmd/igris/hub.go`: the hub removes `hub.json` as soon as it starts ending, before anything more is printed (once; the deferred call stays for other ways out). Test: `TestHubRemovesHubFileBeforeItsLastLine`, which failed before. Live: the piped hub's `hub.json` was gone after Ctrl-C. New in 1.11, so no CHANGELOG entry.

3. **The `cmd/igris` tests wrote the developer's run registry and `hub.json`.** `TestMain` keeps the tests off the developer's home and runs, but nothing moved `XDG_RUNTIME_DIR`: `TestRunHub` wrote the real `$XDG_RUNTIME_DIR/igris/hub.json` while it ran, and a test that starts the in-process serve registered its run in the real `runs/`, where the developer's own `igris hub --all` would take it and a live serve would send ntfy's Open to the test's hub.

   **Fixed** in `cmd/igris/main_test.go`: the tests get their own runtime directory, which a helper process of the signal tests inherits and the parent removes. Test: `TestTestsKeepOutOfTheRuntimeDir`, which failed before. (The first version of the fix left a directory per helper process, twelve, which the gate removed; the version committed leaves none, checked after `make test` and the race run.)

4. **A tail asked right after another could be refused.** Serve queued a tail's reply before it marked the tail over, so a client that asked the next one as soon as the reply came could get `a tail is already being read`. The web UI asks its cards' tails one after another, and through the hub every page's tails of a run share the mirror's one connection; the card's lines then stay as they were until the next round. Since 1.4.

   **Fixed** in `internal/serve/server.go`: the tail is marked over before its reply is queued. Test: `TestTailsOneAfterAnother` (2000 tails in a row with `GOMAXPROCS` raised to 64 for the test), which failed in 2 of 3 runs before the fix (`tail 104`, `tail 1600`) and in 50 of 50 runs after it passed (`-count=5 -cpu 1,2,8,32,64`, with `TestTailOfATask`). The test is statistical: it can pass on the old code. The CHANGELOG and release notes have it.

### Needs a decision

1. **A run page shows the state before the last task when the run ends.** When the last task finishes, the events `task_done` and `run_stopped` come within the 300 ms the page waits before it refreshes; `run_stopped` ends the page without a fetch (SPEC §15.8: serve ends with the run, so there is nothing left to fetch), and the refresh that would have drawn the last task done never happens. Seen on alpha's and gamma's run pages, the hub's and serve's own alike (so since 1.6, not new with the hub): `Done · ✓ completed` over `A-02: needs you · 1 of 2 done` and A-02's card still saying `Your turn` (its buttons removed). The hub's `/` is right (`2 of 2 done`): the hub still draws ended rows. The options: keep it (the word and the line are right); have the hub keep an ended run's last snapshot so its run page can fetch once more at the end (the hub only); or have serve keep its listener up a moment after the end so the page can fetch its final state (serve and hub alike, a change to §15.8's "without a fetch").

### Observations (no change)

- **A user task's text keeps its markdown on the web.** The Your-turn card and sheet read `**Water the plant** — the owner waters it`: the plan cell as written, bold marks and all, on the hub and on serve's own page, and in serve's log line, so not new with the hub; the title next to it (`Your turn: A-01 · Water the plant`) is clean.
- **Mark done takes three taps on the web.** The first opens the note field, the second asks the confirmation, the third sends. As designed (`has-field`, §15.8); the gate's first script tapped once too few.
- **After an answer in the side panel, the panel goes back to the first question** of all runs (beta's), not the next of the run that is open.
- **The wide layout's header bar is the page's**, above the selected run, not on the side panel; it reads as one header across the page and the panel.
- **Gate-only pitfall:** the hub's first port, `127.0.0.1:47820`, is in Linux's ephemeral range; after a while another local program's outgoing connection held it as its source port (`TIME-WAIT … 127.0.0.1:47820 → 127.0.0.1:7878`), and a new hub there said `can't listen on 127.0.0.1:47820: bind: address already in use` while `ss -ltn` showed no listener. The hub's error is right; the re-check moved to `:47821`.
- The owner's `$XDG_RUNTIME_DIR/igris/runs/` exists and is empty; the gate didn't write it (its runtime directory was its own) and left it alone. Earlier test runs may have made it (finding 3).
- Answering folder trust recorded beta's scratch path in Claude Code's own config, and B-01's session left a transcript under `~/.claude/projects/`, as at earlier gates.

**Cleanup.** The gate's runs ended (alpha and gamma completed, beta and delta stopped), the hubs ended with Ctrl-C, the ntfy stand-in was stopped by its exact pid, the private tmux server was killed, the `systemd-run` scopes were inactive, and the scratch tree (projects, `GOBIN`, browser profile, tokens, runtime and state directories) was removed. No igris, Claude Code, browser or test process of the gate was left, and nothing listened on the gate's ports. The owner's own runs (`igris arise S4`, `igris arise`, and `igris arise V111`, this run, each with its serve) were not touched.

## Owner checks left

Only these need the owner before v1.11.0 ships (with V111-R):

1. **A real phone on the hub.** The gate drove Chromium at phone size. Start `igris hub --all --http <tailscale-ip>:7777` with two or three runs, scan the printed QR code with the phone, and check on the phone's own browser (Safari on iOS, which the gate didn't run): the list, the sheet across projects (`1 of N ›`), one answer, and that the phone's existing pairings with the projects' own pages still work.
2. **ntfy's Open on the phone.** With the hub running and the real ntfy app, tap **Open** on a Needs-you notification: it should open that task's question on the hub's page (the gate checked the link the stand-in received, not the app).
3. **The laptop.** From a MacBook over Tailscale or `ssh -L 7777:127.0.0.1:7777`, paired by copying the URL, check the wide layout in Safari or Chrome: two panes, switching runs, `j`/`k`, answering in the side panel.
4. **Decide or defer the item under Needs a decision.** It doesn't block the release.
