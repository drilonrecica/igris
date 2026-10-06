# Synthetic large plan

Fixture for igris tests. Not a real project.

## Legend

| Status | Meaning |
|---|---|
| ready | can start |
| done | finished |

## Working rules

Rules live here.

```
## Fake — heading in a fence

| ID | Status | Model |
|---|---|---|
| X-01 | ready | sonnet |
```

## F1 — Foundation

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| F1-01 | **Task F1-01** — step 0 of Foundation | — | §1.0 | `done` | haiku | Agent |  |
| F1-02 | **Task F1-02** — do thing \| with pipe 1 | F1-01 | §1.1 | done | sonnet | Agent | accept |
| F1-03 | **Task F1-03** — step 2 of Foundation | F1-02 | §1.2 | done | opus | agent | plan |
| F1-04 | **Task F1-04** — step 3 of Foundation | F1-03 | §1.3 | done | haiku | Agent |  |
| F1-05 | **Task F1-05** — step 4 of Foundation | F1-04 | §1.4 | done | — | user |  |
| F1-06 | **Task F1-06** — do thing \| with pipe 5 | F1-05, F1-04 | §1.5 | `done` | sonnet | Agent |  |
| F1-07 | **Task F1-07** — step 6 of Foundation | F1-02...F1-03 | §1.6 | done | opus | agent | plan |
| F1-08 | **Task F1-08** — step 7 of Foundation | F1-07 | §1.7 | done | sonnet | Agent |  |
| F1-09 | **Task F1-09** — step 8 of Foundation | F1-08 | §1.8 | done | sonnet | Agent |  |
| F1-10 | **Task F1-10** — do thing \| with pipe 9 | F1-02...F1-03 | §1.9 | done | haiku | Agent | accept |
| F1-11 | **Task F1-11** — step 10 of Foundation | F1-10, F1-09 | §1.10 | `done` | — | user |  |
| F1-12 | **Task F1-12** — step 11 of Foundation | F1-11 | §1.11 | done | sonnet | Agent |  |
| F1-13 | **Task F1-13** — step 12 of Foundation | F1-02...F1-03 | §1.12 | blocked | haiku | Agent |  |
| F1-G | **Task F1-G** — do thing \| with pipe 13 | F1-01…F1-13 | §1.13 | blocked | sonnet | agent + user | — |

## F2 — Storage

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| F2-01 | **Task F2-01** — step 0 of Storage | F1-G | §2.0 | `done` | haiku | Agent |  |
| F2-02 | **Task F2-02** — do thing \| with pipe 1 | F2-01 | §2.1 | done | sonnet | Agent | accept |
| F2-03 | **Task F2-03** — step 2 of Storage | F2-02 | §2.2 | done | opus | agent | plan |
| F2-04 | **Task F2-04** — step 3 of Storage | F2-03 | §2.3 | done | haiku | Agent |  |
| F2-05 | **Task F2-05** — step 4 of Storage | F2-04 | §2.4 | done | — | user |  |
| F2-06 | **Task F2-06** — do thing \| with pipe 5 | F2-05, F2-04 | §2.5 | `done` | sonnet | Agent |  |
| F2-07 | **Task F2-07** — step 6 of Storage | F2-02...F2-03 | §2.6 | done | opus | agent | plan |
| F2-08 | **Task F2-08** — step 7 of Storage | F2-07, F1-G | §2.7 | done | sonnet | Agent |  |
| F2-09 | **Task F2-09** — step 8 of Storage | F2-08 | §2.8 | done | sonnet | Agent |  |
| F2-10 | **Task F2-10** — do thing \| with pipe 9 | F2-02...F2-03 | §2.9 | done | haiku | Agent | accept |
| F2-11 | **Task F2-11** — step 10 of Storage | F2-10, F2-09 | §2.10 | `done` | — | user |  |
| F2-12 | **Task F2-12** — step 11 of Storage | F2-11 | §2.11 | done | sonnet | Agent |  |
| F2-13 | **Task F2-13** — step 12 of Storage | F2-02...F2-03 | §2.12 | blocked | haiku | Agent |  |
| F2-G | **Task F2-G** — do thing \| with pipe 13 | F2-01…F2-13 | §2.13 | blocked | sonnet | agent + user | — |

