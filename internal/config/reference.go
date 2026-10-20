package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// keyDoc documents one igris.toml key for the generated reference. The key's
// type and default come from Config and Default(); only the prose lives here.
// def overrides the default for keys that are nil in Default() and resolved
// by an accessor instead; values lists the allowed values.
type keyDoc struct {
	desc   string
	values string
	def    string
}

// keyDocs is the prose of docs/config.md, by dotted key path. A table whose
// keys are chosen by the owner ([models], [verify], [phases.<id>]) is
// documented under the table's own path; TestReferenceDocsEveryKey keeps this
// map and Config in step.
var keyDocs = map[string]keyDoc{
	"plan":                       {desc: "Path to the plan file, relative to the project root."},
	"backend":                    {desc: "Where agent sessions run (SPEC §11.3). `auto` picks herdr inside a herdr pane, else tmux inside tmux.", values: "`auto`, `herdr`, `tmux`"},
	"default_mode":               {desc: "Permission mode of a run when `--mode` is not given (SPEC §7). `yolo` and `plan:yolo` always ask for confirmation.", values: "`default`, `accept`, `auto`, `plan`, `plan:default`, `plan:accept`, `plan:auto`, `plan:yolo`, `yolo`"},
	"needs_input_after":          {desc: "How long a session may sit idle at a prompt before it counts as needing you (SPEC §6.3). Greater than zero."},
	"waiting_limit":              {desc: "How long a session may wait on background work before it needs you (SPEC §6.3). Greater than zero.", def: "`\"30m\"`"},
	"nudge":                      {desc: "Remind an idle agent of `igris done` once before it needs you (SPEC §6.3).", def: "`true`"},
	"poll_interval":              {desc: "How often igris checks the session and refreshes the TUI tail. Greater than zero."},
	"models":                     {desc: "Rank alias to the `claude --model` value. Ranks used in the plan's Model column must be listed here."},
	"columns":                    {desc: "Header aliases for the plan table: your header text to the canonical column name (for example `\"Depends on\" = \"Deps\"`)."},
	"claude.extra_args":          {desc: "Arguments appended to every session launch. Model, mode, session, settings, tool, directory, prompt, plugin and MCP flags are rejected, and so are control characters (SPEC §7.4)."},
	"run.verify":                 {desc: "Shell command run after a task signals done; the verify profile `default` (SPEC §6.4). Empty means no verification. Cannot be combined with `[verify] default`."},
	"run.verify_timeout":         {desc: "Time limit for every verify profile. Greater than zero."},
	"run.verify_max_attempts":    {desc: "How many times a failing verify is fed back to the agent before the run stops."},
	"run.commit":                 {desc: "What igris does with the changes after a verified task.", values: "`ask`, `auto`, `never`"},
	"run.commit_message":         {desc: "Go template for the commit message. Variables include `{{.ID}}` and `{{.Title}}`."},
	"run.prompt_template":        {desc: "Path to a custom task prompt template. Empty uses the built-in one."},
	"run.env":                    {desc: "The only variables verify commands and task hooks get, besides `PATH` and `HOME`: names, or prefixes ending in `*` (SPEC §6.4). Unset passes igris's whole environment.", def: "unset"},
	"run.worktree":               {desc: "Run sessions, verify and commits in a git worktree on branch `igris/<run-id>` (SPEC §6.8).", def: "`false`"},
	"verify":                     {desc: "Verify profiles: name to shell command (SPEC §6.4). Names match `[a-z0-9_-]+`, `none` is reserved, commands must not be empty.", def: "no profiles"},
	"phases":                     {desc: "Per-phase settings under `[phases.<id>]` (SPEC §6.4); the id matches a plan phase case-insensitively. `verify` is its only key: a profile name, or `none`.", def: "no phases"},
	"hooks.before_task":          {desc: "Argv list run before every agent session, never through a shell (SPEC §6.7). A non-empty list needs a non-empty first element.", def: "`[]`"},
	"hooks.after_task":           {desc: "Argv list run after every agent session, never through a shell (SPEC §6.7).", def: "`[]`"},
	"hooks.timeout":              {desc: "Time limit for each hook. Greater than zero.", def: "`\"2m\"`"},
	"plan_mode.then":             {desc: "The mode a plain `plan` task switches to when you approve its plan in igris (SPEC §6.3, §7.1). Never `yolo`.", values: "`default`, `accept`, `auto`", def: "`\"auto\"`"},
	"plan_mode.approve_timeout":  {desc: "How long a plan presented for approval waits for your answer in igris before the pane decides (SPEC §6.3). 1m to 24h; `\"0\"` approves plans in the pane only.", def: "`\"30m\"`"},
	"usage.enabled":              {desc: "Read the task sessions' transcripts for token usage in the run log (SPEC §6.9). `false` reads none.", def: "`true`"},
	"usage.statusline":           {desc: "Use igris's status line in task panes to record your subscription quota (5-hour and 7-day usage) for the header and `igris usage` (SPEC §6.3). It replaces your own status line in those panes. `false` leaves it alone and the quota is unavailable.", def: "`false`"},
	"adapt.model":                {desc: "Model rank `igris adapt` uses (SPEC §9).", values: "`sonnet`, `opus`"},
	"tui.mouse":                  {desc: "Click/tap, wheel and hover in the TUI (SPEC §15.5). `false` keeps the terminal's own text selection."},
	"tui.theme":                  {desc: "Which colors to use (SPEC §15.4). `auto` goes by the terminal's background.", values: "`auto`, `dark`, `light`"},
	"tui.rank_colors":            {desc: "Rank to color: `\"#rrggbb\"` or an ANSI color number `\"0\"`–`\"255\"` (SPEC §15.4). Ranks not listed keep the built-in colors."},
	"tui.tail":                   {desc: "Live tail of the current session in the task card (SPEC §15.3). `false` makes no tail calls."},
	"notify.quiet":               {desc: "Quiet hours as `HH:MM-HH:MM`, local time, may cross midnight; held messages go out as a digest (SPEC §10). Start and end must differ.", def: "`\"\"` (none)"},
	"notify.break_through":       {desc: "Events never held in quiet hours. `[]` holds everything.", values: "event names (see below)"},
	"notify.task_done_digest":    {desc: "How `task_done` messages are grouped: `0` or `1` one per task, N ≥ 2 one per N tasks (at most 1000000), `\"phase\"` one per phase.", values: "integer, or `\"phase\"`"},
	"notify.backend.enabled":     {desc: "The backend's own toast (herdr, tmux)."},
	"notify.ntfy.server":         {desc: "ntfy server URL."},
	"notify.ntfy.topic":          {desc: "ntfy topic. The channel is off while this is empty."},
	"notify.ntfy.token":          {desc: "ntfy access token, or `\"env:VAR\"` to read it from the environment."},
	"notify.ntfy.events":         {desc: "Events sent to ntfy. A file igris writes leaves this out while it is the default list.", values: "event names (see below)"},
	"notify.ntfy.template":       {desc: "Message text template (SPEC §10), at most 1000 characters. Empty uses the default text.", def: "`\"\"`"},
	"notify.discord.webhook_url": {desc: "Discord webhook URL (https only), or `\"env:VAR\"`. The channel is off while this is empty."},
	"notify.discord.events":      {desc: "Events sent to Discord.", values: "event names (see below)"},
	"notify.discord.template":    {desc: "Message text template (SPEC §10).", def: "`\"\"`"},
	"notify.webhook.url":         {desc: "Generic webhook URL (http or https), or `\"env:VAR\"`. The channel is off while url and secret are empty."},
	"notify.webhook.secret":      {desc: "Signs the body (`X-Igris-Signature`), or `\"env:VAR\"`. A secret without a url is an error."},
	"notify.webhook.events":      {desc: "Events sent to the webhook.", values: "event names (see below)"},
	"notify.webhook.template":    {desc: "Message text template (SPEC §10).", def: "`\"\"`"},
	"notify.slack.webhook_url":   {desc: "Slack incoming-webhook URL (https only), or `\"env:VAR\"`. The channel is off while this is empty."},
	"notify.slack.events":        {desc: "Events sent to Slack.", values: "event names (see below)"},
	"notify.slack.template":      {desc: "Message text template (SPEC §10).", def: "`\"\"`"},
	"notify.gotify.server":       {desc: "Gotify server URL, for example `https://gotify.example.org`. Needs `token` too."},
	"notify.gotify.token":        {desc: "Gotify application token, or `\"env:VAR\"`. Needs `server` too."},
	"notify.gotify.events":       {desc: "Events sent to Gotify.", values: "event names (see below)"},
	"notify.gotify.template":     {desc: "Message text template (SPEC §10).", def: "`\"\"`"},
}

