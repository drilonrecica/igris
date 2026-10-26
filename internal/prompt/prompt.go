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

//go:embed side_rules.md
var sideRules string

// Rules returns the igris rules passed with --append-system-prompt-file.
// They are static (no template variables) so they hold after compaction.
func Rules() string { return rules }

// SideRules returns the rules of a side session (SPEC §6.10), passed with
// --append-system-prompt-file. runTask is the task a run's session works
// on in the side session's working tree right now, "" when none does (no
// run, or the side session has its own worktree); it must be a valid task
// ID.
func SideRules(runTask string) string {
	if runTask == "" {
		return sideRules
	}
	return sideRules + "4. **A task runs in this tree.** igris's session for " + runTask +
		" is working in this working tree right now. Ask the owner before you edit any file, so the two sessions don't get in each other's way.\n"
}

// Continue returns the first prompt of a session that continues an earlier
// conversation about task id (`claude --resume`). The conversation already
// holds the task prompt, so this only says how to go on.
func Continue(id string) string {
	return "igris reopened this conversation after the previous session of task " + id + " ended. " +
		"Continue the task where you left off: check `git status` and `git diff` first. " +
		"When the task is done and nothing waits on the owner, run `igris done " + id + " --note \"<one-line summary>\"` as your very last action.\n"
}

// Nudge returns the one reminder igris sends a session of task id that
// stopped without `igris done` and has nothing running in the background
// (SPEC §6.3). It asks the agent to finish or to say what it waits for, so
// the owner sees why when Needs you follows.
func Nudge(id string) string {
	return "igris: you ended your turn without running `igris done " + id + "`. " +
		"If the task is finished and nothing waits on the owner, run `igris done " + id + " --note \"<one-line summary>\"` now as your last action. " +
		"If you are waiting for the owner or for something else, say in one line what it is.\n"
}

// LimitContinue returns the one fixed prompt igris sends a session that
// waited at a usage limit and hasn't continued after the reset (SPEC
// §6.3): Claude Code gave up continuing by itself, or two minutes passed.
func LimitContinue() string {
	return "igris: the usage limit has reset; continue the task.\n"
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
	TaskFile     string // the file the task's row is in (SPEC §3.7); PlanFile for a one-file plan
	Resumed      bool
	CommitPolicy string // "ask", "auto" or "never"
	DoneCommand  string // e.g. "igris done M0-01"
	// DepNotes are the handoff notes of the task's direct deps (SPEC §6.1,
	// since 1.8), in the order of its Deps cell; empty with dep_notes off.
	DepNotes []DepNote
}

// DepNote is what a finished dep left for the tasks that depend on it
// (SPEC §6.1): the note of its latest done or skip, and the plan the owner
// approved for that attempt. Both are session text, already cleaned and
// bounded by the builder.
type DepNote struct {
	ID     string
	Title  string
	Status string // "done" or "skipped"
	Note   string // one line
	Plan   string // the approved plan, newlines kept; "" when there is none or it didn't fit
	// PlanCut says Plan was cut to fit its bound; PlanFile is the plan's
	// absolute path, to read the whole of it.
	PlanCut  bool
	PlanFile string
}

// Quoted is Plan as a Markdown quoted block.
func (n DepNote) Quoted() string {
	lines := strings.Split(strings.TrimRight(n.Plan, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight("> "+l, " ")
	}
	return strings.Join(lines, "\n")
}

// Env is what Vars needs besides the task itself.
type Env struct {
	Model        string
	PlanFile     string
	TaskFile     string // default: PlanFile
	CommitPolicy string
	DoneCommand  string // default: "igris done <ID>"
	Resumed      bool
	DepNotes     []DepNote
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
		TaskFile:     e.TaskFile,
		Resumed:      e.Resumed,
		CommitPolicy: e.CommitPolicy,
		DoneCommand:  e.DoneCommand,
		DepNotes:     e.DepNotes,
	}
	if t.Phase != nil {
		v.Phase = t.Phase.ID
		v.PhaseTitle = t.Phase.Title
	}
	if v.DoneCommand == "" {
		v.DoneCommand = "igris done " + t.ID
	}
	if v.TaskFile == "" {
		v.TaskFile = v.PlanFile
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
