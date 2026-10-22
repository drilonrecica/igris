# v1.6 gate

The v1.6 gate (V16-G) checks remote control end to end, in nested runs with real Claude Code sessions. A headless browser drives the web UI: it pairs from the URL, answers commit questions, rejects and then approves a plan, pauses, resumes and stops the run, and finds yolo neither offered nor accepted by the API. ntfy action URLs answer once, then are refused on replay and after expiry. A non-loopback bind is refused without `--listen-public`. Then the gate runs the full suite, race, fuzz and govulncheck.

It ran unattended on Linux (Fedora 44) on 2026-10-22, from the V16-G task session (owner's decision 2026-10-21). The build was this tree (`master` at `3a43fbf`, then with the fix below), built with `-X main.version=1.6.0` into a scratch `GOBIN` put first in `PATH`. Every project was a synthetic scratch git repo under a `mktemp -d` directory. Its tasks were on sonnet: each wrote a file, then ran `igris done`. The config had `commit = "ask"` and `verify = "true"`, so every task ended on a commit question. The nested runs used a private tmux server (`tmux -L igris-gate-v16`, `HERDR_*` unset, `SHELL=/bin/bash`, windows started as `bash --noprofile --norc`). Mode was `auto` throughout, never `yolo` or `plan:yolo`.

The browser was Playwright's Chromium at a 390×844 viewport (a phone's width). ntfy was a local capture server on `127.0.0.1` that logged each publish with its headers, so the buttons' URLs could be read from the `Actions` header. No external service and no account was used. The gate played the owner with `tmux send-keys`: Claude Code's folder-trust question, and one answer in a session's pane. Processes were addressed by exact PID.

Afterwards the gate removed:

- the tmux server and its socket file;
- `/tmp/tmux-1000`, which the gate's own first `tmux -L … ls` had created;
- the capture server, the browser and the scratch tree;
- the browser tool's `.playwright-mcp/` output in the repo.

No igris, Claude Code or test process of the gate was left behind. The owner's own two igris runs were not touched.

**Versions:** Claude Code 2.1.308, tmux 3.7c, Go 1.27.3, Chromium (Playwright).

## The web UI, driven by a browser

Project p1: phase `W1` with six tasks. `W1-02` is in Mode `plan:auto`. `igris.toml` has `[serve] http = "127.0.0.1:47717"` and `[notify.ntfy] actions = true`, with the capture server as `server`. The run was started with `igris arise W1 --mode auto` in the tmux pane, and serve, started by arise, listened on the address.