var durationType = reflect.TypeOf(Duration(0))

// refKey is one documented key: its dotted path, TOML type and default.
type refKey struct {
	path string
	typ  string
	def  string
}

// referenceKeys walks Config for the keys igris.toml accepts, in struct
// order, with the default each has in Default().
func referenceKeys() []refKey {
	var out []refKey
	walkKeys(reflect.ValueOf(*Default()), "", &out)
	// Top-level keys first, so [verify] and [phases], which sit among the
	// tables in the struct, don't open a second "Top level" section.
	sort.SliceStable(out, func(i, j int) bool { return tableOf(out[i].path) == "" && tableOf(out[j].path) != "" })
	return out
}

func walkKeys(v reflect.Value, prefix string, out *[]refKey) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("toml"), ",")
		if name == "" || name == "-" {
			continue
		}
		path := prefix + name
		f := v.Field(i)
		ft := f.Type()
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
			if !f.IsNil() {
				f = f.Elem()
			} else {
				f = reflect.Zero(ft)
			}
		}
		if ft.Kind() == reflect.Struct {
			walkKeys(f, path+".", out)
			continue
		}
		*out = append(*out, refKey{path: path, typ: tomlType(ft), def: formatDefault(f)})
	}
}

func tomlType(t reflect.Type) string {
	switch {
	case t == durationType:
		return "duration"
	case t.Name() == "TaskDoneDigest":
		return "integer or string"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int64:
		return "integer"
	case reflect.Slice:
		return "list of strings"
	case reflect.Map:
		return "table"
	}
	return t.String()
}