## F3 — Ingest

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| F3-01 | **Task F3-01** — step 0 of Ingest | F2-G | §3.0 | done | haiku | Agent |  |
| F3-02 | **Task F3-02** — do thing \| with pipe 1 | F3-01 | §3.1 | done | sonnet | Agent | accept |
| F3-03 | **Task F3-03** — step 2 of Ingest | F3-02 | §3.2 | done | opus | agent | plan |
| F3-04 | **Task F3-04** — step 3 of Ingest | F3-03 | §3.3 | `done` | haiku | Agent |  |
| F3-05 | **Task F3-05** — step 4 of Ingest | F3-04 | §3.4 | skipped (not needed) | — | user |  |
| F3-06 | **Task F3-06** — do thing \| with pipe 5 | F3-05, F3-04 | §3.5 | in progress | sonnet | Agent |  |
| F3-07 | **Task F3-07** — step 6 of Ingest | F3-02...F3-03 | §3.6 | ready | opus | agent | plan |
| F3-08 | **Task F3-08** — step 7 of Ingest | F3-07, F2-G | §3.7 | `ready` | sonnet | Agent |  |
| F3-09 | **Task F3-09** — step 8 of Ingest | F3-08 | §3.8 | blocked | sonnet | Agent |  |
| F3-10 | **Task F3-10** — do thing \| with pipe 9 | F3-02...F3-03 | §3.9 | blocked | haiku | Agent | accept |
| F3-11 | **Task F3-11** — step 10 of Ingest | F3-10, F3-09 | §3.10 | blocked | — | user |  |
| F3-12 | **Task F3-12** — step 11 of Ingest | F3-11 | §3.11 | blocked | sonnet | Agent |  |
| F3-13 | **Task F3-13** — step 12 of Ingest | F3-02...F3-03 | §3.12 | blocked | haiku | Agent |  |
| F3-G | **Task F3-G** — do thing \| with pipe 13 | F3-01…F3-13 | §3.13 | blocked | sonnet | agent + user | — |

## Phase 4 — Index

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| Phase-4-01 | **Task Phase-4-01** — step 0 of Index | F3-G | §4.0 | ready | haiku | Agent |  |
| Phase-4-02 | **Task Phase-4-02** — do thing \| with pipe 1 | Phase-4-01 | §4.1 | blocked | sonnet | Agent | accept |
| Phase-4-03 | **Task Phase-4-03** — step 2 of Index | Phase-4-02 | §4.2 | blocked | opus | agent | plan |
| Phase-4-04 | **Task Phase-4-04** — step 3 of Index | Phase-4-03 | §4.3 | blocked | haiku | Agent |  |
| Phase-4-05 | **Task Phase-4-05** — step 4 of Index | Phase-4-04 | §4.4 | blocked | — | user |  |
| Phase-4-06 | **Task Phase-4-06** — do thing \| with pipe 5 | Phase-4-05, Phase-4-04 | §4.5 | blocked | sonnet | Agent |  |
| Phase-4-07 | **Task Phase-4-07** — step 6 of Index | Phase-4-02...Phase-4-03 | §4.6 | blocked | opus | agent | plan |
| Phase-4-08 | **Task Phase-4-08** — step 7 of Index | Phase-4-07, F3-G | §4.7 | blocked | sonnet | Agent |  |
| Phase-4-09 | **Task Phase-4-09** — step 8 of Index | Phase-4-08 | §4.8 | blocked | sonnet | Agent |  |
| Phase-4-10 | **Task Phase-4-10** — do thing \| with pipe 9 | Phase-4-02...Phase-4-03 | §4.9 | blocked | haiku | Agent | accept |
| Phase-4-11 | **Task Phase-4-11** — step 10 of Index | Phase-4-10, Phase-4-09 | §4.10 | blocked | — | user |  |
| Phase-4-12 | **Task Phase-4-12** — step 11 of Index | Phase-4-11 | §4.11 | blocked | sonnet | Agent |  |
| Phase-4-13 | **Task Phase-4-13** — step 12 of Index | Phase-4-02...Phase-4-03 | §4.12 | blocked | haiku | Agent |  |
| Phase-4-G | **Task Phase-4-G** — do thing \| with pipe 13 | Phase-4-01…Phase-4-13 | §4.13 | blocked | sonnet | agent + user | — |

