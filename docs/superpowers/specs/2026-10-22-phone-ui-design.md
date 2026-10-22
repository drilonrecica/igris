# The phone page — design

Owner's decision 2026-10-22, before v1.6.0 ships. Mockups: https://claude.ai/artifact/Y3jYneQeuwyA85KURUJP7S (four phone screens: the glance while the run works, the Needs-you sheet, the plan, the glance in light).

## Intent

The web UI (SPEC §15.8) is the second face of igris: the one you see from the couch, from bed, or on the road, one-handed, often in a dark room, sometimes in sunlight on a bad connection. It exists to answer two questions in the first second after it opens: **does it need me**, and **how far is it**. Everything else on the page serves those two, or gets out of the way.

It is opinionated like the rest of igris: one way, the brand's, not a dashboard with options. Someone who opens it should know at once that this is igris and nothing else.

## Goals

- The first screen answers both questions without scrolling or reading: one word in the brand's display face (**Working**, **Needs you**, **Paused**, **Done**, **Stopped**), and one bar segmented by task.
- Red means exactly one thing, Needs you, and appears nowhere else on the page.
- Answering a question is one thumb tap: the question comes to the thumb as a sheet from the bottom, with its primary action first and biggest.
- The thumb bar at the bottom holds what you do most: Plan, Pause / Resume, Stop. Nothing you need often sits in the top third.
- The brand on the page: the visor icon, the wordmark with the red i, Chakra Petch for the big word, abyss / night / glow / plume / snow.
- Dark first; a light theme for sunlight, chosen by the phone's own setting (owner's decision: "also offer light").
- Memorable and calm: the page breathes (one pulse on the live dot and the active segment), it never flashes, and it reads the same with colour removed.

## Non-goals

- No new actions. What the page can do is what SPEC §15.8 lists today, nothing more; starting a run, opening a pane and side sessions stay in the terminal, and skip-permissions mode is never offered or accepted (§7.3, §14).
- No settings, no theme toggle, no layout options. The phone's light/dark setting is the one input.
- No framework, no build step, no dependency, nothing loaded from elsewhere (SPEC §15.8, P0-01). One template file, one stylesheet, one script, the fonts the repo already has.
- No desktop layout work beyond "it still works": the page is phone-first; on a wide screen it is the phone layout centred, at most ~30rem wide.

## Anti-goals

What would make this a failure even if it works:

- A long page you scroll to find the question.
- A generic look: grey cards with a red left border, system blue buttons, a dashboard grid.
- Red used for emphasis, errors and danger alike, so that Needs you no longer stands out.
- An animation that keeps running after the run ended, or a pulse that fights the reader.
- Anything the tests can't pin: markup the goldens don't cover, a path the no-JS forms don't reach.

## Constraints (from the code, the spec and the tests)

- **CSP** (SPEC §14): `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'`. Fonts need `font-src 'self'` added; nothing else changes. No inline `style=`, no `<style>`, no inline script, no `on*=` attributes (`TestUIPagesKeepTheCSPAndNoYolo` enforces all of it). All styling lives in `app.css`; state is expressed through classes and data attributes the template sets.
- **Files**: `internal/serve/ui/page.html` (templates `head`, `page`, `live`, `question`, `form`, `confirm`, `message`), `app.css`, `app.js`, embedded by `internal/serve/ui.go`; routes in `ui.go` (`/`, `/live`, `/act`, `/static/app.js`, `/static/app.css`). New static files (the two Chakra Petch woff2, the icon) must be added to the route switch in `ui.go` and the embed directive. `/favicon.ico` should answer with the brand icon (today a 404 on every load; docs/check-v16.md).
- **Live updates** stay as they are: SSE on `/api/v1/events`, `refresh()` replacing `#live` from a fragment route, the 300 ms coalescing, typed-text preservation across swaps, `readTails()` every 4 s while visible, `ended()` on `run_stopped`. The one change: `#live` carries `data-live="/live"` or `data-live="/plan/live"` and the script fetches that, so `/plan` refreshes into the plan and not into the run (a second fragment route in `ui.go`, `TestUIRoutes` and `TestUIScriptPaths` updated). *Builder's call, after the builder check.*
- **No-JS path**: every action is a `<form method="post" action="/act">` with the hidden fields `csrf`, `type`, `task`, `question`, `plan`, `continue`, `yes`, at most one `text` field, a `button.go`, and for stop / skip / done the `data-confirm="1"` + `.confirm` box with `name="confirmed" value="1"` and a `.cancel` button (`TestUIFormActions`, `TestUISecondConfirmations`, `TestUIConfirmationPostsAgain`). Keep the field names and these class names; the look of a form is free.
- **Markup the script and tests match**: `form.cmd`, `section.question`, `.actions`, `details`, `section[aria-label='Run mode']` (removed by `ended()`), `#live`, `#conn`, `#flash`, `pre.tail[data-task]`, `.ran[data-started]`, `time[datetime]`, `meta[name="igris-csrf"]`, `<script src="/static/app.js" defer>` as the only script tag. Renaming any of these means changing `app.js` and `ui_test.go` in the same commit; `TestUIScriptEndsWithTheRun` matches the `gone` selector list in `app.js` text.
- **Threat model** names `TestUIFormBodyBounded`, `TestUIFormCheckedBeforeConfirm`, `TestUIPagesKeepTheCSPAndNoYolo`, `TestUIFormActions`; `tools/threatmodel` fails if one disappears.
- **Goldens** under `internal/serve/testdata/web/` (`running`, `running-live`, `confirm-skip`, `refused-yolo`, `yolo-user`, `session-lost`, `ended`, `empty`) are regenerated with `-update` and reviewed; add goldens for the new states (a question pending, several active tasks, the plan page, the light theme is CSS only so needs none).
- **Readable without colour**: every state is also a word (SPEC §15.8). Touch targets ≥ 44 px. Text contrast ≥ 4.5:1 in both themes.
- **Fonts**: `site/fonts/chakra-petch-latin-{600,700}-normal.woff2` (OFL, 9.7 KB each) are copied under `internal/serve/ui/` and served under `/static/`; `@font-face` in `app.css`; `font-display: swap`. Body text is the phone's sans (a named stack, never bare `system-ui`: some Linux setups map it to a monospace face, see `site/style.css`); IDs and the tail use `ui-monospace, "SF Mono", Menlo, Consolas, monospace`.
- **Binary size**: about 25 KB more for the fonts and icon. Acceptable.
- Tests run only inside the systemd fence, one at a time (AGENTS.md §5).

