# Compatibility

What igris 1.x promises not to break, how versions are marked, and how to upgrade. [SPEC §19](../SPEC.md#19-compatibility) is the normative text; this page says the same for people building on igris, and maps every promise to the tests that keep it.

## The promise

From 1.0 until 2.0, everything listed in the map below keeps working unchanged. 1.x may **add**: new optional plan columns, config keys with defaults, commands and flags, JSON fields, run-log event types, template variables. It never removes anything listed or changes what it means. Something deprecated in 1.x keeps working, with a warning, until 2.0.

Not promised: the TUI (layout, keys, colors), the web UI (since 1.6: its pages, paths and script; the HTTP API it uses is promised, C14), the default task prompt's wording, text output (use `--json`), the hidden `hook` and `__complete` commands, the contents of `.igris/agent-state/`, `hooks/`, `prompts/`, `config/`, `serve.log` and `igris.lock`, snapshot fields beyond the documented ones, the text of ntfy, Discord, Slack, Gotify and backend notifications, and igris's Go packages.

## Versions in files

| Where | Marker | What a reader does |
|---|---|---|
| A plan | `<!-- igris:format 1 -->`, optional; `<!-- igris:format 2 -->` for a plan that includes other files (since 1.2); `<!-- igris:format 3 -->` for a plan with parallel phases (since 1.5) | No marker is format 1, forever. A format igris doesn't know is refused: upgrade igris. igris 1.0 and 1.1 refuse format 2, so a split plan needs igris 1.2 wherever it runs; igris 1.2–1.4 refuse format 3, so a plan with parallel phases needs igris 1.5. ([plan format](plan-format.md)) |
| `--json` output | `"v": 1`, first key | Stays 1 in 1.x. Ignore fields you don't know. ([schemas](json/)) |
| `.igris/runs.jsonl` | `"v": 1` per line; none on lines from igris ≤ 0.4 | Ignore event types and fields you don't know. ([run log](runlog.md)) |
| `.igris/state.json` | `"version"`: 4 from 1.5 (the `active` list); 3 from 0.7; 1 and 2 before | igris reads and upgrades older versions; a newer one says to upgrade igris. igris 1.0–1.4 refuse version 4, so finish a run (or delete its state) before going back to one of them. |
| The generic webhook | `"v": 1` in the payload | Ignore fields you don't know. |
| The run's socket (since 1.4) | `"v": 1` on every message | Stays 1 in 1.x: any 1.x client attaches to any 1.x `igris serve`. Ignore fields and message types you don't know. |
| `.igris/serve-token` (since 1.6) | 43 characters of unpadded base64url and a newline | Written by `igris serve token`. A damaged file authenticates nobody; `igris serve token --rotate` replaces it. Not a state file: igris 1.0–1.5 ignore it. |

## Upgrading

From 1.2 on, `igris update` (or `u` on home) checks for a newer release and updates igris with Homebrew or `go install` after asking. From any 0.x release: install the new igris, then run `igris config migrate` once in each project. It removes settings a release removed (today only `claude.command`, which every `igris init` before 0.7 wrote), shows what it removes, asks first, and keeps a backup in `.igris/config/`. Plans need nothing. A run interrupted under the old version resumes: its state is upgraded on read and the config keeps its hash.

## Surface → test map

One row per frozen surface. `tools/contract` fails when a named test no longer exists or a surface of SPEC §19 has no row, so this map can't quietly lose a test. The compatibility suite (`testdata/compat/`, C13) checks what every released igris wrote against the current one.

| ID | Surface | SPEC | Tests |
|---|---|---|---|
| C1 | Plan format 1: phases, columns, statuses, dependencies, the format marker, the documented leniency; format 2: includes (since 1.2); format 3: the parallel marker and `Group` (since 1.5) | §3, §19 | `internal/plan.TestGoldenLargeSummary`, `internal/plan.TestGoldenInvalid`, `internal/plan.TestResolveDeps`, `internal/plan.TestFormatMarker`, `internal/plan.TestPlanFormatDocExamples`, `internal/plan.TestIncludeSplitPlan`, `internal/plan.TestIncludeProblems`, `internal/plan.TestParallelMarker`, `internal/plan.TestGroupColumn`, `internal/plan.TestGroupValidation`, `internal/plan.TestImpliedDeps`, `internal/plan.TestParallelInIncludedFile`, `cmd/igris.TestReportGoldens` |
| C2 | What igris writes in a plan: only Status cells, every other byte kept; in a split plan, each in the file its row is in (since 1.2) | §4 | `internal/plan.TestWriterPreservesBytes`, `internal/plan.TestWriterEveryCellIsolated`, `internal/plan.TestWriterKeepsBOM`, `internal/plan.TestWriterKeepsFormatMarker`, `internal/plan.TestSplitWriterPreservesBytes`, `internal/plan.TestSplitWriterEveryCellIsolated`, `internal/plan.TestWriterFormat3EveryCellIsolated` |
| C3 | `igris.toml`: keys, defaults, validation; the config hash; removed keys; since 1.8 `commit = "batch"`, `on_conflict = "resolve"`, `dep_notes`, `resolver_rank`, `hold_at`, `[usage.budget]`, `[usage.prices]` and the events `usage_limit` and `budget_exceeded` (igris 1.0–1.7 refuse them) | §12, §13 | `internal/config.TestParseFullSample`, `internal/config.TestParseEmptyGivesDefaults`, `internal/config.TestHashOfV04Config`, `internal/config.TestRemovedClaudeCommand`, `internal/config.TestMigrateInitConfig`, `internal/config.TestV18Keys`, `internal/config.TestHashWithoutV18KeysIsUnchanged` |
| C4 | CLI commands, flags and arguments (`igris update`, since 1.2; `igris draft`, `serve`, `attach` and `stop`, since 1.4; `--parallel` of `arise` and `serve`, since 1.5; `serve --http`, `--listen-public` and `serve token [--rotate]`, since 1.6) | §14 | `cmd/igris.TestServeFlagUsage`, `cmd/igris.TestServeToken`, `cmd/igris.TestParallelFlagUsage`, `cmd/igris.TestParallelFlagValue`, `cmd/igris.TestEveryCommandHelp`, `cmd/igris.TestCompletionGoldens`, `cmd/igris.TestCompletionCoversCommands`, `cmd/igris.TestUpdateCheck` |
| C5 | Exit codes | §14 | `cmd/igris.TestRun`, `cmd/igris.TestAriseExitCodes`, `cmd/igris.TestTUIExit`, `cmd/igris.TestConfigUsage`, `cmd/igris.TestUpdateExitCodes` |
| C6 | `--json` of check, phases, status, history, report, doctor | §14 | `cmd/igris.TestJSONGoldensMatchSchemas`, `cmd/igris.TestJSONRunGoldens`, `cmd/igris.TestReportGoldens` |
| C7 | `igris done`, `skip`, `reset` and the signal files | §6.2, §13 | `cmd/igris.TestDoneAndSkipWriteSignals`, `cmd/igris.TestResetDirect`, `internal/state.TestSignalSlots`, `internal/state.TestResetSignal` |
| C8 | `.igris/` layout and `state.json` versions | §13 | `internal/state.TestLayout`, `internal/state.TestLoadRunVersions`, `internal/state.TestMigratedRunSavesAsVersion4` |
| C9 | The run log, with the events `integrated`, `integration_failed` and `overlap` and the field `slot` (since 1.5) | §13 | `internal/serve.TestSlotOnTheWire`, `internal/state.TestRunLogV1`, `internal/state.TestPeekLog`, `cmd/igris.TestHistoryGoldens`, `cmd/igris.TestRunReportGoldens` |
| C10 | Template variables of `prompt_template`, `commit_message` and notification templates | §6.1, §6.5, §10 | `internal/prompt.TestPromptTemplateEveryVariable`, `internal/engine.TestCommitMessageEveryVariable`, `internal/notify.TestTemplateReplacesText` |
| C11 | The generic webhook: payload, `X-Igris-Event`, `X-Igris-Signature` | §10 | `internal/notify.TestWebhookSend` |
| C12 | The task-hook environment: `IGRIS_TASK_ID`, `IGRIS_PHASE`, `IGRIS_RANK`, `IGRIS_MODEL`, `IGRIS_RESULT`, and since 1.5 `IGRIS_SLOT` in a parallel phase | §6.7, §6.11 | `internal/engine.TestHooksRunAroundEachSession`, `internal/engine.TestParallelTaskWorktrees` |
| C13 | What every release wrote still loads: configs, plans, signals, state, run logs | §17, §19 | `cmd/igris.TestCompatConfigsAndPlans`, `cmd/igris.TestCompatSignals`, `cmd/igris.TestCompatState`, `cmd/igris.TestCompatRunLogs`, `cmd/igris.TestCompatEveryRelease` |
| C14 | Socket protocol 1 (since 1.4): `.igris/igris.sock` and `.igris/igris.sock.path`, the framing, `hello`, `snapshot` and its documented fields, events with the run log's v1 envelope, the commands, `ack`, `error`, `tail`; since 1.6 its HTTP mapping: `/pair`, `/api/v1/events`, `/api/v1/command`, `/api/v1/action/<token>` (ntfy's answer buttons, `[notify.ntfy] actions`), the `igris_token` cookie, bearer auth, `X-Igris-CSRF`, the status codes, `.igris/serve-token` and the `[serve]` keys | §12, §13, §14, §19 | `internal/serve.TestHTTPPairing`, `internal/serve.TestHTTPCommands`, `internal/serve.TestHTTPEventsSnapshotThenEvents`, `internal/serve.TestHTTPCSRFAndOrigin`, `internal/serve.TestActionAnswersOnce`, `internal/serve.TestActionRefusals`, `internal/notify.TestNtfyActions`, `internal/state.TestServeToken`, `internal/config.TestServe`, `internal/serve.TestEventEnvelope`, `internal/serve.TestHelloSnapshotThenEvents`, `internal/serve.TestCommandsMirrorEngine`, `internal/serve.TestMalformedEndsConnection`, `internal/serve.TestSnapshotTracksTheRun`, `internal/serve.TestRealSocket`, `internal/serve.TestListenFallback` |
