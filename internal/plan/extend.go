package plan

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/drilonrecica/igris/internal/textsafe"
)

// Extension is an `igris draft extend` fragment spliced into a plan in
// memory (SPEC §9.1): what it adds, the extended plan, and the new content
// of each file it changes. Nothing is written before Write.
type Extension struct {
	// Plan is the extended plan, parsed from the new content.
	Plan *Plan
	// Additions are the fragment's phases in its order: rows for a phase
	// of the plan, or a new phase.
	Additions []Addition
	// Notes are the lines of the fragment's `## Draft notes`, shown in the
	// review and not written to the plan.
	Notes []string

	path  string                          // the main plan's path
	base  *Plan                           // the plan as read
	sums  map[*planFile][sha256.Size]byte // what each file of base held
	edits []edit                          // the files of base that change, in plan order

	beforeRename func() // test hook: runs after the temp files are written
}

// Addition is one phase of the fragment.
type Addition struct {
	File    string // the plan file it goes into, as messages name it
	Phase   string // phase ID
	Heading string // the heading: the plan's for a phase it adds to
	New     bool   // a new phase, appended to the main plan
	// Tasks are the new tasks in the extended plan, in the fragment's
	// order.
	Tasks []*Task
	// Prose is a new phase's lines outside its heading and task table.
	Prose []string
}

// FileChange is one file of the plan the extension changes.
type FileChange struct {
	Name     string // as messages name it
	Old, New []byte
}

// Files lists the files the extension changes, in plan order.
func (x *Extension) Files() []FileChange {
	out := make([]FileChange, len(x.edits))
	for i, e := range x.edits {
		out[i] = FileChange{Name: e.f.name, Old: e.f.data, New: e.out}
	}
	return out
}

// draftNotes is the heading of the fragment's notes section (SPEC §9.1).
const draftNotes = "draft notes"

// Extend checks fragment, the new phases and tasks a draft session wrote
// for the plan p, and splices it into p's files in memory (SPEC §9.1). p
// must have been read with Load and opts and pass validation. name is the
// fragment's name in messages.
//
// A `##` section whose phase ID is in p adds its rows to that phase's
// table; any other section with a task table is a new phase. The fragment
// is refused, with its problems, when a new task ID is already taken, a
// new task's Status is not ready or blocked, a row has a column its
// phase's table lacks, or a dependency names a task that is neither in p
// nor in the fragment: igris never offers to write those.
func Extend(p *Plan, opts Options, name string, fragment []byte) (*Extension, []Issue) {
	fp := parseTables(name, fragment, opts, nil, p.Format)
	c := &fragmentCheck{p: p, fp: fp, file: fp.files[0]}
	c.issues = append(c.issues, fp.issues...)
	if fp.newerFormat != nil {
		c.issues = append(c.issues, *fp.newerFormat)
	}
	c.sections()
	c.tasks()
	if len(c.issues) > 0 {
		sort.SliceStable(c.issues, func(i, j int) bool { return c.issues[i].Line < c.issues[j].Line })
		return nil, c.issues
	}
	x, err := c.splice(opts)
	if err != "" {
		return nil, []Issue{{File: p.Path, Msg: err}}
	}
	return x, nil
}

// fragmentCheck checks a fragment against the plan it extends.
type fragmentCheck struct {
	p      *Plan
	fp     *Plan     // the fragment, its Deps cells not resolved
	file   *planFile // the fragment's only file
	issues []Issue

	notes []string
	prose map[*Phase][]string // a new phase's lines outside its heading and table
}

func (c *fragmentCheck) add(line int, format string, a ...any) {
	c.issues = append(c.issues, Issue{File: c.file.name, Line: line, Msg: fmt.Sprintf(format, a...)})
}

