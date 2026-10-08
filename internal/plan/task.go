package plan

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/textsafe"
)

// titleMaxRunes is how much of the Task text is used as the title when it
// has no bold span (SPEC §3.2).
const titleMaxRunes = 80

// Task is one row of a task table.
type Task struct {
	ID    string
	Title string // first **bold** span of Text, else its first 80 characters
	Text  string // the full Task cell

	Status     Status // StatusUnknown if the cell matched no keyword
	StatusText string // the Status cell as written
	Suffix     string // free text after the status keyword

	Owner     Owner  // "" if OwnerText is not a known owner
	OwnerText string // the Owner cell as written ("" if the column is missing)
	Rank      string // Model cell; "" for none (—, -, none, empty)
	Mode      string // Mode cell, lower-case; "" for none
	DepsText  string // the Deps cell as written
	Deps      []string

	// Verify is the Verify cell, lower-case, backticks removed: a verify
	// profile, "none" (no verification), or "" when not set (empty, — or -).
	Verify string
	// TimeoutText and ContextText are the Timeout and Context cells as
	// written, "" when not set (empty, — or -).
	TimeoutText string
	ContextText string
	// Timeout is TimeoutText as a duration; 0 when not set or not a
	// duration greater than zero (a validation error, SPEC §3.2).
	Timeout time.Duration
	// Context is the Context cell's paths as written: split at commas,
	// trimmed, backticks stripped, empty entries and duplicates dropped.
	Context []string

	Extra map[string]string // extra columns by header name, e.g. "Spec"
	Cells []string          // every cell as written, in the order of Phase.Columns
	Phase *Phase
	Line  int

	statusStart, statusEnd int // byte span of the Status cell content in the file
}

// Task returns the task with the given ID, or nil. If an ID is duplicated
// (a validation error), the first occurrence is returned.
func (p *Plan) Task(id string) *Task { return p.byID[id] }

// buildTasks turns the table rows of every phase into tasks.
func (p *Plan) buildTasks() {
	p.byID = map[string]*Task{}
	for _, ph := range p.Phases {
		for _, r := range ph.rows {
			t := p.newTask(ph, r)
			ph.Tasks = append(ph.Tasks, t)
			p.Tasks = append(p.Tasks, t)
			if _, dup := p.byID[t.ID]; !dup && t.ID != "" {
				p.byID[t.ID] = t
			}
		}
	}
}

func (p *Plan) newTask(ph *Phase, r row) *Task {
	if len(r.cells) > len(ph.Columns) {
		p.addIssue(r.line.num, "row has %d cells but the table header has %d; escape literal pipes as \\|", len(r.cells), len(ph.Columns))
	}
	if textsafe.HasControl(r.line.text) {
		p.addIssue(r.line.num, "row contains a control character (an escape sequence?); remove it")
	}
	t := &Task{Phase: ph, Line: r.line.num, Extra: map[string]string{}}
	ownerSeen := false
	for i, col := range ph.Columns {
		var c cell
		if i < len(r.cells) {
			c = r.cells[i]
		} else {
			// Missing trailing cell: anchor at the end of the line.
			c = cell{start: len(r.line.text), end: len(r.line.text)}
		}
		t.Cells = append(t.Cells, c.value)
		switch col {
		case ColID:
			t.ID = c.value
		case ColTask:
			t.Text = c.value
		case ColDeps:
			t.DepsText = c.value
		case ColStatus:
			t.StatusText = c.value
			t.Status, t.Suffix, _ = ParseStatus(c.value)
			t.statusStart, t.statusEnd = r.line.start+c.start, r.line.start+c.end
		case ColModel:
			if !isNone(c.value) {
				t.Rank = strings.Trim(c.value, "`")
			}
		case ColOwner:
			ownerSeen = true
			t.OwnerText = c.value
			t.Owner, _ = ParseOwner(c.value)
		case ColMode:
			if !isNone(c.value) {
				t.Mode = strings.ToLower(strings.Trim(c.value, "`"))
			}
		case ColVerify:
			if !unset(c.value) {
				t.Verify = strings.ToLower(strings.Trim(c.value, "`"))
			}
		case ColTimeout:
			if !unset(c.value) {
				t.TimeoutText = c.value
				if d, err := time.ParseDuration(strings.Trim(c.value, " `")); err == nil && d > 0 {
					t.Timeout = d
				}
			}
		case ColContext:
			if !unset(c.value) {
				t.ContextText = c.value
				t.Context = splitContext(c.value)
			}
		default:
			t.Extra[col] = c.value
		}
	}
	if !ownerSeen {
		t.Owner = OwnerAgent
	}
	t.Title = title(t.Text)
	return t
}

// title returns the first **bold** span of text, else its first 80
// characters.
func title(text string) string {
	if _, rest, ok := strings.Cut(text, "**"); ok {
		if bold, _, ok := strings.Cut(rest, "**"); ok && strings.TrimSpace(bold) != "" {
			return strings.TrimSpace(bold)
		}
	}
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= titleMaxRunes {
		return text
	}
	return string([]rune(text)[:titleMaxRunes])
}

// unset reports whether a Verify, Timeout or Context cell is "not set":
// empty, — or -. Unlike isNone, "none" is a value there (SPEC §3.2).
func unset(s string) bool {
	switch strings.Trim(s, " \t`") {
	case "", "—", "-":
		return true
	}
	return false
}

// splitContext splits a Context cell into its paths (SPEC §3.2).
func splitContext(cell string) []string {
	var out []string
	for _, e := range strings.Split(cell, ",") {
		e = strings.TrimSpace(strings.Trim(strings.TrimSpace(e), "`"))
		if e != "" && !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out
}
