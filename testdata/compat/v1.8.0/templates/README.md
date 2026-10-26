# Plan templates

Starter plans for `igris init --template NAME`. Each writes `tasks.md` (or the plan path in `igris.toml`) when there is no plan yet, and never overwrites one; it never touches `igris.toml`. The files here are the ones igris embeds, so you can read them before you pick.

Every template has the same shape:

- phases `M0`, `M1`, … with `M0` Foundation first and the release or ship phase last; every phase ends in a gate for you (`agent + user`);
- `M0-01` is a task for you: write `SPEC.md`, what the project is. Every other task waits for it, so a run starts only once you have said what to build;
- `M0-02` is a decision task (opus, plan mode) that writes `docs/decisions.md` for you to approve;
- **ranks are suggestions**: `sonnet` for well-specified work and `opus` for decisions, the core design and reviews. Change the Model column before the first run, then run `igris check`. Set `[run] verify` in `igris.toml` to the project's checks.

| Template | Phases | Pick it for |
|---|---|---|
| [`go-cli`](go-cli/tasks.md) | M0 Foundation, M1 Core, M2 Release | a Go command-line tool: module, Makefile, `go test`, release builds |
| [`library`](library/tasks.md) | M0 Foundation, M1 Core, M2 Docs and release | a reusable library in any language; the public API is decided in M0 |
| [`web-app`](web-app/tasks.md) | M0 Foundation, M1 Data and API, M2 UI, M3 Ship | a web app; the stack (framework, database, hosting, auth) is decided in M0 |

Try one:

```sh
mkdir my-tool && cd my-tool
igris init --template go-cli
igris check
igris arise M0 --dry-run
```

The wording of a template may change in a 1.x release: it is text for people, not a frozen format.
