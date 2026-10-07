package plan

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/drilonrecica/igris/internal/textsafe"
)

// Canonical column names (SPEC §3.2).
const (
	ColID     = "ID"
	ColTask   = "Task"
	ColDeps   = "Deps"
	ColStatus = "Status"
	ColModel  = "Model"
	ColOwner  = "Owner"
	ColMode   = "Mode"
)

var canonicalCols = []string{ColID, ColTask, ColDeps, ColStatus, ColModel, ColOwner, ColMode}

// Options control parsing.
type Options struct {
	// Columns maps header aliases to canonical column names (config
	// [columns]), e.g. "Depends on" -> "Deps". Matching is case-insensitive.
	Columns map[string]string
}

// Issue is one problem found in the plan, tied to a line.
type Issue struct {
	File string
	Line int
	Msg  string
}

func (i Issue) Error() string {
	if i.Line == 0 {
		return fmt.Sprintf("%s: %s", i.File, i.Msg)
	}
	return fmt.Sprintf("%s:%d: %s", i.File, i.Line, i.Msg)
}

// Plan is a parsed plan file.
type Plan struct {
	Path   string   // file name used in messages
	Phases []*Phase // phases with a task table, in file order
	Tasks  []*Task  // all tasks in file order

	lines    []line
	issues   []Issue    // structural problems found while parsing
	nearMiss []nearMiss // tables with an ID column that lack Status or Model
	byID     map[string]*Task
}

// Phase is a "##" section holding one task table.
type Phase struct {
	ID      string   // e.g. "M0", "Phase-2"
	Title   string   // heading text after the ID, e.g. "Repository foundation"
	Heading string   // full heading text
	Line    int      // line of the heading
	Columns []string // table header in order; canonical names where recognized
	Tasks   []*Task  // in file order

	tableLine int // line of the table header
	rows      []row
}

// nearMiss is a table that looks like a task table but lacks the Status or
// the Model column, e.g. one with "State" instead of "Status".
type nearMiss struct {
	line      int
	hasStatus bool
}

// row is one data row of a task table.
type row struct {
	line  line
	cells []cell
}

// Phase returns the phase with the given ID (case-insensitive), or nil.
func (p *Plan) Phase(id string) *Phase {
	for _, ph := range p.Phases {
		if strings.EqualFold(ph.ID, id) {
			return ph
		}
	}
	return nil
}

func (p *Plan) addIssue(line int, format string, a ...any) {
	p.issues = append(p.issues, Issue{File: p.Path, Line: line, Msg: fmt.Sprintf(format, a...)})
}

// section is a "##" heading while scanning.
type section struct {
	phase      *Phase
	tableFound bool
	control    bool // the heading line holds a control character
	subLine    int  // line of the last "###"-or-deeper heading in the section
}

// Parse parses data. It never fails outright: structural problems are
// recorded and reported by Validate, so every problem is shown at once.
// name is used in messages.
func Parse(name string, data []byte, opts Options) *Plan {
	p := &Plan{Path: name, lines: splitLines(data)}
	aliases := make(map[string]string, len(opts.Columns))
	for k, v := range opts.Columns {
		aliases[strings.ToLower(normalizeHeader(k))] = v
	}

	var cur *section
	var fence string
	firstPhase := map[string]int{} // lower-case phase ID -> heading line
	for i := 0; i < len(p.lines); i++ {
		l := p.lines[i]
		if fence != "" {
			if isFenceClose(l.text, fence) {
				fence = ""
			}
			continue
		}
		if f := fenceOpen(l.text); f != "" {
			fence = f
			continue
		}
		if text, ok := headingText(l.text); ok {
			cur = &section{phase: newPhase(textsafe.Line(text), l.num), control: textsafe.HasControl(l.text)}
			continue
		}
		if cur != nil && strings.HasPrefix(trimIndent(l.text), "###") {
			cur.subLine = l.num
		}
		if !isTableStart(p.lines, i) {
			continue
		}

		header := splitRow(l.text)
		cols, isTask := p.columns(header, aliases, l.num)
		end := i + 2
		for end < len(p.lines) && strings.TrimSpace(p.lines[end].text) != "" && hasPipe(p.lines[end].text) {
			end++
		}
		if isTask {
			p.addTable(cur, cols, l.num, p.lines[i+2:end], firstPhase)
		} else if hasCol(cols, ColID) && (hasCol(cols, ColStatus) || hasCol(cols, ColModel)) {
			p.nearMiss = append(p.nearMiss, nearMiss{line: l.num, hasStatus: hasCol(cols, ColStatus)})
		}
		i = end - 1
	}
	p.buildTasks()
	p.resolveDeps()
	return p
}