## F5 — Query

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| F5-01 | **Task F5-01** — step 0 of Query | Phase-4-G | §5.0 | ready | haiku | Agent |  |
| F5-02 | **Task F5-02** — do thing \| with pipe 1 | F5-01 | §5.1 | blocked | sonnet | Agent | accept |
| F5-03 | **Task F5-03** — step 2 of Query | F5-02 | §5.2 | blocked | opus | agent | plan |
| F5-04 | **Task F5-04** — step 3 of Query | F5-03 | §5.3 | blocked | haiku | Agent |  |
| F5-05 | **Task F5-05** — step 4 of Query | F5-04 | §5.4 | blocked | — | user |  |
| F5-06 | **Task F5-06** — do thing \| with pipe 5 | F5-05, F5-04 | §5.5 | blocked | sonnet | Agent |  |
| F5-07 | **Task F5-07** — step 6 of Query | F5-02...F5-03 | §5.6 | blocked | opus | agent | plan |
| F5-08 | **Task F5-08** — step 7 of Query | F5-07, Phase-4-G | §5.7 | blocked | sonnet | Agent |  |
| F5-09 | **Task F5-09** — step 8 of Query | F5-08 | §5.8 | blocked | sonnet | Agent |  |
| F5-10 | **Task F5-10** — do thing \| with pipe 9 | F5-02...F5-03 | §5.9 | blocked | haiku | Agent | accept |
| F5-11 | **Task F5-11** — step 10 of Query | F5-10, F5-09 | §5.10 | blocked | — | user |  |
| F5-12 | **Task F5-12** — step 11 of Query | F5-11 | §5.11 | blocked | sonnet | Agent |  |
| F5-13 | **Task F5-13** — step 12 of Query | F5-02...F5-03 | §5.12 | blocked | haiku | Agent |  |
| F5-G | **Task F5-G** — do thing \| with pipe 13 | F5-01…F5-13 | §5.13 | blocked | sonnet | agent + user | — |

### Notes

| Term | Meaning |
|---|---|
| x | y |

## F6 — Auth

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| F6-01 | **Task F6-01** — step 0 of Auth | F5-G | §6.0 | ready | haiku | Agent |  |
| F6-02 | **Task F6-02** — do thing \| with pipe 1 | F6-01 | §6.1 | blocked | sonnet | Agent | accept |
| F6-03 | **Task F6-03** — step 2 of Auth | F6-02 | §6.2 | blocked | opus | agent | plan |
| F6-04 | **Task F6-04** — step 3 of Auth | F6-03 | §6.3 | blocked | haiku | Agent |  |
| F6-05 | **Task F6-05** — step 4 of Auth | F6-04 | §6.4 | blocked | — | user |  |
| F6-06 | **Task F6-06** — do thing \| with pipe 5 | F6-05, F6-04 | §6.5 | blocked | sonnet | Agent |  |
| F6-07 | **Task F6-07** — step 6 of Auth | F6-02...F6-03 | §6.6 | blocked | opus | agent | plan |
| F6-08 | **Task F6-08** — step 7 of Auth | F6-07, F5-G | §6.7 | blocked | sonnet | Agent |  |
| F6-09 | **Task F6-09** — step 8 of Auth | F6-08 | §6.8 | blocked | sonnet | Agent |  |
| F6-10 | **Task F6-10** — do thing \| with pipe 9 | F6-02...F6-03 | §6.9 | blocked | haiku | Agent | accept |
| F6-11 | **Task F6-11** — step 10 of Auth | F6-10, F6-09 | §6.10 | blocked | — | user |  |
| F6-12 | **Task F6-12** — step 11 of Auth | F6-11 | §6.11 | blocked | sonnet | Agent |  |
| F6-13 | **Task F6-13** — step 12 of Auth | F6-02...F6-03 | §6.12 | blocked | haiku | Agent |  |
| F6-G | **Task F6-G** — do thing \| with pipe 13 | F6-01…F6-13 | §6.13 | blocked | sonnet | agent + user | — |

