// Package prompt renders the two prompts igris gives a Claude Code session
// (SPEC §6.1): the fixed igris rules and the per-task first message.
package prompt

import (
	"bytes"
	_ "embed" // embeds the default rules and task template
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/drilonrecica/igris/internal/plan"
)

//go:embed rules.md
var rules string

//go:embed task.md.tmpl
var defaultTemplate string

// Rules returns the igris rules passed with --append-system-prompt-file.
// They are static (no template variables) so they hold after compaction.
func Rules() string { return rules }

// Continue returns the first prompt of a session that continues an earlier
// conversation about task id (`claude --resume`). The conversation already
// holds the task prompt, so this only says how to go on.
func Continue(id string) string {
	return "igris reopened this conversation after the previous session of task " + id + " ended. " +
		"Continue the task where you left off: check `git status` and `git diff` first. " +
		"When the task is done and nothing waits on the owner, run `igris done " + id + " --note \"<one-line summary>\"` as your very last action.\n"
}

// Vars are the template variables of the task prompt (SPEC §6.1).
type Vars struct {
	ID           string
	Title        string
	Text         string // full Task cell
	Phase        string
	PhaseTitle   string
	Rank         string
	Model        string
	Owner        string // "agent", "agent + user" or "user"
	Deps         []string
	Extra        map[string]string // extra columns, e.g. Spec
	Context      []string          // the Context cell's paths as written (SPEC §3.2)
	PlanFile     string
	Resumed      bool
	CommitPolicy string // "ask", "auto" or "never"
	DoneCommand  string // e.g. "igris done M0-01"
}

// Env is what Vars needs besides the task itself.
type Env struct {
	Model        string
	PlanFile     string
	CommitPolicy string
	DoneCommand  string // default: "igris done <ID>"
	Resumed      bool
}

// VarsFor builds the template variables for task t.
func VarsFor(t *plan.Task, e Env) Vars {
	v := Vars{
		ID:           t.ID,
		Title:        t.Title,
		Text:         t.Text,
		Rank:         t.Rank,
		Model:        e.Model,
		Owner:        string(t.Owner),
		Deps:         t.Deps,
		Extra:        nonEmpty(t.Extra),
		Context:      t.Context,
		PlanFile:     e.PlanFile,
		Resumed:      e.Resumed,
		CommitPolicy: e.CommitPolicy,
		DoneCommand:  e.DoneCommand,
	}
	if t.Phase != nil {
		v.Phase = t.Phase.ID
		v.PhaseTitle = t.Phase.Title
	}
	if v.DoneCommand == "" {
		v.DoneCommand = "igris done " + t.ID
	}
	return v
}

// Render executes the task template with v. templatePath is the owner's
// prompt_template from igris.toml; empty means the embedded default.
func Render(v Vars, templatePath string) (string, error) {
	name, text := "task.md.tmpl", defaultTemplate
	if templatePath != "" {
		data, err := os.ReadFile(templatePath) //nolint:gosec // the owner's own prompt_template
		if err != nil {
			return "", fmt.Errorf("read prompt_template %s: %w; fix the path or remove prompt_template to use the default prompt", templatePath, err)
		}
		name, text = templatePath, string(data)
	}
	tmpl, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse prompt template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, v); err != nil {
		return "", fmt.Errorf("render prompt template %s: %w; see SPEC §6.1 for the available variables", name, err)
	}
	return strings.TrimRight(buf.String(), "\n") + "\n", nil
}

// nonEmpty returns the entries of m with a value, so an empty extra column
// doesn't render as a bare "- Spec:" line.
func nonEmpty(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	return out
}
