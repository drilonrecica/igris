# In a box

[Skip-permissions mode](modes.md#skip-permissions) lets a session do anything you can. A container is the place to run it: the session can reach only what you mount into the box.

The recipe is [`examples/container/`](https://github.com/drilonrecica/igris/tree/master/examples/container): a Debian image with igris, Claude Code, tmux and git, running as a user `igris`, not root. It sets no `ANTHROPIC_API_KEY`.

## Build and run

```sh
podman build -t igris-box examples/container     # or docker build
cd your-project
podman run -it --rm -v "$PWD":/work -v ~/.claude:/home/igris/.claude igris-box
```

The project is at `/work`, and `~/.claude` carries your Claude Code login. Inside the box, run `igris arise --mode yolo` and type `skip permissions`, as on every run.

## What it protects

The box protects the host, not the project. A session can still change or delete anything in `/work` and use the mounted login. Keep [worktree mode](modes.md#skip-permissions) on (`--worktree`): the run's changes stay on a branch of their own, which you review and merge outside the box.
