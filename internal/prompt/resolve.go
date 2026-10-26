package prompt

import (
	"bytes"
	_ "embed" // embeds the resolver template
	"fmt"
	"strings"
	"text/template"
)

//go:embed resolve.md.tmpl
var resolveTemplate string

// ResolveVars are the variables of the resolver session's first message
// (SPEC §6.11): the task's row as the task prompt renders it, why the
// resolver is here, and the other side. [run] prompt_template doesn't
// apply: there is one resolver prompt.
type ResolveVars struct {
	Vars
	// Reason is "conflict" (the rebase stopped on Files) or "verify"
	// (Profile's Command failed on the integrated tree with Lines).
	Reason  string
	Files   []string
	Profile string
	Command string
	Lines   []string
	// Others are the tasks the run branch took since this task's base,
	// More how many more there were, Commits the subjects of commits that
	// are no task of this run.
	Others  []ResolveOther
	More    int
	Commits []string
	// SkipCommand is how the resolver gives up: "igris skip <ID>".
	SkipCommand string
}

// ResolveOther is a task on the other side of the conflict: its row's
// ID, title and text from the plan.
type ResolveOther struct {
	ID, Title, Text string
}

// ResolveContinue returns the first prompt of a session that continues a
// resolver's conversation (`claude --resume`): the conversation holds the
// resolver prompt, so this only says how to go on.
func ResolveContinue(id string) string {
	return "igris reopened this conversation after the previous resolver session of task " + id + " ended. " +
		"Continue the resolution where you left off: check `git status` and `git diff` first. " +
		"When the resolution is done and the project's checks pass, run `igris done " + id + " --note \"<one-line summary>\"` as your very last action.\n"
}

// RenderResolve renders the resolver prompt with v.
func RenderResolve(v ResolveVars) (string, error) {
	if v.SkipCommand == "" {
		v.SkipCommand = "igris skip " + v.ID
	}
	tmpl, err := template.New("resolve.md.tmpl").Option("missingkey=error").Parse(resolveTemplate)
	if err != nil {
		return "", fmt.Errorf("parse the resolver prompt template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, v); err != nil {
		return "", fmt.Errorf("render the resolver prompt: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n") + "\n", nil
}
