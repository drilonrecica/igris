# Detach and attach

Since 1.4 a run does not live in your terminal. `igris arise` starts the run in a background `igris serve` and attaches to it, so closing the terminal, losing the ssh connection or putting the laptop to sleep only detaches you. The run goes on, and any terminal on the same machine can open it again.

## How it works

- `igris arise M0` asks its start-up questions as before (the skip-permissions phrase included), starts `igris serve` in its own session, and shows the run view. Nothing about the view changes.
- `serve` runs the engine: it holds the lock, writes the plan's Status cells and `state.json`, and talks to Claude Code through herdr or tmux. The run view is a client of it over a unix socket, `.igris/igris.sock`, which only your user can open.
- Its output goes to `.igris/serve.log`, cut to its last 16 MiB each time a serve starts.
- `igris arise --no-tui` and `--dry-run` stay in one process with no socket, because scripts expect `--no-tui` to end with the run.

## Detach

Press `q` in the run view. It asks **Detach** (focused) · **Stop run** · **Cancel**:

- **Detach** closes the view and leaves the run going. From home, you land back on home.
- **Stop run** does what `x` does: the run stops after the current step and the sessions stay open.

Ctrl-C, a closed terminal or a dropped ssh connection detach without asking.

## Attach again

From any terminal in the project:

```sh
igris attach
```

Bare `igris` opens home, which shows the live run with **Attach** as its first action (`enter` or `t`). Over ssh from your phone it is the same: log in, `cd` to the project, `igris attach`.

- **Several terminals at once.** Each can do everything the run view can. A question is answered by the first terminal that answers it; the others see the answer arrive and the dialog closes.
- **A lost connection.** The header says `reconnecting…` and the view comes back with a fresh snapshot of the run, with the last lines of the feed.
- **After `igris update`.** A run keeps the igris that started it until it ends. A newer or older igris can still attach, and its header says `served by igris X.Y.Z`.
- **Skip-permissions mode.** Switching the run to it from an attached terminal asks for the typed phrase in that terminal, as everywhere.

## Stop from a script

```sh
igris stop
```

asks the live run to stop, as **Stop run** does, waits until it has, and exits 0. It exits 1 when no run is live in the project. Sessions stay open.

## `igris serve` by hand

`igris serve [PHASE]` takes the same run flags as `arise` and prints plain log lines to stdout. Use it on a machine you reach over ssh, or from a service, then attach when you want to look. Start-up questions are asked on stdin; a closed stdin declines them. `--mode yolo` asks for the typed phrase on a terminal and refuses without one.

Outside herdr and tmux, serve with the tmux backend runs the sessions in a detached tmux session named `igris-<project>`. `tmux attach -t igris-<project>` shows them. SIGHUP, SIGINT and SIGTERM stop the run, and a second one exits at once.

## If something is off

- **`no run is live in this project`.** No serve answers on the socket. `igris status` shows the last run and whether it can resume; `igris arise` resumes it.
- **A stale socket.** serve replaces a socket nobody listens on. A file or symlink in its place is refused with a message; remove it.
- **A very long project path.** The socket's path can be too long for the system. serve then puts it in `$XDG_RUNTIME_DIR/igris/` (or a 0700 directory of yours in the temp directory) and `.igris/igris.sock.path` names it. `attach` and `stop` follow that file.
- **The run view won't start.** Read `.igris/serve.log`.

The rules are in [SPEC §13 and §14](https://github.com/drilonrecica/igris/blob/master/SPEC.md); the threat model covers [the socket](https://github.com/drilonrecica/igris/blob/master/docs/threat-model.md).
