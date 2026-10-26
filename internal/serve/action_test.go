package serve

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/state"
)

// newActionWeb is an HTTP listener whose server's buttons expire by the
// listener's clock.
func newActionWeb(t *testing.T) *web {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)}
	w := newWebWith(t, func(o *Options) { o.Now = c.now }, func(o *RemoteOptions) { o.Now = c.now })
	w.clock = c
	return w
}

// ask publishes task's start and its question q, as the engine does.
func (w *web) ask(task string, q engine.Question) {
	w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.TaskStarted, Task: task, Title: "Title", Model: "opus"})
	w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.Asked, Task: task, Question: q, Detail: "?"})
}

// buttons makes the buttons of task's message about q.
func (w *web) buttons(task string, q engine.Question) []notify.Action {
	return w.srv.Buttons(w.ts.URL + "/")(notify.Message{Event: notify.NeedsInput, Project: "demo", TaskID: task, Question: string(q)})
}

// press presses a button: POST its URL path, no credentials.
func (w *web) press(a notify.Action, hdr map[string]string) (int, string) {
	w.t.Helper()
	resp := w.do(http.MethodPost, strings.TrimPrefix(a.URL, w.ts.URL), "", "", hdr)
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

// The buttons of each question (SPEC §10), each its own token in a URL
// under the listener, then Open, a link to the task's question on the web
// UI (since 1.8).
func TestActionButtonsPerQuestion(t *testing.T) {
	tests := []struct {
		q    engine.Question
		want string
	}{
		{engine.QuestionCommit, "Commit, Leave uncommitted, Open (view)"},
		{engine.QuestionConfirmSkip, "Skip, Keep working, Open (view)"},
		{engine.QuestionSessionLost, "Retry, Stop, Open (view)"},
		{engine.QuestionHookFailed, "Retry, Stop, Open (view)"},
		{engine.QuestionPlanApproval, "Approve, Open (view)"},
		{engine.QuestionIntegration, "Rerun, Open (view)"},
		{engine.QuestionFilesInTheWay, "Try again, Open (view)"},
		{engine.QuestionConfirmReset, "Reset, Keep, Open (view)"},
	}
	for _, tt := range tests {
		t.Run(string(tt.q), func(t *testing.T) {
			w := newActionWeb(t)
			w.ask("M1-03", tt.q)
			acts := w.buttons("M1-03", tt.q)
			var labels []string
			seen := map[string]bool{}
			for _, a := range acts {
				l := a.Label
				switch {
				case a.View:
					l += " (view)"
					if a.URL != w.ts.URL+"/?task=M1-03" {
						t.Errorf("%s links to %q, want the web UI", a.Label, a.URL)
					}
				case !strings.HasPrefix(a.URL, w.ts.URL+PathAction):
					t.Errorf("%s posts to %q", a.Label, a.URL)
				default:
					tok := strings.TrimPrefix(a.URL, w.ts.URL+PathAction)
					if !actionTokenShape(tok) || seen[tok] || tok == goodToken {
						t.Errorf("%s token %q: not a fresh action token", a.Label, tok)
					}
					seen[tok] = true
				}
				labels = append(labels, l)
			}
			if got := strings.Join(labels, ", "); got != tt.want {
				t.Errorf("buttons %q, want %q", got, tt.want)
			}
		})
	}
}

// answerButtons are the buttons of acts that answer, without Open.
func answerButtons(acts []notify.Action) []notify.Action {
	var out []notify.Action
	for _, a := range acts {
		if !a.View {
			out = append(out, a)
		}
	}
	return out
}

// A message gets answers only for its task's pending question; Open
// always.
func TestActionButtonsOnlyForThePendingQuestion(t *testing.T) {
	w := newActionWeb(t)
	if got := w.buttons("M1-03", engine.QuestionCommit); len(got) != 1 || !got[0].View {
		t.Errorf("nothing asked: %v, want Open alone", got)
	}
	w.ask("M1-03", engine.QuestionCommit)
	for _, tt := range []struct {
		task string
		q    engine.Question
	}{{"M1-03", engine.QuestionConfirmSkip}, {"M1-05", engine.QuestionCommit}, {"", engine.QuestionCommit}} {
		if got := answerButtons(w.buttons(tt.task, tt.q)); got != nil {
			t.Errorf("%s %s: %v, want none", tt.task, tt.q, got)
		}
	}
	if got := answerButtons(w.buttons("M1-03", engine.QuestionCommit)); len(got) != 2 {
		t.Errorf("the pending question: %v", got)
	}
}

// A button answers its question once, with its own answer, whatever
// Origin the ntfy app sends; its sibling then answers nothing.
func TestActionAnswersOnce(t *testing.T) {
	w := newActionWeb(t)
	w.ask("M1-03", engine.QuestionCommit)
	acts := answerButtons(w.buttons("M1-03", engine.QuestionCommit))
	code, text := w.press(acts[1], map[string]string{"Origin": "https://ntfy.sh", "Sec-Fetch-Site": "cross-site"})
	if code != http.StatusOK || text != "Leave uncommitted sent for M1-03" {
		t.Fatalf("press = %d %q", code, text)
	}
	want := engine.Command{Kind: engine.CmdAnswer, Task: "M1-03", Question: engine.QuestionCommit}
	if got := w.send.got(); len(got) != 1 || got[0] != want {
		t.Fatalf("sent %+v, want %+v", got, want)
	}
	for i, a := range acts {
		if code, text := w.press(a, nil); code != http.StatusGone || text != actionAnswered {
			t.Errorf("button %d again: %d %q, want 410 %q", i, code, text, actionAnswered)
		}
	}
	if got := len(w.send.got()); got != 1 {
		t.Errorf("%d commands sent, want 1", got)
	}
}

// Each button sends its own command for its task (SPEC §10).
func TestActionCommands(t *testing.T) {
	tests := []struct {
		q    engine.Question
		i    int
		want engine.Command
	}{
		{engine.QuestionCommit, 0, engine.Command{Kind: engine.CmdAnswer, Yes: true, Task: "T-1", Question: engine.QuestionCommit}},
		{engine.QuestionConfirmSkip, 0, engine.Command{Kind: engine.CmdAnswer, Yes: true, Task: "T-1", Question: engine.QuestionConfirmSkip}},
		{engine.QuestionConfirmSkip, 1, engine.Command{Kind: engine.CmdAnswer, Task: "T-1", Question: engine.QuestionConfirmSkip}},
		{engine.QuestionSessionLost, 0, engine.Command{Kind: engine.CmdRetry, Task: "T-1"}},
		{engine.QuestionHookFailed, 1, engine.Command{Kind: engine.CmdStop, Task: "T-1"}},
		{engine.QuestionPlanApproval, 0, engine.Command{Kind: engine.CmdPlan, Plan: state.PlanAllow, Task: "T-1"}},
		{engine.QuestionConfirmReset, 0, engine.Command{Kind: engine.CmdAnswer, Yes: true, Task: "T-1", Question: engine.QuestionConfirmReset}},
		{engine.QuestionConfirmReset, 1, engine.Command{Kind: engine.CmdAnswer, Task: "T-1", Question: engine.QuestionConfirmReset}},
		{engine.QuestionIntegration, 0, engine.Command{Kind: engine.CmdRetry, Task: "T-1"}},
		{engine.QuestionFilesInTheWay, 0, engine.Command{Kind: engine.CmdRetry, Task: "T-1"}},
	}
	for _, tt := range tests {
		w := newActionWeb(t)
		w.ask("T-1", tt.q)
		if code, _ := w.press(w.buttons("T-1", tt.q)[tt.i], nil); code != http.StatusOK {
			t.Errorf("%s %d: %d", tt.q, tt.i, code)
		}
		if got := w.send.got(); len(got) != 1 || got[0] != tt.want {
			t.Errorf("%s %d sent %+v, want %+v", tt.q, tt.i, got, tt.want)
		}
	}
}

// A button answers for ten minutes, then says expired.
func TestActionExpires(t *testing.T) {
	w := newActionWeb(t)
	w.ask("M1-03", engine.QuestionCommit)
	acts := w.buttons("M1-03", engine.QuestionCommit)
	w.clock.add(actionTTL - time.Second)
	more := w.buttons("M1-03", engine.QuestionCommit) // a later message's
	w.clock.add(time.Second)
	if code, text := w.press(acts[0], nil); code != http.StatusGone || text != actionExpired {
		t.Errorf("after 10 minutes: %d %q, want 410 %q", code, text, actionExpired)
	}
	if code, _ := w.press(more[0], nil); code != http.StatusOK {
		t.Errorf("a newer button: %d, want 200", code)
	}
	if got := len(w.send.got()); got != 1 {
		t.Errorf("%d commands sent, want 1", got)
	}
}

// A question answered elsewhere, asked anew or left by the run's end
// takes no button's answer.
func TestActionAnsweredElsewhere(t *testing.T) {
	type step func(w *web)
	command := func(body string) step {
		return func(w *web) {
			if resp := w.do(http.MethodPost, PathCommand, "bearer", body, nil); resp.StatusCode != http.StatusOK {
				w.t.Fatalf("command %s: %d", body, resp.StatusCode)
			}
		}
	}
	publish := func(k engine.EventKind) step {
		return func(w *web) { w.srv.Publish(engine.Event{At: w.clock.now(), Kind: k, Task: "M1-03"}) }
	}
	tests := []struct {
		name  string
		then  step
		text  string
		other bool // another task's answer: the button still works
	}{
		{"committed", publish(engine.Committed), actionAnswered, false},
		{"answered by a client", command(`{"v":1,"type":"answer","id":"1","yes":true,"task":"M1-03"}`), actionAnswered, false},
		{"answered for no task", command(`{"v":1,"type":"answer","id":"1","yes":true}`), actionAnswered, false},
		{"skipped by a client", command(`{"v":1,"type":"skip","id":"1","text":"x","task":"M1-03"}`), actionAnswered, false},
		{"asked anew", func(w *web) { w.ask("M1-03", engine.QuestionCommit) }, actionAnswered, false},
		{"run ended", publish(engine.RunStopped), actionExpired, false},
		{"another task answered", command(`{"v":1,"type":"answer","id":"1","yes":true,"task":"M1-05"}`), "", true},
		{"a reset answered", command(`{"v":1,"type":"answer","id":"1","yes":true,"question":"confirm_reset","task":"M1-03"}`), "", true},
		{"paused", command(`{"v":1,"type":"pause","id":"1"}`), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newActionWeb(t)
			w.ask("M1-03", engine.QuestionCommit)
			acts := w.buttons("M1-03", engine.QuestionCommit)
			tt.then(w)
			before := len(w.send.got())
			code, text := w.press(acts[0], nil)
			if tt.other {
				if code != http.StatusOK || len(w.send.got()) != before+1 {
					t.Errorf("press = %d %q, want it answered", code, text)
				}
				return
			}
			if code != http.StatusGone || text != tt.text {
				t.Errorf("press = %d %q, want 410 %q", code, text, tt.text)
			}
			if len(w.send.got()) != before {
				t.Errorf("the run was touched: %+v", w.send.got())
			}
		})
	}
}

