package main

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// textNow is the clock text output tells times of day and dates against;
// a seam for tests.
var textNow = time.Now

// when is an RFC 3339 time as text output shows it, local: "09:41" today,
// "Oct 06 21:10" on another day; "" for an unparsable one.
func when(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return ""
	}
	return whenTime(t)
}

func whenTime(t time.Time) string {
	t, now := t.Local(), textNow().Local()
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return look.Clock(t)
	}
	return look.Date(t, now)
}

// peekRun is the snapshot of the run served at root, when one answers; a
// seam for tests.
var peekRun = func(root string) (*serve.Snapshot, bool) {
	s, err := serve.Peek(context.Background(), root, "igris "+igrisVersion()+" status")
	if err != nil {
		return nil, false
	}
	return &s, true
}

// lastRuns are the newest runs of the log at root, newest first; nil when
// it can't be read. `status` reads them to tell a run that stopped itself
// from an interrupted one (report.Interrupted).
func lastRuns(root string) []report.HistoryRun {
	in, err := project.HistoryInput(root, 1)
	if err != nil {
		return nil
	}
	return report.NewHistory(in).Runs
}

// runLine is the status word of the run, with its glyph, and what it is
// about (SPEC §14): "● Working · M2-02 Lock file · opus · mode plan ·
// since 09:41 · pid 4211 on this host".
type runLine struct {
	word  look.Status
	label string // the word with its glyph, or a Done line
	sev   look.Severity
	about []string
	// needs are the tasks that need the owner, from a live run.
	needs map[string]bool
	// next is the command that follows from the run's state; "" when the
	// plan says it.
	next string
}

func (r runLine) String() string { return strings.Join(append([]string{r.label}, r.about...), " · ") }

// liveLine reads the status word from a served run's snapshot.
func liveLine(s *serve.Snapshot, p *plan.Plan, lock *report.RunInfo) runLine {
	r := runLine{needs: map[string]bool{}, next: "igris attach"}
	set := func(w look.Status) { r.word, r.label, r.sev = w, w.Label(), w.Severity() }
	if s.Ended {
		o := endedOutcome(s.End)
		r.word, r.label, r.sev = look.Done, o.Line(), o.Severity()
		r.next = "igris report"
		return r
	}
	for _, q := range s.Questions {
		if !r.needs[q.Task] {
			r.needs[q.Task] = true
			if len(r.about) == 0 {
				r.about = []string{textsafe.Line(q.Task), serve.QuestionKind(q.Question)}
			}
		}
	}
	for _, c := range s.Active {
		var kind string
		switch c.State {
		case serve.StateYourTurn:
			kind = "your turn"
		case serve.StateNeedsYou:
			kind = "needs you"
		case serve.StateLost:
			kind = "session lost"
		default:
			continue
		}
		if !r.needs[c.Task] {
			r.needs[c.Task] = true
			if len(r.about) == 0 {
				r.about = []string{textsafe.Line(c.Task), kind}
			}
		}
	}
	switch {
	case len(r.needs) > 0:
		set(look.NeedsYou)
		if n := len(r.needs); n > 1 {
			r.about = append(r.about, fmt.Sprintf("%s %s you", look.Count(n, "task", "tasks"), look.Plural(n, "needs", "need")))
		}
	case s.Limited != nil && len(s.Active) == 0:
		// The usage hold (SPEC §6.9): the window, its use and the reset
		// with its countdown.
		set(look.Paused)
		r.about = []string{s.Limited.Label(), "resumes " + look.Until(s.Limited.ResetsAt.Local(), textNow())}
	case s.Paused && (s.Holding || len(s.Active) == 0):
		set(look.Paused)
		if s.Holding {
			r.about = []string{"holding"}
		}
	default:
		set(look.Working)
		if len(s.Active) == 0 {
			r.about = []string{"between tasks"}
			break
		}
		c := s.Active[0]
		r.about = taskAbout(p, c.Task, c.Rank, c.Mode, whenTime(c.Started))
		if n := len(s.Active) - 1; n > 0 {
			r.about = append(r.about, look.Count(n, "more task", "more tasks"))
		}
		if lock != nil && lock.Lock == report.LockHere {
			r.about = append(r.about, lock.LockDetail+" on this host")
		}
		if l := s.Limited; l != nil {
			r.about = append(r.about, l.Label()+" · next start "+look.Until(l.ResetsAt.Local(), textNow()))
		}
	}
	return r
}

