package report

import (
	"fmt"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Issue is one problem that makes a plan invalid.
type Issue struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// String is the `file:line: message` form (no line: `file: message`).
func (i Issue) String() string {
	if i.Line == 0 {
		return fmt.Sprintf("%s: %s", i.File, i.Message)
	}
	return fmt.Sprintf("%s:%d: %s", i.File, i.Line, i.Message)
}

// Issues converts plan issues. The result is never nil, so it is `[]` in JSON.
func Issues(issues []plan.Issue) []Issue {
	out := make([]Issue, len(issues))
	for i, is := range issues {
		out[i] = Issue{File: textsafe.Line(is.File), Line: is.Line, Message: textsafe.Line(is.Msg)}
	}
	return out
}

// Warning is something worth knowing that doesn't make the plan invalid.
type Warning struct {
	File    string `json:"file,omitempty"` // empty for a tool version, igris.toml for the config
	Line    int    `json:"line,omitempty"`
	Task    string `json:"task,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Message string `json:"message"`
}

// Invalid is what `phases` and `status` report for a plan that isn't valid.
type Invalid struct {
	Issues []Issue `json:"issues"`
	Plan   string  `json:"plan"`
	Valid  bool    `json:"valid"`
}

// NewInvalid reports issues of the plan at path.
func NewInvalid(path string, issues []plan.Issue) Invalid {
	return Invalid{Issues: Issues(issues), Plan: textsafe.Line(path)}
}

// CheckReport is the result of `igris check`.
type CheckReport struct {
	Issues   []Issue   `json:"issues"`
	Phases   int       `json:"phases"`
	Plan     string    `json:"plan"`
	Tasks    int       `json:"tasks"`
	Valid    bool      `json:"valid"`
	Warnings []Warning `json:"warnings"`
}

// CheckInput is what Check reads.
type CheckInput struct {
	Plan   *plan.Plan
	Models map[string]string
	// Checks are the warnings, in the order to show them: tool versions,
	// config, environment, a parent igris.toml, the plan's hints and drift
	// (checks gives the last two only for a valid plan). Results that are
	// OK are left out.
	Checks []checks.Result
}

// Check validates the plan and lists the problems of in.Checks as its
// warnings.
func Check(in CheckInput) CheckReport {
	p := in.Plan
	issues := p.Validate(in.Models)
	r := CheckReport{
		Issues:   Issues(issues),
		Phases:   len(p.Phases),
		Plan:     textsafe.Line(p.Path),
		Tasks:    len(p.Tasks),
		Valid:    len(issues) == 0,
		Warnings: []Warning{},
	}
	for _, c := range checks.Problems(in.Checks) {
		r.Warnings = append(r.Warnings, Warning{
			File: textsafe.Line(c.File), Line: c.Line, Task: textsafe.Line(c.Task), From: c.From, To: c.To,
			Message: textsafe.Line(c.Message),
		})
	}
	return r
}
