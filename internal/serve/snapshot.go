package serve

import (
	"slices"
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

	Run string `json:"run,omitempty"`
	// Started is when the run started (since 1.6); zero before a run.
	Started time.Time `json:"started,omitzero"`
	Phase   string    `json:"phase,omitempty"`
	Slice   []string  `json:"slice,omitempty"`
	Mode    string    `json:"mode,omitempty"`
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

	// Active are the active tasks in plan order (SPEC §6.11, since 1.5):
	// one in a sequential phase, up to max in a parallel one. Current is
	// the first of them, as 1.4 clients read it.
	Active  []Current `json:"active,omitempty"`
	Current *Current  `json:"current,omitempty"`
	// Question is the first pending question in plan order, the one
	// Answer… opens; Questions every pending question in that order (one
	// per task at most, since 1.5); Reset the pending reset request's
	// (SPEC §6.2).
	Question  *Event       `json:"question,omitempty"`
	Questions []Event      `json:"questions,omitempty"`
	Reset     *Event       `json:"reset,omitempty"`
	Quota     *state.Quota `json:"quota,omitempty"`
	// Feed is the last events, oldest first, without plan text or hook
	// output.
	Feed []Event `json:"feed,omitempty"`
}

// Phase is a phase of the plan with its tasks.
type Phase struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	// Parallel: the phase is marked parallel (SPEC §3.8, since 1.5).
	Parallel bool   `json:"parallel,omitempty"`
	Tasks    []Task `json:"tasks"`
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

// Current is an active task, as its card shows it.
type Current struct {
	Task string `json:"task"`
	// Slot is the task's slot in a parallel phase (SPEC §6.11).
	Slot    *int      `json:"slot,omitempty"`
	Title   string    `json:"title,omitempty"`
	Rank    string    `json:"rank,omitempty"`
	Model   string    `json:"model,omitempty"`
	Mode    string    `json:"mode,omitempty"`
	User    bool      `json:"user,omitempty"`
	Started time.Time `json:"started"`
	// State is the card's state: working, your_turn, waiting, needs_you,
	// verifying, lost or question, since Since; Detail says why.
	State  string    `json:"state"`
	Since  time.Time `json:"since"`
	Detail string    `json:"detail,omitempty"`
	// Reason is the run log's reason of a needs_you state (since 1.7).
	Reason        string              `json:"reason,omitempty"`
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
	started                   time.Time
	slice                     []string
	overrides                 map[string]string
	paused, holding, worktree bool
	ended                     bool
	cards                     []*Current        // the active tasks, in the order they started
	asks                      map[string]*Event // the pending question of each task
	reset                     *Event
	quota                     *state.Quota
	feed                      []Event // a ring of the last feedLen events
	feedNext                  int
}

func newTracker(mode string) *tracker {
	return &tracker{mode: mode, overrides: map[string]string{}, asks: map[string]*Event{}}
}

// card is active task id's card, or nil.
func (t *tracker) card(id string) *Current {
	for _, c := range t.cards {
		if c.Task == id {
			return c
		}
	}
	return nil
}

// drop forgets task id's card: it is no longer active.
func (t *tracker) drop(id string) {
	for i, c := range t.cards {
		if c.Task == id {
			t.cards = append(t.cards[:i:i], t.cards[i+1:]...)
			return
		}
	}
}

