package serve

import (
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Snapshot is what a client starts from (SPEC §13): the run as the run
// view shows it at this moment. The events after it continue it.
type Snapshot struct {
	V        int    `json:"v"`
	Type     string `json:"type"`
	Protocol int    `json:"protocol"`
	// Server names the serving igris, e.g. "igris 1.4.0".
	Server  string `json:"server"`
	Project string `json:"project,omitempty"`
	Backend string `json:"backend,omitempty"`

	Run   string   `json:"run,omitempty"`
	Phase string   `json:"phase,omitempty"`
	Slice []string `json:"slice,omitempty"`
	Mode  string   `json:"mode,omitempty"`
	// Overrides are the per-task modes the owner chose (task → mode).
	Overrides map[string]string `json:"overrides,omitempty"`
	Paused    bool              `json:"paused,omitempty"`
	// Holding says the run holds because of the pause.
	Holding  bool `json:"holding,omitempty"`
	Worktree bool `json:"worktree,omitempty"`
	Ended    bool `json:"ended,omitempty"`
	// End is the outcome once the run ended (run_stopped's detail).
	End string `json:"end,omitempty"`

	Phases []Phase `json:"phases,omitempty"`
	// PlanError says why Phases is empty: the plan can't be read.
	PlanError string `json:"plan_error,omitempty"`

	Current *Current `json:"current,omitempty"`
	// Question is the asked event the current task waits on; Reset the
	// pending reset request's (SPEC §6.2).
	Question *Event       `json:"question,omitempty"`
	Reset    *Event       `json:"reset,omitempty"`
	Quota    *state.Quota `json:"quota,omitempty"`
	// Feed is the last events, oldest first, without plan text or hook
	// output.
	Feed []Event `json:"feed,omitempty"`
}

// Phase is a phase of the plan with its tasks.
type Phase struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Tasks []Task `json:"tasks"`
}