func formatDefault(v reflect.Value) string {
	switch {
	case v.Type() == durationType:
		return fmt.Sprintf("`%q`", Duration(v.Int()).Short())
	case v.Type().Name() == "TaskDoneDigest":
		return fmt.Sprintf("`%d`", v.Int())
	}
	switch v.Kind() {
	case reflect.String:
		return fmt.Sprintf("`%q`", v.String())
	case reflect.Bool:
		return fmt.Sprintf("`%t`", v.Bool())
	case reflect.Int, reflect.Int64:
		return fmt.Sprintf("`%d`", v.Int())
	case reflect.Slice:
		items := make([]string, v.Len())
		for i := range items {
			items[i] = fmt.Sprintf("%q", v.Index(i).String())
		}
		return "`[" + strings.Join(items, ", ") + "]`"
	case reflect.Map:
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		if len(keys) == 0 {
			return "empty"
		}
		items := make([]string, len(keys))
		for i, k := range keys {
			items[i] = fmt.Sprintf("%s = %q", k.String(), v.MapIndex(k).String())
		}
		return "`{ " + strings.Join(items, ", ") + " }`"
	}
	return fmt.Sprint(v.Interface())
}

// tableOf is the table a key lives in: "notify.ntfy" for notify.ntfy.topic,
// "" for a top-level key.
func tableOf(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[:i]
	}
	return ""
}

// Reference renders docs/config.md: every igris.toml key with its type,
// default and allowed values, generated from Config and Default() so it
// cannot drift (TestReferenceUpToDate). Regenerate with `make config-ref`.
func Reference() string {
	var b strings.Builder
	b.WriteString("<!-- Generated by `make config-ref` from internal/config; do not edit by hand. -->\n\n")
	b.WriteString("# igris.toml reference\n\n")
	b.WriteString("`igris.toml` lives in the project root (`igris init` creates it). Every key is optional: a missing key takes the default below. The normative description is [SPEC §12](../SPEC.md).\n\n")
	b.WriteString("- Unknown keys are errors, which catches typos.\n")
	b.WriteString("- Durations use Go syntax (`\"30s\"`, `\"15m\"`, `\"1h30m\"`).\n")
	b.WriteString("- Secret keys accept `\"env:VAR\"` to read the value from an environment variable; an unset or empty variable is an error. `igris.toml` holds no secrets by default and is safe to commit.\n")
	b.WriteString("- `igris config migrate` rewrites a file written by an older release and keeps a backup under `.igris/config/`.\n")

	section := "\x00"
	for _, k := range referenceKeys() {
		table := tableOf(k.path)
		if table != section {
			section = table
			if table == "" {
				b.WriteString("\n## Top level\n\n")
			} else {
				fmt.Fprintf(&b, "\n## `[%s]`\n\n", table)
			}
			b.WriteString("| Key | Type | Default | Description |\n|---|---|---|---|\n")
		}
		d := keyDocs[k.path]
		def := k.def
		if d.def != "" {
			def = d.def
		}
		desc := d.desc
		if d.values != "" {
			desc += " Allowed: " + d.values + "."
		}
		name := strings.TrimPrefix(k.path, table+".")
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", name, k.typ, def, strings.ReplaceAll(desc, "|", "\\|"))
	}

	b.WriteString("\n## Event names\n\n")
	b.WriteString("`events` and `break_through` take any of: ")
	events := make([]string, len(validEvents))
	for i, e := range validEvents {
		events[i] = "`" + e + "`"
	}
	b.WriteString(strings.Join(events, ", "))
	fmt.Fprintf(&b, ".\n\nThe default `events` of a channel are %s; the default `break_through` is %s.\n", formatDefault(reflect.ValueOf(defaultEvents())), formatDefault(reflect.ValueOf(defaultBreakThrough())))
	return b.String()
}