// sections reads the fragment's `##` sections: each a phase with a task
// table, or `## Draft notes`. Text before the first one is ignored.
func (c *fragmentCheck) sections() {
	byLine := map[int]*Phase{}
	for _, ph := range c.fp.Phases {
		byLine[ph.Line] = ph
	}
	reported := map[int]bool{}
	for _, is := range c.issues {
		reported[is.Line] = true
	}
	c.prose = map[*Phase][]string{}
	lines := c.file.lines
	var starts []int // indexes of the "##" heading lines, outside fences
	var fence string
	for i, l := range lines {
		switch {
		case fence != "":
			if isFenceClose(l.text, fence) {
				fence = ""
			}
		case fenceOpen(l.text) != "":
			fence = fenceOpen(l.text)
		default:
			if _, ok := headingText(l.text); ok {
				starts = append(starts, i)
			}
		}
	}
	for k, at := range starts {
		end := len(lines)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		body := trimTrailingBlank(lines[at+1 : end])
		head := lines[at]
		text, _ := headingText(head.text)
		ph := byLine[head.num]
		switch {
		case ph != nil:
			first, last := ph.TableLines()
			var prose []string
			for _, l := range body {
				if l.num < first || l.num > last {
					prose = append(prose, l.text)
				}
			}
			c.prose[ph] = prose
		case strings.EqualFold(strings.TrimSpace(text), draftNotes):
			for _, l := range trimLeadingBlank(body) {
				c.notes = append(c.notes, l.text)
			}
		case !reported[head.num]:
			c.add(head.num, "section %q has no task table; the fragment holds only ## phase sections with task tables, and ## Draft notes", textsafe.Line(text))
		}
	}
	if len(c.fp.Phases) == 0 && len(c.issues) == 0 {
		c.add(0, "the fragment adds no tasks; write ## phase sections with task tables")
	}
}

// tasks checks the fragment's phases and tasks against the plan.
func (c *fragmentCheck) tasks() {
	ids := map[string]bool{}
	for _, t := range c.p.Tasks {
		ids[t.ID] = true
	}
	first := map[string]*Task{}
	for _, t := range c.fp.Tasks {
		if _, dup := first[t.ID]; !dup {
			first[t.ID] = t
		}
	}
	for _, ph := range c.fp.Phases {
		target := c.p.Phase(ph.ID)
		if len(ph.Tasks) == 0 {
			c.add(ph.Line, "phase %s adds no tasks; give its table rows, or leave the section out", ph.ID)
		}
		if target != nil {
			if ph.Parallel && !target.Parallel {
				c.add(ph.Line, "phase %s is not parallel in the plan, and the fragment only adds rows to it; leave the parallel marker out", ph.ID)
			}
			for _, col := range ph.Columns {
				if colIndex(target.Columns, col) < 0 {
					c.add(ph.tableLine, "column %s is not in the table of phase %s (%s:%d); leave it out, since rows are written in that table's columns", col, ph.ID, target.File, target.tableLine)
				}
			}
		} else {
			for _, l := range c.prose[ph] {
				if textsafe.HasControl(l) {
					c.add(ph.Line, "the section of phase %s contains a control character (an escape sequence?); remove it", ph.ID)
					break
				}
			}
		}
		for _, t := range ph.Tasks {
			c.task(t, ids, first)
		}
	}
	if c.p.files[0].fence != "" && c.newPhases() {
		c.issues = append(c.issues, Issue{File: c.p.Path, Msg: "the plan ends inside a fenced code block, where new phases would be hidden; close the block, then extend the plan"})
	}
}

// task checks one new task: a free ID, ready or blocked, deps that exist.
func (c *fragmentCheck) task(t *Task, ids map[string]bool, first map[string]*Task) {
	switch old := c.p.Task(t.ID); {
	case !ValidID(t.ID):
		c.add(t.Line, "%q is not a valid task ID; use letters, digits, '.', '_' and '-', starting with a letter or digit", t.ID)
		return
	case old != nil:
		c.add(t.Line, "task %s is already in the plan (%s:%d); give the new task an ID the plan doesn't use", t.ID, old.Phase.File, old.Line)
	case first[t.ID] != t:
		c.add(t.Line, "task %s is in the fragment twice (first at line %d)", t.ID, first[t.ID].Line)
	}
	if t.Status != Ready && t.Status != Blocked {
		c.add(t.Line, "task %s has Status %q; a new task is ready or blocked", t.ID, t.StatusText)
	}
	if isNone(t.DepsText) {
		return
	}
	known := func(id string) bool { return ids[id] || first[id] != nil }
	for _, tok := range strings.Split(t.DepsText, ",") {
		tok = strings.Trim(tok, " \t`")
		if tok == "" || known(tok) {
			continue
		}
		ends := []string{tok}
		if from, to, isRange := splitRange(tok, nil); isRange {
			if from == "" || to == "" {
				continue // malformed: the extended plan's validation reports it
			}
			ends = []string{from, to}
		}
		for _, d := range ends {
			if !known(d) {
				c.add(t.Line, "task %s depends on %s, which is neither in the plan nor in the fragment", t.ID, d)
			}
		}
	}
}

