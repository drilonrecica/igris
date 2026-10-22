package serve

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// The web UI (SPEC §15.8): server-rendered pages from the snapshot, one
// script that follows the event stream and sends the forms as protocol-1
// commands, and the same forms posting to PathAct without it.

// The web UI's paths, besides "/".
const (
	// PathLive is the page's live part alone, which the script fetches
	// again when an event arrives.
	PathLive = "/live"
	// PathPlan is the plan page: the phases and their tasks, the modes.
	PathPlan = "/plan"
	// PathPlanLive is the plan page's live part alone.
	PathPlanLive = "/plan/live"
	// PathAct takes a form's command when the script doesn't send it.
	PathAct = "/act"
	// pathStatic serves the script, the stylesheet, the icon and the fonts.
	pathStatic = "/static/"
	// pathFavicon is the icon where browsers look for one.
	pathFavicon = "/favicon.ico"
)

// staticFiles are the files served under pathStatic: a list, not the
// whole embedded directory (the templates and the font licence stay in).
var staticFiles = []string{"app.js", "app.css", "icon.svg",
	"fonts/chakra-petch-latin-600-normal.woff2", "fonts/chakra-petch-latin-700-normal.woff2"}

// confirmedField is the form field a second confirmation sets.
const confirmedField = "confirmed"

// recentLen is how many of the feed's last events the page lists.
const recentLen = 5

//go:embed ui/page.html ui/app.js ui/app.css ui/icon.svg ui/fonts/*.woff2
var uiFiles embed.FS

var uiTemplates = template.Must(template.New("ui").Funcs(template.FuncMap{
	"mark":      statusMark,
	"stateText": stateText,
	"slug":      slug,
	"offered":   func(m string) bool { return slices.Contains(remoteModes, m) },
}).ParseFS(uiFiles, "ui/page.html"))

// UIOptions configures the web UI.
type UIOptions struct {
	// Now is the clock for how long a task has run; nil is time.Now.
	Now func() time.Time
}

// ui serves the web UI of a server's run.
type ui struct {
	srv    *Server
	now    func() time.Time
	static http.Handler
}

// UI returns the web UI's handler (SPEC §15.8), for RemoteOptions.UI: the
// listener has authenticated every request it passes on, checked the CSRF
// token of a cookie's POST and put the token for the forms in the
// request's context (CSRFToken).
func (s *Server) UI(opts UIOptions) http.Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	sub, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		panic(err) // the embedded directory is there by construction
	}
	_ = mime.AddExtensionType(".woff2", "font/woff2") // not in every system's table
	return &ui{srv: s, now: opts.Now, static: http.StripPrefix(pathStatic, http.FileServerFS(sub))}
}

func (u *ui) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch p := r.URL.Path; p {
	case "/", PathLive, PathPlan, PathPlanLive:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			method(w, r, http.MethodGet)
			return
		}
		name := map[string]string{"/": "page", PathLive: "live", PathPlan: "plan", PathPlanLive: "plan-live"}[p]
		v := u.view(r)
		v.Plan = p == PathPlan || p == PathPlanLive
		u.render(w, http.StatusOK, name, v)
	case PathAct:
		if method(w, r, http.MethodPost) {
			u.act(w, r)
		}
	case pathFavicon:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			method(w, r, http.MethodGet)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = pathStatic + "icon.svg"
		u.static.ServeHTTP(w, r2)
	default:
		if strings.HasPrefix(p, pathStatic) && slices.Contains(staticFiles, strings.TrimPrefix(p, pathStatic)) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				method(w, r, http.MethodGet)
				return
			}
			u.static.ServeHTTP(w, r)
			return
		}
		u.message(w, http.StatusNotFound, "Not found", "There is nothing at this address.")
	}
}