// endedOutcome is a run's outcome from run_stopped's detail.
func endedOutcome(end string) look.Outcome {
	switch end {
	case "completed":
		return look.Completed
	case "stuck":
		return look.Stuck
	case "failed", "error":
		return look.Failed
	}
	return look.Stopped
}

// taskAbout is what a run line says about its task: its ID and title,
// rank, mode and since when.
func taskAbout(p *plan.Plan, id, rank, mode, since string) []string {
	if id == "" {
		return []string{"between tasks"}
	}
	head := textsafe.Line(id)
	if t := p.Task(id); t != nil {
		if t.Title != "" {
			head += " " + textsafe.Line(t.Title)
		}
		rank = cmp.Or(rank, t.Rank)
	}
	out := []string{head}
	if rank != "" {
		out = append(out, textsafe.Line(rank))
	}
	if mode != "" {
		out = append(out, "mode "+mode)
	}
	if since != "" {
		out = append(out, "since "+since)
	}
	return out
}

// stateLine reads the status word from state.json, the lock and the
// newest runs of the log (recent), when no run answers on the socket: Idle
// for a run that stopped itself, with its outcome and the task to resume,
// ▲ Interrupted for one whose igris ended without stopping it.
func stateLine(r *report.RunInfo, p *plan.Plan, recent []report.HistoryRun) runLine {
	about := taskAbout(p, r.Task, "", r.Mode, when(r.Since))
	switch r.Lock {
	case report.LockHere:
		return runLine{word: look.Working, label: look.Working.Label(), sev: look.Working.Severity(), about: append(about, r.LockDetail+" on this host"), next: "igris history"}
	case report.LockElsewhere:
		return runLine{word: look.Working, label: look.Working.Label(), sev: look.Working.Severity(), about: append(about, r.LockDetail), next: "igris history"}
	case report.LockNone:
		if report.Interrupted(r, recent) {
			break
		}
		l := runLine{word: look.Idle, label: look.Idle.Label(), sev: look.Idle.Severity(), about: about[:1]}
		if len(recent) > 0 {
			l.about = []string{endedOutcome(recent[0].End).String()}
			if r.Task != "" {
				l.about = append(l.about, about[0])
			}
		}
		if r.Task != "" {
			l.next = "igris arise" // resumes the task first
		}
		return l
	}
	l := runLine{word: look.Interrupted, label: look.Interrupted.Label(), sev: look.Interrupted.Severity(), about: about[:1], next: "igris arise"}
	if r.Lock == report.LockStale {
		l.next = "igris arise --force-unlock"
	}
	return l
}

// printStatus writes `igris status` (SPEC §14): the plan, the run's line
// and its rows, then each phase's tasks with their glyphs, then next.
func printStatus(o textOut, r report.StatusReport, p *plan.Plan, root string, live *serve.Snapshot) {
	o.printf("igris · %s · %s", textsafe.Line(filepath.Base(root)), r.Plan)
	var line *runLine
	if live != nil {
		l := liveLine(live, p, r.Run)
		line = &l
	}
	if r.Run != nil {
		o.blank()
		var recent []report.HistoryRun
		if line == nil {
			recent = lastRuns(root)
		}
		line = printRun(o, r.Run, line, p, recent)
	}
	for _, ph := range r.Phases {
		o.blank()
		printPhaseStatus(o, ph, r.Optional, line)
	}
	o.blank()
	o.next(statusNext(r, line))
}

// statusNext is the command that follows from what status showed: the
// live run's, else running the first phase with a next task.
func statusNext(r report.StatusReport, line *runLine) string {
	if line != nil && line.next != "" {
		return line.next
	}
	stuck := false
	for _, ph := range r.Phases {
		switch ph.Outcome {
		case "next":
			return "igris arise " + ph.ID
		case "stuck":
			stuck = true
		}
	}
	if stuck {
		return "igris check"
	}
	return "igris history"
}