## F7 — API

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| F7-01 | **Task F7-01** — step 0 of API | F6-G | §7.0 | ready | haiku | Agent |  |
| F7-02 | **Task F7-02** — do thing \| with pipe 1 | F7-01 | §7.1 | blocked | sonnet | Agent | accept |
| F7-03 | **Task F7-03** — step 2 of API | F7-02 | §7.2 | blocked | opus | agent | plan |
| F7-04 | **Task F7-04** — step 3 of API | F7-03 | §7.3 | blocked | haiku | Agent |  |
| F7-05 | **Task F7-05** — step 4 of API | F7-04 | §7.4 | blocked | — | user |  |
| F7-06 | **Task F7-06** — do thing \| with pipe 5 | F7-05, F7-04 | §7.5 | blocked | sonnet | Agent |  |
| F7-07 | **Task F7-07** — step 6 of API | F7-02...F7-03 | §7.6 | blocked | opus | agent | plan |
| F7-08 | **Task F7-08** — step 7 of API | F7-07, F6-G | §7.7 | blocked | sonnet | Agent |  |
| F7-09 | **Task F7-09** — step 8 of API | F7-08 | §7.8 | blocked | sonnet | Agent |  |
| F7-10 | **Task F7-10** — do thing \| with pipe 9 | F7-02...F7-03 | §7.9 | blocked | haiku | Agent | accept |
| F7-11 | **Task F7-11** — step 10 of API | F7-10, F7-09 | §7.10 | blocked | — | user |  |
| F7-12 | **Task F7-12** — step 11 of API | F7-11 | §7.11 | blocked | sonnet | Agent |  |
| F7-13 | **Task F7-13** — step 12 of API | F7-02...F7-03 | §7.12 | blocked | haiku | Agent |  |
| F7-G | **Task F7-G** — do thing \| with pipe 13 | F7-01…F7-13 | §7.13 | blocked | sonnet | agent + user | — |

## Phase 8 — Workers

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| Phase-8-01 | **Task Phase-8-01** — step 0 of Workers | F7-G | §8.0 | ready | haiku | Agent |  |
| Phase-8-02 | **Task Phase-8-02** — do thing \| with pipe 1 | Phase-8-01 | §8.1 | blocked | sonnet | Agent | accept |
| Phase-8-03 | **Task Phase-8-03** — step 2 of Workers | Phase-8-02 | §8.2 | blocked | opus | agent | plan |
| Phase-8-04 | **Task Phase-8-04** — step 3 of Workers | Phase-8-03 | §8.3 | blocked | haiku | Agent |  |
| Phase-8-05 | **Task Phase-8-05** — step 4 of Workers | Phase-8-04 | §8.4 | blocked | — | user |  |
| Phase-8-06 | **Task Phase-8-06** — do thing \| with pipe 5 | Phase-8-05, Phase-8-04 | §8.5 | blocked | sonnet | Agent |  |
| Phase-8-07 | **Task Phase-8-07** — step 6 of Workers | Phase-8-02...Phase-8-03 | §8.6 | blocked | opus | agent | plan |
| Phase-8-08 | **Task Phase-8-08** — step 7 of Workers | Phase-8-07, F7-G | §8.7 | blocked | sonnet | Agent |  |
| Phase-8-09 | **Task Phase-8-09** — step 8 of Workers | Phase-8-08 | §8.8 | blocked | sonnet | Agent |  |
| Phase-8-10 | **Task Phase-8-10** — do thing \| with pipe 9 | Phase-8-02...Phase-8-03 | §8.9 | blocked | haiku | Agent | accept |
| Phase-8-11 | **Task Phase-8-11** — step 10 of Workers | Phase-8-10, Phase-8-09 | §8.10 | blocked | — | user |  |
| Phase-8-12 | **Task Phase-8-12** — step 11 of Workers | Phase-8-11 | §8.11 | blocked | sonnet | Agent |  |
| Phase-8-13 | **Task Phase-8-13** — step 12 of Workers | Phase-8-02...Phase-8-03 | §8.12 | blocked | haiku | Agent |  |
| Phase-8-G | **Task Phase-8-G** — do thing \| with pipe 13 | Phase-8-01…Phase-8-13 | §8.13 | blocked | sonnet | agent + user | — |