// apply folds one event, ev being ev's wire form. A task's events change
// its own card only.
func (t *tracker) apply(raw engine.Event, ev Event) {
	if raw.Kind == engine.RunStarted && raw.Run != "" {
		t.run = raw.Run
		t.started = raw.At
	}
	if raw.Phase != "" {
		t.phase = raw.Phase
	}
	if raw.Task == "" && len(t.cards) == 1 {
		raw.Task = t.cards[0].Task // a task's event, from a sequential run
	}
	c := t.card(raw.Task)
	if c != nil && c.Tokens != nil && ev.Tokens == nil && (raw.Kind == engine.TaskDone || raw.Kind == engine.TaskSkipped) {
		// The feed's line of a finished task says what it used (§15.8).
		tk := *c.Tokens
		tk.Models = slices.Clone(tk.Models)
		ev.Tokens = &tk
	}
	t.addFeed(ev)
	switch raw.Kind {
	case engine.RunStarted:
		t.slice = ev.Slice
	case engine.TaskStarted, engine.TaskResumed:
		if c == nil {
			c = &Current{Task: raw.Task, Started: ev.At}
			t.cards = append(t.cards, c)
		}
		c.Title, c.Rank, c.Model, c.Mode = raw.Title, raw.Rank, raw.Model, raw.Mode
		c.User = raw.Model == ""
		c.Slot = ev.Slot
		if raw.Session != nil { // a resume reattached it
			c.Session, c.ClaudeSession = raw.Session, raw.ClaudeSession
		}
		setState(c, StateWorking, ev)
		t.holding = false
		t.settle(raw.Task)
	case engine.SessionOpened:
		if c != nil {
			c.Session, c.Mode, c.ClaudeSession = raw.Session, raw.Mode, raw.ClaudeSession
			c.Overdue, c.RankMismatch, c.Tokens = "", "", nil // a new attempt
		}
	case engine.UsageRead:
		if c != nil {
			c.Tokens = ev.Tokens
		}
	case engine.QuotaRead:
		t.quota = raw.Quota
	case engine.TaskOverdue:
		if c != nil {
			c.Overdue = ev.Detail
		}
	case engine.RankMismatch:
		if c != nil {
			c.RankMismatch = ev.Detail
		}
	case engine.YourTurn:
		setState(c, StateYourTurn, ev)
	case engine.AgentWaiting:
		setState(c, StateWaiting, ev)
		if c != nil {
			c.Waiting = true
		}
	case engine.AgentWaitingOver:
		if c != nil {
			c.Waiting = false
			if c.State == StateWaiting {
				setState(c, StateWorking, ev)
			}
		}
	case engine.NeedsYou:
		if c != nil && c.Waiting && c.State == StateWaiting {
			c.State, c.Detail, c.Reason = StateNeedsYou, ev.Detail, ev.Reason // the wait counts from its start
			break
		}
		setState(c, StateNeedsYou, ev)
	case engine.NeedsYouClear, engine.VerifyFailed:
		setState(c, StateWorking, ev)
	case engine.VerifyStarted:
		setState(c, StateVerifying, ev)
	case engine.SessionLost:
		setState(c, StateLost, ev)
		if c != nil {
			c.Session, c.Overdue, c.RankMismatch = nil, "", ""
		}
	case engine.Asked:
		e := ev
		if raw.Question == engine.QuestionConfirmReset {
			t.reset = &e
			break
		}
		t.asks[raw.Task] = &e
		setState(c, StateQuestion, ev)
	case engine.PlanClosed:
		if q := t.asks[raw.Task]; q != nil && q.Question == string(engine.QuestionPlanApproval) {
			delete(t.asks, raw.Task)
		}
		if c != nil && c.State == StateQuestion {
			setState(c, StateNeedsYou, ev)
		}
	case engine.ResetDropped:
		t.dropReset(raw.Task)
	case engine.Retrying:
		if c != nil {
			c.Session, c.Overdue, c.RankMismatch = nil, "", ""
		}
		setState(c, StateWorking, ev)
		t.settle(raw.Task)
	case engine.Committed, engine.NotCommitted:
		t.settle(raw.Task)
	case engine.TaskReset:
		t.dropReset(raw.Task)
		t.drop(raw.Task)
		t.settle(raw.Task)
	case engine.TaskDone, engine.TaskSkipped:
		t.drop(raw.Task)
		t.settle(raw.Task)
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
		clear(t.asks)
		t.reset = nil
	}
}

func setState(c *Current, s string, ev Event) {
	if c == nil {
		return
	}
	c.State, c.Since, c.Detail, c.Reason = s, ev.At, ev.Detail, ev.Reason
}

// settle drops task id's question: a later event answered it.
func (t *tracker) settle(id string) { delete(t.asks, id) }

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

// snapshot returns the run state, with the feed oldest first and the
// active tasks and questions in the order of phases (the plan; tasks it
// doesn't list keep the order they started in, after the others). Nothing
// in it is shared with the tracker.
func (t *tracker) snapshot(phases []Phase) Snapshot {
	s := Snapshot{
		V: Protocol, Type: TypeSnapshot, Protocol: Protocol,
		Run: t.run, Started: t.started, Phase: t.phase, Slice: t.slice, Mode: t.mode,
		Paused: t.paused, Holding: t.holding, Worktree: t.worktree, Ended: t.ended, End: t.end,
		Reset: t.reset, Quota: t.quota,
	}
	if len(t.overrides) > 0 {
		s.Overrides = make(map[string]string, len(t.overrides))
		for k, v := range t.overrides {
			s.Overrides[k] = v
		}
	}
	order := planOrder(phases)
	rank := func(id string) int {
		if i, ok := order[id]; ok {
			return i
		}
		return len(order)
	}
	for _, c := range t.cards {
		s.Active = append(s.Active, *c)
	}
	slices.SortStableFunc(s.Active, func(a, b Current) int { return rank(a.Task) - rank(b.Task) })
	if len(s.Active) > 0 {
		c := s.Active[0]
		s.Current = &c
	}
	for _, q := range t.asks {
		s.Questions = append(s.Questions, *q)
	}
	// Map order is random: sort by the plan, then by when they were asked.
	slices.SortStableFunc(s.Questions, func(a, b Event) int {
		if d := rank(a.Task) - rank(b.Task); d != 0 {
			return d
		}
		return a.At.Compare(b.At)
	})
	if len(s.Questions) > 0 {
		q := s.Questions[0]
		s.Question = &q
	}
	s.Feed = make([]Event, 0, len(t.feed))
	s.Feed = append(s.Feed, t.feed[t.feedNext:]...)
	s.Feed = append(s.Feed, t.feed[:t.feedNext]...)
	return s
}

// planOrder numbers the tasks of phases in plan order.
func planOrder(phases []Phase) map[string]int {
	order := map[string]int{}
	for _, ph := range phases {
		for _, tk := range ph.Tasks {
			if _, ok := order[tk.ID]; !ok {
				order[tk.ID] = len(order)
			}
		}
	}
	return order
}

// maxTitle is how much of a task title the snapshot keeps.
const maxTitle = 200

// phasesOf lists the plan's phases and tasks, their text cleaned for the
// terminal (the plan is untrusted, SPEC §16).
func phasesOf(p *plan.Plan) []Phase {
	out := make([]Phase, 0, len(p.Phases))
	for _, ph := range p.Phases {
		x := Phase{ID: textsafe.Line(ph.ID), Title: cut(textsafe.Line(ph.Title), maxTitle), Parallel: ph.Parallel, Tasks: make([]Task, 0, len(ph.Tasks))}
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
			s.Question, s.Questions, s.Reset = nil, nil, nil
			return encode(s)
		}
	}
}