// render executes template name into a buffer first, so a failure is a
// clean 500 rather than half a page.
func (u *ui) render(w http.ResponseWriter, status int, name string, data any) {
	var b bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&b, name, data); err != nil {
		u.srv.opts.Logf("web UI: page %s not rendered: %v", name, err)
		http.Error(w, "igris: the page could not be rendered", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}

// message shows a page with a title, a text and a way back.
func (u *ui) message(w http.ResponseWriter, status int, title, text string) {
	u.render(w, status, "message", messageView{Title: title, Text: text})
}

type messageView struct{ Title, Text string }

// act sends a form's command (SPEC §15.8, JavaScript off): stop, skip and
// done first show their second confirmation; the rest go to the run at
// once and the browser goes back to the page.
func (u *ui) act(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.message(w, http.StatusBadRequest, "Not sent", "The form could not be read.")
		return
	}
	f := r.PostForm
	req := formRequest(f)
	if _, ok := commandKinds[req.Type]; !ok {
		u.message(w, http.StatusBadRequest, "Not sent", fmt.Sprintf("%q is not a command.", textsafe.Line(cut(req.Type, maxID))))
		return
	}
	// The shape first: nothing of the form is shown back before it is
	// checked (the confirmation page names the task).
	if _, err := req.command(); err != nil {
		u.message(w, http.StatusBadRequest, "Not sent", err.Error())
		return
	}
	if c, ok := confirmOf(req.Type, req.Task); ok && f.Get(confirmedField) != "1" {
		u.render(w, http.StatusOK, "confirm", confirmView{Confirm: c, Form: f, CSRF: CSRFToken(r.Context())})
		return
	}
	rep, status := u.srv.remoteRequest(r.Context(), req)
	if rep.Type == TypeError {
		u.message(w, status, "Not sent", rep.Error)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// formRequest reads a form's command; the form fields are the request's.
func formRequest(f url.Values) Request {
	return Request{
		V: Protocol, Type: f.Get("type"), ID: "web-form",
		Text: f.Get("text"), Task: f.Get("task"), Question: f.Get("question"), Plan: f.Get("plan"),
		Continue: f.Get("continue") == "1", Yes: f.Get("yes") == "1",
	}
}

// confirmation is the second confirmation stop, skip and done ask.
type confirmation struct {
	Title, Text, Label string
}

// confirmOf is the second confirmation a command type asks, if any.
func confirmOf(typ, task string) (confirmation, bool) {
	if task == "" {
		task = "the task"
	}
	switch typ {
	case CmdStop:
		return confirmation{"Stop igris?", "The session stays open; igris arise picks the run up again.", "Stop igris"}, true
	case CmdSkip:
		return confirmation{"Skip " + task + "?", "The reason is recorded with the skip; an open session is closed.", "Skip " + task}, true
	case CmdDone:
		return confirmation{"Mark " + task + " done?", "It's your decision, so igris doesn't verify it.", "Mark " + task + " done"}, true
	}
	return confirmation{}, false
}

// confirmView is the confirmation page: the form again, confirmed.
type confirmView struct {
	Confirm confirmation
	Form    url.Values
	CSRF    string
}

// Fields are the form's fields to post again, in a stable order, without
// the CSRF token (the page sets the current one) and the confirmation.
func (v confirmView) Fields() [][2]string {
	var out [][2]string
	for _, k := range []string{"type", "task", "question", "plan", "text", "continue", "yes"} {
		if val := v.Form.Get(k); val != "" {
			out = append(out, [2]string{k, val})
		}
	}
	return out
}

// pageView is what the page shows: the snapshot, arranged.
type pageView struct {
	CSRF string
	Snap Snapshot
	// Plan says the plan page is rendered, not the run's.
	Plan bool
	// Phases are the plan's phases; Phase is the run's, nil before it is
	// known. Done and Total count its tasks done or skipped, and all.
	Phases      []phaseView
	Phase       *Phase
	Done, Total int
	Cards       []cardView
	// Word is the status word the page leads with (Working, Needs you,
	// Paused, Waiting, or the outcome once ended); Needs says it is Needs
	// you; Line is the short line beside it; Elapsed how long the run has
	// run.
	Word    string
	Needs   bool
	Line    string
	Elapsed string
	// Segments is the phase bar: one per task of the run's phase.
	Segments []segment
	// Sheet is everything that waits on the owner, in plan order: the
	// pending questions (with or without a card), a user task's turn, a
	// blocked or lost session, the reset request. The first is drawn as
	// the sheet; the rest follow it.
	Sheet []questionView
	// Next are the next tasks of the run's phase still to do (at most two).
	Next   []Task
	Header []form // pause or resume, stop
	Mode   *form  // the mode for the next sessions
	// ModeNote says the run's mode can't be chosen here.
	ModeNote string
	Recent   []Event
}

// phaseView is a phase of the task list.
type phaseView struct {
	Phase
	Run   bool // the run's phase
	Tasks []taskView
}

// taskView is a row of the task list; Mode is the owner's mode for the
// task's next session, else its Mode cell. ModeForm sets it.
type taskView struct {
	Task
	Mode     string
	ModeForm *form
}

// cardView is an active task's card.
type cardView struct {
	Current
	Ran      string // how long it has run
	Override string // the owner's mode for its next session
	// Question is the card's entry of the sheet, for its kind label.
	Question *questionView
	Actions  []form
}

// questionView is one thing that waits on the owner, with its answers: a
// pending question (Q), or a card's state with no question event (Q zero,
// Text says it).
type questionView struct {
	Q Event
	// Sheet marks the first entry, drawn as the sheet.
	Sheet bool
	// Kind is the small label: commit?, plan, session lost, your turn…
	Kind    string
	Title   string
	Text    string
	Answers []form
	// NoPlan says the plan was too long for the snapshot.
	NoPlan bool
}

// segment is one task of the phase bar: done, active, needs, todo or
// skipped.
type segment struct {
	ID, State string
}

// form is one command's form: hidden fields, at most one text or select
// field, and the button. Confirm asks the second confirmation first.
type form struct {
	CSRF     string
	Type     string
	Task     string
	Question string
	Plan     string
	Continue bool
	Yes      bool
	Label    string
	Danger   bool
	// Primary is a question's first answer, the full-width button.
	Primary bool
	// Input is the text field's label; Required makes it so, Multi a text
	// area.
	Input    string
	Required bool
	Multi    bool
	// Select lists the choices of the text field instead; Selected is
	// chosen.
	Select   []string
	Selected string
	Confirm  *confirmation
}

// remoteModes are the modes the web UI offers: none that skips
// permissions (SPEC §7.3, §15.8).
var remoteModes = slices.DeleteFunc(slices.Clone(engine.Modes), engine.SkipsPermissions)

// view arranges the snapshot for the page.
func (u *ui) view(r *http.Request) pageView {
	snap := u.srv.Snapshot()
	now := u.now()
	csrf := CSRFToken(r.Context())
	v := pageView{CSRF: csrf, Snap: snap}
	live := snap.Run != "" && !snap.Ended
	for i, ph := range snap.Phases {
		pv := phaseView{Phase: ph, Run: ph.ID == snap.Phase}
		if pv.Run {
			v.Phase = &snap.Phases[i]
		}
		for _, t := range ph.Tasks {
			tv := taskView{Task: t, Mode: snap.Overrides[t.ID]}
			if tv.Mode == "" {
				tv.Mode = t.Mode
			}
			// Task mode, as the TUI's M: an agent task of the run's phase
			// that is still to do (SPEC §15.3).
			agent := t.Owner == "" || t.Owner == "agent" || t.Owner == "agent + user"
			if live && pv.Run && agent && t.Status != "done" && t.Status != "skipped" {
				sel := tv.Mode
				if sel == "" {
					sel = snap.Mode
				}
				tv.ModeForm = &form{CSRF: csrf, Type: CmdTaskMode, Task: t.ID, Label: "Set", Input: "Mode of its next session", Select: remoteModes, Selected: sel}
			}
			pv.Tasks = append(pv.Tasks, tv)
		}
		v.Phases = append(v.Phases, pv)
	}
	if v.Phase != nil {
		v.Total = len(v.Phase.Tasks)
		for _, t := range v.Phase.Tasks {
			if t.Status == "done" || t.Status == "skipped" {
				v.Done++
			}
		}
	}
	asks := map[string]Event{}
	asked := map[string]bool{}
	for _, q := range snap.Questions {
		asks[q.Task] = q
		asked[q.Task] = true
	}
	v.Word, v.Line = statusOf(snap, now)
	v.Needs = v.Word == "Needs you"
	if !snap.Started.IsZero() {
		v.Elapsed = ran(now.Sub(snap.Started))
	}
	v.Segments = segmentsOf(v.Phase, snap.Active, asked)
	if v.Phase != nil {
		for _, t := range v.Phase.Tasks {
			if st := strings.ToLower(t.Status); (st == "ready" || st == "blocked") && len(v.Next) < 2 {
				v.Next = append(v.Next, t)
			}
		}
	}
	for _, c := range snap.Active {
		cv := cardView{Current: c, Ran: ran(now.Sub(c.Started)), Override: snap.Overrides[c.Task]}
		if live {
			if q, ok := asks[c.Task]; ok {
				qv := questionOf(q, csrf)
				cv.Question = &qv
				v.Sheet = append(v.Sheet, qv)
				delete(asks, c.Task)
			} else if qv, ok := sheetOf(c, csrf); ok {
				cv.Question = &qv
				v.Sheet = append(v.Sheet, qv)
			}
			cv.Actions = cardActions(c, csrf)
		}
		v.Cards = append(v.Cards, cv)
	}
	if live {
		for _, q := range snap.Questions {
			if _, left := asks[q.Task]; left {
				v.Sheet = append(v.Sheet, questionOf(q, csrf))
			}
		}
		if snap.Reset != nil {
			v.Sheet = append(v.Sheet, questionOf(*snap.Reset, csrf))
		}
		if len(v.Sheet) > 0 {
			v.Sheet[0].Sheet = true
		}
		pause := form{CSRF: csrf, Type: CmdPause, Label: "Pause after this task"}
		if snap.Paused {
			pause.Label = "Resume"
		}
		stop, _ := confirmOf(CmdStop, "")
		v.Header = []form{pause, {CSRF: csrf, Type: CmdStop, Label: "Stop", Danger: true, Confirm: &stop}}
		if engine.SkipsPermissions(snap.Mode) {
			v.ModeNote = "Next sessions run in " + snap.Mode + ", chosen in the terminal; a skip-permissions mode is set there only."
		}
		v.Mode = &form{CSRF: csrf, Type: CmdMode, Label: "Set", Input: "Mode of the next sessions", Select: remoteModes, Selected: snap.Mode}
	}
	feed := snap.Feed
	if len(feed) > recentLen {
		feed = feed[len(feed)-recentLen:]
	}
	for i := len(feed) - 1; i >= 0; i-- {
		v.Recent = append(v.Recent, feed[i])
	}
	return v
}

// cardActions are an active task's buttons: done, skip, and for an agent
// task retry fresh or continuing.
func cardActions(c Current, csrf string) []form {
	done, _ := confirmOf(CmdDone, c.Task)
	skip, _ := confirmOf(CmdSkip, c.Task)
	out := []form{
		{CSRF: csrf, Type: CmdDone, Task: c.Task, Label: "Mark done", Input: "Note (optional)", Confirm: &done},
		{CSRF: csrf, Type: CmdSkip, Task: c.Task, Label: "Skip", Input: "Reason", Required: true, Danger: true, Confirm: &skip},
	}
	if !c.User {
		out = append(out,
			form{CSRF: csrf, Type: CmdRetry, Task: c.Task, Label: "Retry fresh"},
			form{CSRF: csrf, Type: CmdRetry, Task: c.Task, Continue: true, Label: "Retry, continuing the conversation"})
	}
	return out
}

// questionOf is a pending question with the answers the TUI's dialog
// offers (internal/tui), less the pane: opening one stays in the terminal.
func questionOf(q Event, csrf string) questionView {
	t := q.Task
	v := questionView{Q: q, Kind: "question", Title: "Question"}
	answer := func(label string, yes bool) form {
		return form{CSRF: csrf, Type: CmdAnswer, Task: t, Yes: yes, Label: label}
	}
	a := answersFor(t, csrf)
	switch engine.Question(q.Question) {
	case engine.QuestionCommit:
		v.Kind, v.Title = "commit?", "Commit "+t+"?"
		v.Answers = []form{answer("Commit", true), answer("Leave uncommitted", false)}
	case engine.QuestionConfirmSkip:
		v.Kind, v.Title = "skip?", t+" asks to skip"
		v.Answers = []form{answer("Keep working", false), answer("Skip the task", true)}
		v.Answers[1].Danger = true
	case engine.QuestionSessionLost:
		v.Kind, v.Title = "session lost", t+": session lost"
		v.Answers = []form{a.fresh, a.cont, a.done, a.skip, a.stop}
	case engine.QuestionHookFailed:
		v.Kind, v.Title = "hook failed", t+": before_task hook failed"
		a.fresh.Label = "Retry"
		v.Answers = []form{a.fresh, a.done, a.skip, a.stop}
	case engine.QuestionIntegration:
		v.Kind, v.Title = "didn't integrate", t+" didn't integrate"
		a.fresh.Label = "Rerun fresh"
		a.done.Label = "Mark done (integrated by hand)"
		v.Answers = []form{a.fresh, a.done, a.skip, a.stop}
	case engine.QuestionConfirmReset:
		v.Kind, v.Title = "reset?", "Reset "+t+"?"
		keep, reset := answer("Keep it as it is", false), answer("Reset "+t, true)
		keep.Question, reset.Question = string(engine.QuestionConfirmReset), string(engine.QuestionConfirmReset)
		reset.Danger = true
		v.Answers = []form{keep, reset}
	case engine.QuestionPlanApproval:
		v.Kind, v.Title = "plan", t+" presents a plan"
		v.NoPlan = q.Plan == ""
		then := q.Then
		if then == "" {
			then = "the mode after approval"
		}
		v.Answers = []form{
			{CSRF: csrf, Type: CmdPlan, Task: t, Plan: state.PlanAllow, Label: "Approve: the session switches to " + then},
			{CSRF: csrf, Type: CmdPlan, Task: t, Plan: state.PlanDeny, Label: "Reject with feedback", Input: "Feedback for the session", Required: true, Multi: true, Danger: true},
		}
	default:
		v.Answers = []form{answer("Yes", true), answer("No", false)}
	}
	v.Answers[0].Primary = true
	return v
}

// answers are the forms every sheet may offer for a task.
type answers struct {
	done, skip, stop, fresh, cont form
}

// answersFor builds them for task t.
func answersFor(t, csrf string) answers {
	done, _ := confirmOf(CmdDone, t)
	skip, _ := confirmOf(CmdSkip, t)
	stop, _ := confirmOf(CmdStop, "")
	return answers{
		done:  form{CSRF: csrf, Type: CmdDone, Task: t, Label: "Mark done", Input: "Note (optional)", Confirm: &done},
		skip:  form{CSRF: csrf, Type: CmdSkip, Task: t, Label: "Skip", Input: "Reason", Required: true, Danger: true, Confirm: &skip},
		stop:  form{CSRF: csrf, Type: CmdStop, Label: "Stop igris", Danger: true, Confirm: &stop},
		fresh: form{CSRF: csrf, Type: CmdRetry, Task: t, Label: "Start fresh"},
		cont:  form{CSRF: csrf, Type: CmdRetry, Task: t, Continue: true, Label: "Continue conversation"},
	}
}

// sheetOf is the sheet of a card that waits on the owner without a
// question event: a user task at its turn, a session blocked at a prompt
// in its pane, a lost session. So Needs you never shows with nothing to
// tap (the design, 2026-10-22).
func sheetOf(c Current, csrf string) (questionView, bool) {
	a := answersFor(c.Task, csrf)
	v := questionView{Q: Event{Task: c.Task}}
	switch c.State {
	case StateYourTurn:
		v.Kind, v.Title = "your turn", "Your turn: "+c.Task
		if c.Title != "" {
			v.Title += " · " + c.Title
		}
		v.Text = "This task is yours. Do it in the terminal and run `igris done` there, or mark it done here."
		if c.Detail != "" {
			v.Text = c.Detail + ". " + v.Text
		}
		v.Answers = []form{a.done, a.skip}
	case StateNeedsYou:
		v.Kind, v.Title = "blocked", c.Task+" waits for you in its pane"
		v.Text = c.Detail
		if v.Text == "" {
			v.Text = "The session waits for a permission or an answer; open its pane in the terminal."
		}
		v.Answers = []form{a.done, a.skip, a.stop}
	case StateLost:
		v.Kind, v.Title = "session lost", c.Task+": session lost"
		v.Text = c.Detail
		v.Answers = []form{a.fresh, a.cont, a.done, a.skip, a.stop}
	default:
		return questionView{}, false
	}
	v.Answers[0].Primary = true
	return v, true
}

// statusOf is the page's status word and the line beside it: Needs you
// (a question, a user task's turn, a blocked or lost session) outranks
// Paused, which outranks Working; Waiting before any task; the outcome
// word once ended.
func statusOf(s Snapshot, now time.Time) (word, line string) {
	if s.Ended {
		end := s.End
		if end == "" {
			end = "unknown"
		}
		return endWord(s.End), "ended: " + end
	}
	elapsed := ""
	if !s.Started.IsZero() {
		elapsed = " · " + ran(now.Sub(s.Started))
	}
	asked := map[string]bool{}
	var oldest time.Time
	questions := 0
	note := func(q Event) {
		questions++
		if oldest.IsZero() || q.At.Before(oldest) {
			oldest = q.At
		}
	}
	for _, q := range s.Questions {
		asked[q.Task] = true
		note(q)
	}
	if s.Reset != nil {
		note(*s.Reset)
	}
	// needs counts the tasks that need the owner without a question. A
	// card still in the question state with no question pending is an
	// answer in flight (sent from here or elsewhere), not a need.
	needs := 0
	for _, c := range s.Active {
		switch c.State {
		case StateYourTurn, StateNeedsYou, StateLost:
			needs++
		}
	}
	switch {
	case needs > 0:
		n := needs + questions
		if n == 1 {
			return "Needs you", "1 task needs you"
		}
		return "Needs you", fmt.Sprintf("%d tasks need you", n)
	case questions == 1:
		return "Needs you", "1 question · " + ran(now.Sub(oldest))
	case questions > 1:
		return "Needs you", fmt.Sprintf("%d questions · %s", questions, ran(now.Sub(oldest)))
	case s.Paused && s.Holding:
		return "Paused", "holding" + elapsed
	case s.Paused:
		return "Paused", "after this task" + elapsed
	case len(s.Active) > 0:
		return "Working", "nothing needs you" + elapsed
	case s.Run != "":
		return "Waiting", "nothing running yet" + elapsed
	}
	return "Waiting", "no run yet"
}

// endWord is the status word for a run's outcome.
func endWord(end string) string {
	switch end {
	case "completed":
		return "Done"
	case "failed":
		return "Failed"
	case "stuck":
		return "Stuck"
	}
	return "Stopped"
}

// segmentsOf is the phase bar: one segment per task of ph in plan order,
// from its status and, for an active task, whether it needs the owner.
func segmentsOf(ph *Phase, active []Current, asked map[string]bool) []segment {
	if ph == nil {
		return nil
	}
	states := map[string]string{}
	for _, c := range active {
		states[c.Task] = c.State
	}
	out := make([]segment, 0, len(ph.Tasks))
	for _, t := range ph.Tasks {
		seg := segment{ID: t.ID, State: "todo"}
		st, isActive := states[t.ID]
		switch {
		case strings.EqualFold(t.Status, "done"):
			seg.State = "done"
		case strings.EqualFold(t.Status, "skipped"):
			seg.State = "skipped"
		case isActive && (asked[t.ID] || st == StateYourTurn || st == StateNeedsYou || st == StateLost):
			seg.State = "needs"
		case isActive:
			seg.State = "active"
		}
		out = append(out, seg)
	}
	return out
}

// ran says how long a task has run: 4m12s, 1h05m.
func ran(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Truncate(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// statusMark is a task status as a sign that reads without colour.
func statusMark(status string) string {
	switch strings.ToLower(status) {
	case "done":
		return "✓"
	case "in progress":
		return "●"
	case "ready":
		return "○"
	case "blocked":
		return "◌"
	case "skipped":
		return "–"
	}
	return "?"
}

// slug makes a status a class name: "in progress" → "in-progress".
func slug(status string) string {
	return strings.ReplaceAll(strings.ToLower(status), " ", "-")
}

// stateText says a card's state in words.
func stateText(s string) string {
	switch s {
	case StateWorking:
		return "working"
	case StateYourTurn:
		return "your turn"
	case StateWaiting:
		return "waiting on background work"
	case StateNeedsYou:
		return "needs you"
	case StateVerifying:
		return "verifying"
	case StateLost:
		return "session lost"
	case StateQuestion:
		return "asks you"
	}
	return s
}
