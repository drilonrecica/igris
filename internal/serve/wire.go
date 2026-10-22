// Package serve hosts a run on a unix socket (SPEC §13, decisions.md
// V14-P1): the socket and its path, protocol 1's framing and messages, the
// snapshot a client starts from, and the fan-out of the engine's events to
// every attached client. The engine itself is unchanged: the host passes
// its events to Server.Publish and the clients' commands go to its Send.
package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Protocol is the socket protocol's version, the "v" of every message. It
// stays 1 for all of 1.x: messages, fields and types are only added.
const Protocol = 1

// MaxLine is the longest message line either side accepts, newline
// excluded.
const MaxLine = 1 << 20

// Bounds of what a client sends (SPEC §16).
const (
	maxID     = 64       // a command's id
	maxText   = 16 << 10 // a note, a reason, feedback, a mode
	maxClient = 64       // hello's client string
	maxTail   = 200      // lines a tail may ask for
)

// Message types besides the events and the commands.
const (
	TypeHello    = "hello"
	TypeSnapshot = "snapshot"
	TypeAck      = "ack"
	TypeError    = "error"
	TypeTail     = "tail"
)

// Commands: one per engine.CommandKind (SPEC §13).
const (
	CmdPause    = "pause"
	CmdStop     = "stop"
	CmdDone     = "done"
	CmdSkip     = "skip"
	CmdRetry    = "retry"
	CmdAnswer   = "answer"
	CmdMode     = "mode"
	CmdTaskMode = "task_mode"
	CmdPlan     = "plan"
)

var commandKinds = map[string]engine.CommandKind{
	CmdPause:    engine.CmdPause,
	CmdStop:     engine.CmdStop,
	CmdDone:     engine.CmdDone,
	CmdSkip:     engine.CmdSkip,
	CmdRetry:    engine.CmdRetry,
	CmdAnswer:   engine.CmdAnswer,
	CmdMode:     engine.CmdMode,
	CmdTaskMode: engine.CmdTaskMode,
	CmdPlan:     engine.CmdPlan,
}

// EventTypeTokens is the socket type of engine.UsageRead: the run log's
// `usage` is an attempt's final count, so live usage gets a name of its
// own.
const EventTypeTokens = "tokens"

// Request is a message from a client: hello, a command or a tail request.
// Fields that don't apply to the type are ignored.
type Request struct {
	V    int    `json:"v"`
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	// Client names the client's igris on hello, e.g. "igris 1.4.0".
	Client string `json:"client,omitempty"`
	// Yolo on hello says the client's owner typed the skip-permissions
	// phrase for this run (SPEC §7.3, §14).
	Yolo bool `json:"yolo,omitempty"`
	// Text, Continue, Yes, Task, Question and Plan are engine.Command's.
	Text     string `json:"text,omitempty"`
	Continue bool   `json:"continue,omitempty"`
	Yes      bool   `json:"yes,omitempty"`
	Task     string `json:"task,omitempty"`
	Question string `json:"question,omitempty"`
	Plan     string `json:"plan,omitempty"`
	// N is how many lines a tail asks for.
	N int `json:"n,omitempty"`
}

// Reply answers a command or a tail request: ack, error or tail. An error
// without an ID ends the connection.
type Reply struct {
	V     int      `json:"v"`
	Type  string   `json:"type"`
	ID    string   `json:"id,omitempty"`
	Error string   `json:"error,omitempty"`
	Lines []string `json:"lines,omitempty"`
}

