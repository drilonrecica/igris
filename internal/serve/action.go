package serve

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/state"
)

// Answers from ntfy's buttons (SPEC §10): each button's URL carries an
// action token of its own. A token answers one question of one task with
// one answer, once, for ten minutes; serve keeps only its SHA-256, in
// memory, so a token is gone when serve ends.

const (
	// actionTTL is how long a button answers.
	actionTTL = 10 * time.Minute
	// actionBytes is an action token's randomness; it travels base64url.
	actionBytes = 32
	// maxActionTokens bounds the tokens kept at once; past it a message
	// gets no buttons until older ones expire.
	maxActionTokens = 1024
)

// The texts of a button that doesn't answer (SPEC §10).
const (
	actionAnswered = "already answered"
	actionExpired  = "expired"
)

// answer is one button of a question: its label and the command it sends.
type answer struct {
	label string
	cmd   engine.Command
}

// buttonSets are the buttons of each question that gets some (SPEC §10),
// before Open, which every message about a task gets. Rejecting a plan
// needs feedback, Commit all and Undo are on the web UI's sheet only, and
// a budget's Raise needs a value: Open goes there. Retry starts a fresh
// session: continuing needs a conversation, which a lost session may
// never have had and a failed before_task hook never has.
var buttonSets = map[engine.Question][]answer{
	engine.QuestionCommit: {
		{label: "Commit", cmd: engine.Command{Kind: engine.CmdAnswer, Yes: true}},
		{label: "Leave uncommitted", cmd: engine.Command{Kind: engine.CmdAnswer}},
	},
	engine.QuestionConfirmSkip: {
		{label: "Skip", cmd: engine.Command{Kind: engine.CmdAnswer, Yes: true}},
		{label: "Keep working", cmd: engine.Command{Kind: engine.CmdAnswer}},
	},
	engine.QuestionSessionLost: {
		{label: "Retry", cmd: engine.Command{Kind: engine.CmdRetry}},
		{label: "Stop", cmd: engine.Command{Kind: engine.CmdStop}},
	},
	engine.QuestionHookFailed: {
		{label: "Retry", cmd: engine.Command{Kind: engine.CmdRetry}},
		{label: "Stop", cmd: engine.Command{Kind: engine.CmdStop}},
	},
	engine.QuestionPlanApproval: {
		{label: "Approve", cmd: engine.Command{Kind: engine.CmdPlan, Plan: state.PlanAllow}},
	},
	engine.QuestionConfirmReset: {
		{label: "Reset", cmd: engine.Command{Kind: engine.CmdAnswer, Yes: true}},
		{label: "Keep", cmd: engine.Command{Kind: engine.CmdAnswer}},
	},
	engine.QuestionIntegration: {
		{label: "Rerun", cmd: engine.Command{Kind: engine.CmdRetry}},
	},
	engine.QuestionFilesInTheWay: {
		{label: "Try again", cmd: engine.Command{Kind: engine.CmdRetry}},
	},
	engine.QuestionBudget: {
		{label: "Continue", cmd: engine.Command{Kind: engine.CmdBudget, Text: state.BudgetContinue}},
	},
	engine.QuestionCommitBatch: {
		{label: "Keep", cmd: engine.Command{Kind: engine.CmdAnswer, Yes: true}},
	},
}

// verifyLimitButtons are the buttons of verify_failed_limit, which asks no
// question: Retry starts a fresh session (SPEC §10).
var verifyLimitButtons = []answer{{label: "Retry", cmd: engine.Command{Kind: engine.CmdRetry}}}

// resolveButton is the didn't-integrate question's first option while a
// resolver session may be opened for the task (SPEC §6.11, §10).
var resolveButton = answer{label: "Resolve", cmd: engine.Command{Kind: engine.CmdResolve}}

// opens are the events whose message gets Open (SPEC §10): the Needs-you
// ones and the warnings about a task.
var opens = map[notify.Event]bool{
	notify.NeedsInput: true, notify.SessionLost: true, notify.VerifyFailedLimit: true,
	notify.TaskOverdue: true, notify.RankMismatch: true, notify.BudgetExceeded: true,
}

