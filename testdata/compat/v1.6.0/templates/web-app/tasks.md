<!-- igris:format 1 -->
# Plan — Web application

A starter plan for a web app: data and API, UI, then shipping. The stack is decided in `M0-02`, so the plan names none. Written by `igris init --template web-app`; edit it freely, only the task tables matter to igris (SPEC §3).

**Ranks are suggestions.** `sonnet` is set for well-specified work and `opus` for decisions, the core design and reviews. Change the Model column to what you want to spend before the first run, then run `igris check`. Also set `[run] verify` in `igris.toml` to the stack's test command.

**Start here.** `M0-01` is yours: describe what the app is and who it is for in `SPEC.md`. Every other task waits for it, so a run starts only once you have said what to build.

## M0 — Foundation

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M0-01 | **Write the spec** — say what the app is, its users, its screens and what it must never do, in `SPEC.md` | — | ready | — | user | — |
| M0-02 | **Decide the stack** — framework, database, hosting and auth, with the reasons and the alternatives; write the decision to `docs/decisions.md` for the owner to approve | M0-01 | blocked | opus | agent + user | plan |
| M0-03 | **Skeleton and CI** — the project skeleton for the chosen stack, one passing test, a command to run everything locally, and a CI workflow running the tests and the linter | M0-02 | blocked | sonnet | agent | — |
| M0-G | **M0 gate** — run the skeleton and the tests and approve the stack | M0-01…M0-03 | blocked | sonnet | agent + user | — |

## M1 — Data and API

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M1-01 | **Data model and migrations** — the entities from `SPEC.md`, migrations that run up from an empty database, and tests | M0-G | blocked | opus | agent | — |
| M1-02 | **API** — the endpoints the screens need, input validation, consistent error responses and tests | M1-01 | blocked | sonnet | agent | — |
| M1-03 | **Authentication** — sign-up, sign-in, sessions and access rules for the endpoints, with tests for what must be refused | M1-02 | blocked | opus | agent | — |
| M1-G | **M1 gate** — call the API by hand and approve the data model and the access rules | M1-01…M1-03 | blocked | sonnet | agent + user | — |

## M2 — UI

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M2-01 | **Layout and navigation** — the shell every screen shares: header, navigation and responsive layout | M1-G | blocked | sonnet | agent | — |
| M2-02 | **Screens** — the screens from `SPEC.md`, wired to the API | M2-01 | blocked | sonnet | agent | — |
| M2-03 | **Forms and error states** — validation messages, loading and empty states, and failed requests shown to the user | M2-02 | blocked | sonnet | agent | — |
| M2-04 | **Accessibility** — keyboard use, focus, labels and contrast checked on every screen, and what is found fixed | M2-03 | blocked | sonnet | agent | — |
| M2-G | **M2 gate** — click through every screen and approve the look and the flows | M2-01…M2-04 | blocked | sonnet | agent + user | — |

## M3 — Ship

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M3-01 | **End-to-end tests** — the main flows (sign-up, the core action, sign-out) driven in a browser | M2-G | blocked | sonnet | agent | — |
| M3-02 | **Hosting account and secrets** — create the hosting account, the database and the secrets, and put them where the deploy reads them; never in the repository | M2-G | blocked | — | user | — |
| M3-03 | **Deploy** — a repeatable deploy to the chosen host, migrations included, and a note in `README.md` on how to run it | M3-01, M3-02 | blocked | sonnet | agent | — |
| M3-04 | **Security review** — read the finished app for injection, broken access, leaked secrets and unsafe defaults; fix what is clear and list the rest | M3-03 | blocked | opus | agent | — |
| M3-G | **M3 gate** — use the deployed app and approve going live | M3-01…M3-04 | blocked | sonnet | agent + user | — |
