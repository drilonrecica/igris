# Notifications

igris tells you when it needs you (a question, a plan to approve, a stalled session, a task past its Timeout), when a phase is done or stuck, and when something fails. Add `task_done` to a channel's `events` to hear about every finished agent task too.

## Channels

- the herdr toast, or a tmux status-line message (always on, inside herdr or tmux)
- [ntfy](https://ntfy.sh): push to your phone
- a Discord webhook
- a Slack incoming webhook
- [Gotify](https://gotify.net): push from your own server
- a generic webhook: a signed JSON POST to any URL, for your own automation

```toml
[notify.ntfy]
topic = "my-igris-topic"          # leave empty to turn ntfy off
token = "env:NTFY_TOKEN"          # optional

[notify.discord]
webhook_url = "env:IGRIS_DISCORD_WEBHOOK"   # https only, as Discord gives it
events = ["needs_input", "phase_done"]

[notify.slack]
webhook_url = "env:IGRIS_SLACK_WEBHOOK"     # https only

[notify.gotify]
server = "https://gotify.example.org"
token = "env:GOTIFY_TOKEN"        # an application token; server and token go together

[notify.webhook]
url = "env:IGRIS_WEBHOOK_URL"     # http or https
secret = "env:IGRIS_WEBHOOK_SECRET"   # optional: signs the body
```

The default events are `needs_input`, `session_lost`, `task_overdue`, `rank_mismatch` (since 1.1: a session runs on a model its rank doesn't resolve to), `phase_done`, `phase_stuck`, `run_error` and `verify_failed_limit`. Every key is in the [configuration reference](../../docs/config.md).

Check your setup with `igris notify test`: it sends a sample of each event to every configured channel (the toast too, when run inside herdr or tmux) and prints `ok` or the reason for each failure. `--event needs_input` sends just one.

> On a public ntfy server without a token (such as ntfy.sh), the topic is the password: anyone who knows it can read your notifications and send fake ones. Pick a long random topic, or set a `token`.

## What a message holds, and what is kept secret

- A message has the project, phase, task ID and title and the event; never file contents, diffs or command output.
- Secrets (the ntfy and Gotify tokens, the Discord, Slack and webhook URLs, the webhook secret) can be `env:VAR_NAME` references, are never logged, and are scrubbed from error messages. `igris.toml` is then safe to commit.
- Verify commands, task hooks and git never get the environment variables your notification secrets come from.
- Each channel has a 10 s timeout and one retry. A channel that fails is shown as a warning and never stops the run. igris doesn't follow redirects: a `3xx` is a failure, so set the final URL.
- Plain `http://` works for ntfy, Gotify and the webhook (a server on your LAN), with a warning from `igris check` where a token or the payload would go unencrypted.
- ntfy sends urgent events (`needs_input`, `session_lost`, `task_overdue`, `rank_mismatch`) at high priority. Slack messages have `&`, `<` and `>` escaped and Discord messages have `[` and `]` escaped, so a task title can't ping `@channel` or make a link.

## Templates, quiet hours and digests

```toml
[notify]
quiet = "22:00-07:00"             # local time, may cross midnight; "" (the default) is off
break_through = ["needs_input", "session_lost", "task_overdue"]   # the default: never held
task_done_digest = 5              # 0 (default): one task_done message per task; N: one per N tasks; "phase": one per phase

[notify.ntfy]
topic = "my-igris-topic"
template = "{{.At.Format \"15:04\"}} {{.TaskID}} {{.What}}"
```

- **Templates.** Every channel but the toast takes a `template` (Go `text/template`) that replaces the message text. The variables are `.Event`, `.Project`, `.Phase`, `.TaskID`, `.Title`, `.What`, `.RunID` and `.At` (local time), nothing else; `if`/`else` and `with` work, loops and template calls don't. A template is tried on a sample message when `igris.toml` loads, so a typo is a config error that names the channel, not a silent failure at night.
- **Quiet hours.** During the window, messages to ntfy, Discord, Slack, Gotify and the webhook are held, except the `break_through` events (`[]` holds everything); the toast is never held. When the window ends, each channel gets one digest: `quiet hours 22:00–07:00: N held`, then a line per held message.
- **Digests of finished tasks.** With `task_done_digest`, a channel's `task_done` messages are collected and sent as one, such as `3 tasks done: M1-01, M1-02, M1-03`, when N are collected, when the phase ends and when the run stops.

## The generic webhook

The webhook POSTs JSON with every key present (`""` when unknown): `{"v":1,"event","project","phase","task","title","what","run","at","urgent","text"}`, where `run` is the run ID, `at` an RFC 3339 UTC time and `text` the message as other channels show it. The event is also in the `X-Igris-Event` header.

With a `secret`, `X-Igris-Signature: sha256=<hex>` is the HMAC-SHA256 of the raw body with the secret. Check it before trusting the payload. A valid signature can still be an old payload sent again by anyone who saw it (a plain `http://` hop, a proxy's or your own logs): treat `run` + `at` (or the whole body) as a dedupe key, and reject an `at` older than a few minutes. The payload, both headers and the signature are part of the [compatibility promise](../../docs/compatibility.md); read it ignoring fields you don't know.