// asking is one asking of a question: the buttons of one message share it,
// so once one of them answers, the others don't.
type asking struct {
	// live says the question the buttons answer is still the one pending
	// (s.mu held): once the tracker holds another one (or none), it was
	// answered elsewhere.
	live     func() bool
	task     string
	question engine.Question
	answered bool
}

// actionToken is what serve keeps of a button's token, under its hash.
type actionToken struct {
	of      *asking
	label   string
	cmd     engine.Command
	expires time.Time
}

// Buttons returns the buttons of an ntfy message, for
// engine.Options.Actions: base is the HTTP listener's URL as the phone
// reaches it. A message about one task gets Open to its question on the
// web UI (a grouped one, or one of the run's, to the page); a message
// that announces a question still pending here also gets its answers
// first, one token per button.
func (s *Server) Buttons(base string) notify.Actions {
	base = strings.TrimRight(base, "/")
	return func(m notify.Message) []notify.Action {
		s.mu.Lock()
		defer s.mu.Unlock()
		set, of := s.pending(m)
		var out []notify.Action
		if len(set) > 0 {
			now := s.now()
			s.pruneActions(now)
			if len(s.actions)+len(set) > maxActionTokens {
				s.opts.Logf("ntfy buttons left out: %d answer buttons are still valid", len(s.actions))
				set = nil
			}
			for _, a := range set {
				tok, err := newActionToken()
				if err != nil {
					s.opts.Logf("ntfy buttons left out: %v", err)
					out = nil
					break
				}
				cmd := a.cmd
				if of.task != "" && of.question != engine.QuestionBudget && of.question != engine.QuestionCommitBatch {
					cmd.Task = of.task
				}
				if cmd.Kind == engine.CmdAnswer {
					cmd.Question = of.question
				}
				s.actions[sha256.Sum256([]byte(tok))] = &actionToken{of: of, label: a.label, cmd: cmd, expires: now.Add(actionTTL)}
				out = append(out, notify.Action{Label: a.label, URL: base + PathAction + tok})
			}
		}
		if opens[m.Event] || m.Question != "" {
			open := base + "/"
			if m.TaskID != "" && len(m.Tasks) == 0 {
				open += "?task=" + url.QueryEscape(m.TaskID)
			}
			out = append(out, notify.Action{View: true, Label: "Open", URL: open})
		}
		return out
	}
}

// pending is the answer buttons of m's question while it is pending here,
// with the asking they share; none otherwise. s.mu is held.
func (s *Server) pending(m notify.Message) ([]answer, *asking) {
	if len(m.Tasks) > 0 || m.Event == notify.Digest {
		return nil, nil // a grouped message, a digest: no buttons (SPEC §10)
	}
	q := engine.Question(m.Question)
	of := &asking{task: m.TaskID, question: q}
	set := buttonSets[q]
	switch {
	case q == engine.QuestionBudget:
		ask := s.track.budgetAsk
		if ask == nil {
			return nil, nil
		}
		of.live = func() bool { return s.track.budgetAsk == ask }
	case q == engine.QuestionCommitBatch:
		ask := s.track.batchAsk
		if ask == nil {
			return nil, nil
		}
		of.live = func() bool { return s.track.batchAsk == ask }
	case q == engine.QuestionConfirmReset:
		ask := s.track.reset
		if ask == nil || ask.Task != m.TaskID || m.TaskID == "" {
			return nil, nil
		}
		of.live = func() bool { return s.track.reset == ask }
	case q != "":
		ask := s.track.asks[m.TaskID]
		if m.TaskID == "" || ask == nil || ask.Question != m.Question {
			return nil, nil
		}
		if q == engine.QuestionIntegration && ask.Resolve {
			set = []answer{resolveButton}
		}
		of.live = func() bool { return s.track.asks[of.task] == ask }
	case m.Event == notify.VerifyFailedLimit && m.TaskID != "":
		c := s.track.card(m.TaskID)
		if c == nil || c.State != StateNeedsYou || c.Reason != state.ReasonVerifyLimit {
			return nil, nil
		}
		since := c.Since
		of.live = func() bool {
			c := s.track.card(of.task)
			return c != nil && c.State == StateNeedsYou && c.Reason == state.ReasonVerifyLimit && c.Since.Equal(since)
		}
		set = verifyLimitButtons
	}
	if of.live == nil || len(set) == 0 {
		return nil, nil
	}
	return set, of
}

