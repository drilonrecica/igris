package prompt

import (
	"bytes"
	_ "embed" // embeds the draft rules and prompt
	"fmt"
	"strings"
	"text/template"
)

// DraftID is the session and signal ID of an `igris draft` session: the
// session finishes with `igris done DRAFT` (SPEC §9.1).
const DraftID = "DRAFT"

//go:embed draft_rules.md
var draftRules string

//go:embed draft.md.tmpl
var draftTemplate string

// DraftRules returns the rules of a draft session, passed with
// --append-system-prompt-file instead of the task rules.
func DraftRules() string { return draftRules }

// DraftVars are the variables of the draft prompt (SPEC §9.1).
type DraftVars struct {
	// Source is what the plan is drafted from: "goal", "spec", "import"
	// or "extend".
	Source       string
	PlanFile     string       // the plan the proposal becomes
	ProposalFile string       // where the session writes the proposal
	Goal         string       // goal: the owner's goal; "" makes the session ask for it
	SpecFile     string       // spec: the spec's path, relative to the project root
	Models       []ModelAlias // the config's [models], sorted
	Verify       []string     // the config's verify profile names, sorted; never their commands
	DoneCommand  string       // default: "igris done DRAFT"
}

// RenderDraft renders the first message of a draft session. The template
// is embedded and not owner-replaceable, unlike the task prompt.
func RenderDraft(v DraftVars) (string, error) {
	if v.DoneCommand == "" {
		v.DoneCommand = "igris done " + DraftID
	}
	tmpl, err := template.New("draft.md.tmpl").Option("missingkey=error").Parse(draftTemplate)
	if err != nil {
		return "", fmt.Errorf("parse draft prompt: %w", err)
	}
	data := struct {
		DraftVars
		Format string
	}{v, strings.TrimSpace(format)}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render draft prompt: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n") + "\n", nil
}
