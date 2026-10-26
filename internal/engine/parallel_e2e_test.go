package engine

// End-to-end runs of a realistic parallel plan on the fake backend, the
// fake git repository (repo) and a fake clock, one per SPEC §17 parallel
// scenario: caps, groups and owner-task exclusivity; a conflict and the
// rerun; verify failing after integration; a session lost in one of N;
// stop and resume with several active. Each checks the final plan bytes,
// the run log, the notifications and state.json. Detaching a client with
// N active is in internal/serve (parallel_e2e_test.go), which can run an
// engine where this package can't import serve.

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// parRows are the tasks of the parallel e2e plan with their initial status:
// M1-02 and M1-03 share a Group, M1-03 and M1-04 are fable (capped at one
// by default), M1-05 is a user task and M1-G a gate, both owner tasks.
var parRows = []struct {
	id, format, status string
	rank, group        string
	owner              bool
}{
	{"M1-01", "| M1-01 | **Parser** — tokenize `a \\| b` | — | %s | sonnet | agent | — |", "ready", "sonnet", "", false},
	{"M1-02", "| M1-02 | **Writer** | — | %s | sonnet | agent | io |", "ready", "sonnet", "io", false},
	{"M1-03", "| M1-03 | **Loader** | — | %s | fable | agent | io |", "ready", "fable", "io", false},
	{"M1-04", "| M1-04 | **Schema** | — | %s | fable | agent | — |", "ready", "fable", "", false},
	{"M1-05", "| M1-05 | **Domain** — buy it at any registrar | — | %s | — | user | — |", "ready", "", "", true},
	{"M1-06", "| M1-06 | **Docs** | M1-01 | %s | opus | agent | — |", "blocked", "opus", "", false},
	{"M1-G", "| M1-G | **M1 gate** — owner sign-off | M1-01…M1-06 | %s | fable | agent + user | — |", "blocked", "fable", "", true},
	{"M2-01", "| M2-01 | **API** | M1-G | %s | sonnet | agent |", "blocked", "sonnet", "", false},
}

// parPlan renders the plan with the given statuses (initial ones for IDs
// not in the map). Everything but the Status cells stays byte-identical.
func parPlan(statuses map[string]string) string {
	row := func(i int) string {
		st := parRows[i].status
		if s, ok := statuses[parRows[i].id]; ok {
			st = s
		}
		return fmt.Sprintf(parRows[i].format, st) + "\n"
	}
	var b strings.Builder
	b.WriteString("<!-- igris:format 3 -->\n# Synthetic parallel plan\n\n## M1 — Parallel work\n\n<!-- igris:parallel -->\n\n" +
		"| ID | Task | Deps | Status | Model | Owner | Group |\n|---|---|---|---|---|---|---|\n")
	for i := range 7 {
		b.WriteString(row(i))
	}
	b.WriteString("\n## M2 — Features\n\n| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n")
	b.WriteString(row(7))
	return b.String()
}

func parAllDone() map[string]string {
	m := map[string]string{}
	for _, r := range parRows {
		m[r.id] = "done"
	}
	return m
}

// parTOML runs up to three tasks at once with the default rank caps
// (fable = 1), verifies with make check and commits by itself.
const parTOML = "needs_input_after = \"1h\"\n[run]\ncommit = \"auto\"\nverify = \"make check\"\n[parallel]\nmax = 3\n"

func parThrough(o *Options) { o.Phase, o.Through = "M1", "M2" }

// parE2E is a harness on parPlan with a fake repository in which each task
// changes a file of its own, verify passing and recorded, and sessions
// that signal done a while after they open (see after).
type parE2E struct {
	*harness
	r    *repo
	cmds func() []ran

	mu sync.Mutex
	// after[id] are the delays after which the task's sessions, in the
	// order they open, run `igris done`; the last one repeats and 0 never
	// signals. A task without an entry signals after a minute.
	after map[string][]time.Duration
	opens map[string]int
	// started is the order tasks were started in, peak the most tasks
	// active in M1 at once.
	started []string
	peak    int
}

