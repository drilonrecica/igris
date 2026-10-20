# Upgrade to 1.0

This is for anyone coming from a 0.x release. 1.0 adds no features over the last 0.x; it decides what 1.x promises not to break, writes it down and tests it. See [compatibility](../../docs/compatibility.md).

## Steps

1. **Install the new igris** the way you installed the old one: `brew upgrade igris`, `go install github.com/drilonrecica/igris/cmd/igris@latest`, or a [signed binary](install.md#verifying-a-download). Check with `igris version`.
2. **In each project, run `igris config migrate` once.** It removes settings a release removed, shows the lines, asks first and keeps a backup in `.igris/config/`. Today that is one key, `claude.command`, which every `igris init` before 0.7 wrote. `--yes` skips the question.
3. **Run `igris doctor` and `igris check`.** Both should be clean.

That is all. Plans need nothing. A run interrupted under the old version resumes: its state is read and upgraded on the next save, and the config keeps its hash, so you don't see a "config changed" notice.

## What changed on the way to 1.0

These came in 0.7 and the 1.0 release candidates. They are the last breaking changes: from 1.0 on, 1.x only adds.

- **`claude.command` was removed** from `igris.toml`. Until you run `igris config migrate`, `check`, `arise` and `doctor` stop with `claude.command was removed in 0.7`.
- **`--json` output changed** (the last time). Every output starts with `"v": 1`; `doctor --json` is an object, `{"v": 1, "checks": [...]}`, instead of a bare array; `status --json` leaves out a task's `rank` and `mode` when the cell has none, instead of `"—"`; the `counts` key `"in progress"` is now `"in_progress"`; and pending signals are `{"task", "action"}` objects. Each output has a JSON schema in [`docs/json/`](../../docs/json/).
- **Exit codes settled.** A malformed task ID in `igris done` and `igris skip`, and an unknown `igris notify test --event`, are usage errors now (exit `2`, was `1`). `igris adapt` stopped with Ctrl-C exits `0`. Exit codes `3`–`9` are reserved.
- **`.igris/state.json` is version 3.** igris reads 1, 2 and 3 and upgrades them. A newer version is refused with a message to upgrade igris.
- **The plan format has a name:** format 1. `<!-- igris:format 1 -->` is optional, forever. A plan declaring a format igris doesn't know is refused with "plan format N needs a newer igris".
- **New:** `igris config migrate`, and `igris help COMMAND` shows that command's help.

The [CHANGELOG](../../CHANGELOG.md) lists every release in full.

## If you script igris

- Read `doctor`'s checks from `.checks`, use the `in_progress` key, read signals as `{task, action}` objects, and treat a missing `rank` or `mode` as none.
- Test for a non-zero exit code, or for `2` (a usage error), never for exactly `1`: a 1.x release may give a failure its own code from `3` to `9`.
- Read `--json`, the run log and the webhook ignoring fields and event types you don't know. 1.x adds them.
- Text output is for people and may change.

## Going back

A config that uses a key added in a later 1.x fails on an older igris as an unknown key; the fix is to upgrade. igris 0.6 and older can't read a version-3 `state.json`: finish the run first, or delete `.igris/state.json` to start a new one.
