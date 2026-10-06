# `igris adapt` real-plan trial (M6-05)

Run `igris adapt` on two real plans from your other projects. Work on **copies** in a scratch directory; don't commit the plans or the results anywhere public (AGENTS.md §5).

## Per plan

1. Copy the plan and its project's `igris.toml` (if any) into a scratch directory with `herdr` running.
2. `igris check --plan plan.md` — note the errors igris reports.
3. `igris adapt --plan plan.md` (add `--model opus` for a hard one). Answer any questions in the adapt pane.
4. In the review, check the diff:
   - every task, ID, description, dependency, status and model is still there (count the tasks before and after);
   - prose dependencies became IDs, and nothing was reworded;
   - tasks without a usable model have Model `?` and are listed under `## Adapt notes`.
5. **Reject** once (`r`) and confirm the plan is unchanged and the proposal is still in `.igris/adapt/`. Run `igris adapt` again.
6. **Accept** (`a`). If models are left at `?`, confirm the second prompt starts on "Keep reviewing", then fill them in by hand.
7. `igris check --plan plan.md` passes; `.igris/adapt/<plan>.<timestamp>.bak.md` equals the original.
8. `igris arise <phase> --dry-run` shows a sensible launch order and models.

## Record

For each plan: task count before/after, errors `check` reported, edits you had to make after accepting, anything the session got wrong or asked needlessly, anything the review screen made hard to judge. Report problems back as new tasks; don't patch adapt ad hoc.

# M6 gate (M6-G)

Exit: a non-canonical plan can be converted, reviewed as a diff, and accepted or rejected safely.

Automated (checked when this was written): `make fmt lint test` and `go test -race ./...` are green; `igris adapt --help` (`--model`, `--plan`) matches SPEC §9; scenario tests cover renamed columns, prose deps and missing models end to end on the fake backend, including accept + backup.

Owner confirms:

- [ ] Two real plans adapted per the trial above, each accepted and passing `igris check`.
- [ ] Reject left the plan untouched and the proposal in `.igris/adapt/`.
- [ ] No problems found, or each one is recorded as a task.

When all three are ticked, mark M6-05 and M6-G `done` in `imp-docs/tasks.md`.