func newParE2E(t *testing.T, tomlText string) *parE2E {
	h := newParallel(t, parPlan(nil), tomlText)
	p := &parE2E{harness: h, r: h.repo(), after: map[string][]time.Duration{}, opens: map[string]int{}}
	p.cmds = h.commands()
	git := h.gitFn
	h.gitFn = func(c runner.Cmd) (runner.Result, error) {
		if c.Args[0] == "worktree" && c.Args[1] == "add" && c.Args[2] == "-b" {
			p.r.mu.Lock()
			p.r.changes[c.Args[4]] = []string{strings.ToLower(c.Args[3][strings.LastIndex(c.Args[3], "-")+1:]) + ".go"}
			p.r.mu.Unlock()
		}
		return git(c)
	}
	h.on(func(ev Event) {
		switch ev.Kind {
		case SessionOpened:
			p.mu.Lock()
			d := time.Minute
			if ds := p.after[ev.Task]; len(ds) > 0 {
				d = ds[min(p.opens[ev.Task], len(ds)-1)]
			}
			p.opens[ev.Task]++
			p.mu.Unlock()
			if d > 0 {
				h.signalAt(ev.At.Sub(t0)+d, ev.Task)
			}
		case YourTurn:
			// The owner buys the domain and runs `igris done` a minute later.
			h.signalAt(ev.At.Sub(t0)+time.Minute, ev.Task)
		case TaskStarted:
			p.started = append(p.started, ev.Task)
			p.checkActive(ev.Task)
		}
	})
	return p
}

// checkActive fails when the tasks in progress in M1, which are its active
// tasks (done is written only after integration), break a rule of SPEC
// §5.1: more than max, more than one fable, two of one Group, or an owner
// task beside another.
func (p *parE2E) checkActive(started string) {
	pl, err := plan.Load(p.path("tasks.md"), plan.Options{Root: p.root})
	if err != nil {
		p.t.Fatal(err)
	}
	var active []string
	fable, groups, owner := 0, map[string]int{}, false
	for _, task := range pl.Tasks {
		i := slices.IndexFunc(parRows, func(r struct {
			id, format, status string
			rank, group        string
			owner              bool
		}) bool {
			return r.id == task.ID
		})
		row := parRows[i]
		if task.Status != plan.InProgress || !strings.HasPrefix(task.ID, "M1-") {
			continue
		}
		active = append(active, task.ID)
		if row.rank == "fable" {
			fable++
		}
		if row.group != "" {
			groups[row.group]++
		}
		owner = owner || row.owner
	}
	p.peak = max(p.peak, len(active))
	switch {
	case len(active) > 3:
		p.t.Errorf("%s started with %v active: more than max = 3", started, active)
	case fable > 1:
		p.t.Errorf("%s started with %v active: %d fable tasks, the cap is 1", started, active, fable)
	case groups["io"] > 1:
		p.t.Errorf("%s started with %v active: two tasks of Group io", started, active)
	case owner && len(active) > 1:
		p.t.Errorf("%s started with %v active: an owner task runs alone", started, active)
	}
}

// check asserts the end state of a completed parallel e2e run: the plan's
// bytes, the run log's types, the notifications, an empty active list in
// state.json, no signals, worktrees or branches left, and the lock free.
func (p *parE2E) check(statuses map[string]string, wantLog string, wantToasts ...string) {
	p.t.Helper()
	if got, want := p.read("tasks.md"), parPlan(statuses); got != want {
		p.t.Errorf("plan after the run:\n%s\nwant:\n%s", got, want)
	}
	if got := p.logged(); got != wantLog {
		p.t.Errorf("run log:\n%s\nwant:\n%s", got, wantLog)
	}
	if got := strings.Join(p.toasts(), "\n"); got != strings.Join(wantToasts, "\n") {
		p.t.Errorf("toasts:\n%s\nwant:\n%s", got, strings.Join(wantToasts, "\n"))
	}
	run, err := p.dir.LoadRun()
	if err != nil {
		p.t.Fatal(err)
	}
	if run.Current != nil || len(run.Active) != 0 || strings.Join(run.Phases, " ") != "M1 M2" || run.Through != "M2" {
		p.t.Errorf("state.json = %+v (current %+v, active %+v)", run, run.Current, run.Active)
	}
	if sigs, bad, err := p.dir.ListSignals(); err != nil || len(sigs)+len(bad) != 0 {
		p.t.Errorf("signals left: %v %v %v", sigs, bad, err)
	}
	p.r.mu.Lock()
	if len(p.r.branches) != 0 || len(p.r.wts) != 0 {
		p.t.Errorf("left behind: branches %v, worktrees %v", p.r.branches, p.r.wts)
	}
	p.r.mu.Unlock()
	p.assertUnlocked()
}

