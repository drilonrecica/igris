# Compatibility suite

What igris releases wrote, kept so every later igris is tested against it (SPEC §17, §19). `cmd/igris/compat_test.go` runs it as part of `go test`.

- `vX.Y.Z/` is captured from the release itself with `make compat TAG=vX.Y.Z` (`tools/compat.sh`), which builds the tag in a temporary worktree:
  - `init/`: the `igris.toml` `igris init` wrote, and the plan `init --example` wrote (the release's `examples/tasks.md` where `init` had no `--example`);
  - `examples/`, `advanced/`: the example configs and plans the release shipped;
  - `signals/`: the files `igris done P1-02` and `igris skip P1-03` wrote.
- `state/` holds `state.json` files and `runs/` run logs, shaped after the structs of the releases their names give. They need a live session to produce, so they are written by hand from each tag's `internal/state`.

Never edit a captured file to make a test pass: a failure here means a change broke something a release wrote.
