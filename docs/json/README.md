# JSON output schemas

The `--json` output of igris is part of its 1.x contract (SPEC §19). Each output is one JSON object whose first key is `"v": 1`, described by a [JSON Schema](https://json-schema.org/) (draft 2020-12) here:

| Schema | Output of |
|---|---|
| [`check.schema.json`](check.schema.json) | `igris check --json` |
| [`phases.schema.json`](phases.schema.json) | `igris phases --json` |
| [`status.schema.json`](status.schema.json) | `igris status --json` |
| [`invalid-plan.schema.json`](invalid-plan.schema.json) | `igris phases --json` and `igris status --json` on a plan that doesn't validate (exit 1) |
| [`history.schema.json`](history.schema.json) | `igris history --json` |
| [`history-task.schema.json`](history-task.schema.json) | `igris history TASK-ID --json` |
| [`report.schema.json`](report.schema.json) | `igris report --json` |
| [`usage.schema.json`](usage.schema.json) | `igris usage --json` (since 1.1) |
| [`doctor.schema.json`](doctor.schema.json) | `igris doctor --json` |

**What stays the same.** In 1.x, fields may be added, but none is removed or changes meaning, and `v` stays 1; it changes only with a breaking change, in igris 2.0. Ignore fields you don't know. A field marked optional (not in `required`) is left out when it has no value; igris never writes `null` or a placeholder like `"—"`.

igris's tests check every JSON golden output against these schemas, and fail on a field a schema doesn't list.
