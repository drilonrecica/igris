# A plan with a parallel phase

<!-- igris:format 3 -->

## A — First, one at a time

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **Start** | — | done | sonnet | agent |

## B — Second, side by side

<!-- igris:parallel -->

| ID | Task | Deps | Status | Model | Owner | Group |
|---|---|---|---|---|---|---|
| B-1 | **Schema** | A-1 | ready | sonnet | agent | db |
| B-2 | **Store** — builds on B-1 | A-1 | ready | sonnet | agent | db |
| B-3 | **Client** | A-1 | ready | opus | agent | — |
| B-G | **Gate** — the owner approves | B-1…B-3 | blocked | sonnet | agent + user | — |
