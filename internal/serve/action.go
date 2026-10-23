package serve

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
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

// answer is one button of a question: its label and the command it sends,
// or with view set a link to the web UI instead.
type answer struct {
	label string
	cmd   engine.Command
	view  bool
}

// buttonSets are the buttons of each question that gets some (SPEC §10).
// Rejecting a plan needs feedback, so its second button opens the web UI.
// Retry starts a fresh session: continuing needs a conversation, which a
// lost session may never have had and a failed before_task hook never has.
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
		{label: "Open", view: true},
	},
}

// asking is one asking of a question: the buttons of one message share it,
// so once one of them answers, the others don't.
type asking struct {
	// ask is the tracker's pending question the buttons answer: while
	// the tracker holds another one for the task (or none), the question
	// was answered elsewhere.
	ask      *Event
	task     string
	answered bool
}

// actionToken is what serve keeps of a button's token, under its hash.
type actionToken struct {
	of      *asking
	label   string
	cmd     engine.Command
	expires time.Time
}

// Buttons returns the answer buttons of a question's ntfy message, for
// engine.Options.Actions: base is the HTTP listener's URL as the phone
// reaches it. A message gets buttons only while its task's question is
// pending here, one token per button.
func (s *Server) Buttons(base string) notify.Actions {
	base = strings.TrimRight(base, "/")
	return func(m notify.Message) []notify.Action {
		set := buttonSets[engine.Question(m.Question)]
		if len(set) == 0 || m.TaskID == "" {
			return nil
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		ask := s.track.asks[m.TaskID]
		if ask == nil || ask.Question != m.Question {
			return nil
		}
		now := s.now()
		s.pruneActions(now)
		if len(s.actions)+len(set) > maxActionTokens {
			s.opts.Logf("ntfy buttons left out: %d answer buttons are still valid", len(s.actions))
			return nil
		}
		of := &asking{ask: ask, task: m.TaskID}
		out := make([]notify.Action, 0, len(set))
		for _, a := range set {
			if a.view {
				out = append(out, notify.Action{View: true, Label: a.label, URL: base + "/"})
				continue
			}
			tok, err := newActionToken()
			if err != nil {
				s.opts.Logf("ntfy buttons left out: %v", err)
				return nil
			}
			cmd := a.cmd
			cmd.Task = m.TaskID
			if cmd.Kind == engine.CmdAnswer {
				cmd.Question = engine.Question(m.Question)
			}
			s.actions[sha256.Sum256([]byte(tok))] = &actionToken{of: of, label: a.label, cmd: cmd, expires: now.Add(actionTTL)}
			out = append(out, notify.Action{Label: a.label, URL: base + PathAction + tok})
		}
		return out
	}
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
	case s.track.asks[t.of.task] != t.of.ask:
		// Answered elsewhere, or the question was asked anew.
		t.of.answered = true
		return engine.Command{}, "", actionAnswered, true
	}
	t.of.answered = true
	return t.cmd, t.label, "", true
}

// answering notes a command from any client: one that may answer a
// task's question ends its buttons (SPEC §10: answered elsewhere). One
// for no task in particular is for the first active task, which serve
// doesn't decide, so it ends every task's buttons.
func (s *Server) answering(c engine.Command) {
	switch c.Kind {
	case engine.CmdAnswer:
		if c.Question == engine.QuestionConfirmReset {
			return
		}
	case engine.CmdRetry, engine.CmdDone, engine.CmdSkip, engine.CmdPlan:
	default:
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.actions {
		if c.Task == "" || t.of.task == c.Task {
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
	h.srv.opts.Logf("ntfy answer: %s for %s", label, cmd.Task)
	h.srv.answering(cmd)
	if h.srv.opts.Sender != nil {
		h.srv.opts.Sender.Send(cmd)
	}
	actionText(w, http.StatusOK, label+" sent for "+cmd.Task)
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