// newPhases reports whether the fragment has a phase the plan doesn't.
func (c *fragmentCheck) newPhases() bool {
	for _, ph := range c.fp.Phases {
		if c.p.Phase(ph.ID) == nil {
			return true
		}
	}
	return false
}

// insert is bytes put into a file at an offset.
type insert struct {
	at   int
	text string
}

// splice builds the extended files and parses them. It returns a message
// when the result isn't the plan with the fragment's tasks added.
func (c *fragmentCheck) splice(opts Options) (*Extension, string) {
	p := c.p
	main := p.files[0]
	inserts := map[*planFile][]insert{}
	var appends []string // new phases' lines
	x := &Extension{path: p.Path, base: p, Notes: c.notes, sums: map[*planFile][sha256.Size]byte{}}
	for _, ph := range c.fp.Phases {
		target := p.Phase(ph.ID)
		if target == nil {
			if len(appends) > 0 {
				appends = append(appends, "")
			}
			// The section as written: heading, prose and table.
			for _, l := range trimTrailingBlank(c.file.lines[ph.Line-1 : sectionEnd(c.file, ph)]) {
				appends = append(appends, l.text)
			}
			x.Additions = append(x.Additions, Addition{File: main.name, Phase: ph.ID, Heading: ph.Heading, New: true, Prose: c.prose[ph]})
			continue
		}
		f := target.file
		eol := lineEnding(f)
		_, lastNum := target.TableLines()
		l := f.lines[lastNum-1]
		var b strings.Builder
		for _, r := range ph.rows {
			b.WriteString(eol)
			b.WriteString(renderRow(target, ph, r))
		}
		inserts[f] = append(inserts[f], insert{at: l.start + len(l.text), text: b.String()})
		x.Additions = append(x.Additions, Addition{File: f.name, Phase: target.ID, Heading: target.Heading})
	}

	out := map[*planFile][]byte{}
	for _, f := range p.files {
		x.sums[f] = sha256.Sum256(f.data)
		ins := inserts[f]
		if len(ins) == 0 && (f != main || len(appends) == 0) {
			continue
		}
		sort.Slice(ins, func(i, j int) bool { return ins[i].at < ins[j].at })
		var b bytes.Buffer
		pos := 0
		for _, in := range ins {
			b.Write(f.data[pos:in.at])
			b.WriteString(in.text)
			pos = in.at
		}
		b.Write(f.data[pos:])
		if f == main && len(appends) > 0 {
			appendSections(&b, appends, lineEnding(f))
		}
		out[f] = b.Bytes()
		x.edits = append(x.edits, edit{f: f, out: out[f]})
	}

	total := 0
	for _, f := range p.files {
		if d, ok := out[f]; ok {
			total += len(d)
		} else {
			total += len(f.data)
		}
	}
	if total > MaxSize {
		return nil, fmt.Sprintf("the extended plan would be over %d MiB, more than igris reads; add less, or split the plan (§3.7)", MaxSize>>20)
	}
	mainData := main.data
	if d, ok := out[main]; ok {
		mainData = d
	}
	inc := newIncluder(p.Path, len(mainData), opts.Root)
	inc.data = map[string][]byte{}
	for f, d := range out {
		if f != main {
			inc.data[f.real] = d
		}
	}
	x.Plan = parse(p.Path, mainData, opts, inc)
	return x, c.placed(x)
}

// placed checks that the extended plan is the plan with the fragment's
// tasks added to their phases, and fills in the additions' tasks. It
// returns a message when it isn't.
func (c *fragmentCheck) placed(x *Extension) string {
	ext := x.Plan
	if len(ext.Tasks) != len(c.p.Tasks)+len(c.fp.Tasks) {
		return fmt.Sprintf("igris couldn't add the fragment's tasks: the extended plan has %d tasks, not %d; extend it by hand", len(ext.Tasks), len(c.p.Tasks)+len(c.fp.Tasks))
	}
	for _, t := range c.p.Tasks {
		if e := ext.Task(t.ID); e == nil || !strings.EqualFold(e.Phase.ID, t.Phase.ID) {
			return fmt.Sprintf("igris couldn't add the fragment's tasks without moving task %s; extend the plan by hand", t.ID)
		}
	}
	for i, ph := range c.fp.Phases {
		for _, t := range ph.Tasks {
			e := ext.Task(t.ID)
			if e == nil || !strings.EqualFold(e.Phase.ID, ph.ID) {
				return fmt.Sprintf("igris couldn't add task %s to phase %s; extend the plan by hand", t.ID, ph.ID)
			}
			x.Additions[i].Tasks = append(x.Additions[i].Tasks, e)
		}
	}
	return ""
}

