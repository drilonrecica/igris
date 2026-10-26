# Limits and budgets

Three things keep a long, unattended run from burning more than you meant to: igris **holds** new tasks while your subscription's usage limit is near or hit, a **budget** pauses the run when it has used what you set aside, and an **estimate** in dollars tells you what the tokens would cost. None of them ever stops a session, kills it or switches its model: a limit or a budget pauses, and you decide.

## Usage limits

When Claude Code says `You've hit your weekly limit · resets 9am`, the reset time is in the session's transcript. igris reads it (and a hook tells it within seconds) and does three things:

- **The session waits by itself.** Its card says `◌ Usage limit · 7d · continues 09:00 (≈ 3h12m)`. The task's Timeout clock stops for the wait, there is no idle reminder and no Needs you. igris sets `autoContinueAtUsageLimit` in the settings file it gives each task session, so the session continues at the reset whatever your own Claude Code setting is. If it is still idle two minutes after the reset, igris sends one fixed prompt, `igris: the usage limit has reset; continue the task.`
- **New tasks are held.** No task starts until the window resets, plus 60 seconds. The header and the web UI show the hold and its countdown. When it ends, igris selects the next task as usual.
- **You get one notification** per limit episode, `usage_limit`, which is not urgent and respects quiet hours.

A reset more than 24 hours away is a long hold, not Needs you: the notification tells you, and **Stop** is always there.

### Holding before a hit

With `[usage] statusline = true` igris knows your 5-hour and 7-day usage ([usage and plans](usage.md#subscription-quota)). Then it also holds a new task while a window is at or above `hold_at` percent (default `95`), until it resets, or sooner if a fresh reading falls below it. `hold_at = 0` holds only after a real hit. A held run says why, and **Start anyway** begins the next task regardless.

```toml
[usage]
statusline = true
hold_at = 95
```

## Budgets

A budget is what you want a run, a phase or a rank to use, in tokens or in estimated dollars:

```toml
[usage.budget]
run = "20M"                  # input + output tokens, subagents included
[usage.budget.phases]
m2 = "$15"                   # estimated dollars, by the price table
[usage.budget.ranks]
fable = "5M"
```

igris checks them about every ten seconds against the attempts of this run (side sessions don't count; a resolver session counts as its task's). When one is reached the run is **Needs you** (`budget`), you get an urgent `budget_exceeded` notification, and no new task starts. Running sessions keep running. Then you choose, in the terminal or on the web UI:

- **Raise**: a new value for this run only; `igris.toml` is untouched.
- **Continue**: this budget no longer holds this run.
- **Stop**.

The card shows `budget 14.2M / 20M · 71%`, Ember from 80%. `igris usage` and `igris report` list the budgets with their use. A budget never picks a cheaper model for you: models come from your plan.

## The price estimate

Tokens come with `≈ $12.40`, wherever they are shown. It is an **estimate** at Anthropic's API list prices, which igris embeds with their date. It is never fetched, and it is not a bill: a subscription includes this usage. The estimate is flat (no long-context tier, no batch discount), and tokens of a model without a price are counted apart as unpriced.

Override a price, or add a model igris doesn't know, per model in US dollars per million tokens:

```toml
[usage.prices]
"claude-opus-5-5" = { input = 4.0, cache_write_5m = 5.0, cache_write_1h = 8.0, cache_read = 0.20, output = 20.0 }
```

`igris usage --json` carries it as `estimate: { usd, as_of, source, unpriced }`.

## Where the time went

`igris report` and `igris history` split each task's time into **agent**, **verify**, **needs you** and **limit wait**, for the run, each phase and each rank. In a parallel phase tasks overlap, so a sum is followed by its wall time (`33m39s (28m45s wall)`). A wait for a limit is nobody's time, which is why it has its own column.

## What a session could fake

A session can write its transcript and `.igris/quota.json`, so it could fake a hit, a quota or a low token count. A hold and a budget are information and a soft limit, not a guard against a hostile session, and neither ever stops one. A reset more than 8 days ahead, or in the past, is ignored with a warning. See the [threat model](../../docs/threat-model.md).
