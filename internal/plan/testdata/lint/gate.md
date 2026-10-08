## M1 — Phase

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| M1-01 | **One** | — | ready | sonnet |
| M1-02 | **Two** | M1-01 | blocked | sonnet |
| M1-03 | **Three** | M1-02 | blocked | sonnet |
| M1-04 | **Four** | M1-01 | blocked | sonnet |
| M1-05 | **Five** | — | ready | sonnet |
| M1-G | **Gate** — run every check | M1-03 | blocked | sonnet |