## F9 — Billing

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| F9-01 | **Task F9-01** — step 0 of Billing | Phase-8-G | §9.0 | ready | haiku | Agent |  |
| F9-02 | **Task F9-02** — do thing \| with pipe 1 | F9-01 | §9.1 | blocked | sonnet | Agent | accept |
| F9-03 | **Task F9-03** — step 2 of Billing | F9-02 | §9.2 | blocked | opus | agent | plan |
| F9-04 | **Task F9-04** — step 3 of Billing | F9-03 | §9.3 | blocked | haiku | Agent |  |
| F9-05 | **Task F9-05** — step 4 of Billing | F9-04 | §9.4 | blocked | — | user |  |
| F9-06 | **Task F9-06** — do thing \| with pipe 5 | F9-05, F9-04 | §9.5 | blocked | sonnet | Agent |  |
| F9-07 | **Task F9-07** — step 6 of Billing | F9-02...F9-03 | §9.6 | blocked | opus | agent | plan |
| F9-08 | **Task F9-08** — step 7 of Billing | F9-07, Phase-8-G | §9.7 | blocked | sonnet | Agent |  |
| F9-09 | **Task F9-09** — step 8 of Billing | F9-08 | §9.8 | blocked | sonnet | Agent |  |
| F9-10 | **Task F9-10** — do thing \| with pipe 9 | F9-02...F9-03 | §9.9 | blocked | haiku | Agent | accept |
| F9-11 | **Task F9-11** — step 10 of Billing | F9-10, F9-09 | §9.10 | blocked | — | user |  |
| F9-12 | **Task F9-12** — step 11 of Billing | F9-11 | §9.11 | blocked | sonnet | Agent |  |
| F9-13 | **Task F9-13** — step 12 of Billing | F9-02...F9-03 | §9.12 | blocked | haiku | Agent |  |
| F9-G | **Task F9-G** — do thing \| with pipe 13 | F9-01…F9-13 | §9.13 | blocked | sonnet | agent + user | — |

## F10 — Reports

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| F10-01 | **Task F10-01** — step 0 of Reports | F9-G | §10.0 | ready | haiku | Agent |  |
| F10-02 | **Task F10-02** — do thing \| with pipe 1 | F10-01 | §10.1 | blocked | sonnet | Agent | accept |
| F10-03 | **Task F10-03** — step 2 of Reports | F10-02 | §10.2 | blocked | opus | agent | plan |
| F10-04 | **Task F10-04** — step 3 of Reports | F10-03 | §10.3 | blocked | haiku | Agent |  |
| F10-05 | **Task F10-05** — step 4 of Reports | F10-04 | §10.4 | blocked | — | user |  |
| F10-06 | **Task F10-06** — do thing \| with pipe 5 | F10-05, F10-04 | §10.5 | blocked | sonnet | Agent |  |
| F10-07 | **Task F10-07** — step 6 of Reports | F10-02...F10-03 | §10.6 | blocked | opus | agent | plan |
| F10-08 | **Task F10-08** — step 7 of Reports | F10-07, F9-G | §10.7 | blocked | sonnet | Agent |  |
| F10-09 | **Task F10-09** — step 8 of Reports | F10-08 | §10.8 | blocked | sonnet | Agent |  |
| F10-10 | **Task F10-10** — do thing \| with pipe 9 | F10-02...F10-03 | §10.9 | blocked | haiku | Agent | accept |
| F10-11 | **Task F10-11** — step 10 of Reports | F10-10, F10-09 | §10.10 | blocked | — | user |  |
| F10-12 | **Task F10-12** — step 11 of Reports | F10-11 | §10.11 | blocked | sonnet | Agent |  |
| F10-13 | **Task F10-13** — step 12 of Reports | F10-02...F10-03 | §10.12 | blocked | haiku | Agent |  |
| F10-G | **Task F10-G** — do thing \| with pipe 13 | F10-01…F10-13 | §10.13 | blocked | sonnet | agent + user | — |