## Decided (as the owner decided it, or as shown and loved in the mockups)

**Needs you, precisely.** The status word is `Needs you` whenever any active task waits on the owner: a pending question (`Questions`, `Reset`), a user task at its turn (`your_turn`), or a session blocked at a prompt in its pane or lost (`needs_you`, `lost`). The sheet always exists in that state. For a question it holds the question and its answers. For a user task it says `Your turn: <ID> <title>` and that the work is done in the terminal, with Mark done (note) and Skip (reason) as its buttons. For a blocked or lost session it says what the card's state text says today (`waits for a permission in its pane`, `the session is gone`) and offers the actions that apply (the session-lost choices; for a blocked pane, Mark done, Skip, Stop). So red never appears without something to tap, and the companion line counts tasks (`1 task needs you`, `2 tasks need you`, `1 question · 4 min` when it is a question). `Needs you` outranks `Paused`; `Paused` outranks `Working`. *Agent's call after the builder check.*

**Text fields.** Skip's reason, Mark done's note and Reject's feedback are in the form's markup always, so the no-JS path can fill them before the first POST (`/act` shape-checks before it confirms). With JavaScript the field is collapsed until its button is tapped, as the `.confirm` box is today: the script adds a class on `html` at load, and the stylesheet collapses `.field` only under it. The card's row of three buttons is therefore three forms; tapping Skip or Mark done opens its field and its confirmation inside the card, tapping Retry opens the two retry choices. *Agent's call.*

**Elapsed.** The phase bar's `· 47 min` is the run's elapsed time, from a new additive snapshot field `started` (RFC 3339, when the run started; `internal/serve/snapshot.go`, protocol 1, additive per §19), formatted like `ran`. *Agent's call.* The card's elapsed stays per task.

**After the end.** serve ends with the run (§14), so nothing on the page can be fetched after `run_stopped`. `ended()` therefore removes the thumb bar as well as the forms and the sheet; the final screen is the status word (`Done`, `Stopped`, `Failed`, `Stuck`), the companion line with the time, the phase bar as it ended, the cards without actions, and Recent. The plan is read in the terminal from there. *Agent's call; replaces "the thumb bar keeps `Plan`" below.*

**Screens.** Two pages and one sheet.

1. **The run** at `/`. Top bar: visor icon, wordmark `igris` (red i) `· <project>`, a live dot at the right that pulses while the event stream is up and goes grey with the word `reconnecting…` when it isn't (replacing the `#conn` bar). Then the **status word** in Chakra Petch 700, ~34 px: `Working` (glow core), `Needs you` (plume, with a soft red glow), `Paused` (snow, with `(holding)` after it when the pause has taken effect), `Done` / `Stopped` / `Failed` after the run ended (the outcome word, snow), `Waiting` while the run has started but no task has yet. Beside the word, one short line: `nothing needs you`, `1 question · 4 min`, `2 tasks need you`, `paused after this task`, `ended 14:02`. Then the **phase bar**: one segment per task of the current phase in plan order, filled glow for done, pulsing glow for active, plume for a task that needs you, outlined for not yet, a dash for skipped; under it `phase <ID> · <title>` left and `<done> of <total> done · <elapsed>` right. Then one **card per active task** (several in a parallel phase, in plan order): ID in mono glow, title, `rank → model · mode · elapsed · tokens`, the overdue / rank-mismatch warning as a word, the tail (last lines, mono, on abyss), and the task's actions as a row of three equal buttons (Mark done · Skip · Retry; Retry opens the two choices). Then **Next up** (the next two tasks of the phase with their rank) and **Recent** (the last 5 events, time in the phone's zone). Then the **thumb bar**, sticky at the bottom: `Plan` (a link to `/plan`), `Pause after task` / `Resume`, `Stop` (plume text, outlined). After the run ended the thumb bar is gone (see **After the end**).
2. **The plan** at `/plan`: every phase with its title, a thin segmented bar for a phase that isn't the current one, and for the current phase its task list with marks (done ✓ filled glow, active ring, needs-you plume, not yet outline, skipped dash, a user's task marked `you` in plume text), the rank at the right, and the task-mode `details` for tasks still to do, as today. A `Back to the run` button and the run-mode select live in its thumb bar. It is a full page, server-rendered, with the same head and script (so it also refreshes live).
3. **The Needs-you sheet.** When a question is pending, the run page shows it as a sheet anchored to the bottom over a dim scrim, above the thumb bar: the task's ID, the kind of question as a small plume label (`commit?`, `plan`, `session lost`, `skip?`, `hook failed`, `reset?`), the question in one sentence at ~20 px, the detail (the plan text, the changed files, the output) in a mono box that scrolls inside the sheet, the **primary answer as one full-width glow button**, the other answers as outlined buttons under it. With two or more questions, the sheet shows the first in plan order with `1 of 2` and a `Next` link that scrolls the next one into view (each question is its own `section.question`; the sheet is the first one, the others render below it in the same style). Stop / skip / done confirmations render inside the sheet in place of the question's buttons. Without JavaScript the sheet is simply the first section after the header, same markup, no scrim.

