# In a box: igris in a container

A Debian image with igris, Claude Code, tmux and git, for runs in skip-permissions mode (`yolo`) where the session may do anything: it can only do it to what you mount into the box.

## Build

```sh
podman build -t igris-box examples/container     # or: docker build -t igris-box examples/container
```

The image installs the igris release the `Containerfile` pins (`IGRIS_VERSION`), after checking the tarball against that release's `checksums.txt`, and Claude Code with its own installer. It runs as the user `igris`, not root, and sets no `ANTHROPIC_API_KEY`.

## Run

From your project:

```sh
podman run -it --rm \
  -v "$PWD":/work \
  -v ~/.claude:/home/igris/.claude \
  igris-box
```

`/work` is your project; `~/.claude` carries your Claude Code login, so the sessions run on your subscription. Docker is the same command. With rootless podman on SELinux, add `:Z` to the project mount; if files in `/work` end up owned by the wrong user, add `--userns=keep-id` (podman) or `--user "$(id -u):$(id -g)"` (docker).

Inside, start the run as usual and confirm skip permissions by typing `skip permissions`, as on every run:

```sh
igris arise --mode yolo
```

## What the box protects

The box protects **your machine**, not your project. A session in skip-permissions mode can still rewrite or delete anything in `/work`, and use the login in `~/.claude`. Keep `--worktree` on: `igris arise --mode yolo --worktree` keeps the run's changes on a branch of their own, which you review and merge outside the box.