// Event is an engine event on the socket: the run log's v1 envelope and
// field meanings (state.Event) plus the fields the log never holds.
type Event struct {
	V     int       `json:"v"`
	At    time.Time `json:"at"`
	Type  string    `json:"type"`
	Run   string    `json:"run,omitempty"`
	Phase string    `json:"phase,omitempty"`
	Task  string    `json:"task,omitempty"`
	Title string    `json:"title,omitempty"`
	Rank  string    `json:"rank,omitempty"`
	Model string    `json:"model,omitempty"`
	Mode  string    `json:"mode,omitempty"`
	// Profile is the verify profile, as on the run log's verify_* lines.
	Profile string `json:"profile,omitempty"`
	Detail  string `json:"detail,omitempty"`

	Changes       []Change            `json:"changes,omitempty"`
	Waiting       []Waiting           `json:"waiting,omitempty"`
	Session       *backend.SessionRef `json:"session,omitempty"`
	ClaudeSession string              `json:"claude_session,omitempty"`
	Slice         []string            `json:"slice,omitempty"`
	Question      string              `json:"question,omitempty"`
	Output        []string            `json:"output,omitempty"`
	Plan          string              `json:"plan,omitempty"`
	Then          string              `json:"then,omitempty"`
	Tokens        *Tokens             `json:"tokens,omitempty"`
	Quota         *state.Quota        `json:"quota,omitempty"`
	// Slot is the task's slot in a parallel phase (SPEC §6.11), since 1.5.
	Slot *int `json:"slot,omitempty"`
}

// Change is a Status cell the run wrote.
type Change struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Waiting is an unfinished task with the deps it waits on; Text says it
// in words (plan.Waiting.String).
type Waiting struct {
	Task  string   `json:"task"`
	Unmet []string `json:"unmet,omitempty"`
	Text  string   `json:"text"`
}

// Tokens is engine.Tokens: the current attempt's usage so far.
type Tokens struct {
	In          int64    `json:"in"`
	Out         int64    `json:"out"`
	Models      []string `json:"models,omitempty"`
	Unavailable bool     `json:"unavailable,omitempty"`
}

// Bounds of what an event carries, so every line stays under MaxLine.
const (
	maxDetail    = 64 << 10
	maxPlan      = 512 << 10
	maxOutput    = 60
	maxListItems = 1000
)

// fromEngine converts ev, stamped with the run ID. The engine has cleaned
// its text (SPEC §16); the cuts keep the line bounded.
func fromEngine(ev engine.Event, run string) Event {
	out := Event{
		V: Protocol, At: ev.At.UTC(), Type: eventType(ev.Kind), Run: run,
		Phase: ev.Phase, Task: ev.Task, Title: ev.Title, Rank: ev.Rank, Model: ev.Model, Mode: ev.Mode,
		Profile: ev.Verify, Detail: cut(ev.Detail, maxDetail),
		Session: ev.Session, ClaudeSession: ev.ClaudeSession,
		Question: string(ev.Question), Plan: cut(ev.Plan, maxPlan), Then: ev.Then, Quota: ev.Quota,
		Slot: ev.Slot,
	}
	for _, c := range head(ev.Changes, maxListItems) {
		out.Changes = append(out.Changes, Change{ID: c.ID, From: c.From.String(), To: c.To.String()})
	}
	for _, w := range head(ev.Waiting, maxListItems) {
		out.Waiting = append(out.Waiting, waitingOf(w))
	}
	out.Slice = head(ev.Slice, 10*maxListItems)
	for _, l := range tail(ev.Output, maxOutput) {
		out.Output = append(out.Output, cut(l, 4<<10))
	}
	if t := ev.Tokens; t != nil {
		out.Tokens = &Tokens{In: t.In, Out: t.Out, Models: head(t.Models, 32), Unavailable: t.Unavailable}
	}
	return out
}

func waitingOf(w plan.Waiting) Waiting {
	if w.Task == nil {
		return Waiting{Unmet: w.Unmet}
	}
	return Waiting{Task: w.Task.ID, Unmet: w.Unmet, Text: cut(textsafe.Line(w.String()), 4<<10)}
}

// eventType is the socket type of an engine event kind.
func eventType(k engine.EventKind) string {
	if k == engine.UsageRead {
		return EventTypeTokens
	}
	return string(k)
}

// encode marshals m as one line, newline included.
func encode(m any) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxLine {
		return nil, fmt.Errorf("a %T of %d bytes is over the %d-byte line limit", m, len(b), MaxLine)
	}
	return append(b, '\n'), nil
}

