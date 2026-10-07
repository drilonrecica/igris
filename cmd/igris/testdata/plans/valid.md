# Example plan

A small plan in igris's canonical format (SPEC §3). Try it:

```sh
cd examples
igris check
igris status
igris arise P1 --dry-run
```

Only the task tables matter to igris: the `Task` text is what each session is told to do, and every other section, like this one, is ignored.

## P1 — Hello CLI

| ID | Task | Deps | Status | Model | Owner | Mode | Spec |
|---|---|---|---|---|---|---|---|
| P1-01 | **Project skeleton** — module, `main.go`, a Makefile with `build` and `test` | — | done | sonnet | agent | — | §1 |
| P1-02 | **Flag parsing** — `--name` and `--shout` flags with tests | P1-01 | ready | sonnet | agent | — | §2 |
| P1-03 | **Greeting design** — decide how greetings are chosen and localized; write the decision to `docs/greeting.md` | P1-01 | ready | opus | agent | plan | §3 |
| P1-04 | **Pick a license** — your call, not the agent's | — | ready | — | user | — | — |
| P1-G | **P1 gate** — run the binary and confirm the output is what you want | P1-01…P1-04 | blocked | sonnet | agent + user | — | — |

## P2 — Polish

| ID | Task | Deps | Status | Model | Owner | Mode | Spec |
|---|---|---|---|---|---|---|---|
| P2-01 | **Greeting engine** — implement the design from P1-03 | P1-G | blocked | opus | agent | — | §3 |
| P2-02 | **README** — usage, flags, examples | P1-02, P2-01 | blocked | sonnet | agent | — | §4 |
| P2-03 | **Release checklist** — security and robustness review of the finished tool | P2-01…P2-02 | blocked | fable | agent | — | §5 |