| Step | Result |
|---|---|
| serve starts | `serve.log`: `no device is paired yet; run igris serve token to pair one`, `ntfy's answer buttons go to http://127.0.0.1:47717`, `web UI on http://127.0.0.1:47717/ for paired devices`. `.igris/serve-token` is 0600 |
| Unpaired `GET /` and `GET /api/v1/events` | `401` with the full header set of §14: CSP, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, nosniff, COOP, CORP, `Cache-Control: no-store` |
| `igris serve token` | printed the pairing URL and a QR code. The token appeared in neither `serve.log`, the run log, `state.json` nor the ntfy messages |
| Browser opens the pairing URL | redirected to `/` (no token in the URL). Cookie `igris_token`: `HttpOnly`, `SameSite=Strict`, `Path=/`, not `Secure` on plain loopback, 400 days, invisible to `document.cookie`. The page showed the header, W1-01's card with its live tail, its actions, the run's mode, the phase and Recent |
| yolo offered? | no. Neither the run's mode selector nor any task's offers `yolo` or `plan:yolo` |
| yolo by the API, from the paired page (cookie + CSRF) | `mode yolo` with `yes`, `mode plan:yolo` with `yes`, `task_mode YOLO` with `yes`, and `mode " Plan:Yolo "`: each `403` with `skip-permissions mode is never set remotely; set it in the terminal (SPEC §7.3)` |
| yolo by a bearer client and by the no-JS form path | bearer `mode yolo`: `403`. Form `POST /act` `type=mode&text=yolo&yes=1`, and `task_mode … plan:yolo … confirmed=1`: `403`, "Not sent" with the same text. No `mode_changed` in the run log, and nothing about yolo in `serve.log` |
| Other refusals | wrong CSRF header: `403`. A form post with a foreign `Origin`: `403`. `hello` over HTTP: `400` (`there is no hello over HTTP…`) |
| W1-01 commit question | appeared on the page over the event stream, without a reload. Its ntfy message had `Commit` and `Don't commit` buttons. **Commit** in the browser: committed `W1-01: Write a.txt`. Both ntfy buttons then answered `410 already answered` (answered elsewhere) |
| W1-02 plan | the page showed the plan text with **Approve: the session switches to auto** and **Reject with feedback**. Reject with empty feedback: the form refused to send (`required`), and nothing reached the run. Reject with feedback ("also print the file with cat"): `plan_rejected`, and the session presented a revised plan that had the step. The first plan's ntfy **Approve** then answered `410 already answered`, so it can't approve the second plan. **Approve** in the browser: `plan_approved`. The second plan's ntfy Approve answered `410` too |
| W1-03 left uncommitted | **Leave uncommitted** in the browser. `c.txt` went into W1-04's commit, as for any uncommitted change |
| Pause | **Pause after this task** while W1-04 asked: `· paused`. Commit in the browser: `· paused (holding)`, no card, buttons `Resume` and `Stop`. **Resume**: W1-05 started |
| Stop | **Stop** showed its second confirmation ("Stop igris? The session stays open; igris arise picks the run up again."). **Cancel** sent nothing. Then **Stop** and **Stop igris**: `run stopped: stopped`, serve exited, and the session stayed. See the finding below for the page |
| Reconnect after a resume | the same cookie paired the page with the next serve (same token). The event stream had stayed up for about 14 minutes before the stop |
| Completion | after a resume, W1-05 and W1-06 were committed from the browser. The page showed `phase W1 · 6/6 done · mode auto · ended: completed` with no form left |

The result: commits `W1-01`, `W1-02`, `W1-04` (with `c.txt`), `W1-05`, `W1-06`, and every Status `done`.

## ntfy answer buttons

Read from the capture server's `Actions` header (`http` actions, `POST`, `clear: true`; for a plan, `Approve` plus `Open`, a `view` action on `http://127.0.0.1:47717/`). Each was answered with `curl`.

| Step | Result |
|---|---|
| W1-02 commit question: `GET` its Commit URL (a link preview) | `405 method not allowed`, nothing sent |
| `POST` Commit, with `Origin: https://ntfy.sh` | `200 Commit sent for W1-02`. `serve.log`: `ntfy answer: Commit for W1-02`, then `W1-02 committed` |
| `POST` the same URL again (replay) | `410 already answered` |
| `POST` its sibling `Don't commit` | `410 already answered` |
| W1-03 commit question, left 10 min 4 s, then `POST` both buttons | `410 expired` each; no commit, and the question stayed open |
| After the run | no action token appears in `.igris/` (serve keeps only hashes, in memory) |

## Bind rules

Project p2, run with `igris serve`, `[serve] http` first on loopback. Each refusal exited 1 before the run started (no run log was written):

