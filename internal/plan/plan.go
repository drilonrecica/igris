package plan

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	// Optional, added in v0.4 (SPEC §3.2).
	ColVerify  = "Verify"
	ColTimeout = "Timeout"
	ColContext = "Context"
)

// FormatVersion is the newest plan format igris reads: formats 1 and 2
// (SPEC §3, §19). Format 2 is format 1 plus includes (§3.7).
const FormatVersion = 2

// formatMarker matches a format marker line, `<!-- igris:format 1 -->`;
// the group is what follows "igris:format".
var formatMarker = regexp.MustCompile(`^<!--\s*igris:format\b(.*)-->$`)

// includeLine matches an include line, `<!-- igris:include phases/v2.md -->`
// (SPEC §3.7); the group is what follows "igris:include".
var includeLine = regexp.MustCompile(`^<!--\s*igris:include\b(.*)-->$`)

var canonicalCols = []string{ColID, ColTask, ColDeps, ColStatus, ColModel, ColOwner, ColMode, ColVerify, ColTimeout, ColContext}

// Options control parsing.
type Options struct {
	// Columns maps header aliases to canonical column names (config
	// [columns]), e.g. "Depends on" -> "Deps". Matching is case-insensitive.
	Columns map[string]string
	// Root is the directory included files must stay inside (SPEC §3.7),
	// the same root as Rules.Root: the project root for arise and home,
	// the working directory for check, phases and status. "" is the
	// working directory.
	Root string
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

// Plan is a parsed plan: the main plan file and the files it includes
// (SPEC §3.7).
type Plan struct {
	Path   string   // the main plan's file name used in messages
	Phases []*Phase // phases with a task table, in plan order
	Tasks  []*Task  // all tasks in plan order
	// Format is the plan format the main plan declares with a format
	// marker (SPEC §3), or 1 when it declares none.
	Format int

	files    []*planFile // the main plan first, then the included files in plan order
	includes []include   // the main plan's include lines
	issues   []Issue     // structural problems found while parsing
	nearMiss []nearMiss  // tables with an ID column that lack Status or Model
	// newerFormat is the issue for a format this igris doesn't read; then
	// it is the plan's only issue.
	newerFormat *Issue
	byID        map[string]*Task
}

// Phase is a "##" section holding one task table.
type Phase struct {
	File    string   // name of the file the phase is in, as in messages
	ID      string   // e.g. "M0", "Phase-2"
	Title   string   // heading text after the ID, e.g. "Repository foundation"
	Heading string   // full heading text
	Line    int      // line of the heading
	Columns []string // table header in order; canonical names where recognized
	Header  []string // table header cells as written (emphasis removed), in the order of Columns
	Tasks   []*Task  // in file order
	// Include is the include path as written (SPEC §3.7) for a phase of
	// an included file, "" for one in the main plan.
	Include string

	file      *planFile
	tableLine int // line of the table header
	rows      []row
}

// nearMiss is a table that looks like a task table but lacks the Status or
// the Model column, e.g. one with "State" instead of "Status".
type nearMiss struct {
	file      string
	line      int
	hasStatus bool
}

// planFile is one file of the plan.
type planFile struct {
	name    string // as in messages: the main plan's path, or its directory joined with the include path
	lines   []line
	incLine int    // the main plan's line that includes the file; 0 for the main plan
	inc     string // the include path as written; "" for the main plan
	real    string // an included file's path with symlinks resolved, as read
	data    []byte // the file as read

	markers []int  // lines holding a format marker
	fence   string // the fence still open at the end of the file, "" for none
}

// include is an include line of the main plan (SPEC §3.7).
type include struct {
	line int
	path string // as written, trimmed
	name string // the main plan's directory joined with path
}

// row is one data row of a task table.
type row struct {
	line  line
	cells []cell
}

// TableLines returns the first and the last line of the phase's task
// table: the header, through the last row (or the delimiter row).
func (ph *Phase) TableLines() (first, last int) {
	last = ph.tableLine + 1
	if n := len(ph.rows); n > 0 {
		last = ph.rows[n-1].line.num
	}
	return ph.tableLine, last
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

// Files lists the names of the plan's files as messages show them: the
// main plan first, then each file it includes, in plan order (SPEC §3.7).
func (p *Plan) Files() []string {
	out := make([]string, len(p.files))
	for i, f := range p.files {
		out[i] = f.name
	}
	return out
}

// Split reports whether the main plan has include lines.
func (p *Plan) Split() bool { return len(p.includes) > 0 }

// Include is an include line of the main plan (SPEC §3.7).
type Include struct {
	Line int
	Path string // as written, trimmed
}

// Includes lists the main plan's include lines in file order, also those
// that were not read.
func (p *Plan) Includes() []Include {
	out := make([]Include, len(p.includes))
	for i, inc := range p.includes {
		out[i] = Include{Line: inc.line, Path: inc.path}
	}
	return out
}

func (p *Plan) addIssue(file string, line int, format string, a ...any) {
	p.issues = append(p.issues, Issue{File: file, Line: line, Msg: fmt.Sprintf(format, a...)})
}

// sortIssues sorts issues in plan order (SPEC §3.7).
func (p *Plan) sortIssues(issues []Issue) {
	less := p.issueOrder()
	sort.SliceStable(issues, func(i, j int) bool { return less(issues[i], issues[j]) })
}

// issueOrder returns the plan order of issues: by line, a line of an
// included file taking the place of the include line, after that line's
// own issues. A file the plan doesn't know sorts as the main plan.
func (p *Plan) issueOrder() func(a, b Issue) bool {
	inc := make(map[string]int, len(p.files))
	for _, f := range p.files[min(1, len(p.files)):] {
		inc[f.name] = f.incLine
	}
	key := func(is Issue) (int, int) {
		if at, ok := inc[is.File]; ok {
			return at, is.Line
		}
		return is.Line, -1
	}
	return func(a, b Issue) bool {
		a1, a2 := key(a)
		b1, b2 := key(b)
		return a1 < b1 || a1 == b1 && a2 < b2
	}
}

// section is a "##" heading while scanning.
type section struct {
	phase        *Phase // nil after an include line
	afterInclude int    // the include line that ended the section above
	tableFound   bool
	control      bool // the heading line holds a control character
	subLine      int  // line of the last "###"-or-deeper heading in the section
}

// Parse parses data as a plan of one file. It never fails outright:
// structural problems are recorded and reported by Validate, so every
// problem is shown at once. name is used in messages. Parse reads no other
// file, so an include line is an issue; Load reads a split plan.
func Parse(name string, data []byte, opts Options) *Plan {
	return parse(name, data, opts, nil)
}

// parse parses the main plan and, with inc, the files it includes.
func parse(name string, data []byte, opts Options, inc *includer) *Plan {
	p := &Plan{Path: name, Format: 1}
	aliases := make(map[string]string, len(opts.Columns))
	for k, v := range opts.Columns {
		aliases[strings.ToLower(normalizeHeader(k))] = v
	}
	main := &planFile{name: name, lines: splitLines(data), data: data}
	p.files = []*planFile{main}
	items := (&scanner{p: p, f: main, aliases: aliases}).scan()

	var phases []*Phase
	for _, it := range items {
		if it.phase != nil {
			phases = append(phases, it.phase)
			continue
		}
		if p.newerFormat != nil {
			continue // a newer format's includes may mean something else
		}
		if p.Format != 2 {
			p.addIssue(name, it.inc.line, "an include needs plan format 2; add <!-- igris:format 2 --> on a line of its own before the first ## heading")
		}
		if inc == nil {
			p.addIssue(name, it.inc.line, "includes are not read here; inline the included file")
			continue
		}
		f, msg := inc.read(p, it.inc)
		if msg != "" {
			p.addIssue(name, it.inc.line, "%s", msg)
			continue
		}
		p.files = append(p.files, f)
		for _, sub := range (&scanner{p: p, f: f, aliases: aliases, included: true}).scan() {
			phases = append(phases, sub.phase)
		}
	}
	p.addPhases(phases)
	p.buildTasks()
	p.resolveDeps()
	return p
}

// item is a phase, or an include line of the main plan, in file order.
type item struct {
	phase *Phase
	inc   *include
}

// scanner reads the phases of one file of the plan.
type scanner struct {
	p        *Plan
	f        *planFile
	aliases  map[string]string
	included bool // an included file: no includes, its marker isn't the plan's

	markerLine int // line of the format marker, 0 for none
}

func (s *scanner) addIssue(line int, format string, a ...any) {
	s.p.addIssue(s.f.name, line, format, a...)
}

// scan returns the file's phases and include lines in file order.
func (s *scanner) scan() []item {
	var items []item
	var cur *section
	var fence string
	lines := s.f.lines
	for i := 0; i < len(lines); i++ {
		l := lines[i]
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
		trimmed := strings.TrimSpace(l.text)
		if m := formatMarker.FindStringSubmatch(trimmed); m != nil {
			s.f.markers = append(s.f.markers, l.num)
			s.formatMarker(cur != nil, m[1], l.num)
			continue
		}
		if m := includeLine.FindStringSubmatch(trimmed); m != nil {
			if s.included {
				s.addIssue(l.num, "only the main plan may include files; move this include to %s", s.p.Path)
				continue
			}
			inc := &include{line: l.num, path: strings.TrimSpace(m[1])}
			inc.name = filepath.Join(filepath.Dir(s.p.Path), filepath.FromSlash(inc.path))
			s.p.includes = append(s.p.includes, *inc)
			items = append(items, item{inc: inc})
			cur = &section{afterInclude: l.num}
			continue
		}
		if text, ok := headingText(l.text); ok {
			ph := newPhase(textsafe.Line(text), l.num)
			ph.File, ph.Include, ph.file = s.f.name, s.f.inc, s.f
			cur = &section{phase: ph, control: textsafe.HasControl(l.text)}
			continue
		}
		if cur != nil && strings.HasPrefix(trimIndent(l.text), "###") {
			cur.subLine = l.num
		}
		if !isTableStart(lines, i) {
			continue
		}

		header := splitRow(l.text)
		cols, isTask := s.columns(header, l.num)
		end := i + 2
		for end < len(lines) && strings.TrimSpace(lines[end].text) != "" && hasPipe(lines[end].text) {
			end++
		}
		if isTask {
			if ph := s.addTable(cur, header, cols, l.num, lines[i+2:end]); ph != nil {
				items = append(items, item{phase: ph})
			}
		} else if hasCol(cols, ColID) && (hasCol(cols, ColStatus) || hasCol(cols, ColModel)) {
			s.p.nearMiss = append(s.p.nearMiss, nearMiss{file: s.f.name, line: l.num, hasStatus: hasCol(cols, ColStatus)})
		}
		i = end - 1
	}
	s.f.fence = fence
	return items
}

// NewerFormat says the plan declares a format this igris doesn't read, so
// the fix is a newer igris, not an edit or `igris adapt`.
func (p *Plan) NewerFormat() bool { return p.newerFormat != nil }

// formatMarker reads a format marker line (SPEC §3): one per file, before
// its first "##" heading, naming a whole number. The main plan's marker is
// the plan's format; a format this igris doesn't read is recorded as the
// plan's only issue. An included file may declare format 1 or 2 (§3.7).
func (s *scanner) formatMarker(afterHeading bool, arg string, num int) {
	switch {
	case afterHeading:
		s.addIssue(num, "the format marker must come before the first ## heading")
		return
	case s.markerLine != 0:
		s.addIssue(num, "a second format marker (the first is on line %d)", s.markerLine)
		return
	}
	s.markerLine = num
	arg = strings.TrimSpace(arg)
	n, err := strconv.Atoi(arg)
	if !isDigits(arg) || err != nil || n < 1 {
		s.addIssue(num, "the format marker needs a whole number, e.g. <!-- igris:format 1 -->")
		return
	}
	switch {
	case s.included && n > FormatVersion:
		s.addIssue(num, "an included file may declare format 1 or 2, not %d; the main plan's marker is the plan's format", n)
	case s.included:
	case n > FormatVersion:
		s.p.Format = n
		s.p.newerFormat = &Issue{File: s.f.name, Line: num, Msg: fmt.Sprintf("plan format %d needs a newer igris (this igris reads formats 1 and 2)", n)}
	default:
		s.p.Format = n
	}
}

// addPhases sets the plan's phases from the phases of every file in plan
// order, leaving out a phase whose ID an earlier one has (SPEC §3.1, §3.7).
func (p *Plan) addPhases(phases []*Phase) {
	first := map[string]*Phase{} // lower-case phase ID -> its first phase
	for _, ph := range phases {
		key := strings.ToLower(ph.ID)
		if f, dup := first[key]; dup {
			if f.File == ph.File {
				p.addIssue(ph.File, ph.Line, "duplicate phase ID %s (first at line %d); phase IDs must be unique", ph.ID, f.Line)
			} else {
				p.addIssue(ph.File, ph.Line, "phase %s is also in %s:%d; phase IDs must be unique across the plan's files", ph.ID, f.File, f.Line)
			}
			continue
		}
		first[key] = ph
		p.Phases = append(p.Phases, ph)
	}
}

// addTable attaches a task table to the current section and returns its
// phase, or nil if the table belongs to none.
func (s *scanner) addTable(cur *section, header []cell, cols []string, at int, rows []line) *Phase {
	switch {
	case cur == nil:
		s.addIssue(at, "task table outside a phase; put it under a \"## <phase ID> — <title>\" heading")
		return nil
	case cur.phase == nil:
		s.addIssue(at, "task table after the include line at line %d, which ends the section above it; put the table under a \"## <phase ID> — <title>\" heading", cur.afterInclude)
		return nil
	case cur.phase.ID == "":
		s.addIssue(at, "task table under an empty \"##\" heading; give the heading a phase ID")
		return nil
	case cur.tableFound:
		if cur.subLine > cur.phase.tableLine {
			// "### M1 — …" sub-sections under one "## V1" heading.
			s.addIssue(at, "phase %s has a second task table (first at line %d); only \"##\" headings start a phase, so make the heading at line %d a \"##\" heading", cur.phase.ID, cur.phase.tableLine, cur.subLine)
			return nil
		}
		s.addIssue(at, "phase %s has a second task table (first at line %d); a phase may contain only one", cur.phase.ID, cur.phase.tableLine)
		return nil
	}
	cur.tableFound = true
	ph := cur.phase
	if cur.control {
		s.addIssue(ph.Line, "the heading of phase %s contains a control character (an escape sequence?); remove it", ph.ID)
	}
	ph.Columns = cols
	for _, h := range header {
		ph.Header = append(ph.Header, normalizeHeader(h.value))
	}
	ph.tableLine = at
	for _, l := range rows {
		ph.rows = append(ph.rows, row{line: l, cells: splitRow(l.text)})
	}
	return ph
}

// columns maps header cells to canonical names (aliases applied) and reports
// whether the table is a task table (has ID, Status and Model).
func (s *scanner) columns(header []cell, at int) ([]string, bool) {
	cols := make([]string, len(header))
	seen := map[string]bool{}
	for i, h := range header {
		name := normalizeHeader(h.value)
		if target, ok := s.aliases[strings.ToLower(name)]; ok {
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
				s.addIssue(at, "column %s appears more than once in the table header", c)
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
