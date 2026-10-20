# v1.1 gate

The v1.1 gate (V11-G) checks the exit criteria for igris 1.1: a real plan-mode task approved from igris on herdr and on tmux, a usage report of a real run, a forced rank mismatch caught, and the compatibility suite green. Publishing (V11-R) is the owner's.

It ran on Linux (Fedora 44) on 2026-10-20, from the owner's Claude Code session, which drove igris in a herdr tab it created itself and in a private tmux server (`tmux -L igrisgate`). Both projects were synthetic scratch repos on sonnet. The build was `master` at `12d9801` (`igris v1.0.0-12-g12d9801`), run from a scratch directory on `PATH` so the installed igris was left alone.

**Versions:** Claude Code 2.1.304, herdr 0.9.1, tmux 3.7c.

## Plan approval from igris

| Backend | Mode | Result |
|---|---|---|
| herdr | `plan:accept` | The session presented its plan; igris showed the dialog and marked the task needs you; **Approve** (enter) switched the session to accept mode; the log has `plan_presented`, `needs_you` (`plan_approval`), `plan_approved` (`mode: accept`, file and hash, no text) and `needs_you_clear`; the plan is in `.igris/plans/G0-01-a1-1.md` |
| tmux | `plan:auto` | The same sequence with `mode: auto`; the task finished and the phase completed |

In accept mode the session still asked before a compound `rtk … && igris done` bash command; that is Claude Code's accept mode, not igris.

## Usage report

`igris usage` on the herdr run: totals, by task, by model, by rank, top consumers, the audit and `quota unavailable` (`[usage] statusline` off). `--json` printed the same data with `"v": 1`. The `usage` line of each attempt was in `runs.jsonl`.

## Forced rank mismatch

G0-02 (rank sonnet) was switched to Opus with `/model opus` in its pane. The next response logged `rank_mismatch` (`on claude-opus-5-5, not its rank sonnet`), raised Needs you with reason `rank_mismatch`, sent the `rank_mismatch` backend notification, and `igris usage` reported `mismatch` and named the task. igris did not touch the session.

## Findings

- `igris usage` "By model": an attempt that ran on two models appears as one row keyed `claude-sonnet-5-5, claude-opus-5-5` rather than being counted under each model. Cosmetic; not changed by this gate.
- Claude Code's `/model` saved the choice as the user's default model (`~/.claude/settings.json`); anyone forcing a mismatch this way should reset it afterwards.

## The suite

`make fmt lint test` on the gate's tree: 0 lint issues, every package ok, including the compatibility suite over every release's captured files and the contract map.