| Command | Result |
|---|---|
| `--http 192.168.31.31:47718` (the LAN address) | `the HTTP listener can't start: 192.168.31.31:47718 is reachable beyond loopback and Tailscale; use a loopback address (with ssh -L) or this machine's Tailscale address, or pass --listen-public with [serve] tls_cert and tls_key`, exit 1 |
| `--http 0.0.0.0:47718`, `--http [::]:47718` | the same, exit 1 |
| `--http 100.64.0.1:47718` (Tailscale range) | passed the bind rule; `bind: cannot assign requested address` (this machine has no such address), exit 1 |
| `--http 192.168.31.31:47718 --listen-public`, no TLS | `--listen-public needs TLS: set [serve] tls_cert and tls_key in igris.toml`, exit 1 |
| `[serve] http` set to the LAN address, no flag | refused as with `--http`, exit 1 |
| the same through `igris arise B1` (which can't pass `--listen-public`) | `igris serve did not start the run: it ended before the run started: … reachable beyond loopback and Tailscale …`, exit 1 |
| LAN address with `--listen-public` and a self-signed `tls_cert`/`tls_key` | `warning: the HTTP listener on 192.168.31.31:47718 is reachable beyond loopback and Tailscale; only the token and TLS guard it`. It served: HSTS `max-age=31536000`, TLS 1.1 refused, plain HTTP on the port `400`, pairing cookie `Secure`, and the pairing URL `https://192.168.31.31:47718/pair?token=…`. `igris stop`: `the run stopped; its sessions stay open`, exit 0 |

## The suite

- `go test -race -count=1 ./...`: every package ok (before the fix). `go test -race ./internal/serve/` again after it: ok.
- `make fmt lint test` (after the fix): 0 issues, every package ok, including the compatibility suite.
- `make fuzz FUZZTIME=30s`: all eleven targets ran 30 s each with no failure (`FuzzParse` config and plan, `FuzzSplitRow`, `FuzzLoadIncludes`, `FuzzSplice`, `FuzzSpliceGenerated`, `FuzzExtend`, `FuzzParseSignal`, `FuzzParseAgentState`, `FuzzLoadRun`, `FuzzParseImport`).
- govulncheck: `Your code is affected by 0 vulnerabilities`. One module finding is not reachable: GO-2026-5024 in `golang.org/x/sys` (Windows only, not called), as at the V13–V15 gates.

Every test run went through a `systemd-run --user --scope` fence (MemoryMax 6G, TasksMax 400), one at a time, with `pgrep` before and after. No `.test` process was left.

## Findings

Fixed (with a regression test):

- **After a stop from the web UI (or any end of the run), the page kept showing a live run.** On `run_stopped` the script closed the event stream and fetched `/live` 300 ms later. But serve ends with the run (§14), so the fetch failed and the page said `Failed to fetch`. The header never said `ended`. The cards kept Mark done, Skip and Retry, and the header kept Pause and Stop, all of which now fail. Since the stream had been closed on purpose, `reconnecting…` never showed. The tail loop also went on posting to the closed port every 4 s, seen as 31 `ERR_CONNECTION_REFUSED` in the console. That goes against §15.8 ("shows the run's state … ended with its outcome"). **Fixed** in `internal/serve/ui/app.js`: on `run_stopped` the script applies the ended page itself, as the server renders it for an ended snapshot. The header gets `· ended: <outcome>`, and the forms, questions, action rows, task mode selectors and the run's mode go. The stream and the tail loop stop, and a `/live` reply still on its way is dropped. Test: `TestUIScriptEndsWithTheRun` (internal/serve). It checks that the script ends the page on `run_stopped` without fetching it. It also checks that the script removes every kind of element the server's ended page leaves out and a live page has, so the two stay in step. It failed before the fix. `make test` has no JavaScript runtime, so the behaviour was checked live on the rebuilt binary: Stop gave `phase W1 · 4/6 done · mode auto · ended: stopped`, no form, no flash error and no further request, and completion gave `ended: completed`. The web UI is new in 1.6.0, so there is no CHANGELOG entry.

Needs a decision: none.

Observations (no change):

- **`/favicon.ico` is a 404** on each page load (the browser asks for it; the UI has no icon), so the console shows one error. Harmless: it carries the cookie, so it isn't a failed authentication, and an unpaired browser sends no credential. A `<link rel="icon" href="data:,">` in the head (the CSP allows `data:` images) would silence it, or the igris icon could be served. That is a design choice for the not-frozen UI.
- **A lost connection while the run goes on** (`reconnecting…`) was not seen live: serve can't be cut off from the page without ending the run, which ends the page through `run_stopped`. The page did keep one event stream for about 14 minutes without a drop.
- **ntfy's `Retry`/`Stop` and `Skip`/`Keep` sets** were not produced live (no session was lost and no skip was requested). They are covered by `action_test.go`.
- **Claude Code refused the foreground `sleep 100`** in W1-05 and asked the owner what to do. igris reminded it once and showed it in the card's tail. The owner's answer in the pane let it finish, as at the V15 gate.
- **Gate-only pitfalls.** zsh doesn't split an unquoted variable, so `$T send-keys …` and a loop over `"--http ADDR"` strings went to tmux and igris as one word. The first gave `exit 127`, the second `exit 2` usage errors from igris, which were correct. Twice a call without the gate environment ran the owner's older igris (`unknown key(s) "serve"`, `unknown command "serve"`). A first copy of p1's plan into p2 carried W1-03 `in progress`. That serve resumed it, found no session (`session_lost`), opened nothing and stopped on the timeout's SIGTERM. The plan was then reset.
- Answering folder trust records the scratch paths in Claude Code's own config, and the sessions left transcripts under `~/.claude/projects/`, as at earlier gates. The gate didn't edit the owner's Claude configuration to remove them.

## Owner checks (2026-10-22, with the agent)

Done on the owner's OnePlus 13 (Android, Chrome 155) over Tailscale, against a scratch project with two sonnet tasks, `[serve] http` on the machine's Tailscale address and a throwaway ntfy topic with `actions = true`. Two findings, both outside what the headless gate could see:

- **Fedora's firewall dropped the port.** `tailscale0` sits in no firewalld zone, so the default zone applies and the phone got no answer at all. `sudo firewall-cmd --add-port=7777/tcp` fixed it. **Docs:** the Tailscale section of the phone page now says so.
- **The pairing cookie never came back: `SameSite=Strict` is withheld on a navigation another app started.** The camera's QR scan opens Chrome with the pairing URL; Chrome treats that navigation, its redirect to `/` and even a later reload of that tab as cross-site, and sends no Strict cookie, so the page said `not paired`. A diagnostic server on the same port that set a Strict, a Lax and an attribute-less cookie proved it: after the redirect the phone sent the Lax and the plain one and not the Strict one. ntfy's `Open` button, which also starts the browser from another app, would fail the same way. **Fixed:** the cookie is `SameSite=Lax` (code, `http_test.go`, SPEC §14, the decision V16-P1 amended, threat model, phone page, CHANGELOG). Lax still withholds the cookie on a cross-site `POST` and on subresource requests, and every change needs the CSRF token.

One more thing seen on the way: a session that igris had held its first prompt for died when a watcher of the agent's own pressed Enter at Claude Code's folder-trust dialog, whose default is **No, exit** in a folder with pre-approved permissions (which `igris init` writes). igris held the prompt as designed and raised session lost. Not a defect; noted because the gates answer that dialog by hand and a reader of their scripts should press Down first.

## Owner checks left

Only these need the owner before v1.6.0 ships (with V16-R):

1. **Pair the phone by QR over Tailscale.** In a scratch or real project set `[serve] http = "<this machine's Tailscale address>:7777"` (`tailscale ip -4`), start a run with `igris arise <phase>`, run `igris serve token` and scan the QR code with the phone's camera. Check that the phone opens the run (header, the active task's card with its tail, the phase list), that a question or Pause works from the phone, and that the page reads well at the phone's width in its light and dark themes. This gate used a 390-pixel browser on loopback, not a phone on the tailnet.
2. **Answer a real question from an ntfy button.** With the same listener, set `[notify.ntfy] actions = true` with your real topic and let a task reach its commit question (`commit = "ask"`). On the phone's notification, tap **Commit**. Check that the notification clears, the commit is made, and a second tap (or the other button, if the app still shows it) does nothing.