// Only POST answers (a link preview's GET doesn't), an unknown or
// malformed token is refused like a failed authentication, and the owner
// token is no action token.
func TestActionRefusals(t *testing.T) {
	w := newActionWeb(t)
	w.ask("M1-03", engine.QuestionCommit)
	acts := w.buttons("M1-03", engine.QuestionCommit)
	path := strings.TrimPrefix(acts[0].URL, w.ts.URL)
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPut} {
		if resp := w.do(m, path, "", "", nil); resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d, want 405", m, resp.StatusCode)
		}
	}
	if resp := w.do(http.MethodPost, path, "", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("POST after the refused methods: %d", resp.StatusCode)
	}
	for _, p := range []string{PathAction + goodToken, PathAction + "short", PathAction, PathAction + strings.Repeat("!", 43)} {
		resp := w.do(http.MethodPost, p, "bearer", "", nil)
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusGone || strings.TrimSpace(string(b)) != actionExpired {
			t.Errorf("%s: %d %q, want 410 %q", p, resp.StatusCode, b, actionExpired)
		}
		if resp.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s: no security headers", p)
		}
	}
	// Ten unknown tokens in a window lock the address out (SPEC §14).
	for range authFails - 4 {
		w.do(http.MethodPost, PathAction+badToken, "", "", nil)
	}
	w.ask("M1-05", engine.QuestionCommit)
	fresh := w.buttons("M1-05", engine.QuestionCommit)
	if code, _ := w.press(fresh[0], nil); code != http.StatusTooManyRequests {
		t.Errorf("a valid button while limited: %d, want 429", code)
	}
	w.clock.add(authWindow)
	if code, _ := w.press(fresh[0], nil); code != http.StatusOK {
		t.Errorf("after the window: %d, want 200", code)
	}
	if got := len(w.send.got()); got != 2 {
		t.Errorf("%d commands sent, want 2", got)
	}
}