// addTable attaches a task table to the current section.
func (p *Plan) addTable(cur *section, cols []string, at int, rows []line, firstPhase map[string]int) {
	switch {
	case cur == nil:
		p.addIssue(at, "task table outside a phase; put it under a \"## <phase ID> — <title>\" heading")
		return
	case cur.phase.ID == "":
		p.addIssue(at, "task table under an empty \"##\" heading; give the heading a phase ID")
		return
	case cur.tableFound:
		if cur.subLine > cur.phase.tableLine {
			// "### M1 — …" sub-sections under one "## V1" heading.
			p.addIssue(at, "phase %s has a second task table (first at line %d); only \"##\" headings start a phase, so make the heading at line %d a \"##\" heading", cur.phase.ID, cur.phase.tableLine, cur.subLine)
			return
		}
		p.addIssue(at, "phase %s has a second task table (first at line %d); a phase may contain only one", cur.phase.ID, cur.phase.tableLine)
		return
	}
	cur.tableFound = true
	ph := cur.phase
	if cur.control {
		p.addIssue(ph.Line, "the heading of phase %s contains a control character (an escape sequence?); remove it", ph.ID)
	}
	ph.Columns = cols
	ph.tableLine = at
	for _, l := range rows {
		ph.rows = append(ph.rows, row{line: l, cells: splitRow(l.text)})
	}
	key := strings.ToLower(ph.ID)
	if first, dup := firstPhase[key]; dup {
		p.addIssue(ph.Line, "duplicate phase ID %s (first at line %d); phase IDs must be unique", ph.ID, first)
		return
	}
	firstPhase[key] = ph.Line
	p.Phases = append(p.Phases, ph)
}

// columns maps header cells to canonical names (aliases applied) and reports
// whether the table is a task table (has ID, Status and Model).
func (p *Plan) columns(header []cell, aliases map[string]string, at int) ([]string, bool) {
	cols := make([]string, len(header))
	seen := map[string]bool{}
	for i, h := range header {
		name := normalizeHeader(h.value)
		if target, ok := aliases[strings.ToLower(name)]; ok {
			name = target
		}
		if c := canonical(name); c != "" {
			name = c
		}
		cols[i] = name
	}
	isTask := hasCol(cols, ColID) && hasCol(cols, ColStatus) && hasCol(cols, ColModel)
	if isTask {
		for _, c := range cols {
			if canonical(c) != "" && seen[c] {
				p.addIssue(at, "column %s appears more than once in the table header", c)
			}
			seen[c] = true
		}
	}
	return cols, isTask
}

func hasCol(cols []string, name string) bool {
	for _, c := range cols {
		if c == name {
			return true
		}
	}
	return false
}

// canonical returns the canonical column name matching name
// case-insensitively, or "".
func canonical(name string) string {
	for _, c := range canonicalCols {
		if strings.EqualFold(c, name) {
			return c
		}
	}
	return ""
}

// normalizeHeader strips surrounding whitespace, '*' and backticks.
func normalizeHeader(s string) string {
	return strings.Trim(s, " \t*`")
}

// headingText returns the text of a level-2 ATX heading ("## text").
func headingText(s string) (string, bool) {
	s = trimIndent(s)
	rest, ok := strings.CutPrefix(s, "##")
	if !ok || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	// An optional closing sequence of '#' is not part of the text.
	if t := strings.TrimRight(rest, "#"); t != rest && (t == "" || strings.HasSuffix(t, " ") || strings.HasSuffix(t, "\t")) {
		rest = strings.TrimSpace(t)
	}
	return rest, true
}

// newPhase derives the phase ID and title from heading text (SPEC §3.1).
func newPhase(text string, num int) *Phase {
	ph := &Phase{Heading: text, Line: num}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ph
	}
	n := 1
	ph.ID = fields[0]
	if len(fields) > 1 && isAlpha(fields[0]) && isDigits(fields[1]) {
		ph.ID = fields[0] + "-" + fields[1]
		n = 2
	}
	rest := text
	for range n {
		rest = strings.TrimLeft(rest, " \t")
		rest = rest[strings.IndexAny(rest+" ", " \t"):]
	}
	ph.Title = strings.TrimLeft(rest, " \t—–-:·")
	return ph
}

func isAlpha(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return s != ""
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// trimIndent removes up to three leading spaces (CommonMark block indent).
func trimIndent(s string) string {
	for i := 0; i < 3 && strings.HasPrefix(s, " "); i++ {
		s = s[1:]
	}
	return s
}

// fenceOpen returns the opening fence ("```", "~~~~", …) if s starts a fenced
// code block.
func fenceOpen(s string) string {
	s = trimIndent(s)
	for _, ch := range []byte{'`', '~'} {
		n := 0
		for n < len(s) && s[n] == ch {
			n++
		}
		if n >= 3 {
			if ch == '`' && strings.Contains(s[n:], "`") {
				return "" // backtick fences can't have backticks in the info string
			}
			return s[:n]
		}
	}
	return ""
}

// isFenceClose reports whether s closes a block opened with fence.
func isFenceClose(s, fence string) bool {
	s = strings.TrimSpace(trimIndent(s))
	return len(s) >= len(fence) && strings.Trim(s, fence[:1]) == ""
}