// newActionToken is a fresh random action token.
func newActionToken() (string, error) {
	b := make([]byte, actionBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pruneActions forgets the expired tokens; s.mu is held.
func (s *Server) pruneActions(now time.Time) {
	for k, t := range s.actions {
		if !now.Before(t.expires) {
			delete(s.actions, k)
		}
	}
}

// redeem answers with the token's button, once. It returns the command
// to send, or why it isn't sent: known says the token was one of serve's
// (an unknown one counts as a failed authentication).
func (s *Server) redeem(tok string) (cmd engine.Command, label, refusal string, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneActions(now) // an expired token is unknown from here on
	t := s.actions[sha256.Sum256([]byte(tok))]
	switch {
	case t == nil:
		return engine.Command{}, "", actionExpired, false
	case t.of.answered:
		return engine.Command{}, "", actionAnswered, true
	case s.track.ended:
		return engine.Command{}, "", actionExpired, true
	case !t.of.live():
		// Answered elsewhere, or the question was asked anew.
		t.of.answered = true
		return engine.Command{}, "", actionAnswered, true
	}
	t.of.answered = true
	return t.cmd, t.label, "", true
}

// answering notes a command from any client: one that may answer a
// question ends its buttons (SPEC §10: answered elsewhere). One for no
// task in particular is for the first active task, which serve doesn't
// decide, so it ends every task's buttons. A reset request, the budget
// and the batch question are answered by their own commands only.
func (s *Server) answering(c engine.Command) {
	var answers func(of *asking) bool
	switch c.Kind {
	case engine.CmdAnswer:
		switch c.Question {
		case engine.QuestionConfirmReset:
			answers = func(of *asking) bool {
				return of.question == engine.QuestionConfirmReset && (c.Task == "" || of.task == c.Task)
			}
		case engine.QuestionCommitBatch:
			answers = func(of *asking) bool { return of.question == engine.QuestionCommitBatch }
		default:
			answers = func(of *asking) bool {
				return of.question != engine.QuestionConfirmReset && of.question != engine.QuestionBudget &&
					(c.Task == "" || of.task == c.Task)
			}
		}
	case engine.CmdBudget:
		answers = func(of *asking) bool { return of.question == engine.QuestionBudget }
	case engine.CmdRetry, engine.CmdDone, engine.CmdSkip, engine.CmdPlan, engine.CmdResolve:
		answers = func(of *asking) bool {
			return of.question != engine.QuestionConfirmReset && of.question != engine.QuestionBudget &&
				of.question != engine.QuestionCommitBatch && (c.Task == "" || of.task == c.Task)
		}
	default:
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.actions {
		if answers(t.of) {
			t.of.answered = true
		}
	}
}

// action answers an ntfy button: POST PathAction<token> (SPEC §10). The
// token is the whole authority, so neither the owner token nor CSRF
// applies: the phone's ntfy app sends no cookie, and nothing ambient
// grants a request this path.
func (h *remote) action(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	key := addrKey(r.RemoteAddr)
	if wait, limited := h.limit.blocked(key); limited {
		h.tooMany(w, r, wait)
		return
	}
	tok := strings.TrimPrefix(r.URL.Path, PathAction)
	if !actionTokenShape(tok) {
		h.limit.fail(key)
		actionText(w, http.StatusGone, actionExpired)
		return
	}
	cmd, label, refusal, known := h.srv.redeem(tok)
	if refusal != "" {
		if !known {
			h.limit.fail(key)
		}
		actionText(w, http.StatusGone, refusal)
		return
	}
	what := cmd.Task
	if what == "" {
		what = "the run"
	}
	h.srv.opts.Logf("ntfy answer: %s for %s", label, what)
	h.srv.answering(cmd)
	if h.srv.opts.Sender != nil {
		h.srv.opts.Sender.Send(cmd)
	}
	actionText(w, http.StatusOK, label+" sent for "+what)
}

// actionTokenShape reports whether tok looks like an action token: the
// base64url of actionBytes bytes.
func actionTokenShape(tok string) bool {
	if len(tok) != base64.RawURLEncoding.EncodedLen(actionBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(tok)
	return err == nil
}

// actionText answers a button with a short text.
func actionText(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, text+"\n")
}
