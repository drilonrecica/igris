# A plan in three files

<!-- igris:format 2 -->

## A — Foundation

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **Module** | — | done | sonnet | agent |
| A-2 | **Config** | A-1 | ready | sonnet | agent |

<!-- igris:include phases/b.md -->
<!-- igris:include phases/c.md -->

## D — Release

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| D-1 | **Release** | A-2, B-1…B-2, C-1 | blocked | opus | agent |
| D-G | **Gate** | D-1 | blocked | sonnet | agent + user |
