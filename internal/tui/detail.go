package tui

import (
	"strings"

	"github.com/drilonrecica/igris/internal/plan"
)

// detailPage shows task id in full (SPEC §15.3): its text, status, rank,
// owner, mode, dependencies with their states and the extra columns. It
// reads the plan as last loaded each time it is drawn.
func (m *model) detailPage(id string) *page {
	return &page{title: id + " · esc closes", body: func(w int) []string {
		var t *plan.Task
		if m.plan != nil {
			t = m.plan.Task(id)
		}
		if t == nil {
			return wrap(id+" is no longer in the plan.", w)
		}
		out := wrap(t.ID+" — "+t.Title, w)
		out = append(out, "")
		out = append(out, wrap(strings.ReplaceAll(t.Text, "**", ""), w)...)
		out = append(out, "")
		return append(out, fields(m.taskFacts(t), w)...)
	}}
}

// fact is a labelled line of the details; more lines follow under it.
type fact struct {
	name  string
	lines []string
}

// taskFacts are the details of t below its text.
func (m *model) taskFacts(t *plan.Task) []fact {
	owner := t.OwnerText
	if owner == "" {
		owner = "—"
	}
	facts := []fact{
		{"Status", []string{m.glyph(t) + " " + statusWord(t)}},
		{"Rank", []string{rank(t)}},
		{"Owner", []string{owner}},
		{"Mode", []string{m.modeText(t)}},
	}
	if t.Phase != nil {
		facts = append(facts, fact{"Phase", []string{strings.TrimSpace(t.Phase.ID + " " + t.Phase.Title)}})
	}
	deps := fact{name: "Deps"}
	for _, d := range t.Deps {
		dt := m.plan.Task(d)
		if dt == nil {
			deps.lines = append(deps.lines, "? "+d+" (not in the plan)")
			continue
		}
		deps.lines = append(deps.lines, m.glyph(dt)+" "+d+" "+statusWord(dt)+" · "+dt.Title)
	}
	if len(deps.lines) == 0 {
		deps.lines = []string{"none"}
	}
	facts = append(facts, deps)
	if wt := m.plan.WaitingOn(t); wt != nil && t.Status != plan.Done && t.Status != plan.Skipped {
		facts = append(facts, fact{"Waits on", []string{strings.Join(wt.Unmet, ", ")}})
	}
	if t.Phase != nil {
		for _, col := range t.Phase.Columns {
			if v, ok := t.Extra[col]; ok {
				facts = append(facts, fact{col, []string{v}})
			}
		}
	}
	return facts
}

// statusWord is t's Status cell as written, or its status by name.
func statusWord(t *plan.Task) string {
	if s := strings.TrimSpace(t.StatusText); s != "" {
		return s
	}
	return t.Status.String()
}

// modeText says which mode t's next session runs in, and why (SPEC §7.2).
func (m *model) modeText(t *plan.Task) string {
	mode, why := m.taskMode(t), "run mode"
	switch {
	case m.overrides[t.ID] != "":
		why = "your override"
	case t.Mode != "":
		why = "Mode column"
	case mode == "":
		mode = "default"
	}
	return mode + badge(mode) + " (" + why + ")"
}

// fields lays facts out as "Name   value" with the values aligned and
// wrapped under themselves.
func fields(facts []fact, w int) []string {
	nameW := 0
	for _, f := range facts {
		nameW = max(nameW, textWidth(f.name))
	}
	var out []string
	for _, f := range facts {
		for i, l := range f.lines {
			name := ""
			if i == 0 {
				name = f.name
			}
			out = append(out, hang(pad(name, nameW)+"  ", l, w)...)
		}
	}
	return out
}

// logPage shows the whole log kept in memory, newest at the bottom.
func (m *model) logPage() *page {
	return &page{title: "Log · esc closes", top: maxTop, body: func(w int) []string {
		var out []string
		for _, e := range m.log {
			out = append(out, hang(e.at.In(m.loc).Format("15:04")+" ", e.text, w)...)
		}
		if len(out) == 0 {
			out = []string{"nothing logged yet"}
		}
		return out
	}}
}