// Tokens are bounded: past the cap a message gets no buttons, and expired
// ones make room again.
func TestActionTokensBounded(t *testing.T) {
	w := newActionWeb(t)
	w.ask("M1-03", engine.QuestionCommit)
	for range maxActionTokens / 2 {
		if answerButtons(w.buttons("M1-03", engine.QuestionCommit)) == nil {
			t.Fatal("no buttons below the cap")
		}
	}
	if got := answerButtons(w.buttons("M1-03", engine.QuestionCommit)); got != nil {
		t.Errorf("past the cap: %v", got)
	}
	w.clock.add(actionTTL)
	if got := answerButtons(w.buttons("M1-03", engine.QuestionCommit)); len(got) != 2 {
		t.Errorf("after they expired: %v", got)
	}
}

// The run's questions (SPEC §10, since 1.8): a budget reached gets
// Continue, the batch question Keep (Undo only on the web UI and in the
// TUI), each then Open to the page; they send no task.
func TestActionRunQuestions(t *testing.T) {
	tests := []struct {
		q     engine.Question
		ev    notify.Event
		ask   engine.Event
		label string
		want  engine.Command
	}{
		{engine.QuestionBudget, notify.BudgetExceeded,
			engine.Event{Kind: engine.Asked, Question: engine.QuestionBudget, Budget: &engine.BudgetUse{Budget: "run", Limit: "20M", Used: "20.4M"}},
			"Continue", engine.Command{Kind: engine.CmdBudget, Text: state.BudgetContinue}},
		{engine.QuestionCommitBatch, notify.NeedsInput,
			engine.Event{Kind: engine.Asked, Question: engine.QuestionCommitBatch, Detail: "Keep the 2 commits of this run?", Undo: true},
			"Keep", engine.Command{Kind: engine.CmdAnswer, Yes: true, Question: engine.QuestionCommitBatch}},
	}
	for _, tt := range tests {
		t.Run(string(tt.q), func(t *testing.T) {
			w := newActionWeb(t)
			tt.ask.At = w.clock.now()
			w.srv.Publish(tt.ask)
			acts := w.srv.Buttons(w.ts.URL + "/")(notify.Message{Event: tt.ev, Project: "demo", Question: string(tt.q)})
			if len(acts) != 2 || acts[0].Label != tt.label || !acts[1].View || acts[1].URL != w.ts.URL+"/" {
				t.Fatalf("buttons %+v, want %s and Open to the page", acts, tt.label)
			}
			code, text := w.press(acts[0], nil)
			if code != http.StatusOK || text != tt.label+" sent for the run" {
				t.Errorf("press = %d %q", code, text)
			}
			if got := w.send.got(); len(got) != 1 || got[0] != tt.want {
				t.Errorf("sent %+v, want %+v", got, tt.want)
			}
		})
	}
}

