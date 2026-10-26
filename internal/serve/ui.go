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
	"unicode"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
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
	"tokens":    tokensText,
	"spans":     codeSpans,
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
	u.render(w, status, "message", messageView{Project: u.srv.opts.Project, Title: title, Text: text})
}

type messageView struct{ Project, Title, Text string }

// refuse is the page of a request the listener turned away before the UI
// saw it (not paired, a stale pairing URL, a missing CSRF token): the
// brand and the next step, with no stylesheet, since an unpaired browser
// can't load one and a stale cookie's every failed request counts against
// its address (SPEC §14, §15.8). msg is the listener's error: a title
// before its first ": ", else the status's.
func (u *ui) refuse(w http.ResponseWriter, status int, msg string) {
	title, text, ok := strings.Cut(msg, ": ")
	if !ok {
		title, text = refusedTitle(status), msg
	}
	u.render(w, status, "refused", messageView{Title: capital(title), Text: sentence(text)})
}

// refusedTitle names a refusal without a title of its own.
func refusedTitle(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "Not paired"
	case http.StatusTooManyRequests:
		return "Too many attempts"
	case http.StatusNotFound:
		return "Not found"
	case http.StatusServiceUnavailable:
		return "Not now"
	}
	return "Refused"
}

// capital starts s with a capital letter.
func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// sentence starts s with a capital and ends it with a full stop.
func sentence(s string) string {
	s = capital(s)
	if s != "" && !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return s
}

// span is a piece of a sentence; Code marks a command.
type span struct {
	Text string
	Code bool
}

// codeSpans splits s at its backticks, so a command is drawn as code
// rather than with literal backticks.
func codeSpans(s string) []span {
	var out []span
	for i, part := range strings.Split(s, "`") {
		if part != "" {
			out = append(out, span{Text: part, Code: i%2 == 1})
		}
	}
	return out
}

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
		u.render(w, http.StatusOK, "confirm", confirmView{Project: u.srv.opts.Project, Confirm: c, Form: f, CSRF: CSRFToken(r.Context())})
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
	Project string
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
	// Status is the word the page leads with, the line beside it and its
	// tone (SPEC §15.4); Elapsed is how long the run has run.
	Status  status
	Elapsed string
	// Segments is the phase bar: one per task of the run's phase.
	Segments []segment
	// Sheet is everything that waits on the owner, in plan order: the
	// pending questions (with or without a card), a user task's turn, a
	// blocked or lost session, the reset request. With the script on they
	// are the sheet, one at a time; without it they follow each other.
	Sheet []questionView
	// Next are the next tasks of the run's phase still to do (at most
	// two), none once the run ended.
	Next   []Task
	Header []form // pause or resume, stop
	Mode   *form  // the mode for the next sessions
	// ModeNote says the run's mode can't be chosen here.
	ModeNote string
	Recent   []recentView
	// Budgets are the run's budgets with what they used (SPEC §6.9).
	Budgets []budgetView
}

// budgetView is a budget's line, `budget 14.2M / 20M · 71%`, with its
// tone: ember from 80%, bold ember from 95% (SPEC §6.9, §15).
type budgetView struct {
	Line, Tone string
}

// budgetViews are b as the page shows them.
func budgetViews(b []engine.BudgetUse) []budgetView {
	out := make([]budgetView, 0, len(b))
	for _, u := range b {
		v := budgetView{Line: u.Line()}
		switch {
		case u.State == engine.BudgetContinued:
		case u.Percent >= 95:
			v.Tone = "ember strong"
		case u.Percent >= 80:
			v.Tone = "ember"
		}
		out = append(out, v)
	}
	return out
}

// capitalize starts s with a capital letter, as Recent's lines do.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// status is the page's status word, its line and its tone: "" for the
// accent, needs, ember, plain or dim.
type status struct {
	Word, Line, Tone string
	// Ran is the clock the line ends with, from Since (the oldest question,
	// or the run's start); the page ticks it on. Empty for none.
	Ran   string
	Since time.Time
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
	// Glyph is the card's state as a glyph: ! when its entry is in the
	// sheet, ◌ waiting on background work, else ●.
	Glyph string
	// Question is the card's entry of the sheet: the card links to it
	// instead of offering Actions a second time.
	Question *questionView
	Actions  []form
}