// encodeEvent marshals ev, giving up its largest parts when the line would
// be too long: the plan text, the hook output, then the lists.
func encodeEvent(ev Event) ([]byte, error) {
	b, err := encode(ev)
	if err == nil {
		return b, nil
	}
	ev.Plan, ev.Output = "", nil
	if b, err = encode(ev); err == nil {
		return b, nil
	}
	ev.Changes, ev.Waiting, ev.Slice = nil, nil, nil
	return encode(ev)
}

// errNoHello and the like end a connection.
var (
	errNoHello = errors.New("the first message must be hello")
	errVersion = errors.New("protocol version not supported")
)

// decodeRequest parses one line from a client. Anything but a JSON object
// with "v": 1 and a type is an error that ends the connection.
func decodeRequest(line []byte) (Request, error) {
	var r Request
	if err := json.Unmarshal(line, &r); err != nil {
		return Request{}, fmt.Errorf("not a protocol message: %w", err)
	}
	switch {
	case r.V != Protocol:
		return Request{}, fmt.Errorf("%w: v %d (this igris speaks %d)", errVersion, r.V, Protocol)
	case r.Type == "":
		return Request{}, errors.New("a message without a type")
	}
	return r, nil
}

// checkID reports whether a command's id is usable: 1–64 bytes without
// control characters.
func checkID(id string) error {
	switch {
	case id == "":
		return errors.New("a command needs an id")
	case len(id) > maxID || !utf8.ValidString(id) || textsafe.HasControl(id) || strings.ContainsRune(id, '\t'):
		return fmt.Errorf("an id is 1-%d printable characters", maxID)
	}
	return nil
}

// questions are the questions an answer may name.
var questions = map[engine.Question]bool{
	"": true, engine.QuestionConfirmSkip: true, engine.QuestionSessionLost: true, engine.QuestionHookFailed: true,
	engine.QuestionCommit: true, engine.QuestionConfirmReset: true, engine.QuestionPlanApproval: true,
	engine.QuestionIntegration: true,
}

// command converts a command request into the engine's command, checking
// its shape: text bounded and cleaned, task IDs and choices as the engine
// knows them. The engine checks the rest as it does for the TUI.
func (r Request) command() (engine.Command, error) {
	kind, ok := commandKinds[r.Type]
	if !ok {
		return engine.Command{}, fmt.Errorf("unknown message type %q", r.Type)
	}
	if len(r.Text) > maxText {
		return engine.Command{}, fmt.Errorf("text is over %d bytes", maxText)
	}
	if r.Task != "" && (len(r.Task) > maxID || !plan.ValidID(r.Task)) {
		return engine.Command{}, fmt.Errorf("%q is not a task ID", textsafe.Line(cut(r.Task, maxID)))
	}
	c := engine.Command{Kind: kind, Text: textsafe.Clean(r.Text), Continue: r.Continue, Yes: r.Yes, Task: r.Task}
	switch kind {
	case engine.CmdAnswer:
		q := engine.Question(r.Question)
		if !questions[q] {
			return engine.Command{}, fmt.Errorf("unknown question %q", textsafe.Line(cut(r.Question, maxID)))
		}
		c.Question = q
	case engine.CmdTaskMode:
		if r.Task == "" {
			return engine.Command{}, errors.New("task_mode needs a task")
		}
	case engine.CmdPlan:
		switch r.Plan {
		case state.PlanAllow, state.PlanPane:
		case state.PlanDeny:
			if strings.TrimSpace(c.Text) == "" {
				return engine.Command{}, errors.New("rejecting a plan needs feedback in text")
			}
		default:
			return engine.Command{}, fmt.Errorf("plan is %s, %s or %s", state.PlanAllow, state.PlanDeny, state.PlanPane)
		}
		c.Plan = r.Plan
	}
	return c, nil
}

// cut shortens s to at most n bytes, on a rune boundary, marking the cut.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const mark = "…"
	i := n - len(mark)
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i] + mark
}

func head[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func tail[T any](s []T, n int) []T {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