// printRun writes the Run block: the status line, then the rows that say
// more. It returns the line it showed. recent are the log's newest runs,
// for a line read from state.json.
func printRun(o textOut, r *report.RunInfo, line *runLine, p *plan.Plan, recent []report.HistoryRun) *runLine {
	row := func(label, value string) {
		if value != "" {
			o.println(o.fit(fmt.Sprintf("%-8s %s", label, value)))
		}
	}
	switch {
	case line != nil:
	case r.Unreadable != "":
		line = &runLine{label: look.GlyphWarn + " " + r.Unreadable, sev: look.Warn, next: "igris doctor"}
	case r.Lock == report.LockUnreadable:
		line = &runLine{label: look.GlyphWarn + " lock unreadable", sev: look.Warn, about: []string{r.LockDetail}, next: "igris doctor"}
	default:
		l := stateLine(r, p, recent)
		line = &l
	}
	head := look.Cell{Text: line.String(), Sev: line.sev, Span: len(line.label)}
	o.println("Run      " + o.fitCell(head, 9))
	if r.Unreadable == "" {
		phases := strings.Join(r.Phases, ", ")
		if r.Through != "" {
			phases += " (through " + r.Through + ")"
		}
		row("Phases", phases)
		if r.Selection != nil {
			row("Slice", r.Selection.String())
		}
		row("Started", when(r.StartedAt))
		row("Session", r.Session)
	}
	// The run line tells the lock's state, except a stale or unreadable
	// one, or any lock next to a state file that can't be read.
	if r.Lock == report.LockStale || r.Lock == report.LockUnreadable || (r.Unreadable != "" && r.Lock != report.LockNone) {
		row("Lock", r.Lock+" ("+r.LockDetail+")")
	}
	signals := make([]string, 0, len(r.Signals)+1)
	for _, s := range r.Signals {
		signals = append(signals, s.String())
	}
	if r.SignalsUnreadable > 0 {
		signals = append(signals, fmt.Sprintf("%d unreadable", r.SignalsUnreadable))
	}
	row("Signals", strings.Join(signals, ", "))
	return line
}

// printPhaseStatus writes a phase's line and its tasks' table.
func printPhaseStatus(o textOut, r report.PhaseStatus, opt report.Optional, line *runLine) {
	satisfied := r.Counts[report.CountKey(plan.Done)] + r.Counts[report.CountKey(plan.Skipped)]
	head := r.ID
	if r.Title != "" {
		head += " — " + r.Title
	}
	outcome := r.Outcome
	if r.Next != "" {
		outcome += " " + r.Next
	}
	o.println(o.fit(fmt.Sprintf("%s · %d/%d · %s", head, satisfied, r.Total, outcome)))
	// The optional columns are shown only when some task of the plan sets
	// them (SPEC §14).
	header := []string{"ID", "STATUS", "RANK", "OWNER", "MODE"}
	if opt.Verify {
		header = append(header, "VERIFY")
	}
	if opt.Timeout {
		header = append(header, "TIMEOUT")
	}
	if opt.Context {
		header = append(header, "CONTEXT")
	}
	tb := &look.Table{Indent: "  ", Header: append(header, "WAITS ON"), Fold: true}
	for _, t := range r.Tasks {
		glyph, sev := taskMark(t.Status)
		if line != nil && line.needs[t.ID] {
			glyph, sev = look.GlyphNeedsYou, look.Needs
		}
		cells := []look.Cell{look.Mark(sev, glyph, t.ID), look.TextCell(t.Status), look.TextCell(t.Rank), look.TextCell(t.Owner), look.TextCell(t.Mode)}
		if opt.Verify {
			cells = append(cells, look.TextCell(t.Verify))
		}
		if opt.Timeout {
			cells = append(cells, look.TextCell(t.Timeout))
		}
		if opt.Context {
			cells = append(cells, look.TextCell(strings.Join(t.Context, ", ")))
		}
		waits := make([]string, len(t.WaitsOn))
		for i, x := range t.WaitsOn {
			waits[i] = x.ID
			if x.Phase != r.ID {
				waits[i] += " (phase " + x.Phase + ")"
			}
		}
		tb.Row(append(cells, look.TextCell(strings.Join(waits, ", ")))...)
	}
	o.table(tb)
}

// taskMark is the glyph of a task's status word and how it is drawn: glow
// for done and running (SPEC §14).
func taskMark(status string) (string, look.Severity) {
	var s plan.Status = -1
	for _, c := range []plan.Status{plan.Ready, plan.Blocked, plan.InProgress, plan.Done, plan.Skipped} {
		if c.String() == status {
			s = c
		}
	}
	switch s {
	case plan.Done, plan.InProgress:
		return look.TaskGlyph(s), look.Accent
	}
	return look.TaskGlyph(s), look.Plain
}
