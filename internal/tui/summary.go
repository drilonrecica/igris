package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/price"
	"github.com/drilonrecica/igris/internal/serve"
)

// The end-of-run summary (SPEC §15.3, since 1.7), in place of `the run
// ended — press q to go home`: the helm, Done and the outcome, the phase,
// the counts, wall time, needs-you time, tokens, commits, verify, the
// skip reasons and `next:` with the commands that follow. It is folded
// from the run's events as the view sees them, so an attached view that
// joined late sums what it saw (a question pending when it joined from
// when it was asked); `igris report` has the whole record.

// summary is what the run's events add up to.
type summary struct {
	run        string // the run ID (SPEC §13), for `igris report`
	start, end time.Time
	phases     []string // the run's phases, from RunStarted's scope
	outcome    look.Outcome
	err        string // RunFailed's error

	done, skipped int
	skips         []string        // "M1-06 skipped: reason", in order
	failed        map[string]bool // tasks whose last attempt hit the verify limit or an error
	in, out       int64           // tokens of the attempts folded so far
	usage         bool            // some attempt's usage was read
	usd           float64         // the price estimate of the priced attempts (SPEC §6.9)
	commits       int
	lastCommit    string // the task of the last commit
	verifyOK      int
	verifyFail    int
	waiting       []string // what waits, on a stuck phase or a slice not run

	needSince time.Time // when the owner was last needed; zero while not
	needsYou  time.Duration
}

// note folds ev into the summary; the model folds the tokens itself, as
// it knows each attempt's count (fold).
func (s *summary) note(ev engine.Event) {
	switch ev.Kind {
	case engine.RunStarted:
		s.start, s.run = ev.At, ev.Run
		s.phases = scopePhases(ev.Detail)
	case engine.TaskStarted, engine.TaskResumed, engine.Retrying:
		delete(s.failed, ev.Task) // a new attempt
	case engine.TaskDone:
		s.done++
		delete(s.failed, ev.Task)
	case engine.TaskSkipped:
		s.skipped++
		delete(s.failed, ev.Task)
		s.skips = append(s.skips, note(ev.Task+" skipped", ev.Detail))
	case engine.VerifyPassed:
		s.verifyOK++
	case engine.VerifyFailed:
		s.verifyFail++
	case engine.VerifyLimit, engine.HookFailed:
		if s.failed == nil {
			s.failed = map[string]bool{}
		}
		s.failed[ev.Task] = true
	case engine.Committed:
		s.commits++
		s.lastCommit = ev.Task // the event carries no SHA: the task names the commit
	case engine.PhaseStuck:
		for _, w := range ev.Waiting {
			s.waiting = append(s.waiting, w.String())
		}
	case engine.NotRun:
		for _, w := range ev.Waiting {
			s.waiting = append(s.waiting, "not run: "+w.String())
		}
	case engine.RunFailed:
		s.err = ev.Detail
	case engine.RunStopped:
		s.end, s.outcome = ev.At, outcomeOf(ev.Detail)
		s.need(false, ev.At)
	}
}

// fold adds an attempt's tokens once the attempt is over.
func (s *summary) fold(t *engine.Tokens) {
	if t == nil || t.Unavailable {
		return
	}
	s.in, s.out, s.usage = s.in+t.In, s.out+t.Out, true
	if t.Priced {
		s.usd += t.USD
	}
}

// need records at at whether anything waits on the owner: the time it
// does adds up to the needs-you time.
func (s *summary) need(needed bool, at time.Time) {
	switch {
	case needed && s.needSince.IsZero():
		s.needSince = at
	case !needed && !s.needSince.IsZero():
		if at.After(s.needSince) {
			s.needsYou += at.Sub(s.needSince)
		}
		s.needSince = time.Time{}
	}
}

// outcomeOf is the outcome RunStopped's detail names.
func outcomeOf(detail string) look.Outcome {
	switch detail {
	case "completed":
		return look.Completed
	case "stopped":
		return look.Stopped
	case "stuck":
		return look.Stuck
	}
	return look.Failed
}

// resultOutcome is the outcome of a run that ended with res and err, for
// a feed that ended without a RunStopped event. A serve that is gone
// (killed, crashed) left its run interrupted, not failed (SPEC §15.4).
func resultOutcome(res engine.Result, err error) look.Outcome {
	if errors.Is(err, serve.ErrGone) {
		return look.InterruptedRun
	}
	if err != nil {
		return look.Failed
	}
	return outcomeOf(res.Outcome.String())
}