## F11 — Admin

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| F11-01 | **Task F11-01** — step 0 of Admin | F10-G | §11.0 | ready | haiku | Agent |  |
| F11-02 | **Task F11-02** — do thing \| with pipe 1 | F11-01 | §11.1 | blocked | sonnet | Agent | accept |
| F11-03 | **Task F11-03** — step 2 of Admin | F11-02 | §11.2 | blocked | opus | agent | plan |
| F11-04 | **Task F11-04** — step 3 of Admin | F11-03 | §11.3 | blocked | haiku | Agent |  |
| F11-05 | **Task F11-05** — step 4 of Admin | F11-04 | §11.4 | blocked | — | user |  |
| F11-06 | **Task F11-06** — do thing \| with pipe 5 | F11-05, F11-04 | §11.5 | blocked | sonnet | Agent |  |
| F11-07 | **Task F11-07** — step 6 of Admin | F11-02...F11-03 | §11.6 | blocked | opus | agent | plan |
| F11-08 | **Task F11-08** — step 7 of Admin | F11-07, F10-G | §11.7 | blocked | sonnet | Agent |  |
| F11-09 | **Task F11-09** — step 8 of Admin | F11-08 | §11.8 | blocked | sonnet | Agent |  |
| F11-10 | **Task F11-10** — do thing \| with pipe 9 | F11-02...F11-03 | §11.9 | blocked | haiku | Agent | accept |
| F11-11 | **Task F11-11** — step 10 of Admin | F11-10, F11-09 | §11.10 | blocked | — | user |  |
| F11-12 | **Task F11-12** — step 11 of Admin | F11-11 | §11.11 | blocked | sonnet | Agent |  |
| F11-13 | **Task F11-13** — step 12 of Admin | F11-02...F11-03 | §11.12 | blocked | haiku | Agent |  |
| F11-G | **Task F11-G** — do thing \| with pipe 13 | F11-01…F11-13 | §11.13 | blocked | sonnet | agent + user | — |

## Phase 12 — Release

| ID | Task | Deps | Spec | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|---|
| Phase-12-01 | **Task Phase-12-01** — step 0 of Release | F11-G | §12.0 | ready | haiku | Agent |  |
| Phase-12-02 | **Task Phase-12-02** — do thing \| with pipe 1 | Phase-12-01 | §12.1 | blocked | sonnet | Agent | accept |
| Phase-12-03 | **Task Phase-12-03** — step 2 of Release | Phase-12-02 | §12.2 | blocked | opus | agent | plan |
| Phase-12-04 | **Task Phase-12-04** — step 3 of Release | Phase-12-03 | §12.3 | blocked | haiku | Agent |  |
| Phase-12-05 | **Task Phase-12-05** — step 4 of Release | Phase-12-04 | §12.4 | blocked | — | user |  |
| Phase-12-06 | **Task Phase-12-06** — do thing \| with pipe 5 | Phase-12-05, Phase-12-04 | §12.5 | blocked | sonnet | Agent |  |
| Phase-12-07 | **Task Phase-12-07** — step 6 of Release | Phase-12-02...Phase-12-03 | §12.6 | blocked | opus | agent | plan |
| Phase-12-08 | **Task Phase-12-08** — step 7 of Release | Phase-12-07, F11-G | §12.7 | blocked | sonnet | Agent |  |
| Phase-12-09 | **Task Phase-12-09** — step 8 of Release | Phase-12-08 | §12.8 | blocked | sonnet | Agent |  |
| Phase-12-10 | **Task Phase-12-10** — do thing \| with pipe 9 | Phase-12-02...Phase-12-03 | §12.9 | blocked | haiku | Agent | accept |
| Phase-12-11 | **Task Phase-12-11** — step 10 of Release | Phase-12-10, Phase-12-09 | §12.10 | blocked | — | user |  |
| Phase-12-12 | **Task Phase-12-12** — step 11 of Release | Phase-12-11 | §12.11 | blocked | sonnet | Agent |  |
| Phase-12-13 | **Task Phase-12-13** — step 12 of Release | Phase-12-02...Phase-12-03 | §12.12 | blocked | haiku | Agent |  |
| Phase-12-G | **Task Phase-12-G** — do thing \| with pipe 13 | Phase-12-01…Phase-12-13 | §12.13 | blocked | sonnet | agent + user | — |
