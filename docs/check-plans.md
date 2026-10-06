# Real-plan check: `check`, `phases`, `status` (M1-G)

`make test` covers the parser with synthetic plans. This check runs the read-only commands on 2–3 real plans from other projects, to find what synthetic fixtures miss: odd headings, wide tables, prose around tables, unusual statuses or dependency styles.

These commands only read the plan, so you can run them in the real project. Don't paste private plan text into issues or this repo (AGENTS.md §5); describe the shape of the problem or reproduce it with a synthetic plan.

## Per plan

Record: project (a nickname is fine), plan size (phases, tasks), whether it was written for igris.

```sh
igris check  --plan path/to/plan.md
igris phases --plan path/to/plan.md
igris status --plan path/to/plan.md
igris status PHASE --plan path/to/plan.md     # one phase you know well
igris check --plan path/to/plan.md --json | head -40
igris status --plan path/to/plan.md --json | head -40
```

- [ ] `check` exits 0 on a plan you believe is valid, or each problem it reports is real (`file:line: message` points at the right line)
- [ ] Every message says what's wrong and what to do; none is confusing or needs SPEC to understand
- [ ] Readiness warnings (`warning: … ready but …` / `blocked but …`) match what you see in the plan
- [ ] `phases` lists every phase in file order with plausible counts per status
- [ ] `status` picks the task you would pick next in each phase; `stuck` phases name the right unmet dependencies
- [ ] Wide task descriptions don't break the output in your normal terminal width
- [ ] `--json` output parses (`| jq .` works) and has the same facts as the text output
- [ ] Nothing was written: `git status` in the project shows no change to the plan

A plan that isn't in the canonical format is expected to fail `check`; the useful question is whether the errors explain why. Converting it is the adapt trial (`docs/adapt-trial.md`).

## Report

For each finding, one line: plan nickname, command, what you saw, what you expected. For example:

```
plan-b  status M2  says "stuck" but M2-04 is ready (its dep is in a skipped phase)
plan-a  check      line 88: "unknown status 'wip'" — fine, but doesn't say which statuses are valid
```

## Result

Date `____`, plans checked `____`, result **pass / findings**. When there are no open findings, mark M1-G `done` in `imp-docs/tasks.md` and V011-01 `done` in `imp-docs/ROADMAP.md`.