// A budget answered on the web UI ends its Continue button.
func TestActionBudgetAnsweredElsewhere(t *testing.T) {
	w := newActionWeb(t)
	w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.Asked, Question: engine.QuestionBudget, Budget: &engine.BudgetUse{Budget: "run", Limit: "20M", Used: "20M"}})
	acts := w.srv.Buttons(w.ts.URL + "/")(notify.Message{Event: notify.BudgetExceeded, Project: "demo", Question: string(engine.QuestionBudget)})
	w.srv.answering(engine.Command{Kind: engine.CmdBudget, Text: "30M"})
	if code, text := w.press(acts[0], nil); code != http.StatusGone || text != actionAnswered {
		t.Errorf("press = %d %q, want 410 %q", code, text, actionAnswered)
	}
}

// verify_failed_limit asks no question: Retry, a fresh session, while the
// task still needs the owner for it, then Open.
func TestActionVerifyLimit(t *testing.T) {
	w := newActionWeb(t)
	w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.TaskStarted, Task: "T-1", Title: "Title", Model: "opus"})
	m := notify.Message{Event: notify.VerifyFailedLimit, Project: "demo", TaskID: "T-1"}
	if got := answerButtons(w.srv.Buttons(w.ts.URL + "/")(m)); got != nil {
		t.Errorf("before the limit: %v", got)
	}
	w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.NeedsYou, Task: "T-1", Reason: state.ReasonVerifyLimit, Detail: "verify failed 3 times"})
	acts := w.srv.Buttons(w.ts.URL + "/")(m)
	if len(acts) != 2 || acts[0].Label != "Retry" || acts[1].URL != w.ts.URL+"/?task=T-1" {
		t.Fatalf("buttons %+v", acts)
	}
	if code, _ := w.press(acts[0], nil); code != http.StatusOK {
		t.Errorf("press = %d", code)
	}
	if got := w.send.got(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdRetry, Task: "T-1"}) {
		t.Errorf("sent %+v", got)
	}
}