// Task is a task as the run view's list shows it.
type Task struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status"`
	Owner  string `json:"owner,omitempty"`
	Rank   string `json:"rank,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

// Current is the task the run works on.
type Current struct {
	Task    string    `json:"task"`
	Title   string    `json:"title,omitempty"`
	Rank    string    `json:"rank,omitempty"`
	Model   string    `json:"model,omitempty"`
	Mode    string    `json:"mode,omitempty"`
	User    bool      `json:"user,omitempty"`
	Started time.Time `json:"started"`
	// State is the card's state: working, your_turn, waiting, needs_you,
	// verifying, lost or question, since Since; Detail says why.
	State         string              `json:"state"`
	Since         time.Time           `json:"since"`
	Detail        string              `json:"detail,omitempty"`
	Session       *backend.SessionRef `json:"session,omitempty"`
	ClaudeSession string              `json:"claude_session,omitempty"`
	// Waiting says the agent waits on background work (SPEC §6.3).
	Waiting      bool    `json:"waiting,omitempty"`
	Overdue      string  `json:"overdue,omitempty"`
	RankMismatch string  `json:"rank_mismatch,omitempty"`
	Tokens       *Tokens `json:"tokens,omitempty"`
}

// The card's states.
const (
	StateWorking   = "working"
	StateYourTurn  = "your_turn"
	StateWaiting   = "waiting"
	StateNeedsYou  = "needs_you"
	StateVerifying = "verifying"
	StateLost      = "lost"
	StateQuestion  = "question"
)

// feedLen is how many events the snapshot's feed keeps.
const feedLen = 200

// tracker folds the events into the snapshot's run state, as the run view
// folds them into its own (internal/tui).
type tracker struct {
	run, phase, mode, end     string
	slice                     []string
	overrides                 map[string]string
	paused, holding, worktree bool
	ended                     bool
	cur                       *Current
	question, reset           *Event
	quota                     *state.Quota
	feed                      []Event // a ring of the last feedLen events
	feedNext                  int
}

func newTracker(mode string) *tracker {
	return &tracker{mode: mode, overrides: map[string]string{}}
}

// apply folds one event, ev being ev's wire form.
func (t *tracker) apply(raw engine.Event, ev Event) {
	if raw.Kind == engine.RunStarted && raw.Run != "" {
		t.run = raw.Run
	}
	if raw.Phase != "" {
		t.phase = raw.Phase
	}
	t.addFeed(ev)
	switch raw.Kind {
	case engine.RunStarted:
		t.slice = ev.Slice
	case engine.TaskStarted, engine.TaskResumed:
		if t.cur == nil || t.cur.Task != raw.Task {
			t.cur = &Current{Task: raw.Task, Started: ev.At}
		}
		t.cur.Title, t.cur.Rank, t.cur.Model, t.cur.Mode = raw.Title, raw.Rank, raw.Model, raw.Mode
		t.cur.User = raw.Model == ""
		if raw.Session != nil { // a resume reattached it
			t.cur.Session, t.cur.ClaudeSession = raw.Session, raw.ClaudeSession
		}
		t.setState(StateWorking, ev)
		t.holding = false
		t.settle()
	case engine.SessionOpened:
		if t.cur != nil {
			t.cur.Session, t.cur.Mode, t.cur.ClaudeSession = raw.Session, raw.Mode, raw.ClaudeSession
			t.cur.Overdue, t.cur.RankMismatch, t.cur.Tokens = "", "", nil // a new attempt
		}
	case engine.UsageRead:
		if t.cur != nil && t.cur.Task == raw.Task {
			t.cur.Tokens = ev.Tokens
		}
	case engine.QuotaRead:
		t.quota = raw.Quota
	case engine.TaskOverdue:
		if t.cur != nil {
			t.cur.Overdue = ev.Detail
		}
	case engine.RankMismatch:
		if t.cur != nil && t.cur.Task == raw.Task {
			t.cur.RankMismatch = ev.Detail
		}
	case engine.YourTurn:
		t.setState(StateYourTurn, ev)
	case engine.AgentWaiting:
		t.setState(StateWaiting, ev)
		if t.cur != nil {
			t.cur.Waiting = true
		}
	case engine.AgentWaitingOver:
		if t.cur != nil {
			t.cur.Waiting = false
			if t.cur.State == StateWaiting {
				t.setState(StateWorking, ev)
			}
		}
	case engine.NeedsYou:
		if t.cur != nil && t.cur.Waiting && t.cur.State == StateWaiting {
			t.cur.State, t.cur.Detail = StateNeedsYou, ev.Detail // the wait counts from its start
			break
		}
		t.setState(StateNeedsYou, ev)
	case engine.NeedsYouClear, engine.VerifyFailed:
		t.setState(StateWorking, ev)
	case engine.VerifyStarted:
		t.setState(StateVerifying, ev)
	case engine.SessionLost:
		t.setState(StateLost, ev)
		if t.cur != nil {
			t.cur.Session, t.cur.Overdue, t.cur.RankMismatch = nil, "", ""
		}
	case engine.Asked:
		e := ev
		if raw.Question == engine.QuestionConfirmReset {
			t.reset = &e
			break
		}
		t.question = &e
		t.setState(StateQuestion, ev)
	case engine.PlanClosed:
		if t.question != nil && t.question.Question == string(engine.QuestionPlanApproval) {
			t.question = nil
		}
		if t.cur != nil && t.cur.State == StateQuestion {
			t.setState(StateNeedsYou, ev)
		}
	case engine.ResetDropped:
		t.dropReset(raw.Task)
	case engine.Retrying:
		if t.cur != nil {
			t.cur.Session, t.cur.Overdue, t.cur.RankMismatch = nil, "", ""
		}
		t.setState(StateWorking, ev)
		t.settle()
	case engine.Committed, engine.NotCommitted:
		t.settle()
	case engine.TaskReset:
		t.dropReset(raw.Task)
		if t.cur != nil && t.cur.Task == raw.Task {
			t.cur = nil
			t.settle()
		}
	case engine.TaskDone, engine.TaskSkipped:
		t.cur = nil
		t.settle()
	case engine.PauseOn:
		t.paused = true
	case engine.PauseOff:
		t.paused, t.holding = false, false
	case engine.Paused:
		t.holding = true
	case engine.ModeChanged:
		t.mode = raw.Detail
	case engine.TaskModeChanged:
		t.overrides[raw.Task] = raw.Detail
	case engine.WorktreeReady:
		t.worktree = true
	case engine.RunStopped:
		t.ended, t.end = true, raw.Detail
		t.settle()
		t.reset = nil
	}
}

func (t *tracker) setState(s string, ev Event) {
	if t.cur == nil {
		return
	}
	t.cur.State, t.cur.Since, t.cur.Detail = s, ev.At, ev.Detail
}

// settle drops the current task's question: a later event answered it.
func (t *tracker) settle() { t.question = nil }

func (t *tracker) dropReset(task string) {
	if t.reset != nil && (task == "" || t.reset.Task == task) {
		t.reset = nil
	}
}

// addFeed keeps ev in the feed, without the parts only a dialog shows.
func (t *tracker) addFeed(ev Event) {
	if ev.Type == EventTypeTokens || ev.Type == string(engine.QuotaRead) {
		return // counters, not feed lines
	}
	ev.Plan, ev.Output = "", nil
	if len(t.feed) < feedLen {
		t.feed = append(t.feed, ev)
		return
	}
	t.feed[t.feedNext] = ev
	t.feedNext = (t.feedNext + 1) % feedLen
}

// snapshot returns the run state, with the feed oldest first. Nothing in
// it is shared with the tracker.
func (t *tracker) snapshot() Snapshot {
	s := Snapshot{
		V: Protocol, Type: TypeSnapshot, Protocol: Protocol,
		Run: t.run, Phase: t.phase, Slice: t.slice, Mode: t.mode,
		Paused: t.paused, Holding: t.holding, Worktree: t.worktree, Ended: t.ended, End: t.end,
		Question: t.question, Reset: t.reset, Quota: t.quota,
	}
	if len(t.overrides) > 0 {
		s.Overrides = make(map[string]string, len(t.overrides))
		for k, v := range t.overrides {
			s.Overrides[k] = v
		}
	}
	if t.cur != nil {
		c := *t.cur
		s.Current = &c
	}
	s.Feed = make([]Event, 0, len(t.feed))
	s.Feed = append(s.Feed, t.feed[t.feedNext:]...)
	s.Feed = append(s.Feed, t.feed[:t.feedNext]...)
	return s
}

// maxTitle is how much of a task title the snapshot keeps.
const maxTitle = 200

// phasesOf lists the plan's phases and tasks, their text cleaned for the
// terminal (the plan is untrusted, SPEC §16).
func phasesOf(p *plan.Plan) []Phase {
	out := make([]Phase, 0, len(p.Phases))
	for _, ph := range p.Phases {
		x := Phase{ID: textsafe.Line(ph.ID), Title: cut(textsafe.Line(ph.Title), maxTitle), Tasks: make([]Task, 0, len(ph.Tasks))}
		for _, tk := range ph.Tasks {
			x.Tasks = append(x.Tasks, Task{
				ID: textsafe.Line(tk.ID), Title: cut(textsafe.Line(tk.Title), maxTitle), Status: tk.Status.String(),
				Owner: textsafe.Line(string(tk.Owner)), Rank: textsafe.Line(tk.Rank), Mode: textsafe.Line(tk.Mode),
			})
		}
		out = append(out, x)
	}
	return out
}

// encodeSnapshot marshals s, giving up the feed and then the plan when the
// line would be too long.
func encodeSnapshot(s Snapshot) ([]byte, error) {
	for {
		b, err := encode(s)
		if err == nil {
			return b, nil
		}
		switch {
		case len(s.Feed) > 0:
			s.Feed = s.Feed[len(s.Feed)/2+len(s.Feed)%2:]
		case len(s.Phases) > 0:
			s.Phases, s.PlanError = nil, "the plan is too large for the snapshot"
		default:
			s.Question, s.Reset = nil, nil
			return encode(s)
		}
	}
}
