## A — First

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| A-1 | **Start** | — | done | sonnet | agent | — |
| A-2 | **Next** | A-1 | blocked | sonnet | agent | — |
| A-3 | **Later** | A-2 | ready | opus | agent | plan |
| A-4 | **Yours** | — | ready | — | user | — |

## B — Second

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| B-1 | **Resume me** | A-1 | in progress | sonnet | agent | yolo |
| B-2 | **After** | B-1, A-3 | ready | sonnet | agent | — |
