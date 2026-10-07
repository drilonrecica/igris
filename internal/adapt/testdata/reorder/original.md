# Widget factory

Plan for the widget factory. Ship by spring.

## W1 — Parsing

| Status | ID | Model | task | Depends on |
|---|---|---|---|---|
| done | W1-01 | sonnet | **Read widget files** from disk | — |
| todo | W1-02 | sonnet | **Parse widget headers** | W1-01 |
| todo | W1-03 | opus | **Validate widgets** against the schema | W1-01 |
| todo | W1-04 | sonnet | **Report parse errors** | W1-02 |

## W2 — Rendering

| Status | ID | Model | task | Depends on |
|---|---|---|---|---|
| todo | W2-01 | opus | **Lay out widgets** | W1-03 |
| todo | W2-02 | sonnet | **Draw widgets** | W2-01 |
| todo | W2-03 | sonnet | **Export PNG** | W2-02 |

## Later

| Status | ID | Model | task | Depends on |
|---|---|---|---|---|
| todo | L-01 | sonnet | **Widget themes** | W2-02 |
| todo | L-02 | | **Widget animations** | W2-02 |

Notes: the schema lives in docs/schema.md.