// scopePhases are the phase IDs of a run's scope, "phase M0, M1; only
// A-2" (engine.Range.Scope).
func scopePhases(scope string) []string {
	scope, _, _ = strings.Cut(scope, ";")
	scope = strings.TrimPrefix(strings.TrimSpace(scope), "phase ")
	var ids []string
	for _, id := range strings.Split(scope, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// reportCommand is the command that shows the run in full.
func (s *summary) reportCommand() string {
	if s.run == "" {
		return "igris report"
	}
	return "igris report " + s.run
}

// summaryWide is where the summary's facts stand in two columns; under it
// they stack (SPEC §15.3).
const summaryWide = 72

// summaryScreen draws the end-of-run summary in h lines of w cells: the
// header and its rule, the helm and the facts centred, a rule and the
// bar with Home focused.
func (m *model) summaryScreen(w, h int) []string {
	out := []string{m.header(w), m.th.rule(w, "")}
	rows := max(h-chromeRows, 0)
	body := m.summaryBody(w, h, rows)
	for i := range rows {
		out = append(out, fit(line(body, i), w))
	}
	out = append(out, m.th.rule(w, ""))
	bar := m.fitBar(w, barRows)
	bar.barZones(&m.zones, len(out), m.modal())
	out = append(out, bar.text...)
	return out[:min(len(out), h)]
}

// summaryBody lays the summary out in at most rows lines of w cells. The
// helm goes first when the rows run out, then the blank lines.
func (m *model) summaryBody(w, h, rows int) []string {
	s := &m.sum
	width := min(w-4, 72)
	left := max((w-width)/2, 0)
	indent := strings.Repeat(" ", left)
	centre := func(text string) string {
		return strings.Repeat(" ", left+max((width-textWidth(text))/2, 0)) + text
	}

	helm := func(grid []string) []string {
		var out []string
		for _, l := range m.th.helm(grid) {
			out = append(out, strings.TrimRight(strings.Repeat(" ", left+max((width-look.HelmWidth(grid))/2, 0))+l, " "))
		}
		return out
	}

	var head []string
	head = append(head, centre(m.th.paint(outcomeLook(s.outcome), s.outcome.Line())))
	if p := m.summaryPhase(); p != "" {
		head = append(head, centre(fit(p, width)))
	}

	facts := m.summaryFacts(w, width)

	var notes []string
	for _, l := range s.skips {
		notes = append(notes, indent+fit(look.GlyphSkipped+" "+l, width))
	}
	for _, l := range s.waiting {
		notes = append(notes, indent+m.th.paint(lookWarn, fit(look.GlyphWarn+" "+l, width)))
	}
	if s.err != "" {
		for _, l := range wrap(look.GlyphFail+" "+s.err, width) {
			notes = append(notes, indent+m.th.paint(lookFail, l))
		}
	}

	next := indent + fit(m.th.paint(lookDim, "next: ")+m.th.paint(lookTitle, strings.Join(m.nextCommands(), "  ·  ")), width)

	blocks := [][]string{nil, head, facts, notes, {next}}
	joined := func(blank bool) []string {
		var out []string
		for _, b := range blocks {
			if len(b) == 0 {
				continue
			}
			if blank && len(out) > 0 {
				out = append(out, "")
			}
			out = append(out, b...)
		}
		return out
	}
	body := joined(true)
	// The helm goes first: L where the screen is large and it fits, else
	// S, else none.
	for _, grid := range [][]string{look.HelmFor(w, h, rows, m.th.colored()), look.HelmS} {
		if grid == nil || w < look.HelmMinWidth {
			continue
		}
		blocks[0] = helm(grid)
		if b := joined(true); len(b) <= rows {
			body = b
			break
		}
		blocks[0] = nil
	}
	if len(body) > rows {
		body = joined(false) // then the blank lines
	}
	return body[:min(len(body), max(rows, 0))]
}

// outcomeLook is how an outcome's headline is drawn.
func outcomeLook(o look.Outcome) tone {
	switch o.Severity() {
	case look.Accent:
		return lookAccentBold
	case look.Warn:
		return lookWarn
	case look.Fail:
		return lookFail
	}
	return lookTitle
}

// summaryPhase names the run's phases, with the title when it is one:
// "phase M1 · Plan parser & writer".
func (m *model) summaryPhase() string {
	ids := m.sum.phases
	if len(ids) == 0 && m.phase != "" {
		ids = []string{m.phase}
	}
	if len(ids) == 0 {
		return ""
	}
	text := "phase " + strings.Join(ids, ", ")
	if len(ids) == 1 && m.plan != nil {
		if ph := m.plan.Phase(ids[0]); ph != nil && ph.Title != "" {
			text += " · " + ph.Title
		}
	}
	return text
}

// summaryFacts are the counts and times, in two columns when the screen
// is wide enough, else one.
func (m *model) summaryFacts(w, width int) []string {
	s := &m.sum
	counts := look.Count(s.done, "done", "done") + " · " + look.Count(s.skipped, "skipped", "skipped")
	if n := len(s.failed); n > 0 {
		counts += " · " + look.Count(n, "failed", "failed")
	}
	var left, right []string
	left = append(left, counts)
	if s.usage {
		tokens := "tokens " + look.InOut(s.in, s.out)
		if s.usd > 0 {
			tokens += " · " + look.Dollars(s.usd)
		}
		left = append(left, tokens)
	}
	if s.commits > 0 {
		c := look.Count(s.commits, "commit", "commits")
		if s.lastCommit != "" {
			c += " · last " + s.lastCommit
		}
		left = append(left, c)
	}
	if !s.start.IsZero() && !s.end.IsZero() && s.end.After(s.start) {
		right = append(right, "wall "+look.Duration(s.end.Sub(s.start)))
	}
	if s.needsYou > 0 {
		right = append(right, "needs you "+look.Duration(s.needsYou))
	}
	if s.verifyOK+s.verifyFail > 0 {
		v := fmt.Sprintf("verify %d %s", s.verifyOK, look.GlyphDone)
		if s.verifyFail > 0 {
			v += fmt.Sprintf(" · %d %s", s.verifyFail, look.GlyphFail)
		}
		right = append(right, v)
	}
	indent := strings.Repeat(" ", max((w-width)/2, 0))
	var out []string
	if w < summaryWide || len(right) == 0 {
		// One column, on the block's left edge with the notes.
		for _, l := range append(left, right...) {
			out = append(out, indent+fit(l, width))
		}
		return s.withLegend(out, indent, width)
	}
	colW := (width - 4) / 2
	for i := range max(len(left), len(right)) {
		l := pad(fit(line(left, i), colW), colW)
		out = append(out, strings.TrimRight(indent+l+"    "+fit(line(right, i), colW), " "))
	}
	return s.withLegend(out, indent, width)
}

// withLegend adds the estimate's legend line (SPEC §6.9) under the facts
// when they show one.
func (s *summary) withLegend(out []string, indent string, width int) []string {
	if s.usd > 0 {
		out = append(out, indent+fit(price.Legend(), width))
	}
	return out
}

// nextCommands are the commands that follow the run (SPEC §15.3): the
// next phase with work after a completed run, a resume after a stop, the
// phase's status when it is stuck, status after an interrupted run (it
// says whether the stale lock needs --force-unlock), doctor after an
// error; and the report.
func (m *model) nextCommands() []string {
	var out []string
	switch s := &m.sum; s.outcome {
	case look.Completed:
		if id := m.nextPhase(); id != "" {
			out = append(out, "igris arise "+id)
		}
	case look.Stopped:
		out = append(out, "igris arise")
	case look.Stuck:
		if m.phase != "" {
			out = append(out, "igris status "+m.phase)
		}
	case look.InterruptedRun:
		out = append(out, "igris status")
	default:
		out = append(out, "igris doctor")
	}
	return append(out, m.sum.reportCommand())
}

// nextPhase is the first phase after the run's with a task left to do,
// in the plan as last loaded; "" when there is none.
func (m *model) nextPhase() string {
	if m.plan == nil {
		return ""
	}
	last := m.phase
	if n := len(m.sum.phases); n > 0 {
		last = m.sum.phases[n-1]
	}
	after := last == ""
	for _, ph := range m.plan.Phases {
		if !after {
			after = ph.ID == last
			continue
		}
		for _, t := range ph.Tasks {
			if !t.Status.Satisfied() {
				return ph.ID
			}
		}
	}
	return ""
}

// unfinishedTasks counts the tasks of the phases ids in the plan that
// are neither done nor skipped.
func unfinishedTasks(p *plan.Plan, ids []string) int {
	n := 0
	for _, id := range ids {
		if ph := p.Phase(id); ph != nil {
			for _, t := range ph.Tasks {
				if !t.Status.Satisfied() {
					n++
				}
			}
		}
	}
	return n
}
