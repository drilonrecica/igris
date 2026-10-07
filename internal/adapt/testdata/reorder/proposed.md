# Widget factory

Plan for the widget factory. Ship by spring 2027.

## W1 — Parsing widgets

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| W1-01 | **Read widget files** from disk | — | done | sonnet |
| W1-02 | **Parse widget headers** | W1-01 | ready | sonnet |
| W1-03 | **Validate widgets** against the schema | W1-01, W1-02 | blocked | opus |
| W1-04 | **Report parse errors** | W1-02 | blocked | sonnet |

## W2 — Rendering

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| W2-02 | **Draw widgets** | W2-01 | blocked | sonnet |
| W2-01 | **Lay out widgets** | W1-03 | blocked | opus |
| W2-03 | **Export PNG** | W2-02 | blocked | sonnet |
| L-01 | **Widget themes** | W2-02 | blocked | sonnet |

## W3 — Animation

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| W3-00 | **Wait for the animation spec from the client** | — | ready | — |
| L-02 | **Widget animations** | W2-02, W3-00 | blocked | ? |

Notes: the schema lives in docs/schema.md.

## Adapt notes

- L-02 had no model; set to `?`.
