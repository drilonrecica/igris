# Demo plan

The synthetic plan recorded for the README demo (`make demo`, see `README.md` here). The tasks are trivial on purpose so each session finishes in well under a minute.

## D1 — Hello script

| ID | Task | Deps | Status | Model | Owner | Mode | Spec |
|---|---|---|---|---|---|---|---|
| D1-01 | **Greeting script** — write `hello.sh`, a POSIX sh script that prints `Hello, shadow.` | — | ready | sonnet | agent | — | — |
| D1-02 | **Shout flag** — `sh hello.sh --shout` prints the greeting in upper case; without the flag the output is unchanged | D1-01 | blocked | opus | agent | — | — |
