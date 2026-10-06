# README demo

`demo.tape` records the GIF at the top of the README: igris runs phase `D1` of the synthetic plan here (`tasks.md`, two trivial tasks, one on sonnet and one on opus) in herdr, with real Claude Code sessions.

## Prerequisites

- [VHS](https://github.com/charmbracelet/vhs) with its `ttyd` and `ffmpeg` dependencies
- herdr with `herdr integration install claude` done, and the default prefix `ctrl+b` (the tape uses `ctrl+b 1` to switch back to igris's tab)
- `claude` logged in with your subscription, `ANTHROPIC_API_KEY` **not** set

## Record

```sh
make demo
```

This runs `make install` first, so the igris on your `PATH` is this build: the herdr panes and Claude's `igris done` find igris through your shell's own `PATH`. Then it runs the tape. It works from a normal terminal or a herdr pane: `make demo` clears the `HERDR_*` variables so the recorded herdr starts fresh instead of refusing to nest. The tape:

1. runs `setup.sh`, which creates a new scratch project, `$TMPDIR/igris-demo.XXXXXX` (git repo, plan, `igris.toml`, `igris init`). The folder is new every time, so Claude Code always asks to trust it, and the tape answers that prompt on camera;
2. opens it in a separate herdr session, `igris-demo`, so your everyday session is untouched. It removes a leftover `igris-demo` session first, and stops and deletes the session at the end;
3. runs `igris arise D1 --mode auto` (Claude Code's auto mode approves safe commands, so nothing waits on you; no skip-permissions) and waits on igris's own log lines (`NEEDS YOU`, `D1-01 done`, `phase D1 complete`, …), cutting the stretches where a session is just working.

Make sure `igris version` in a fresh shell shows this build. Two real sessions run, so a recording takes a few minutes and uses a little of your Claude usage.

## Review

Before committing `igris-demo.gif`, check that it shows the TUI, both sessions with their ranks, and the phase completing, and that it is small (aim for under 3 MB; `gifsicle -O3 --lossy=80` helps). To tune the pacing, change the `Sleep` lines in `demo.tape`; a `Wait` that times out means a session got stuck, so watch the `igris-demo` herdr session (`herdr --session igris-demo`) to see why.
