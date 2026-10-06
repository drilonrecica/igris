# Gizmo plan

## G — Gizmos

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| G-1 | **Lexer** | — | ready | sonnet |
| G-2 | **Parser** | G-1 | blocked | sonnet |
| G-3 | **Checker** | G-1, G-2 | blocked | opus |
| G-4 | **Report** | G-1…G-3 | blocked | sonnet |
