# The igris design language

How igris looks and speaks on its three surfaces: the terminal UI, the web UI on your phone, and the command line's text output. The site and the notifications follow the same words. It is the owner's decision of 2026-10-23 ([decisions.md V17-P1](decisions.md#v17-p1--the-igris-design-language-v17)); the normative text for each surface stays in [`SPEC.md`](../SPEC.md) §14 and §15, and this page is what the SPEC's rules add up to and why. If they disagree, the SPEC wins and this page is fixed.

The brand itself (the helm, the wordmark, the palette) is in [`docs/brand/`](brand/README.md). This page is about using it.

## Why

Before 1.7, igris had three visual systems that had drifted apart. Red painted every warning in the terminal, so the one red that mattered, *you are needed*, no longer stood out. The run screen and home had their own header, rule and bar code. The terminal carried almost none of the brand. One glyph meant three things (`⨯` was a blocked task, a failed check and a phase that was merely waiting). Keys meant different things on different screens. The phone's sheet hid Stop. The command line printed each command its own way.

Seven rules fix that. Everything below follows from them.

## The seven rules

1. **One vocabulary on three surfaces, from one source.** Glyphs, status words, severities, colors, rank colors, plurals, time and token formats live in one Go package, `internal/look`, which the TUI and the CLI use and the web UI's CSS tokens mirror, with a test that keeps them equal. A word is spelled one way everywhere.
2. **Red means you.** Plume red is drawn for exactly two things: **Needs you**, and the `[SKIP PERMISSIONS]` badge. Every warning is **Ember**. Nothing else is red, on any surface. The brand mark is not a status: the wordmark's red `i` and the helm's plume (with its pink stroke) are the only other red, and the test that pins the rule names them as the exceptions.
3. **Status first.** Every screen leads with one of seven status words, and when igris needs you that is the loudest thing on the screen: the header word, a banner, the card, the sheet. Nothing competes with it.
4. **One glyph table.** Each glyph has one meaning and keeps it on every surface. Color never carries meaning alone: every state is a glyph or a word, and color and weight only add emphasis.
5. **Brand at the moments that matter.** The wordmark with the red `i` in every header, **Arise.** when a run starts, the helm on GET STARTED and on the end-of-run summary. During a run igris is quiet: no decoration, nothing moving but what is working.
6. **Calm density.** Open screens with rules between sections, not boxes around everything; only task cards and dialogs are boxed. Panes are as large as their content needs. A dialog floats over the dimmed screen, so what it asks about stays visible behind it.
7. **One way to do things.** One key map for every screen, every binding in the help, the key shown on its button. Fewer choices up front: the common modes first, the rest under *More modes…*. Cancel is always in the same place.

## Vocabulary

### Glyphs

| Glyph | Meaning | Where |
|---|---|---|
| `✓` | done, ok, passed | a finished task, a passing check, a verify pass, a step completed |
| `●` | running | a task with a live session, the current phase |
| `!` | needs you | a task waiting on the owner; the banner and the header word |
| `·` | ready | a task that could run next; a step not reached yet |
| `⨯` | blocked | a task whose dependencies aren't met; nothing else |
| `–` | skipped | a skipped task; an unavailable value (`—` in tables stays the empty cell) |
| `▲` | warning | a doctor or check warning, drift, overdue, rank mismatch, interrupted, a run elsewhere that igris can't attach to, reconnecting, a stuck phase |
| `✗` | failed | a doctor failure, a verify failure, a failed delivery |
| `◌` | waiting | a phase that waits on other phases; a check still running; a session waiting on background work; a notification being sent |

Glyphs are always followed by a word where there is room (`▲ doctor: 1 warning`, `✗ FAILED: timeout`), and the word is enough on its own where there isn't. The focus marks `›…‹` and `›`, the progress bar `█░` and the ETA's `≈` are not status glyphs and keep their jobs.

Gone in 1.7: `⚠` and `⚠ rank` (now `▲`, `▲ rank`), `!` as `doctor`'s and the web UI's warning mark (now `▲`; `!` is needs you only), `⨯` for a waiting phase (now `◌`), for a failed check (now `✗`) and for a backend that isn't reachable (now `✗`), `…` as a glyph for a check in flight (now `◌`).

### Status words

Seven words say what igris is doing. They are the header's right-hand word in the TUI, the big word on the phone, the `Run` line of `igris status` and the first word of a notification's title.

| Word | When | Severity |
|---|---|---|
| **Working** | a run is going and nothing waits on you; also while a task waits on background work, and while a pause is armed but a task still runs (`Working · pauses after M1-03`) | accent |
| **Needs you** | anything waits on you: a question, a user task at its turn, a session blocked or lost, a reset request | plume |
| **Paused** | the pause has taken effect: no task runs, igris waits for Resume | plain |
| **Stopping** | Stop was confirmed; igris winds down (sessions stay open) | plain |
| **Done** | the run has ended. The outcome follows the word with its glyph: `Done ✓ completed`, `Done – stopped`, `Done ▲ stuck`, `Done ✗ failed`, and `Done ▲ interrupted` when the igris serving the run died without ending it | by outcome |
| **Idle** | no run in this project (home) | dim |
| **Interrupted** | the last run ended without ending itself (no `run_stopped` in the log; an `igris stop` is Idle); Resume picks it up (home) | ember |

A task's state line uses the task's glyph and a short phrase, never a second vocabulary: `● Working · 4m12s`, `! Needs you · idle 45s · o opens the session`, `! Needs you · verify failed 3 times` (a reason, not a clock, when the task isn't idle), `◌ Waiting on background work · 4m`, `▲ Overdue · 52m of 45m` (on the Needs-you line instead while the task needs you: one state, never two), `✓ done · 14m02s`. Words are sentence case; `NEEDS YOU`, `INTERRUPTED` and the other shouting caps are gone, as are `state:` prefixes.

The rest of the run's states map onto the seven words: a hold (the plan or `igris.toml` changed while the run was going) is `Paused · tasks.md changed`; the verify limit asks you, so it is Needs you; a phase stuck before the run ends is `Done ▲ stuck`. **Stopping** is shown by the client that confirmed Stop (the protocol has no stop-requested event, and the protocol doesn't change); every other client shows Working until Done. On home, STOPPED (a run that stopped itself, mid-task or between tasks: its log ends with `run_stopped`), CONFIG INVALID, PLAN INVALID and the lock cards show **Idle** with the card saying why (the lock cards lead with `▲`); a run served elsewhere on this host shows **Working** or **Needs you** with Attach; a run igris can't attach to (another user or host) is `▲ running elsewhere`. `igris status` asks the live run for its snapshot over the socket (read-only, the existing protocol) when one answers; otherwise it shows Idle, Interrupted or Working from `state.json` and the run log. Interrupted is kept for a run that did not stop itself (no `run_stopped`: a crash, a kill, a lost terminal); an `igris stop` mid-task is Idle (`Idle · stopped · M2-02 Lock file`).

### Severity and color

| Severity | Dark | Light | Used for |
|---|---|---|---|
| **Plume** (needs you) | `#E3172B`, words `#FF5C74` | `#E3172B`, words `#B3122E` | `!`, the Needs-you banner's edge, the sheet's edge, the phase bar's segment, the `[SKIP PERMISSIONS]` badge; the words Needs you in the text shade. Nothing else |
| **Ember** (warning) | `#FFB000` | `#9A6700` | `▲` and `✗` and their lines: warnings, failures, Interrupted, Overdue, Rank mismatch, reconnecting…, the quota from 80% |
| **Glow** (accent) | `#3CC8FF` | `#0A86C8`, words `#0A6FA8` | `●`, `✓`, the working word, the focused region, the primary button, **Arise.**, the helm's outline and visor; words on the light ground in the text shade |
| **Text** | `#EAF4FF` | `#05070F` | everything that is read |
| **Dim** | faint | faint | done, skipped and blocked rows, secondary text, inactive buttons; on the web `#9DB0D3` / `#4A5B7A` |
| **Frame** | `#2C5AA0` | `#8FA9CC` | rules, column lines, card borders |

The helm adds shades of its own, art and never text, named in `internal/look` and mirrored by the web UI's tokens (dark / light): face `#0A1124` / `#0E1730` and its shadow half `#05070F`, ridges `#236C92` / `#0C4F7C`, visor core `#B8F0FF` / `#8FE3FF`, chin bars `#32A3D3` / `#0B70AA`, the plume's stroke `#F05A6E`. Its outline and visor are Glow and its plume is Plume. The S tile is the app icon and keeps its dark shades on a light terminal.

Ember is the functional brand color for warnings on every surface: amber on dark, dark gold on light. It replaces the red that warnings wore in the TUI and the amber that only the quota wore. A failure (`✗`) is the strongest warning and uses Ember in bold, not red: it is a condition to fix, not igris waiting for your answer. The quota is Ember from 80% and bold Ember from 95%: running low is a warning, not igris waiting on you.

A mark shade and a text shade. Plume and the light Glow are marks: glyphs, edges, segments, borders. Words in those colors use the text shade of the same name, which is what makes them AA against the ground (plume itself measures 4.2 on both grounds; the light Glow 3.6 on snow). Ember dark measures 11 on abyss; Ember light measures 4.4 on snow and 4.9 on white cards, so Ember words on the light ground are drawn bold. The web UI's `--needs-text` and `--accent-text` are these text shades, and the test that compares its tokens with `internal/look` covers them too.

Terminal colors are drawn only on a terminal, with `NO_COLOR` unset and `TERM` not `dumb`; bold, faint and reverse video are not colors and stay. The focused element is reverse video plus `›…‹`, so focus is visible under `NO_COLOR`.

### Rank colors

One scheme for the TUI, the web UI and the site. A rank's name is always written next to its color.

| Rank | Name | Dark | Light |
|---|---|---|---|
| `haiku` | Steel | `#9FB3C8` | `#52657A` |
| `sonnet` | Sky | `#6EA8FF` | `#1D5FD1` |
| `opus` | Violet | `#B48CFF` | `#6D3FD6` |
| `fable` | Orchid | `#FF8AD8` | `#B0237A` |

The ladder runs from grey through blue and violet to orchid: cooler and dimmer for the small ranks, warmer and brighter for the big ones, and never cyan (the accent), amber (Ember) or red (plume). Fable was gold before 1.7; gold is now Ember and means a warning, so fable moves (the upgrade note says so). `[tui.rank_colors]` still overrides any of them, and a rank without a color is drawn plain.

### Type

In the terminal, bold is the display face: the wordmark, the status word, titles, the focused button. On the web and the site, Chakra Petch 700 is the display face (the wordmark, the status word, phase IDs, buttons), Chakra Petch 600 the section labels, the phone's own sans the body, a monospace stack for IDs and the tail. No other face, no icon font, no emoji.

### Time and numbers

| What | Format | Example |
|---|---|---|
| a clock time today, in the TUI, the web UI and `status` | `HH:MM`, local | `09:41` |
| a clock time in a headless log line (`serve`, `--no-tui`) | `HH:MM:SS`, local | `09:41:07` |
| a date | `Mon DD HH:MM`, local; the year is added when it isn't this year | `Oct 06 21:10`, `Oct 06 2025 21:10` |
| a duration | the two largest units, no spaces; a known duration under a second is `1s` | `45s`, `4m12s`, `1h47m`, `2d3h` |
| an estimate | `≈` and minutes | `≈ 14m left`, `over ≈ 18m typical` |
| an age | one unit and `ago` | `25m ago`, `3h ago`, `2d ago` |
| tokens | as `igris usage` prints them: a decimal only under 10 units, then `k`, `M`, `G` | `812`, `41k`, `2.3M`, `14M`; `2.3M in · 41k out`; narrow: `2.3M/41k` |
| the `report` markdown's header | local, like the rest of the text | `Oct 08 09:15 → 11:02 (1h47m) · completed` |
| progress | `done/total`, the percentage rounded down, a bar of `█` and `░` | `7/12 · 58% ██████░░░░` |
| a count | the number and the word, pluralised | `1 task needs you`, `2 tasks need you`, `3 warnings` |

`--json`, the run log and the socket protocol keep RFC 3339 UTC: they are frozen ([SPEC §19](../SPEC.md#19-compatibility)). Every text surface shows local time.

### Words

- Sentence case for everything but the section labels (`TASKS`, `LOG`, `HEALTH`), which are small caps in spirit: uppercase, letter-spaced on the web.
- `mode plan`, `rank sonnet → model claude-sonnet-5-5`, `phase M1`: the noun, then the value, no colon.
- One verb per action and it is the button's label everywhere: Arise, Resume, Attach, Answer…, Open session, Details, Mode, Task mode, Pause / Resume, Done…, Skip…, Retry…, Stop…, Side…, Home. An ellipsis means a dialog follows. When the bar runs out of room it shortens labels to their verb (`Open`, `Side…`) before it folds actions into More….
- `next:` after every success on the command line, and on every empty result, names the command to run next.
- Commands are quoted in backticks in prose; a `next:` line is bare so it can be copied. The plan's words (titles, notes) are cleaned before they are drawn.
- Numbers never appear without their noun; a glyph never appears without its word where there is room.

## Layout

**Open screens.** The run screen, home, every page and the narrow layout are open: a header line, a rule, sections divided by labelled rules (`── TASKS ──`), a rule, the action bar. No outer frame. Two columns are divided by one vertical line. Only two things are boxed: a task card (square corners, a heavy border on the selected one) and a dialog (rounded corners, the only rounded thing on the screen, so it reads as floating).

**The header** is the same on every screen: the wordmark, `·`, the project, then what the screen is about (the phase and its progress on the run screen; the plan file, the backend and the default mode on home), and the status word at the right. When the width runs out it drops, in this order: side sessions, the quota, the progress bar, the percentage, the backend, the mode; the wordmark, the project, the phase and the status word never drop.

**Panes are sized by content.** TASKS is as wide as its longest row and no wider; the card gets the rest. LOG takes what is left under the cards, never the whole screen. On home the right column ends where RECENT ends.

**Needs you is on top.** A banner under the header names the first task in plan order that needs you and the kind of question, with Answer…; with more it adds `· 2 tasks need you`, and the header word is `! Needs you` whatever the count. The card that needs you is selected when Needs you starts, unless the selected card itself needs you; the selection never jumps away from a card that needs you. In the narrow layout the switcher shows the task that needs you first, whatever the plan order, and the card leads with the status word.

**The narrow layout** keeps the task titles, in two columns when they fit; the bar wraps or folds into `More…`; a dialog takes the whole screen. Usable at 50×20.

**Dialogs float.** A dialog is drawn over the dimmed screen, centred, as wide as its text needs up to 100 columns. Its options are a numbered list: the first is the SPEC's default and is focused, a destructive choice is last and never the default. Cancel, where a dialog has one, is always its last option and `esc` picks it; under a text field the button row ends with Cancel too. A question that must be answered (commit, session lost, hook failed, integration, reset, plan approval) has no Cancel: `esc` closes the dialog and the question stays pending, as its hint `esc keep asking` says. The dialog's last line is its key hint.

**The action bar** is the key-hint footer of every screen: each button carries its key (`[o Open session]`), the bar shows only the actions that apply now, and `[?]` and `[q Home]` (or `[q Quit]` on home) are always last.

## Brand moments

- **The wordmark** opens every header: `igris` in bold with the second `i` in plume. On the web and the site it is Chakra Petch 700 and the visor icon goes before it.
- **Arise.** When a run starts, the first line of the log is `Arise. phase M1 · 7 tasks · mode plan`, in bold accent; `igris serve` and `--no-tui` print the same line. It is the only line in the log that isn't an event.
- **The helm** is hand-drawn pixel art ([`docs/brand/helm-pixels.txt`](brand/helm-pixels.txt)), two pixels to a cell with `▀`, the top pixel in the foreground and the bottom one in the background. **L** is the mark, 24 × 13 cells: the glow outline, the navy face split by a ridge, the side ridges, the V-visor with its bright core, three chin bars, the plume with its pink stroke. **S-tile** is the app icon, 19 × 6 cells: the visor and the plume's arc on a dark rounded tile. L stands where the screen is at least 80 columns by 32 rows and it fits, S-tile from 40 columns and on shorter screens, nothing below 40. Without color it is the S shape in plain blocks, without the tile; 256-color terminals take the nearest colors. It stands on two screens: GET STARTED (next to the tagline, *Arise. One task, one fresh session, the right rank.*) and the end-of-run summary. It is never shown during a run.
- **The summary** replaces `the run ended — press q to go home`: the helm, `Done` and the outcome, the phase, the counts (done, skipped, failed), wall time, needs-you time, tokens, commits, verify, the skip reasons, and `next:` with the commands that follow. Home is focused.
- **Quiet during a run.** The only things that move are the running glyph and the live tail; on the phone, the live dot and the active segment. No spinner in the header, no banner that isn't Needs you, no color on what is fine.

## One key map

The same key means the same thing on every screen. A capital letter is the rarer sibling of its lowercase, and the two are listed side by side in the help. Keys appear on their buttons; the help (`?`) lists every binding, these included.

| Key | Meaning | Run screen | Home and pages |
|---|---|---|---|
| `a` `A` | **a**rise · **A**dapt | — | `a` Arise… / Resume… / Attach (the one way into the run, by state); `A` Adapt |
| `d` `D` | **d**one · **D**raft | `d` Done… | `D` Draft… |
| `s` `S` | **s**kip · **S**ide sessions | `s` Skip…, `S` Side… | `S` Side… |
| `m` `M` | **m**ode · task **M**ode | `m` Mode, `M` Task mode | — |
| `i` `I` | doctor (**i**nspect) · **I**nit | — | `i` Doctor, `I` Init |
| `e` `E` | **e**dit · plan from t**E**mplate | — | `e` Edit the file this screen is about, `E` Plan from template… |
| `o` | **o**pen session | the selected task's pane | the running task's pane (a run elsewhere) |
| `t` | de**t**ails | the selected task | the selected phase or task |
| `p` | **p**ause / resume | toggle pause after the current task | — |
| `r` | **r**etry | Retry… (continue or fresh) | — |
| `x` | stop, close, reject (**x**) | Stop… the run | Close… a side session (the Side sessions page), Reject… a proposal (the adapt and draft review) |
| `v` `c` `h` | pre**v**iew · **c**heck · **h**istory | `h` History on the end-of-run summary | the pages |
| `n` `u` `,` | **n**otify test · **u**pdates · settings | — | the pages |
| `y` | cop**y** | the resume command, the log line, the report command on the summary | the ID, the fix command, the path, the proposal path on the review |
| `[` `]` | previous / next card | with several active tasks | — |
| `↑` `↓` `j` `k` | move in a list | | |
| `tab` `shift+tab` | next / previous region | | |
| `enter` `space` | activate the focused element; on the adapt and draft review, Accept is the focused button (also `A` there) | | |
| `1`…`9` | pick a dialog option | | |
| `esc` | close the dialog with its safe choice, else back | | |
| `q` | leave this screen: Home (asks Detach · Stop run · Cancel while the run is live) | Quit on home, back on a page | |
| `ctrl+c` | quit the app (a run keeps going in serve) | | |
| `?` | help: every binding | | |

What changed in 1.7: `t` no longer means Attach on home (that is `a`, with Arise and Resume, the one way into the run); `q` always leaves the screen you are on; the adapt and draft review no longer binds `a` Accept, `r` Reject and `h`/`l` (Accept is `enter` on the focused button or `A`, Reject is `x`, focus moves with `←`/`→` and `tab`); fable's color changes. The upgrade note lists them all, and V17-01 amends the SPEC's and the help's `ctrl+c` text, which still says it stops the run (since 1.4 it detaches and the run goes on in serve).

## Fewer choices up front

**The mode picker** (the wizard's *Mode* step, `m` and `M`) shows plan, auto, accept, manual and default; the last option is *More modes…*, which adds plan:default, plan:accept, plan:auto, yolo `[SKIP PERMISSIONS]` and plan:yolo `[SKIP PERMISSIONS]`. The wizard's list has **As planned** (the Mode column, then `default_mode`) before them; `m` and `M` during a run don't, since a session has no As planned. No mode is removed, and yolo and plan:yolo still open the typed-phrase field. The start wizard is shorter: the *Through* step is skipped when there is no later phase.

**Cancel in the same place.** Cancel is the last option of every dialog that has one, and `esc` picks it; the default is focused wherever it stands.

## The surfaces

- **The TUI** draws everything with `internal/look` and one chrome: one header, one rule, one bar for the run screen, home and every page. Goldens at 120×40, 80×24 and 50×20 cover the layout; colored goldens cover the looks.
- **The web UI** mirrors the palette, the rank colors and the status words as CSS tokens in `app.css`, with a test that they equal `internal/look`. Its Needs-you sheet collapses to a peek strip so Plan, Pause and Stop stay reachable; a card whose question is in the sheet carries a red marker that opens the sheet instead of duplicate buttons; several questions are a stepper in the sheet. Warnings are Ember with `▲` (the `! ` marker goes), Needs you is red, nothing else is red. Light theme text reaches AA through the text shades. One radius scale: 8 px fields, 12 px buttons, 14 px cards, 22 px for the sheet's top corners. Its words stay (`Pause after this task`, `Leave uncommitted`, `Retry fresh`).
- **The command line** prints every table in one style (headers, no trailing spaces, long cells truncated to the width), uses the glyphs in `status`, `history` and `--dry-run` (a rank mismatch in `report` and `usage` is `▲ rank` in Ember), groups its help by what you are doing (Start, Inspect, Run, Signal, Plan with AI, Maintain), wraps a flag error as `igris CMD: …; see igris CMD -h`, answers an unknown command with *did you mean*, and ends every success and every empty result with `next:`. `--json` is untouched.
- **Notifications** lead with the status word and use the glyphs and wording of `internal/look`; high priority and red are for Needs you only. The payload formats and the template fields are frozen and don't change.
- **The site** uses the same tokens: one rank scheme, one radius scale, Ember, the same focus style.

## Mockups

The screens as approved. The layout code is free in the details (spacing, where a line breaks at a given width); the words, the glyphs, the order and what is boxed are the design.

### The run screen, wide (100 columns)

```
igris · sinjal      phase M1 · 3/8 · 37% ███░░░░░░░ · 2 active · mode plan · herdr       ! Needs you
────────────────────────────────────────────────────────────────────────────────────────────────────
TASKS                             │ ! M1-04 needs you · idle without a signal              [Answer…]
 ✓ M1-01  Go module       sonnet  │ ┌─ M1-03  Parser ──────────────────────────────────────────────┐
 ✓ M1-02  Entrypoint      sonnet  │ │ ● Working · 4m12s · ≈ 14m left                               │
 ● M1-03  Parser          sonnet  │ │ sonnet → claude-sonnet-5-5 · mode plan                       │
 ! M1-04  Writer          sonnet  │ │ tokens 2.3M in · 41k out                                     │
 · M1-05  Docs            opus    │ │ Read internal/plan/parse.go                                  │
 · M1-06  Buy the domain  user    │ │ Edit internal/plan/parse_test.go                             │
 ⨯ M1-07  Release         sonnet  │ │ [o Open session] [t Details]                                 │
   waits on M1-03, M1-05          │ └──────────────────────────────────────────────────────────────┘
 – M1-08  Old idea        haiku   │ ┏━ › M1-04  Writer ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
                                  │ ┃ ! Needs you · idle 45s · o opens the session                 ┃
                                  │ ┃ sonnet → claude-sonnet-5-5 · mode plan · 6m02s               ┃
                                  │ ┃ the session is idle without a signal                         ┃
                                  │ ┃ [Answer…] [o Open session] [t Details]                       ┃
                                  │ ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛
── LOG ─────────────────────────────────────────────────────────────────────────────────────────────
09:41  Arise. phase M1 · 8 tasks · mode plan
09:41  M1-03 started · sonnet · mode plan
09:41  M1-04 started · sonnet · mode plan
09:44  ! M1-04 needs you · the session is idle without a signal
────────────────────────────────────────────────────────────────────────────────────────────────────
[›Answer…‹] [o Open] [m Mode] [p Pause] [d Done] [r Retry] [s Skip] [x Stop] [S Side…] [?] [q Home]
```

Plume: the header word, the banner, `!` and the Needs-you line of the card. Glow: `●`, `✓`, `Working`, the focused button, **Arise.**. The header word is `● Working` when nothing needs you.

### The run screen, narrow (50×20)

```
igris · sinjal · M1 · 3/8 ███░░░       ! Needs you
‹ ! M1-04 · 1 of 2 › ● M1-03
┏━ › M1-04  Writer ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
┃ ! Needs you · idle 45s · o opens the session   ┃
┃ sonnet → sonnet · mode plan · 6m02s · 2.3M/41k ┃
┃ the session is idle without a signal           ┃
┃ [Answer…] [o Open session] [t Details]         ┃
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛
── TASKS ─────────────────────────────────────────
 ✓ M1-01 Go module        ● M1-03 Parser
 ✓ M1-02 Entrypoint       ! M1-04 Writer
 · M1-05 Docs             · M1-06 Buy the domain
 ⨯ M1-07 Release          – M1-08 Old idea
── LOG ───────────────────────────────────────────
09:41  M1-04 started · sonnet · mode plan
09:44  ! M1-04 needs you · the session is idle wi…
[›Answer…‹] [o Open] [m Mode] [p Pause] [More…]
[?] [q Home]
```

The switcher line puts the task that needs you first. Titles stay; the rank and the status word lead the card.

### Home (100 columns)

```
igris · sinjal · tasks.md · herdr ✓ · mode auto                                                 Idle
────────────────────────────────────────────────────────────────────────────────────────────────────
PHASES                                              │ READY · phase M2  Config & state
 ✓ M0  Foundations        ██████████  3/3  done     │ next  · M2-02  Lock file                  opus
 ✓ M1  Plan parser        ██████████  2/2  done     │ then  · M2-03  State file               sonnet
›· M2  Config & state     ██░░░░░░░░  1/4  next     │ 3 left · 2 wait on M2-02
 ◌ M3  Engine             ░░░░░░░░░░  0/3  wait     │ ── HEALTH ────────────────────────────────────
 ◌ M4  TUI                ░░░░░░░░░░  0/2  wait     │ ✓ plan valid · 5 phases · 14 tasks
                                                    │ ▲ doctor: 1 warning · uncommitted changes
                                                    │ ── RECENT ────────────────────────────────────
                                                    │ Oct 06 21:10  M2  3 done · 1 skipped · stopped
                                                    │ Oct 05 18:02  M1  6 done · completed
────────────────────────────────────────────────────────────────────────────────────────────────────
[›a Arise…‹] [v Preview] [c Check] [D Draft…] [i Doctor] [e Edit plan] [, Settings] [?] [q Quit]
```

With a run served elsewhere on this host the header word is `● Working` or `! Needs you`, the card is RUNNING with the task and the last events, and `a` reads **Attach**. Phases that wait are `◌`, not `⨯`.

### GET STARTED (home without `igris.toml`)

```
igris · newproj · herdr ✓                                                                       Idle
────────────────────────────────────────────────────────────────────────────────────────────────────
               ▄▄▄          GET STARTED
            ▄██▀▀▀▀         Arise. One task, one fresh session, the right rank.
     ▄▄▄▄       ▄▄▄▄
     ▀██████▄██████▀        ✗ 1  igris.toml   not found · I Init writes it
         ▀▀███▀▀            · 2  plan         tasks.md not found · D Draft… or E Plan from template…
            ▀               · 3  check        —
                            · 4  preview      —
                            · 5  arise        —
── HEALTH ──────────────────────────────────────────────────────────────────────────────────────────
✗ doctor: 1 failure · igris.toml not found
────────────────────────────────────────────────────────────────────────────────────────────────────
[›I Init‹] [D Draft…] [i Doctor] [, Settings] [u Updates] [?] [q Quit]
```

The helm is drawn here as it is without color, the plain S shape; in color a screen of at least 80 × 32 shows the L mark and a smaller one the S tile ([`docs/brand/helm-pixels.txt`](brand/helm-pixels.txt)). Where the card beside the helm would be narrower than 40 columns the helm stands above it; below 40 columns it is dropped and the tagline stays.

### A floating dialog (100×24, over the dimmed run screen)

```
igris · sinjal            phase M1 · 3/8 · 37% ███░░░░░░░ · mode plan · herdr            ! Needs you
────────────────────────────────────────────────────────────────────────────────────────────────────
TASKS                             │ ┏━ › M1-03  Parser ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
 ✓ M1-01  Go module       sonnet  │ ┃ ! Needs you · commit?                                        ┃
 ✓ M1-02  Entrypoint      sonnet  │ ┃ sonnet → claude-sonnet-5-5 · mode plan · 14m02s              ┃
 ! M1-03  Parser          sonnet  │ ┃ 4 files · +212 −8                                            ┃
 · M1-05  Docs          ╭──────────────────────────────────────────────────╮                       ┃
 · M1-06  Buy the domain│ Commit M1-03?                                    │━━━━━━━━━━━━━━━━━━━━━━━┛
 ⨯ M1-07  Release       │                                                  │
   waits on M1-03, M1-05│ Commit the changes of M1-03 as "M1-03: Parser"?  │
 – M1-08  Old idea      │ 4 files · +212 −8                                │
── LOG ─────────────────│                                                  │────────────────────────
09:41  Arise. phase M1 ·│ › 1. Commit                                      │
09:55  ✓ M1-03 verify pa│   2. Leave uncommitted                           │
09:55  ! M1-03 needs you│                                                  │
                        │ enter choose · 1–2 pick · esc keep asking        │
                        ╰──────────────────────────────────────────────────╯





────────────────────────────────────────────────────────────────────────────────────────────────────
[Answer…] [o Open] [m Mode] [p Pause] [d Done] [r Retry] [s Skip] [x Stop] [S Side…] [?] [q Home]
```

Everything outside the dialog is dimmed and stays readable; the bar is inactive. The safe choice is first and focused; the dialog's last line is its key hint.

### The end-of-run summary (100 columns)

```
igris · sinjal                     phase M1 · 8/8 · 100% ██████████ · herdr                     Done
────────────────────────────────────────────────────────────────────────────────────────────────────

                                                    ▄▄▄
                                                 ▄██▀▀▀▀
                                          ▄▄▄▄       ▄▄▄▄
                                          ▀██████▄██████▀
                                              ▀▀███▀▀
                                                 ▀

                                          Done ✓ completed
                                  phase M1 · Plan parser & writer

                        7 done · 1 skipped                  wall 1h47m
                        tokens 14M in · 310k out            needs you 12m30s
                        commits 7 · last 3fa29c1            verify 8 ✓ · 1 ✗ then ✓

                        – M1-06 skipped: domain bought by hand

                        next: igris arise M2  ·  igris report

────────────────────────────────────────────────────────────────────────────────────────────────────
[›q Home‹] [h History] [y Copy report command] [?]
```

`Done – stopped` and `Done ▲ stuck` (with `2 tasks wait on M1-05, which is blocked`) and `Done ✗ failed` (with the error) take the same screen. `failed` counts tasks whose last attempt hit the verify limit or an error and is shown only when it isn't zero. Short of rows, the helm shrinks from L to S and is then dropped, before the blank lines; narrow, the two columns stack.

### The web sheet, collapsed and open (390 px)

```
┌──────────────────────────────────────┐   ┌──────────────────────────────────────┐
│ ◈ igris · sinjal                   ● │   │ ◈ igris · sinjal                   ● │
│                                      │   │░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░│
│ Needs you                            │   │░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░│
│ 1 question · 4m                      │   ├──────────────────────────────────────┤
│                                      │   │                 ────                 │
│ ████▓▓▓░░░░░░░░                      │   │ M1-03 · commit?      1 of 2 ›        │
│ phase M1 · 3 of 8 · 47m              │   │                                      │
│                                      │   │ Commit the changes of M1-03          │
│ ┌──────────────────────────────────┐ │   │ as "M1-03: Parser"?                  │
│ │ M1-03  Parser                  ! │ │   │                                      │
│ │ sonnet → sonnet · 14m02s         │ │   │ ┌─ 4 files · +212 −8 ──────────────┐ │
│ │ ▸ answer in the sheet            │ │   │ │ internal/plan/parse.go           │ │
│ └──────────────────────────────────┘ │   │ │ internal/plan/parse_test…        │ │
│ ┌──────────────────────────────────┐ │   │ └──────────────────────────────────┘ │
│ │ M1-04  Writer                  ● │ │   │                                      │
│ │ sonnet → sonnet · 6m02s          │ │   │ ┌──────────────────────────────────┐ │
│ │ [Done] [Skip] [Retry]            │ │   │ │              Commit              │ │
│ └──────────────────────────────────┘ │   │ └──────────────────────────────────┘ │
│                                      │   │ ┌──────────────────────────────────┐ │
├──────────────────────────────────────┤   │ │        Leave uncommitted         │ │
│ ! M1-03 · commit?                  ▴ │   │ └──────────────────────────────────┘ │
├──────────────────────────────────────┤   │              ▾ collapse              │
│ Plan │ Pause after this task │ Stop  │   ├──────────────────────────────────────┤
└──────────────────────────────────────┘   │ Plan │ Pause after this task │ Stop  │
                                           └──────────────────────────────────────┘
```

Collapsed: the sheet is a peek strip above the thumb bar, with the task, the kind of question and a plume top edge; a tap opens it. Open: the scrim dims the page, the sheet shows the question with its detail and the primary answer as the one full-width glow button, `1 of 2 ›` steps to the next question inside the sheet, `▾ collapse` folds it back. The card whose question is in the sheet shows `▸ answer in the sheet` with a plume `!` instead of its three buttons. The thumb bar never moves.

### The CLI help

```
igris · run a markdown task plan through Claude Code, one fresh session per task

Usage
  igris                        the home screen (on a terminal)
  igris <command> [flags]      `igris <command> -h` lists its flags

Start
  init           create igris.toml, .igris/, the allow rules    --example  --template NAME
  doctor         check this project and machine, read-only      --json

Inspect
  check          validate the plan                              --strict  --plan PATH  --json
  phases         phases, counts and parallelism                 --json
  status         tasks, the current run, unmet deps             [PHASE]  --json
  history        past runs, or one task's attempts              [TASK-ID]  -n N  --json
  report         one run as markdown                            [RUN]  --json
  usage          tokens and the rank audit                      [RUN | --since D]  --json

Run
  arise          run or resume with the TUI                     [PHASE]  --through  --mode  …
  serve          run headless; `serve token` prints the pairing…--http ADDR:PORT  --listen-public
  attach         open this project's live run
  stop           stop the live run (sessions stay open)

Signal (from a session)
  done ID        the task is finished                           --note TEXT
  skip ID        the task is skipped                            --reason TEXT
  reset ID       back to ready or blocked                       --force

Plan with AI
  draft          write a plan from a goal, a spec or an export  goal | spec | import | extend
  adapt          convert a plan in another format               --model sonnet|opus

Maintain
  notify test    send a sample of each notification             --event NAME
  config migrate drop deprecated settings from igris.toml       --yes
  update         update igris through its package manager       --check  --yes
  completion     print a completion script                      bash | zsh | fish
  help, version

Exit codes  0 ok · 1 failure · 2 usage error
```

An unknown command answers `igris: unknown command "stauts"; did you mean status?` and nothing else; a bad flag answers `igris status: unknown flag --jsn; see igris status -h`.

### `igris status`

```
igris · sinjal · tasks.md

Run    ● Working · M2-02 Lock file · opus · mode plan · since 09:41 · pid 4211 on this host
       phases M2 · 1/4 done

M2 — Config & state · 1/4 · next M2-02
  ID       STATUS       RANK    OWNER         MODE  WAITS ON
  ✓ M2-01  done         sonnet  agent         —
  ● M2-02  in progress  opus    agent         plan
  · M2-03  ready        sonnet  agent         —
  ⨯ M2-04  blocked      sonnet  agent + user  —     M2-02, M2-03

M3 — Engine · 0/3 · wait
  ID       STATUS       RANK    OWNER         MODE  WAITS ON
  ⨯ M3-01  blocked      opus    agent         —     M2-04 (phase M2)

next: igris attach
```

The `Run` line is the status word with its glyph; `! Needs you · M2-02 · commit?` when something waits (from the live run's snapshot over the socket; without one, from `state.json`). Tables fit the width: long cells are truncated with `…`, WAITS ON folds onto a second line. `next:` ends every text output, with the one command that follows from what was shown (`igris arise M2` when idle, `igris check` after a problem).

## What doesn't change

`--json`, the socket protocol, the run log, the file formats and the plan format are frozen ([SPEC §19](../SPEC.md#19-compatibility)) and untouched. The TUI, the web UI and the text output are not frozen; 1.7 changes them under these rules and the release notes list the keys and modes that moved.