// integratedOrder is the order the run log says tasks were integrated in.
func (p *parE2E) integratedOrder() string {
	p.t.Helper()
	events, err := p.dir.Events()
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		if e.Type == state.EventIntegrated {
			out = append(out, e.Task)
		}
	}
	return strings.Join(out, " ")
}

// Log and toast fragments of the parallel e2e plan.
const (
	parLogAgent     = " task_started verify_passed committed integrated task_done"
	parToastYour    = "request: phase M1 · M1-05 Domain: your turn: do it, then mark it done or skipped in igris"
	parToastM1Done  = "done: phase M1: complete"
	parToastM2Done  = "done: phase M2: complete"
	parStartedOrder = "M1-01 M1-02 M1-04 M1-03 M1-05 M1-06 M1-G M2-01"
)

// Three tasks start at once; M1-03 waits for its Group (M1-02) and the
// fable cap (M1-04) though a slot is free; the user task M1-05 waits until
// nothing else is active and holds back M1-06 after it; the gate runs
// alone; M2 starts only when M1 is complete (SPEC §5.1).
func TestParallelE2ECapsGroupsAndExclusivity(t *testing.T) {
	p := newParE2E(t, parTOML)
	p.after["M1-01"] = []time.Duration{4 * time.Minute}
	p.after["M1-04"] = []time.Duration{2 * time.Minute}
	p.clock.At(90*time.Second, func() {
		// M1-02 is done and a slot is free, but M1-03 shares its Group
		// with nobody now and its rank with M1-04: it waits.
		if got := p.statuses(); !strings.HasPrefix(got, "M1-01=in progress M1-02=done M1-03=ready M1-04=in progress M1-05=ready") {
			t.Errorf("after 90s: %s", got)
		}
	})
	p.clock.At(3*time.Minute+30*time.Second, func() {
		// Only M1-01 is active: M1-05 waits for it, M1-06 for M1-01.
		if got := p.statuses(); !strings.HasPrefix(got, "M1-01=in progress M1-02=done M1-03=done M1-04=done M1-05=ready M1-06=blocked") {
			t.Errorf("after 3m30s: %s", got)
		}
	})
	res, err := p.run(parThrough)
	if err != nil || res.Outcome != Completed || res.Phase != "M2" {
		t.Fatalf("Run = %s in %q, %v", res.Outcome, res.Phase, err)
	}
	if got := strings.Join(p.started, " "); got != parStartedOrder {
		t.Errorf("started %s, want %s", got, parStartedOrder)
	}
	if p.peak != 3 {
		t.Errorf("at most %d tasks active at once, want 3", p.peak)
	}
	if got := p.slots(); got != "M1-01:0 M1-02:1 M1-04:2 M1-03:1 M1-05:0 M1-06:0 M1-G:0 M2-01:-" {
		t.Errorf("slots: %s", got)
	}
	if got := p.integratedOrder(); got != "M1-02 M1-04 M1-03 M1-01 M1-06 M1-G" {
		t.Errorf("integrated %s", got)
	}
	if got := p.opened(); got != "M1-01 M1-02 M1-04 M1-03 M1-06 M1-G M2-01" {
		t.Errorf("sessions opened for %s", got)
	}
	if p.count(Overlap) != 0 {
		t.Errorf("overlap warnings for tasks that change files of their own: %s", p.kinds())
	}
	p.check(parAllDone(),
		"run_started"+strings.Repeat(" task_started", 3)+
			" verify_passed committed integrated task_done"+ // M1-02, onto the run branch as it was
			" verify_passed committed verify_passed integrated task_done"+ // M1-04, verified again once rebased
			" task_started verify_passed committed integrated task_done"+ // M1-03, started from M1-04's
			" verify_passed committed verify_passed integrated task_done"+ // M1-01
			" task_started notification task_done"+ // M1-05
			parLogAgent+parLogAgent+" notification"+ // M1-06, M1-G
			" task_started verify_passed task_done notification run_stopped", // M2-01, nothing to commit

		parToastYour, parToastM1Done, parToastM2Done)
}

