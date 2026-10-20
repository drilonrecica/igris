# Plan templates

Since 1.2 `igris init --template NAME` writes a starter plan, so a new project begins with phases, gates and ranks instead of an empty file.

```sh
mkdir my-tool && cd my-tool
git init
igris init --template go-cli
igris check
igris arise M0 --dry-run
```

It does what `igris init` does, then writes the template's plan at the plan path in `igris.toml` when there is no file there. It never overwrites a plan (`kept tasks.md (already exists)`), never changes an existing `igris.toml`, and can't be combined with `--example`. An unknown name is a usage error that lists the names.

## The templates

| Template | Phases | Pick it for |
|---|---|---|
| `go-cli` | M0 Foundation, M1 Core, M2 Release | a Go command-line tool: module, Makefile, `go test`, release builds |
| `web-app` | M0 Foundation, M1 Data and API, M2 UI, M3 Ship | a web app; the stack is decided in M0 |
| `library` | M0 Foundation, M1 Core, M2 Docs and release | a library in any language; the public API is decided in M0 |

They share one shape:

- Every phase ends in a gate (`agent + user`) that names what you approve.
- `M0-01` is a task for **you**: write `SPEC.md`, what the project is. Everything else waits for it, so a run starts only once you've said what to build.
- `M0-02` is a decision task (`opus`, `plan` mode) that writes `docs/decisions.md` for you to approve: layout and dependencies, the stack, or the public API.

The files are in [`examples/templates/`](https://github.com/drilonrecica/igris/tree/main/examples/templates) so you can read them first.

## Ranks are suggestions

Templates use `sonnet` for well-specified work and `opus` for decisions, core design and reviews. The plan format has no way to mark a rank as a guess, so the template says so in a paragraph under its title and `init` repeats it: review the Model column before the first run, then run `igris check`. Also set `[run] verify` in `igris.toml` to the project's checks (`go test ./...` for `go-cli`).

## From the home screen

With a valid `igris.toml` and no plan, `E` (**Plan from template…**) offers the example plan and the three templates, each with a one-line description, then asks before writing.

A template's wording may change in a 1.x release: it is text for people, not a frozen format.