**Colour.** Tokens in `app.css` `:root`, dark by default, overridden under `prefers-color-scheme: light`:

| token | dark | light |
|---|---|---|
| ground | abyss `#05070F` | snow `#EAF4FF` |
| card | night `#0B1E46` | white `#FFFFFF` |
| line | deep `#123A7A` / `#0F2A5C` | `#C9D8EE` |
| text | snow `#EAF4FF` | abyss `#05070F` |
| muted | `#9DB0D3` | `#4A5B7A` |
| accent (live, done, primary button) | glow `#3CC8FF` | `#0A9BE6` |
| accent text on ground | glow core `#B8F0FF` | `#0A86C8` |
| needs you | plume `#E3172B`, text `#FF5C74` | plume `#E3172B`, text `#B3122E` |

Red is used for Needs you (the word, the segment, the sheet's label and top edge, the gate's `you`), and for the `Stop` button's text. Nothing else.

**Type.** Chakra Petch 700 for the status word, the wordmark, phase IDs; 600 for section labels (`NEXT UP`, `RECENT`, uppercase, letter-spaced, muted). Body 16 px in the phone's sans. Task IDs and the tail in mono, 13 px and 12 px.

**Motion.** Two animations only: the live dot and the active segment pulse (2.4 s ease). Both stop under `prefers-reduced-motion` and when the run has ended. The sheet appears without animation (the page re-renders from `/live`; a transition would replay on every refresh).

**Icons.** The brand visor (`docs/brand/igris-icon.svg`, inlined in the template; the only decorative image) and three small inline stroke SVGs for the task marks. No icon font, no emoji.

**Copy.** The words stay igris's: `Pause after this task`, `Resume`, `Stop igris?`, `Leave uncommitted`, `Retry fresh`, `Retry, continuing the conversation`, `Approve: the session switches to <mode>`, `Reject with feedback`. The status word's companion line is new text and is the only new vocabulary.

## Not in this design

The mockups show a `Show diff` button on the commit sheet and a `Mode for next: auto` pill on the plan page. Neither is built: no new actions. The run-mode select stays a select, in the plan page's thumb bar. The `tokens` figure on a card refreshes with the page, not with every `tokens` event, as today.

## Left to the builder

- Exact spacing, radii (cards 14 px, buttons 12 px, the sheet 22 px top corners in the mockups), shadow values, the pulse keyframes.
- How the sheet is marked up so it is one `section.question` with a class that the stylesheet turns into a sheet, and how `/plan` shares the `live` template's parts without duplicating them (template blocks).
- The favicon route and the `<link rel="icon">`: serve the brand icon as SVG under `/static/` and at `/favicon.ico`.
- The small `ended` behaviour: the status word changes, the sheet and the forms go, the thumb bar keeps `Plan`; `ended()` in `app.js` adapts to the new markup (it currently appends `ended: …` to `header .meta`).
- Golden names and the fixture snapshots for the new states.
- Whether `Recent` shows 5 or 8 events.
- The exact companion line for each state and the elapsed-time format (reuse `ran` in `ui.go`).

## Verification

- `make fmt lint test` green inside the fence; goldens regenerated with `-update` and read as a diff.
- The headless check the V16 gate did, repeated by the agent with Playwright at 390×844 against a scratch run on loopback: pair, the status word for each state (working, needs you with a commit question, paused, ended), answer a commit question from the sheet, the second confirmation for Stop inside the sheet, `/plan`, no `yolo` anywhere, the no-JS path (`/act` with JavaScript disabled), `prefers-color-scheme: light` emulated, `prefers-reduced-motion` emulated.
- The owner's phone over Tailscale (the scratch project is still up): the glance in a dark room and in light, the sheet, one answer by thumb.
