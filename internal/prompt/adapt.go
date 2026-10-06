package prompt

import (
	"bytes"
	_ "embed" // embeds the adapt rules, prompt and canonical format
	"fmt"
	"sort"
	"strings"
	"text/template"
)

// AdaptID is the session and signal ID of an `igris adapt` session: the
// session finishes with `igris done ADAPT` (SPEC §9).
const AdaptID = "ADAPT"

//go:embed adapt_rules.md
var adaptRules string

//go:embed adapt.md.tmpl
var adaptTemplate string

// format is SPEC §3 verbatim; a test keeps it in step with SPEC.md.
//
//go:embed format.md
var format string

// AdaptRules returns the rules of an adapt session, passed with
// --append-system-prompt-file instead of the task rules.
func AdaptRules() string { return adaptRules }

// ModelAlias is one [models] entry: a rank and the Claude model it maps to.
type ModelAlias struct {
	Rank  string
	Model string
}

// Aliases returns the [models] config as aliases sorted by rank.
func Aliases(models map[string]string) []ModelAlias {
	out := make([]ModelAlias, 0, len(models))
	for r, m := range models {
		out = append(out, ModelAlias{Rank: r, Model: m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out
}

// AdaptVars are the variables of the adapt prompt (SPEC §9).
type AdaptVars struct {
	PlanFile     string       // the plan being adapted
	ProposalFile string       // where the session writes the converted copy
	Issues       []string     // `igris check` problems, "file:line: message"
	Models       []ModelAlias // the config's [models], sorted
	DoneCommand  string       // default: "igris done ADAPT"
}

// RenderAdapt renders the first message of an adapt session. The template
// is embedded and not owner-replaceable, unlike the task prompt.
func RenderAdapt(v AdaptVars) (string, error) {
	if v.DoneCommand == "" {
		v.DoneCommand = "igris done " + AdaptID
	}
	tmpl, err := template.New("adapt.md.tmpl").Option("missingkey=error").Parse(adaptTemplate)
	if err != nil {
		return "", fmt.Errorf("parse adapt prompt: %w", err)
	}
	data := struct {
		AdaptVars
		Format string
	}{v, strings.TrimSpace(format)}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render adapt prompt: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n") + "\n", nil
}
