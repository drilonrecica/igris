# Control from your phone

Since 1.6, `igris serve` can listen on HTTP, so a browser on your phone can follow a run and answer its questions, and a notification can carry buttons that answer for you. It is off until you give it an address. There is no relay and no igris cloud: the phone talks to your machine directly, over Tailscale or an ssh tunnel you set up.

Anyone who holds the pairing token controls the run, short of skip-permissions mode, which can never be set remotely. Read [what it can do](#what-it-can-and-cant-do) and [keep it private](#keep-it-private) before you turn it on.

## Turn it on

Put an address in `igris.toml`:

```toml
[serve]
http = "100.101.102.103:7777"   # this machine's Tailscale address
```

or pass it for one run: `igris serve --http 100.101.102.103:7777`. A run started with `igris arise` runs in a background `igris serve` and listens on `[serve] http` too.

igris binds only addresses it can call private: loopback (`127.0.0.1`, `::1`) and Tailscale's ranges (`100.64.0.0/10`, `fd7a:115c:a1e0::/48`). Any other address, `0.0.0.0` included, is refused unless you pass `--listen-public` and set `tls_cert` and `tls_key` under `[serve]`; igris then serves TLS from those files and warns at start. Use an IP address, not a host name.

## Reach it from your phone

### Tailscale

Install [Tailscale](https://tailscale.com) on the machine that runs igris and on the phone, sign in to the same tailnet, and set `http` to the machine's Tailscale address (`tailscale ip -4`). Traffic is WireGuard-encrypted end to end, and nothing is exposed to the internet.

### ssh -L

Without Tailscale, keep the listener on loopback and tunnel to it:

```toml
[serve]
http = "127.0.0.1:7777"
```

```sh
ssh -L 7777:127.0.0.1:7777 you@your-machine      # on the laptop or phone that has the browser
```

Then open `http://127.0.0.1:7777/` on the device that ran `ssh -L`. This is the way to use it from a laptop; phone ssh apps with port forwarding work the same.

## Pair a device

In the project, with a run going (or before it):

```sh
igris serve token
```

It prints a pairing URL and, under it, a QR code. Scan the code with the phone, or open the URL in its browser. Opening it stores the token in an `HttpOnly`, `SameSite=Strict` cookie (`Secure` over TLS) and redirects to the same address without the token, so it doesn't stay in the address bar or the history. If the listener's address is `0.0.0.0` or has no port the phone can use, the URL is printed without a QR code.

The token is a file, `.igris/serve-token` (0600), one per project. `igris serve token` prints the same URL again; `igris serve token --rotate` makes a new token and unpairs every device at once, a live run included. Pair again afterwards. Scripts can send the token as `Authorization: Bearer <token>` instead of a cookie.

## What it can and can't do

The page at `/` is the narrow run view's actions, sized for a phone:

- the run's phase, progress, mode and pause, the phases and their tasks, and a card for every active task with its state, model, how long it has run, its question and the session's last lines;
- answer a question: the commit question, a plan for approval (approve, or reject with feedback), a session's skip or reset request, a lost session;
- pause or resume, stop, retry (fresh or continuing), mark a task done with a note, skip with a reason;
- choose the mode for the next sessions or for one task.

Stop, skip and done ask a second time before they are sent. The page follows the run live and says `reconnecting…` when the connection drops; with JavaScript off it shows the run as of loading and its buttons still work.

It cannot start a run, open a session's pane or open side sessions: those stay in the terminal. And **skip-permissions mode (`yolo`, `plan:yolo`) is never offered and always refused**, whatever is sent: it needs the typed phrase at a terminal, as everywhere ([Modes](modes.md)).

## Answer from ntfy

With the listener on, set `actions = true` under `[notify.ntfy]`:

```toml
[notify.ntfy]
topic = "my-long-random-topic"
actions = true
```

A question's ntfy message then carries buttons: `Commit` · `Don't commit` on the commit question, `Skip` · `Keep` on a session's skip request, `Retry` · `Stop` on a lost session or a failed `before_task` hook, and `Approve` · `Open` on a plan (rejecting needs feedback, so `Open` takes you to the web UI).

Each button's link carries its own token. It answers that one question, once, within 10 minutes; after the question is answered anywhere (another button, the web UI, an attached terminal) or the run ends, a button only says `already answered` or `expired`. It is never your pairing token, so a forwarded notification can't do more than that. The link is the listener's address, so the phone must reach it as above; a listener on `0.0.0.0` gets no buttons, and neither does a quiet-hours digest or a grouped message for several tasks.

Discord, Slack, Gotify, the webhook and the toast can't take replies and are unchanged.

## Keep it private

- Treat the pairing URL and QR code like a password. Anyone who saw them, or who holds the paired phone, can drive the run until you `--rotate`.
- Prefer Tailscale or `ssh -L`. `--listen-public` puts a port on the internet that one token guards; use it only with a certificate you trust.
- Browsers send a cookie to every port of the same host, so another web service on the listener's address (a dev server your project starts) can see it. A loopback address or a bearer client avoids that.
- `100.64.0.0/10` is the whole carrier-grade NAT range; a machine whose ISP gives it an address there binds as if it were Tailscale's.
- Failed logins are rate-limited per address, changes made with the cookie need a CSRF token and a same-origin request, and responses carry a strict content security policy.

The [threat model](https://github.com/drilonrecica/igris/blob/master/docs/threat-model.md) lists every one of these, with the accepted risks.

## What is promised

The HTTP mapping of protocol 1 (`/pair`, `/api/v1/events`, `/api/v1/command`, `/api/v1/action/…`, the cookie, bearer auth, `X-Igris-CSRF`, `.igris/serve-token`) and the `[serve]` and `[notify.ntfy] actions` keys are frozen for 1.x. The web UI's pages, paths and script are not; use the API for anything you build ([compatibility](../../docs/compatibility.md)).