// A rebase conflict reruns its task fresh by itself, once; a task that
// conflicts again is Needs you while the others go on, and the owner's
// rerun integrates it (SPEC §6.11).
func TestParallelE2EConflictsAndTheRerun(t *testing.T) {
	p := newParE2E(t, parTOML)
	p.after["M1-01"] = []time.Duration{6 * time.Minute}
	p.after["M1-04"] = []time.Duration{2 * time.Minute}
	var asked Event
	p.on(func(ev Event) {
		switch {
		case ev.Kind == RunStarted:
			p.r.conflictOn(taskWT(ev.Run, "M1-02"), 1, "shared.go")
			p.r.conflictOn(taskWT(ev.Run, "M1-04"), 2, "schema.go", "shared.go")
		case ev.Kind == Asked && ev.Question == QuestionIntegration:
			asked = ev
			p.clock.At(ev.At.Sub(t0)+5*time.Minute, func() { p.eng.Send(Command{Kind: CmdRetry, Task: ev.Task}) })
		}
	})
	res, err := p.run(parThrough)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if asked.Task != "M1-04" || !slices.Equal(asked.Output, []string{"schema.go", "shared.go"}) {
		t.Errorf("question %+v, want M1-04's with its conflicting files", asked)
	}
	// M1-01 went on and was integrated while M1-04 waited on the owner.
	if done := p.event(TaskDone, "M1-01").At; !done.After(asked.At) || !done.Before(asked.At.Add(5*time.Minute)) {
		t.Errorf("M1-01 done at %v, want while M1-04 waited (asked at %v)", done.Sub(t0), asked.At.Sub(t0))
	}
	for id, want := range map[string][]string{
		"M1-02": {"integration_failed/conflict:shared.go; then rerun", "task_retried:fresh", "integrated:M1-02: Writer", "task_done:did M1-02"},
		"M1-04": {"integration_failed/conflict:schema.go, shared.go; then rerun", "task_retried:fresh",
			"integration_failed/conflict:schema.go, shared.go; then needs you", "needs_you/integration:didn't integrate",
			"needs_you_clear/integration", "task_retried:fresh", "integrated:M1-04: Schema", "task_done:did M1-04"},
	} {
		var got []string
		for _, l := range p.logOf(id) {
			for _, k := range []string{"integration_failed", "task_retried", "needs_you", "integrated", "task_done"} {
				if strings.HasPrefix(l, k) {
					got = append(got, l)
					break
				}
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("log of %s %q, want %q", id, got, want)
		}
	}
	// A fresh session per attempt, each in a new worktree from the run
	// branch's head.
	for id, n := range map[string]int{"M1-02": 2, "M1-04": 3} {
		if got := len(p.be.Prompts(id)); got != n {
			t.Errorf("%s had %d sessions, want %d", id, got, n)
		}
		if got := len(p.r.calls(".: worktree add -b igris/" + p.runBase() + "-" + id + " ")); got != n {
			t.Errorf("%s got %d worktrees, want %d", id, got, n)
		}
	}
	if got := p.count(NeedsYou); got != 1 {
		t.Errorf("%d needs-you events, want only M1-04's second failure", got)
	}
	p.check(parAllDone(), p.logged(),
		"request: phase M1 · M1-04 Schema: didn't integrate", parToastYour, parToastM1Done, parToastM2Done)
}

// Verify failing on the integrated tree reruns the task fresh by itself
// with on_conflict = "rerun" (the default); the failure's output goes to
// the run log, never into a session (SPEC §6.11).
func TestParallelE2EVerifyFailsAfterIntegration(t *testing.T) {
	p := newParE2E(t, parTOML)
	p.after["M1-04"] = []time.Duration{2 * time.Minute}
	var mu sync.Mutex
	n := 0
	p.verifyFn = func(c runner.Cmd) (runner.Result, error) {
		if strings.HasSuffix(c.Dir, "-M1-04") {
			mu.Lock()
			n++
			k := n
			mu.Unlock()
			// M1-04's first attempt passes in its worktree, then fails
			// once rebased onto M1-01's and M1-02's commits.
			if k == 2 {
				return runner.Result{ExitCode: 1, Stdout: []byte("--- FAIL: TestSchemaParser\nFAIL\n")}, nil
			}
		}
		return runner.Result{}, nil
	}
	res, err := p.run(parThrough)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	log := p.logOf("M1-04")
	for _, want := range []string{
		"verify_passed:profile default",
		"committed:M1-04: Schema",
		"verify_failed:profile default: integrated tree: exit status 1",
		"integration_failed/verify:profile default: exit status 1; then rerun",
		"task_retried:fresh",
		"integrated:M1-04: Schema",
		"task_done:did M1-04",
	} {
		if !has(log, want) {
			t.Errorf("log of M1-04 lacks %q: %q", want, log)
		}
	}
	if prompts := p.be.Prompts("M1-04"); len(prompts) != 2 || strings.Contains(strings.Join(prompts, ""), "TestSchemaParser") {
		t.Errorf("prompts of M1-04 %q: a fresh session that never sees the integrated tree's failure", prompts)
	}
	if p.count(NeedsYou) != 0 {
		t.Errorf("needs you for a first failure: %s", p.kinds())
	}
	p.check(parAllDone(), p.logged(), parToastYour, parToastM1Done, parToastM2Done)
}

// M1-02's session dies while M1-01 and M1-04 work: only M1-02 waits on the
// owner, the others are integrated meanwhile, and the owner's continue
// resumes M1-02's conversation (SPEC §6.11).
func TestParallelE2ESessionLostInOneOfN(t *testing.T) {
	p := newParE2E(t, parTOML)
	p.after["M1-02"] = []time.Duration{0, time.Minute}
	p.after["M1-04"] = []time.Duration{2 * time.Minute}
	var asked Event
	p.on(func(ev Event) {
		switch {
		case ev.Kind == SessionOpened && ev.Task == "M1-02" && p.count(SessionLost) == 0:
			ref := *ev.Session
			p.clock.At(ev.At.Sub(t0)+30*time.Second, func() { p.be.Kill(ref) })
		case ev.Kind == Asked && ev.Question == QuestionSessionLost:
			asked = ev
			p.clock.At(ev.At.Sub(t0)+5*time.Minute, func() {
				p.eng.Send(Command{Kind: CmdRetry, Task: "M1-02", Continue: true})
			})
		}
	})
	res, err := p.run(parThrough)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if asked.Task != "M1-02" || p.count(SessionLost) != 1 {
		t.Fatalf("question %+v after %d lost sessions, want one for M1-02", asked, p.count(SessionLost))
	}
	for _, id := range []string{"M1-01", "M1-04"} {
		if done := p.event(TaskDone, id).At; !done.After(asked.At) || !done.Before(asked.At.Add(5*time.Minute)) {
			t.Errorf("%s done at %v, want while M1-02 waited (asked at %v)", id, done.Sub(t0), asked.At.Sub(t0))
		}
	}
	var specs []string
	for _, s := range p.be.Opened() {
		if s.TaskID == "M1-02" {
			specs = append(specs, arg(s, "--session-id")+"|"+arg(s, "--resume"))
		}
	}
	if len(specs) != 2 || !strings.HasSuffix(specs[1], "|"+strings.TrimSuffix(specs[0], "|")) {
		t.Errorf("M1-02's sessions %q, want the second to resume the first", specs)
	}
	// Continued in the same worktree, on the same slot.
	if got := len(p.r.calls(".: worktree add -b igris/" + p.runBase() + "-M1-02 ")); got != 1 {
		t.Errorf("M1-02 got %d worktrees, want 1", got)
	}
	if retried := p.logEvent(state.EventTaskRetried, "M1-02"); retried.Slot == nil || *retried.Slot != 1 {
		t.Errorf("task_retried %+v, want slot 1 kept", retried)
	}
	p.check(parAllDone(), p.logged(),
		"request: phase M1 · M1-02 Writer: session lost", parToastYour, parToastM1Done, parToastM2Done)
}

// Stop with three tasks active leaves every session open and every
// worktree in place, and state.json lists the three; M1-02's session runs
// `igris done` while igris is down. The next arise reattaches all three,
// opens no new session for them, and finishes the range (SPEC §6.11, §13).
func TestParallelE2EStopAndResumeWithSeveralActive(t *testing.T) {
	p := newParE2E(t, parTOML)
	for _, id := range []string{"M1-01", "M1-02", "M1-04"} {
		p.after[id] = []time.Duration{0}
	}
	p.clock.At(30*time.Second, func() { p.eng.Send(Command{Kind: CmdStop}) })
	if res, err := p.run(parThrough); err != nil || res.Outcome != Stopped {
		t.Fatalf("first run = %s, %v", res.Outcome, err)
	}
	inProgress := map[string]string{"M1-01": "in progress", "M1-02": "in progress", "M1-04": "in progress"}
	if got := p.read("tasks.md"); got != parPlan(inProgress) {
		t.Errorf("plan after the stop:\n%s", got)
	}
	if closed := p.be.Closed(); len(closed) != 0 {
		t.Errorf("sessions closed by the stop: %v", closed)
	}
	base := p.runBase()
	run, err := p.dir.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	var active []string
	for _, a := range run.Active {
		slot := "-"
		if a.Slot != nil {
			slot = fmt.Sprint(*a.Slot)
		}
		active = append(active, fmt.Sprintf("%s:%s:%v", a.TaskID, slot, a.Session != nil))
		if want := taskWT(base, a.TaskID); a.Worktree == nil || a.Worktree.Path != want || a.Worktree.Branch != "igris/"+base+"-"+a.TaskID {
			t.Errorf("%s's worktree in state.json %+v, want %s", a.TaskID, a.Worktree, want)
		}
		if _, err := os.Stat(p.path(taskWT(base, a.TaskID))); err != nil {
			t.Errorf("%s's worktree after the stop: %v", a.TaskID, err)
		}
	}
	if got := strings.Join(active, " "); got != "M1-01:0:true M1-02:1:true M1-04:2:true" {
		t.Errorf("active in state.json: %s", got)
	}
	if run.Current == nil || run.Current.TaskID != "M1-01" {
		t.Errorf("current in state.json %+v, want the first active task", run.Current)
	}

	// While igris is down M1-02's session finishes.
	if err := p.signal("M1-02"); err != nil {
		t.Fatal(err)
	}
	p.events, p.started = nil, nil
	for _, id := range []string{"M1-01", "M1-04"} {
		p.after[id] = []time.Duration{time.Minute}
	}
	p.clock.At(31*time.Second, func() {
		// The reattached tasks hold their slots: nothing else fits beside
		// M1-04 but a non-fable task outside io, and there is none ready.
		if got := p.opened(); got != "M1-01 M1-02 M1-04" {
			t.Errorf("sessions opened when resumed: %s, want the three reattached", got)
		}
	})
	p.clock.At(2*time.Minute, func() {
		for _, id := range []string{"M1-01", "M1-04"} {
			if err := p.signal(id); err != nil {
				t.Fatal(err)
			}
		}
	})
	if res, err := p.run(resumeLast); err != nil || res.Outcome != Completed || res.Phase != "M2" {
		t.Fatalf("resumed run = %s in %q, %v", res.Outcome, res.Phase, err)
	}
	if got := p.count(TaskResumed); got != 3 {
		t.Errorf("%d task_resumed events, want 3: %s", got, p.kinds())
	}
	if got := p.opened(); got != "M1-01 M1-02 M1-04 M1-03 M1-06 M1-G M2-01" {
		t.Errorf("sessions opened for %s, want no second session for the reattached", got)
	}
	if got := p.integratedOrder(); !strings.HasPrefix(got, "M1-02 ") {
		t.Errorf("integrated %s, want M1-02 first: its done came in while igris was down", got)
	}
	if got := p.slots(); !strings.HasPrefix(got, "M1-01:0 M1-02:1 M1-04:2 M1-01:0 M1-02:1 M1-04:2 ") {
		t.Errorf("slots: %s, want the reattached tasks on their slots", got)
	}
	p.check(parAllDone(), p.logged(), parToastYour, parToastM1Done, parToastM2Done)
	// The run log has both runs: the stop, then the resumed run.
	if got := p.logged(); !strings.HasPrefix(got, "run_started task_started task_started task_started run_stopped run_started task_resumed task_resumed task_resumed") {
		t.Errorf("run log: %s", got)
	}
}
