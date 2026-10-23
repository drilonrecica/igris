# README demo and screenshots

Everything here is recorded with [VHS](https://github.com/charmbracelet/vhs) from synthetic scratch projects that `setup.sh` creates; nothing comes from a real project.

| File | Recorded by | What it shows |
|---|---|---|
| `igris-demo.gif` | `demo.tape` (`make demo`) | home, the start wizard, **Arise.**, the parallel phase `D1` of `tasks.md` with two tasks working at once (sonnet and opus, real Claude Code sessions), both commit questions answered, the end-of-run summary |
| `igris-home.png`, `igris-home-narrow.png` | `home.tape`, `home-narrow.tape` (`make demo-screens`) | home on igris's example plan with two tasks done, wide and at phone width; no session starts |
| `igris-run.png`, `igris-phone.png` | by hand (V17-10) | the run screen with a task that needs you, and the web UI on a phone |

## Prerequisites

- VHS with its `ttyd` and `ffmpeg` dependencies
- for `make demo`: herdr with the default prefix `ctrl+b` (the tape hides herdr's sidebar with `ctrl+b b` and leaves with `ctrl+b q`), and `claude` logged in with your subscription, `ANTHROPIC_API_KEY` **not** set
- for `make demo-screens`: tmux

## Record

```sh
make demo           # the GIF; two real sessions, a few minutes, a little of your Claude usage
make demo-screens   # the two home screenshots; no session
```

Both run `make install` first, so the igris on your `PATH` is this build: herdr panes and Claude's `igris done` find igris through your shell's own `PATH`. Make sure `igris version` in a fresh shell shows it. Both work from a normal terminal, a herdr pane or a Claude Code session: they drop the `HERDR_*` variables, so the recorded herdr starts fresh instead of refusing to nest, and the `CLAUDE*` ones, so the recorded sessions aren't child sessions of yours (those write no transcript, and the summary would have no tokens).

`demo.tape`:

1. runs `setup.sh`, which creates a new scratch project, `$TMPDIR/igris-demo.XXXXXX/demo` (git repo, plan, `igris.toml`, `igris init`);
2. starts `claude` there once, off camera, to answer Claude Code's folder trust question (nothing is sent); the tasks' worktrees lie inside the folder and are trusted with it;
3. opens the project in a separate herdr session, `igris-demo`, so your everyday session is untouched (a leftover `igris-demo` session is removed first, and the session is stopped and deleted at the end);
4. runs bare `igris`, and from home `a`, `enter` (phase D1), `3` (mode auto: Claude Code approves safe actions itself; no skip-permissions) and `enter` (Arise);
5. waits on what igris shows (`Arise.`, `Commit D1-01?`, `Copy report command`), cutting the stretches where the sessions are just working, and answers each commit question with `1`.

`home.tape` and `home-narrow.tape` run `setup.sh home` (igris's example plan, `P1-01` and `P1-04` done) and take a screenshot of bare `igris` in a private tmux server, so the header shows a reachable backend.

## Review

Before committing `igris-demo.gif`, watch it through: the wizard, both cards working at once with their ranks, the commit dialogs, a summary with tokens, and no warnings in the log. Keep it small (aim for under 3 MB). To tune the pacing, change the `Sleep` lines in `demo.tape`. A `Wait` that times out means a session got stuck: the scratch project's `.igris/serve.log` says why.