// renderRow renders a fragment row in the columns of target's table:
// cells as written, missing ones empty.
func renderRow(target, ph *Phase, r row) string {
	cells := make([]string, len(target.Columns))
	for i, col := range ph.Columns {
		if i < len(r.cells) {
			cells[colIndex(target.Columns, col)] = r.line.text[r.cells[i].start:r.cells[i].end]
		}
	}
	return "| " + strings.Join(cells, " | ") + " |"
}

// colIndex returns the index of col in cols: canonical names exactly,
// other columns case-insensitively; -1 if it isn't there.
func colIndex(cols []string, col string) int {
	for i, c := range cols {
		if strings.EqualFold(c, col) {
			return i
		}
	}
	return -1
}

// appendSections appends lines to b, a file, after one blank line, each
// ending in eol.
func appendSections(b *bytes.Buffer, lines []string, eol string) {
	if data := b.Bytes(); len(data) > 0 {
		if data[len(data)-1] != '\n' {
			b.WriteString(eol)
		}
		if ls := splitLines(b.Bytes()); len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1].text) != "" {
			b.WriteString(eol)
		}
	}
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString(eol)
	}
}

// lineEnding is the line ending f uses: its first one, else "\n".
func lineEnding(f *planFile) string {
	if i := bytes.IndexByte(f.data, '\n'); i > 0 && f.data[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// sectionEnd returns the index in f.lines of the line after ph's section:
// the next "##" heading outside a fence, or the end of the file.
func sectionEnd(f *planFile, ph *Phase) int {
	var fence string
	for i := ph.Line; i < len(f.lines); i++ {
		t := f.lines[i].text
		switch {
		case fence != "":
			if isFenceClose(t, fence) {
				fence = ""
			}
		case fenceOpen(t) != "":
			fence = fenceOpen(t)
		default:
			if _, ok := headingText(t); ok {
				return i
			}
		}
	}
	return len(f.lines)
}

// trimLeadingBlank drops the blank lines at the start of lines.
func trimLeadingBlank(lines []line) []line {
	for len(lines) > 0 && strings.TrimSpace(lines[0].text) == "" {
		lines = lines[1:]
	}
	return lines
}

// trimTrailingBlank drops the blank lines at the end of lines.
func trimTrailingBlank(lines []line) []line {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1].text) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Write writes the extension to the plan's files (SPEC §9.1): each changed
// file through a temp file and a rename, keeping its file mode. If any file
// of the plan changed on disk since it was read, nothing is written and the
// error wraps ErrConcurrentEdit. As for Status cells of a split plan (§4
// rule 7), the renames are not atomic together.
func (x *Extension) Write() error {
	p := x.base
	stale := func() error {
		switch same, err := unchanged(x.path, p, x.sums); {
		case err != nil:
			return err
		case !same:
			return fmt.Errorf("not extending %s: %w after the review; run `igris draft extend` again", x.path, ErrConcurrentEdit)
		}
		return nil
	}
	if err := stale(); err != nil {
		return err
	}
	var writes []*pending
	defer func() {
		for _, pw := range writes {
			if pw.tmp != "" {
				_ = os.Remove(pw.tmp) // best effort; the write already failed
			}
		}
	}()
	for _, e := range x.edits {
		pw := &pending{f: e.f, dst: e.f.real}
		if e.f == p.files[0] {
			var err error
			if pw.dst, err = resolve(x.path); err != nil {
				return err
			}
		}
		info, err := os.Stat(pw.dst)
		if err != nil {
			return fmt.Errorf("stat plan %s: %w", e.f.name, err)
		}
		if pw.tmp, err = writeTemp(pw.dst, e.out, info.Mode().Perm()); err != nil {
			return err
		}
		writes = append(writes, pw)
	}
	if x.beforeRename != nil {
		x.beforeRename()
	}
	if err := stale(); err != nil {
		return err
	}
	for _, pw := range writes {
		if err := os.Rename(pw.tmp, pw.dst); err != nil {
			return fmt.Errorf("replace plan %s: %w", pw.f.name, err)
		}
		pw.tmp = ""
		syncDir(filepath.Dir(pw.dst))
	}
	return nil
}
