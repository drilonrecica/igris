## M1 — Phase

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| M1-01 | **One** | — | ready | sonnet |
| M1-02 | **Two** | M1-01 | blocked | sonnet |
| M1-G | **Gate** — run every check | M1-01 | blocked | sonnet |
| M1-R | **Release** after the gate | M1-G | blocked | sonnet |
| M1-S | **Announce** the release | M1-R | blocked | sonnet |
