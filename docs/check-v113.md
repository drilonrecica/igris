# v1.13 gate

The v1.13 gate (V113-G) checks a bare `igris hub` (V113-01, decision V113-P1) live. It covers:

- a bare `igris hub` picking this machine's Tailscale address and port 7780 and saying so;
- a second bare `igris hub` while the first runs (a hub already runs, exit 0);
- 7780 held by another listener (pass `--http`, exit 1);
- stdin piped: all runs, and a run started later joining;
- `igris hub token` printing the same URL, and pairing through it in a browser;
- `make fmt lint test`, the race detector and govulncheck.

It ran unattended on Linux (Fedora 44) on 2026-11-12, 17:39–17:50, from the V113-G task session. The build was this tree (`master` at `20e6298`), built with `-X main.version=1.13.0` into a scratch `GOBIN` first in `PATH`. Both projects were synthetic scratch git repos under one `mktemp -d` directory, each with one user task (Owner `user`, so no Claude session started), `verify = "true"` and `commit = "ask"`. Neither run used `yolo` or `plan:yolo`.

**Where it ran.** The owner's own hub (`igris hub --all --http 100.84.7.96:7780`, 1.12.1, pid 2695665) held this machine's Tailscale address and port 7780 all through the gate, and it wasn't stopped. So the hub was tested in a rootless network namespace of its own: `unshare --user --map-root-user --net`, a dummy interface named `tailscale0` with `100.100.1.1/32` (inside `100.64.0.0/10`) plus `lo`, then a second `unshare --user` back to uid 1000. The hub finds its address through `net.InterfaceAddrs`, so this takes the same path as the real interface. Inside the namespace ran a private tmux server, `tmux -L gate`, started from `env -i` inside a `systemd-run --user --scope` (MemoryMax 6G, TasksMax 400). It had only `HOME`, `USER`, `LANG`, `TERM`, `COLORTERM`, `SHELL=/bin/bash` and the gate's `PATH` (the scratch `GOBIN`, `~/.local/bin` for Node, then the system's, not `~/go/bin`). `XDG_RUNTIME_DIR` (the run registry and `hub.json`), `XDG_STATE_HOME` (the hub token) and `TMUX_TMPDIR` pointed into the scratch tree, so the gate never saw the owner's runs or hub. The real Tailscale address was probed once from outside the namespace, read-only (below). Every window said `igris is /tmp/igris-gate-v113.…/bin/igris` and `igris 1.13.0`. The browser was headless Chromium from `playwright-core` 1.64.1, run inside the namespace with one persistent profile and the iPhone-sized viewport (390×844, touch), so it opened the exact URL the hub printed.

**Versions:** tmux 3.8, Go 1.27.4, Node 24.21.0, playwright-core 1.64.1, govulncheck 1.8.0.

**Projects:** alpha (A1-01, a user task), started first; beta (B1-01, a user task), started while the piped hub ran.

## A bare `igris hub`

| Step | Result |
|---|---|
| `igris hub` on a terminal, alpha live | the checklist: `Put which runs in the hub? 1 run live:`, `[x] 0  All (and new runs)`, `[ ] 1  alpha …`; Enter kept All |
| … then | `listening on http://100.100.1.1:7780 (this machine's Tailscale address; --http picks another)`, `the hub's token is new (…/state/igris/hub-token): every device pairs once`, `hub on http://100.100.1.1:7780/ for paired devices; Ctrl-C ends the hub, never a run`, the pairing URL with its warning, the QR code, `run joined: alpha` |
| the listener and `hub.json` | `ss -ltn`: `100.100.1.1:7780` only; `hub.json` `{"v":1,"url":"http://100.100.1.1:7780/","pid":…}` |
| a second `igris hub`, the first running | `a hub already runs at http://100.100.1.1:7780; open it, or pair another device with igris hub token`, **exit 0**, and it bound nothing |
| Ctrl-C on the first | `the hub ended; the runs go on`, exit 0; `hub.json` removed, alpha still running |
| 7780 held by `python3 -m http.server --bind 100.100.1.1 7780`, then `igris hub` | `igris hub: port 7780 is in use: pass --http ADDR:PORT`, **exit 1** |
| `sleep 600 \| igris hub` (stdin a pipe, the listener stopped) | `not on a terminal, so no checklist: the hub takes every run, and the runs started later (--all)`, then the same `listening on http://100.100.1.1:7780 …` line, the same pairing URL (the token kept), `run joined: alpha` |
| beta started with `igris arise B1` | the hub printed `17:44:15 run joined: beta` within seconds of its start (alpha had joined at `17:44:07`) |

**On this machine's real address** (outside the namespace, the owner's environment, read-only): this tree's `igris hub </dev/null` printed `a hub already runs at http://100.84.7.96:7780; open it, or pair another device with igris hub token`, exit 0. It picked the real Tailscale address `100.84.7.96` and port 7780 and recognised the owner's 1.12.1 hub from its `hub.json`. That hub kept running. `igris hub token` there printed `http://100.84.7.96:7780/pair?token=…` (the token isn't recorded here).

## `igris hub token` and pairing

| Step | Result |
|---|---|
| `igris hub token`, the first hub running | `  http://100.100.1.1:7780/pair?token=G-J9…N9t0`, the same as the hub printed, character for character; the `--tls-cert` line; `next: igris hub`; exit 0 |
| `igris hub token --http 127.0.0.1:7781` | the same token at `http://127.0.0.1:7781/pair?token=…` |
| browser, `/` unpaired | `401` · `Not paired · Run igris hub token on the machine and open its URL on this device.` |
| browser, the printed URL | `200`, redirected to `/`: `igris · hub · Runs · 1 live run · 1 needs you · alpha ! Needs you · 1 task needs you · A1-01: needs you · phase A1 · Wait · 0 of 1 done`, the sheet `alpha · A1-01 · YOUR TURN`; one cookie `igris_hub` (host `100.100.1.1`, path `/`, HttpOnly, SameSite Lax) |
| reload | `200`, the same page |
| the same browser after the hub's restart (piped), beta joined | `/` `200` without pairing again: `Runs · 2 live runs · 2 need you`, alpha and beta, `1 of 2 ›` on the sheet |

Because the port is fixed and the token is kept, a device paired with one bare hub stays paired with the next one at the same address, which is why V113-P1 fixed the port.

## The suite

- **Lint and tests:** `make fmt lint test`: `0 issues.`, every Go package ok (36), the extension's 178 unit tests passing.
- **Race:** `go test -race -count=1 ./...`: every package ok (36), `cmd/igris` 13.7 s.
- **govulncheck** (1.8.0): `Your code is affected by 0 vulnerabilities.` The one module-level advisory is as at the v1.12 gate: GO-2026-5024, `golang.org/x/sys` v0.36.0, `x/sys/windows`, not called.

Each Go test run went through a `systemd-run --user --scope` fence (MemoryMax 6G, TasksMax 400), one at a time, with `pgrep` before and after. No test process was left.

## Findings

### Fixed

None.

### Needs a decision

None.

### Observations (no change)

- **A gate pitfall, not igris: tmux gives a new window the `PATH` of the client that asked for it.** Windows made by `tmux new-window` from the agent's own shell got that shell's `PATH` (`~/go/bin` and so the installed 1.12.1 first), even with `-e PATH=…`. An interactive bash's rc file did the same. The first two tries at alpha ran 1.12.1. They were stopped by their exact PIDs and their scratch state was reset before anything was recorded. After that the gate ran every tmux client with the gate's `PATH` and every window with `bash --noprofile --norc`, and each window's `type igris` and the serve's argv (`/tmp/igris-gate-v113.…/bin/igris serve -- A1`) confirmed the build. Later gates that make windows from outside the private server should do the same.
- **The hub has no `/api/v1/events` of its own.** A probe there got `404`: each run's stream is under `/p/<id>/api/v1/events`. As designed.
- **The QR code needs about 40 rows.** At 120×40 the end of the hub's output (the QR code, `run joined`) scrolled past the first screen until the window was made taller. As before 1.13.

**Cleanup.** The hub was ended with Ctrl-C (`hub.json` removed). Alpha's and beta's `arise` and `serve` were stopped by their exact PIDs. The private tmux server was killed, which ended the namespace and the `systemd-run` scope (inactive), and the scratch tree (projects, `GOBIN`, runtime, state, tmux and browser-profile directories) was removed. No gate process was left, and nothing listened on `100.100.1.1`. The owner's runs (`igris arise M10`, `igris arise V113` with their serves) and the owner's hub on `100.84.7.96:7780` weren't touched.

## Owner checks left

1. **A bare `igris hub` binding the real Tailscale address.** On this machine 7780 was held by the owner's `--http` hub the whole time, so the bind itself was tested on a namespace's address. Once the owner's `igris hub --all --http 100.84.7.96:7780` is ended, `igris hub` should print `listening on http://100.84.7.96:7780 (this machine's Tailscale address; …)`, and the phone, paired before with the old `--http` hub at the same address and token, should still be paired.