// While a resolver session may be opened, the didn't-integrate question's
// first option is Resolve (SPEC §6.11, §10).
func TestActionResolveFirst(t *testing.T) {
	w := newActionWeb(t)
	w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.TaskStarted, Task: "T-1", Title: "Title", Model: "opus"})
	w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.Asked, Task: "T-1", Question: engine.QuestionIntegration, Resolve: true, Detail: "?"})
	acts := w.buttons("T-1", engine.QuestionIntegration)
	if len(acts) != 2 || acts[0].Label != "Resolve" {
		t.Fatalf("buttons %+v", acts)
	}
	if code, _ := w.press(acts[0], nil); code != http.StatusOK {
		t.Errorf("press = %d", code)
	}
	if got := w.send.got(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdResolve, Task: "T-1"}) {
		t.Errorf("sent %+v", got)
	}
}

// Open goes on every message about a task, a warning's too; a grouped
// message's goes to the page; other events get none.
func TestActionOpen(t *testing.T) {
	w := newActionWeb(t)
	b := w.srv.Buttons(w.ts.URL + "/")
	for _, tt := range []struct {
		m    notify.Message
		want string
	}{
		{notify.Message{Event: notify.TaskOverdue, TaskID: "T-1"}, w.ts.URL + "/?task=T-1"},
		{notify.Message{Event: notify.RankMismatch, TaskID: "T-1"}, w.ts.URL + "/?task=T-1"},
		{notify.Message{Event: notify.NeedsInput, TaskID: "T-1"}, w.ts.URL + "/?task=T-1"},
		{notify.Message{Event: notify.SessionLost, TaskID: "T-1"}, w.ts.URL + "/?task=T-1"},
		{notify.Message{Event: notify.NeedsInput, TaskID: "T-1", Tasks: []string{"T-1", "T-2"}}, w.ts.URL + "/"},
		{notify.Message{Event: notify.PhaseDone}, ""},
		{notify.Message{Event: notify.TaskDone, TaskID: "T-1"}, ""},
	} {
		got := b(tt.m)
		switch {
		case tt.want == "" && got != nil:
			t.Errorf("%s: %+v, want none", tt.m.Event, got)
		case tt.want != "" && (len(got) != 1 || !got[0].View || got[0].Label != "Open" || got[0].URL != tt.want):
			t.Errorf("%s: %+v, want Open to %s", tt.m.Event, got, tt.want)
		}
	}
}
