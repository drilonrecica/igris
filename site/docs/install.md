# Install

igris is one static binary. You need Claude Code and a place for sessions to live (herdr or tmux) before you run a phase.

## What you need

- **Claude Code**, logged in the way you normally use it (a Pro or Max subscription, say). igris starts ordinary interactive sessions, so it uses your normal login and limits.
- **[herdr](https://herdr.dev) or [tmux](https://github.com/tmux/tmux) 3.2 or later.** Each session runs in a herdr tab or a tmux window, so everything survives a disconnect and you can reconnect over SSH, even from a phone.
- **git**, recommended: igris can commit after each task and warns when the tree is dirty.
- **Linux or macOS** (amd64 or arm64). On Windows, use WSL2 ([below](#windows-wsl2)).

igris needs Claude Code 2.1.291 or later (tested with 2.1.309), herdr 0.9.1 and tmux 3.2 or later (tested with 3.7). `igris check` and `igris arise` warn when one of them is missing, older, or a newer major version; they never refuse to run because of it.

> If `ANTHROPIC_API_KEY` is set in your environment, Claude Code bills the API instead of your subscription. igris warns you about this at start and asks you to confirm.

## Install igris

**Homebrew** (Linux and macOS):

```sh
brew install drilonrecica/tap/igris
```

**Go** (the version pinned in `go.mod` or newer; an older Go fetches it):

```sh
go install github.com/drilonrecica/igris/cmd/igris@latest
```

**A prebuilt binary** from the [Releases](https://github.com/drilonrecica/igris/releases) page: `igris_<version>_<os>_<arch>.tar.gz` for linux and darwin on amd64 and arm64. Verify the download (next section), then put the binary on your `PATH`:

```sh
tar xzf igris_<version>_<os>_<arch>.tar.gz
install -m 755 igris ~/.local/bin/igris
igris version
```

## Verifying a download

Every release since 0.6.0 is signed with the igris release key, an SSH key. OpenSSH 8.1 or later verifies it, on every Linux and macOS, with nothing else to install. Download `checksums.txt` and `checksums.txt.sig` from the same release as your archive, fetch `allowed_signers` from the release's tag, check that it holds the key published in the [README](../../README.md#verifying-a-download), and verify:

```sh
curl -fsSLO https://raw.githubusercontent.com/drilonrecica/igris/v<version>/allowed_signers
ssh-keygen -Y verify -f allowed_signers -I igris -n file -s checksums.txt.sig < checksums.txt
sha256sum -c --ignore-missing checksums.txt    # macOS: shasum -a 256 -c --ignore-missing checksums.txt
```

The first command must print `Good "file" signature for igris`, and the second `OK` for your archive. The signature covers `checksums.txt`, which lists every archive and its SBOM. Homebrew checks each archive against the sha256 in its formula, and `go install` checks modules against the Go checksum database.

## Updating igris

From 1.2, `igris update` asks GitHub for the latest release and, when it is newer, updates igris the way it was installed, after asking:

```sh
igris update --check   # the current and latest versions and the method; runs nothing
igris update           # asks Update? [y/N]; --yes skips the question (needed without a terminal)
```

- **Homebrew:** it runs `brew upgrade igris`.
- **`go install`:** it runs `go install github.com/drilonrecica/igris/cmd/igris@vX.Y.Z` with `GOBIN` set to the directory of the igris you run. Within about 30 minutes of a release the Go checksum database may not know the tag yet (`unknown revision`); retry later, or with `GOPROXY=direct GONOSUMDB=github.com/drilonrecica/igris` as igris suggests.
- **A prebuilt binary:** nothing runs. igris prints the release link and the download, [verify](#verifying-a-download) and install steps for your platform.

On the home screen, `u` (**Updates**) does the same in a dialog, and offers no Update while a run holds the project. A development build isn't updated. When it is done, quit and start igris again; igris never replaces or restarts itself. Runs in other projects keep going and their sessions use the new igris for `igris done`.

igris contacts GitHub only then: never at start, on a timer or during a run. It reads only the release's tag from the answer, and leaves the new binary's integrity to Homebrew and Go.

## Shell completion

`igris completion bash|zsh|fish` prints a completion script for subcommands, flags, and phase and task IDs from your plan.

```sh
# bash: add to ~/.bashrc
source <(igris completion bash)

# zsh: add to ~/.zshrc, after compinit
source <(igris completion zsh)

# fish: install once
igris completion fish > ~/.config/fish/completions/igris.fish
```

## Where igris runs

`igris arise` runs inside **herdr** or **tmux**: sessions have to outlive igris and your connection. `igris init` writes `backend = "auto"`, which uses herdr inside a herdr pane and tmux inside tmux; set `backend = "herdr"` or `"tmux"` in `igris.toml` to pin one. Outside both, `igris arise` exits and says where to run it.

Only `arise` and `adapt` need a backend. `init`, `doctor`, `check`, `phases`, `status`, `history`, `report`, `done`, `skip`, `reset` and `notify test` run in any shell.

### herdr

igris starts every session in its own herdr tab, so run `igris arise` **inside a herdr pane** with the herdr server running:

1. Install [herdr](https://herdr.dev) and start it (`herdr`), ideally on the machine where your project lives. Reconnect over SSH whenever you like; everything is still running.
2. Optional: `herdr integration install claude`, so herdr itself also shows Claude's state. igris doesn't need it: it reads the state from Claude Code hooks.
3. In a herdr pane, `cd` to your project and run igris.

### tmux

Each session opens in its own tmux window, in the background, named `<task> · <rank>`:

1. Install tmux 3.2 or newer and start it (`tmux`, or `tmux new -s work`). Detach with `ctrl+b d`; reattach later, also over SSH, with `tmux attach`.
2. In a tmux window, `cd` to your project and run igris.
3. Press `o` in igris (or `ctrl+b` and the window number) to switch to a session's window, and come back the same way.

### Windows (WSL2)

igris doesn't run natively on Windows. It runs inside [WSL2](https://learn.microsoft.com/windows/wsl/install), like herdr and Claude Code:

1. Install WSL2 with a Linux distribution (Ubuntu is fine) and open it in Windows Terminal.
2. **Inside WSL**, install herdr or tmux, Claude Code and igris (the `linux_amd64` binary, or `linux_arm64` on ARM). Don't mix sides: an igris in WSL can't drive a Claude Code or herdr installed on Windows.
3. Keep the project in the Linux filesystem (`~/code/…`), not under `/mnt/c`. Files on the Windows drive are much slower from WSL, and Windows tools touching the same checkout make for surprising diffs.

Plans and `igris.toml` saved by Windows editors work: CRLF line endings and a UTF-8 byte order mark are fine, and igris keeps both when it writes a Status cell.

## Next

[Quick start](quick-start.md) takes a project from nothing to a finished phase.