// questionView is one thing that waits on the owner, with its answers: a
// pending question (Q), or a card's state with no question event (Q zero,
// Text says it).
type questionView struct {
	Q Event
	// Sheet marks the first entry, the sheet's when the page loads; N of
	// Of says which of the page's entries this is (its anchor is q-N), and
	// NextN the entry its stepper goes to, back to the first from the last.
	Sheet bool
	N, Of int
	NextN int
	// Key names the entry across refreshes, so the script keeps showing it.
	Key string
	// Kind is the small label: commit?, plan, session lost, your turn…
	Kind    string
	Title   string
	Text    string
	Answers []form
	// NoPlan says the plan was too long for the snapshot.
	NoPlan bool
}

// segment is one task of the phase bar: done, active, needs, todo or
// skipped, and Label the state in words.
type segment struct {
	ID, State, Label string
}

// recentView is a line of Recent: the event in plain words, with its
// glyph, its tone (done, alert or ""), when and how long ago.
type recentView struct {
	Glyph, Text, Tone string
	At                time.Time
	Ago               string
}

// form is one command's form: hidden fields, at most one text or select
// field, and the button. Confirm asks the second confirmation first.
type form struct {
	CSRF     string
	Type     string
	Task     string
	Question string
	Plan     string
	// Text is a fixed text the command carries (limit: start); a form
	// with a field takes its text from the owner instead.
	Text     string
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
	v := pageView{CSRF: csrf, Snap: snap, Budgets: budgetViews(snap.Budgets)}
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
	v.Status = statusOf(snap, now)
	if !snap.Started.IsZero() {
		v.Elapsed = look.Duration(now.Sub(snap.Started))
	}
	v.Segments = segmentsOf(v.Phase, snap.Active, asked)
	if v.Phase != nil && !snap.Ended {
		for _, t := range v.Phase.Tasks {
			if st := strings.ToLower(t.Status); (st == "ready" || st == "blocked") && len(v.Next) < 2 {
				v.Next = append(v.Next, t)
			}
		}
	}
	// The cards' entries first, in the cards' order, then the questions
	// without a card and the reset request; each card learns its entry's
	// place once every entry is known.
	carded := map[int]int{} // card → its entry
	for _, c := range snap.Active {
		cv := cardView{Current: c, Ran: look.Duration(now.Sub(c.Started)), Override: snap.Overrides[c.Task], Glyph: look.GlyphRunning}
		switch c.State {
		case StateWaiting:
			cv.Glyph = look.GlyphWaiting
		case StateLimited:
			// The window and the reset with its countdown, live (SPEC §15).
			cv.Glyph = look.GlyphWaiting
			if l := c.Limit; l != nil {
				cv.Detail = look.Window(l.Window) + " · continues " + look.Until(l.ResetsAt.In(now.Location()), now)
			} else {
				cv.Detail = ""
			}
		}
		if live {
			if q, ok := asks[c.Task]; ok {
				carded[len(v.Cards)] = len(v.Sheet)
				v.Sheet = append(v.Sheet, questionOf(q, csrf))
				delete(asks, c.Task)
			} else if qv, ok := sheetOf(c, csrf); ok {
				carded[len(v.Cards)] = len(v.Sheet)
				v.Sheet = append(v.Sheet, qv)
			} else {
				cv.Actions = cardActions(c, csrf)
			}
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
		if snap.BudgetQuestion != nil {
			v.Sheet = append(v.Sheet, questionOf(*snap.BudgetQuestion, csrf))
		}
		if len(v.Sheet) > 0 {
			v.Sheet[0].Sheet = true
		}
		for i := range v.Sheet {
			q := &v.Sheet[i]
			q.N, q.Of, q.NextN = i+1, len(v.Sheet), (i+1)%len(v.Sheet)+1
			q.Key = q.Kind + " " + q.Q.Task
		}
		for c, q := range carded {
			v.Cards[c].Question = &v.Sheet[q]
			v.Cards[c].Glyph = look.GlyphNeedsYou
		}
		pause := form{CSRF: csrf, Type: CmdPause, Label: "Pause after this task"}
		if snap.Paused {
			pause.Label = "Resume"
		}
		stop, _ := confirmOf(CmdStop, "")
		if snap.Limited != nil {
			// Start anyway beside Pause and Stop while the usage hold holds
			// (SPEC §6.9, §15).
			v.Header = append(v.Header, form{CSRF: csrf, Type: CmdLimit, Text: LimitStart, Label: "Start anyway"})
		}
		v.Header = append(v.Header, pause, form{CSRF: csrf, Type: CmdStop, Label: "Stop", Danger: true, Confirm: &stop})
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
		v.Recent = append(v.Recent, recentOf(feed[i], now))
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
	v := questionView{Q: q, Kind: questionKind(engine.Question(q.Question)), Title: "Question"}
	answer := func(label string, yes bool) form {
		return form{CSRF: csrf, Type: CmdAnswer, Task: t, Yes: yes, Label: label}
	}
	a := answersFor(t, csrf)
	switch engine.Question(q.Question) {
	case engine.QuestionCommit:
		v.Title = "Commit " + t + "?"
		v.Answers = []form{answer("Commit", true), answer("Leave uncommitted", false)}
	case engine.QuestionConfirmSkip:
		v.Title = t + " asks to skip"
		v.Answers = []form{answer("Keep working", false), answer("Skip the task", true)}
		v.Answers[1].Danger = true
	case engine.QuestionSessionLost:
		v.Title = t + ": session lost"
		v.Answers = []form{a.fresh, a.cont, a.done, a.skip, a.stop}
	case engine.QuestionHookFailed:
		v.Title = t + ": before_task hook failed"
		a.fresh.Label = "Retry"
		v.Answers = []form{a.fresh, a.done, a.skip, a.stop}
	case engine.QuestionIntegration:
		v.Title = t + " didn't integrate"
		a.fresh.Label = "Rerun fresh"
		a.done.Label = "Mark done (integrated by hand)"
		v.Answers = []form{a.fresh, a.done, a.skip, a.stop}
	case engine.QuestionFilesInTheWay:
		v.Title = t + " didn't integrate"
		a.fresh.Label = "Try again"
		a.done.Label = "Mark done (integrated by hand)"
		v.Answers = []form{a.fresh, a.done, a.skip, a.stop}
	case engine.QuestionConfirmReset:
		v.Title = "Reset " + t + "?"
		keep, reset := answer("Keep it as it is", false), answer("Reset "+t, true)
		keep.Question, reset.Question = string(engine.QuestionConfirmReset), string(engine.QuestionConfirmReset)
		reset.Danger = true
		v.Answers = []form{keep, reset}
	case engine.QuestionBudget:
		// The run's, not a task's (SPEC §6.9, §15.5): raise it for this
		// run, let it go on, or stop.
		v.Title = "Budget reached"
		if b := q.Budget; b != nil {
			v.Title = b.Title()
		}
		stop, _ := confirmOf(CmdStop, "")
		v.Answers = []form{
			{CSRF: csrf, Type: CmdBudget, Label: "Raise", Input: "New value for this run (30M, $60)", Required: true},
			{CSRF: csrf, Type: CmdBudget, Text: state.BudgetContinue, Label: "Continue (it no longer holds this run)"},
			{CSRF: csrf, Type: CmdStop, Label: "Stop igris", Danger: true, Confirm: &stop},
		}
	case engine.QuestionPlanApproval:
		v.Title = t + " presents a plan"
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

// questionKind is a question's small label, on the sheet and in Recent.
func questionKind(q engine.Question) string {
	switch q {
	case engine.QuestionCommit:
		return "commit?"
	case engine.QuestionConfirmSkip:
		return "skip?"
	case engine.QuestionSessionLost:
		return "session lost"
	case engine.QuestionHookFailed:
		return "hook failed"
	case engine.QuestionIntegration, engine.QuestionFilesInTheWay:
		return "didn't integrate"
	case engine.QuestionConfirmReset:
		return "reset?"
	case engine.QuestionPlanApproval:
		return "plan"
	case engine.QuestionBudget:
		return "budget reached"
	}
	return "question"
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
		v.Text = "This task is yours. Do it in the terminal and run igris done there, or mark it done here."
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

// statusOf is the page's status word, the line beside it and its tone
// (SPEC §15.4): Needs you (a question, a user task's turn, a blocked or
// lost session) outranks Paused, which outranks Working; Working also
// while a pause waits for the active tasks and before the first task;
// Idle with no run; Done with the outcome once ended.
func statusOf(s Snapshot, now time.Time) status {
	if s.Ended {
		o := endOutcome(s.End)
		return status{Word: look.Done.String(), Line: o.Glyph() + " " + o.String(), Tone: outcomeTone(o)}
	}
	// clock ends st's line with how long since at, when there is an at.
	clock := func(st status, at time.Time) status {
		if !at.IsZero() {
			st.Ran, st.Since = look.Duration(now.Sub(at)), at
		}
		return st
	}
	var oldest time.Time
	questions := 0
	note := func(q Event) {
		questions++
		if oldest.IsZero() || q.At.Before(oldest) {
			oldest = q.At
		}
	}
	for _, q := range s.Questions {
		note(q)
	}
	if s.Reset != nil {
		note(*s.Reset)
	}
	if s.BudgetQuestion != nil {
		note(*s.BudgetQuestion)
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
	working := func(line string) status { return clock(status{Word: look.Working.String(), Line: line}, s.Started) }
	switch {
	case needs > 0:
		n := needs + questions
		return status{Word: look.NeedsYou.String(), Line: fmt.Sprintf("%s %s you", look.Count(n, "task", "tasks"), look.Plural(n, "needs", "need")), Tone: "needs"}
	case questions > 0:
		return clock(status{Word: look.NeedsYou.String(), Line: look.Count(questions, "question", "questions"), Tone: "needs"}, oldest)
	case s.Limited != nil:
		// The usage hold (SPEC §6.9, §15.4): the window, its use and the
		// reset with its countdown.
		l := s.Limited
		return status{Word: look.Paused.String(), Line: l.Label() + " · resumes " + look.Until(l.ResetsAt.In(now.Location()), now), Tone: "plain"}
	case s.Paused && s.Holding:
		return clock(status{Word: look.Paused.String(), Line: "holding", Tone: "plain"}, s.Started)
	case s.Paused && len(s.Active) == 1:
		return working("pauses after " + s.Active[0].Task)
	case s.Paused && len(s.Active) > 1:
		return working("pauses after these tasks")
	case s.Paused:
		return clock(status{Word: look.Paused.String(), Line: "after this task", Tone: "plain"}, s.Started)
	case len(s.Active) > 0:
		return working("nothing needs you")
	case s.Run != "":
		return working("nothing running yet")
	}
	return status{Word: look.Idle.String(), Line: "no run yet", Tone: "dim"}
}

// endOutcome is a run's outcome from run_stopped's detail; an error is a
// failure, anything else a stop.
func endOutcome(end string) look.Outcome {
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

// outcomeTone is how the word Done is drawn for an outcome.
func outcomeTone(o look.Outcome) string {
	switch o.Severity() {
	case look.Accent:
		return ""
	case look.Warn, look.Fail:
		return "ember"
	}
	return "plain"
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
		seg := segment{ID: t.ID, State: "todo", Label: "to do"}
		st, isActive := states[t.ID]
		switch {
		case strings.EqualFold(t.Status, "done"):
			seg.State, seg.Label = "done", "done"
		case strings.EqualFold(t.Status, "skipped"):
			seg.State, seg.Label = "skipped", "skipped"
		case isActive && (asked[t.ID] || st == StateYourTurn || st == StateNeedsYou || st == StateLost):
			seg.State, seg.Label = "needs", "needs you"
		case isActive:
			seg.State, seg.Label = "active", "working"
		}
		out = append(out, seg)
	}
	return out
}

// recentOf is an event as a line of Recent: in plain words, with how long
// ago it was and, for a finished task, the tokens it used.
func recentOf(ev Event, now time.Time) recentView {
	id := ev.Task
	r := recentView{At: ev.At, Ago: look.Age(now.Sub(ev.At))}
	with := func(s, more string) string {
		if more == "" {
			return s
		}
		return s + " · " + more
	}
	done := func(text string) { r.Glyph, r.Text, r.Tone = look.GlyphDone, text, "done" }
	warn := func(glyph, text string) { r.Glyph, r.Text, r.Tone = glyph, text, "alert" }
	switch engine.EventKind(ev.Type) {
	case engine.RunStarted:
		r.Text = with("Run started", ev.Detail)
	case engine.PhaseStarted:
		r.Text = "Phase " + ev.Phase + " started"
	case engine.PhaseDone:
		done("Phase " + ev.Phase + " complete")
	case engine.PhaseStuck:
		warn(look.GlyphWarn, "Phase "+ev.Phase+" is stuck")
	case engine.TaskStarted:
		r.Glyph, r.Text = look.GlyphRunning, with(id+" started", ev.Title)
	case engine.TaskResumed:
		r.Glyph, r.Text = look.GlyphRunning, id+" resumed"
	case engine.SessionOpened:
		r.Text = with(id+" session opened", ev.Mode)
	case engine.TaskDone:
		done(with(with(id+" done", ev.Detail), tokensText(ev.Tokens)))
	case engine.TaskSkipped:
		r.Glyph, r.Text = look.GlyphSkipped, with(with(id+" skipped", ev.Detail), tokensText(ev.Tokens))
	case engine.TaskReset:
		r.Text = with("Reset", ev.Detail)
	case engine.Asked:
		r.Glyph, r.Text = look.GlyphNeedsYou, id+" asked: "+questionKind(engine.Question(ev.Question))
		if engine.Question(ev.Question) == engine.QuestionBudget {
			r.Text = "Budget reached: raise it, continue or stop"
		}
	case engine.BudgetExceeded:
		r.Glyph, r.Text = look.GlyphNeedsYou, capitalize(ev.Detail)
	case engine.BudgetRaised:
		r.Glyph, r.Text = look.GlyphRunning, capitalize(ev.Detail)
	case engine.YourTurn:
		r.Glyph, r.Text = look.GlyphNeedsYou, id+" is your turn"
	case engine.NeedsYou:
		r.Glyph, r.Text = look.GlyphNeedsYou, with(id+" needs you", ev.Detail)
	case engine.NeedsYouClear, engine.AgentWaitingOver:
		r.Glyph, r.Text = look.GlyphRunning, id+" is working again"
	case engine.AgentWaiting:
		r.Glyph, r.Text = look.GlyphWaiting, id+" waits on background work"
	case engine.SessionLimited:
		r.Glyph, r.Text = look.GlyphWaiting, with(id+" waits at a usage limit", ev.Detail)
		if ev.Detail != "" {
			r.Text = id + " " + ev.Detail
		}
	case engine.SessionLimitOver:
		r.Glyph, r.Text = look.GlyphRunning, id+" "+ev.Detail
	case engine.LimitHold:
		r.Glyph, r.Text = look.GlyphWaiting, with("Paused before "+id, ev.Detail)
	case engine.LimitResume:
		r.Glyph, r.Text = look.GlyphRunning, with("Resumed", ev.Detail)
	case engine.SessionLost:
		warn(look.GlyphFail, id+" session lost")
	case engine.TaskOverdue:
		warn(look.GlyphWarn, with(id+" overdue", ev.Detail))
	case engine.RankMismatch:
		warn(look.GlyphWarn, with(id+" rank mismatch", ev.Detail))
	case engine.PlanClosed:
		r.Text = with(id+" plan", ev.Detail)
	case engine.Retrying:
		r.Text = with(id+" new session", ev.Detail)
	case engine.VerifyStarted:
		r.Glyph, r.Text = look.GlyphWaiting, id+" verifying"
	case engine.VerifyPassed:
		done(id + " verify passed")
	case engine.VerifyFailed:
		warn(look.GlyphFail, with(id+" verify failed", ev.Detail))
	case engine.Committed:
		done(with(id+" committed", ev.Detail))
	case engine.NotCommitted:
		r.Text = with(id+" not committed", ev.Detail)
	case engine.Integrated:
		done(with(id+" integrated", ev.Detail))
	case engine.IntegrationFailed:
		warn(look.GlyphFail, id+" didn't integrate")
	case engine.HookFailed:
		warn(look.GlyphFail, with(id+" hook failed", ev.Detail))
	case engine.PauseOn:
		r.Text = "Pause after this task: on"
	case engine.PauseOff:
		r.Text = "Pause after this task: off"
	case engine.Paused:
		r.Text = "Paused before " + id
	case engine.ModeChanged:
		r.Text = "Mode of the next sessions: " + ev.Detail
	case engine.TaskModeChanged:
		r.Text = "Mode of " + id + "'s next session: " + ev.Detail
	case engine.Warning:
		warn(look.GlyphWarn, ev.Detail)
	case engine.RunFailed:
		warn(look.GlyphFail, ev.Detail)
	case engine.RunStopped:
		o := endOutcome(ev.Detail)
		r.Glyph, r.Text = o.Glyph(), "Run ended: "+o.String()
	default:
		r.Text = strings.TrimSpace(strings.ReplaceAll(ev.Type, "_", " ") + " " + id)
		r.Text = with(capital(r.Text), ev.Detail)
	}
	return r
}

// tokensText is a session's tokens as §15.4 writes them: "2.3M in · 41k
// out"; "" when unknown.
func tokensText(t *Tokens) string {
	if t == nil || t.Unavailable || (t.In == 0 && t.Out == 0) {
		return ""
	}
	if t.Priced {
		return look.InOut(t.In, t.Out) + " · " + look.Dollars(t.USD)
	}
	return look.InOut(t.In, t.Out)
}

// statusMark is a task status as its glyph (SPEC §15.4), which reads
// without colour.
func statusMark(status string) string {
	switch strings.ToLower(status) {
	case "done":
		return look.GlyphDone
	case "in progress":
		return look.GlyphRunning
	case "ready":
		return look.GlyphReady
	case "blocked":
		return look.GlyphBlocked
	case "skipped":
		return look.GlyphSkipped
	}
	return look.GlyphSkipped // an unavailable value
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
	case StateLimited:
		return "usage limit"
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
