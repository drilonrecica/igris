# v1.0 gate: stable

The v1.0 gate (V10-G) checks the exit criteria for igris 1.0 (ROADMAP v1.0): the soak is complete, no bug is open that loses or corrupts data, writes outside Status cells, runs the wrong model or skips a confirmation, the fuzzing and compatibility suites are green, and the docs pages are live.

It ran on Linux (Fedora 44) on 2026-10-20, from the owner's Claude Code session, against `master` after V10-06 (`d6f31ee`) plus this gate's docs fixes. Publishing (V10-R) is the owner's.

**Versions:** igris 1.0.0-rc.2 (installed from its tag; 1.0.0 is rc.2 with no code changes), Claude Code 2.1.304, herdr 0.9.1, tmux 3.7c, Go 1.27.2.

## Soak

| Requirement (ROADMAP v1.0) | Result |
|---|---|
| ≥ 3 real phases on herdr | Done on rc.1: several real projects, on Linux and macOS (owner, 2026-10-20) |
| ≥ 2 real phases on tmux | **Waived by the owner.** tmux is covered by the backend conformance suite in `make test` |
| One run over Termius | Done on rc.1 |
| One `adapt` | Done on rc.1, on a non-canonical plan |
| rc.2 (after the final security review changed reattach, config confirmation and file reading) | **Approved by the owner without a separate soak.** Those fixes are covered by their regression tests and the conformance suite, not by a real resume on herdr or tmux |

Soak findings: (1) a `claude.command` left in an old `igris.toml` stops `check` and `arise`, and the error names `igris config migrate`; (2) an old development build stayed on `PATH` after upgrading. Both are upgrade friction, covered by the upgrade guide (`site/docs/upgrade.md`); no code change. Seen during the releases: right after a tag is pushed, `go install …@vX` can fail with `unknown revision` from the Go checksum database for about 30 minutes; the 1.0.0 release notes say how to get around it.

## Open bugs

None open in the categories the exit criteria name: data loss or corruption, writes outside Status cells, the wrong model, a skipped confirmation. The final security review (V10-05, [`threat-model.md`](threat-model.md)) found one high-severity issue (reattach to a pane a rewritten `state.json` named) and one medium (a config changed during a run used silently by the next one); both are fixed in rc.2 with regression tests.

## Config validation on rc.2

In a scratch project with the installed rc.2: `extra_args = ["--add-dir", "/"]` → `igris check` exits 1 with `claude.extra_args contains "--add-dir", which igris sets itself or which could change the model or permission mode (SPEC §7.4); remove it`; an ntfy `server` with `u:p@` → `notify.ntfy.server has a user or password in it; use token, not user:pass@ in the URL`. As the V07-P1 amendment of 2026-10-20 says.

## Docs pages live

All 14 pages of <https://drilonrecica.github.io/igris/> answer 200: the project page, `/docs/` and install, quick start, plan format, config, modes, notifications, adapt, several projects, FAQ, troubleshooting, upgrade and compatibility. A crawl of every internal link on them (17 targets, anchors included) found none broken; external links weren't followed.

## The suite

On the gate's tree: `make fmt lint test` (0 lint issues; includes the compatibility suite over every release's captured files and the contract map), `go test -race ./...` (no failures, no races), `make fuzz FUZZTIME=30s` (7 targets: config `FuzzParse`; plan `FuzzSplice`, `FuzzSpliceGenerated`, `FuzzSplitRow`, `FuzzParse`; state `FuzzParseSignal`, `FuzzParseAgentState`; no crashers), govulncheck v1.8.0: **0 vulnerabilities affecting igris**. It still lists GO-2026-5024 in `golang.org/x/sys` v0.36.0 (indirect, fixed in v0.44.0), in code igris doesn't reach, as at the v0.7 gate.

## Docs fixed by this gate

- CHANGELOG `[1.0.0]`: the final security review was a one-time review, not something that runs on every change; the compatibility suite and the fuzz targets are what run in CI.
- 1.0.0 release notes: how to get around the checksum database right after the release.
- `SECURITY.md`: supported versions are the latest 1.x (was "pre-1.0, latest 0.6.x").
- README: status says v1.0.0; the roadmap shows 1.x, 2.0 and later as plans, no longer v0.7 and v1.0.

## Result

The exit criteria are met, with the two waivers above recorded. Next: V10-R, publishing v1.0.0 (signed release, marked Latest, the Homebrew tap's formula, the site's 1.0 copy, `make compat TAG=v1.0.0`).
