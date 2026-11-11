<!-- igris:format 1 -->
# Plan — Library

A starter plan for a reusable library: the public API, the core, docs and a first release. The language is yours; the public API is decided in `M0-02`. Written by `igris init --template library`; edit it freely, only the task tables matter to igris (SPEC §3).

**Ranks are suggestions.** `sonnet` is set for well-specified work and `opus` for decisions, the core design and reviews. Change the Model column to what you want to spend before the first run, then run `igris check`. Also set `[run] verify` in `igris.toml` to the language's test command.

**Start here.** `M0-01` is yours: describe what the library does and who uses it in `SPEC.md`. Every other task waits for it, so a run starts only once you have said what to build.

## M0 — Foundation

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M0-01 | **Write the spec** — say what the library does, who uses it, and what it will never do, in `SPEC.md` | — | ready | — | user | — |
| M0-02 | **Design the public API** — the types, functions and errors a user sees, their names and the compatibility promise; write the decision to `docs/decisions.md` for the owner to approve | M0-01 | blocked | opus | agent + user | plan |
| M0-03 | **Skeleton and CI matrix** — the package layout, one passing test, and a CI workflow that runs the tests on the language versions and platforms you support | M0-02 | blocked | sonnet | agent | — |
| M0-G | **M0 gate** — read the API design, run the tests and approve | M0-01…M0-03 | blocked | sonnet | agent + user | — |

## M1 — Core

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M1-01 | **Implementation** — the public API from the decision, with the smallest internals that work | M0-G | blocked | opus | agent | — |
| M1-02 | **Errors and edge cases** — empty, huge and malformed input, and every error the API promises, each with a test | M1-01 | blocked | sonnet | agent | — |
| M1-03 | **Tests and fuzzing** — table-driven tests for the public API and a fuzz test for each parser or decoder | M1-01 | blocked | sonnet | agent | — |
| M1-04 | **Benchmarks** — benchmarks of the operations users call most, with a note of the numbers | M1-02, M1-03 | blocked | sonnet | agent | — |
| M1-G | **M1 gate** — use the library from a scratch project and approve its behavior | M1-01…M1-04 | blocked | sonnet | agent + user | — |

## M2 — Docs and release

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M2-01 | **API docs and examples** — documentation for every public name and runnable examples of the main uses | M1-G | blocked | sonnet | agent | — |
| M2-02 | **README and CHANGELOG** — what it is, install, a first example, and the changelog's first entry | M1-G | blocked | sonnet | agent | — |
| M2-03 | **API stability review** — read the public surface for names, types and errors you would regret after 1.0; list what to change before release | M2-01, M2-02 | blocked | opus | agent | — |
| M2-04 | **Publish workflow** — a workflow that tests, tags and publishes a release to the package registry from a tag | M2-03 | blocked | sonnet | agent | — |
| M2-G | **M2 gate** — dry-run the publish workflow and approve the release | M2-01…M2-04 | blocked | sonnet | agent + user | — |
| M2-05 | **Publish 0.1.0** — tag and publish the first release, and check that it installs | M2-G | blocked | — | user | — |
