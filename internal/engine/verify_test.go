package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

const verifyTOML = "[run]\nverify = \"make test\"\nverify_max_attempts = 2\n"

// verifyResults makes the fake runner answer the verify command with the
// given results in order; the last one repeats.
func (h *harness) verifyResults(results ...func() (runner.Result, error)) {
	n := 0
	h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
		r := results[min(n, len(results)-1)]
		n++
		return r()
	}
}

func pass() (runner.Result, error) { return runner.Result{}, nil }

func failWith(code int, out string) func() (runner.Result, error) {
	return func() (runner.Result, error) { return runner.Result{ExitCode: code, Stdout: []byte(out)}, nil }
}

// resignal makes a task's session run `igris done` again 4s after each
// verify failure, up to n times.
func (h *harness) resignal(n int) {
	failures := 0
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		if prev != nil {
			prev(ev)
		}
		if ev.Kind == VerifyFailed {
			if failures++; failures <= n {
				id := ev.Task
				h.signalAt(ev.At.Sub(t0)+4*time.Second, id)
			}
		}
	}
}

func TestVerifyPasses(t *testing.T) {
	h := newHarness(t, chainPlan, verifyTOML)
	h.verifyResults(pass)
	h.onEvent = func(ev Event) {
		if ev.Kind == TaskStarted && ev.Task == "A-1" {
			// A session weakening verify mid-run changes nothing (SPEC §13).
			h.write(state.ConfigFile, "[run]\nverify = \"true\"\n")
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := h.calls("sh")
	if len(calls) != 3 {
		t.Fatalf("%d verify runs, want 3", len(calls))
	}
	for _, c := range calls {
		if c.Name != "sh" || strings.Join(c.Args, " ") != "-c make test" || c.Dir != h.root || c.Timeout != 15*time.Minute || !c.Combined {
			t.Errorf("verify command = %+v", c)
		}
	}
	// The notification after the first start is the config-change toast.
	if got, want := h.logged(), "run_started task_started notification verify_passed task_done"+strings.Repeat(" task_started verify_passed task_done", 2)+" notification run_stopped"; got != want {
		t.Errorf("run log = %s\nwant      %s", got, want)
	}
	if got := h.count(VerifyPassed); got != 3 {
		t.Errorf("%d verify_passed events", got)
	}
}

func TestVerifyFailureGoesBackToTheSession(t *testing.T) {
	h := newHarness(t, chainPlan, verifyTOML)
	var out strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&out, "line %d\n", i)
	}
	h.verifyResults(failWith(2, out.String()), pass)
	h.be.Script("A-1", append(states(backend.Working, 3), backend.Idle)...)
	h.resignal(1)
	h.onEvent = func(prev func(Event)) func(Event) {
		return func(ev Event) {
			prev(ev)
			if ev.Kind == VerifyFailed {
				if sig, err := h.dir.ReadSignal("A-1"); err != nil || sig != nil {
					t.Errorf("signal kept after the failure: %+v, %v", sig, err)
				}
				run, err := h.dir.LoadRun()
				if err != nil || run.Current.VerifyAttempts != 1 {
					t.Errorf("state.json attempts = %+v, %v; want 1", run.Current, err)
				}
			}
		}
	}(h.onEvent)
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=done A-2=done A-3=done") {
		t.Errorf("statuses = %s", got)
	}
	prompts := h.be.Prompts("A-1")
	if len(prompts) != 2 {
		t.Fatalf("A-1 got %d prompts, want the task and one failure", len(prompts))
	}
	fb := prompts[1]
	for _, want := range []string{"igris verification `default` (`make test`) failed (exit status 2)", "line 41\n", "line 100\n```", "run `igris done A-1` again"} {
		if !strings.Contains(fb, want) {
			t.Errorf("failure prompt lacks %q:\n%s", want, fb)
		}
	}
	if strings.Contains(fb, "line 40\n") {
		t.Errorf("failure prompt has more than %d lines of output", verifyTailLines)
	}
	if got := h.event(VerifyFailed, "A-1").Detail; got != "default (`make test`) failed (exit status 2), attempt 1 of 2" {
		t.Errorf("verify_failed detail %q", got)
	}
	if got := h.logged(); !strings.HasPrefix(got, "run_started task_started verify_failed verify_passed task_done") {
		t.Errorf("run log = %s", got)
	}
	if got := h.count(VerifyLimit); got != 0 {
		t.Errorf("%d verify_limit events", got)
	}
}

func TestVerifyLimit(t *testing.T) {
	h := newHarness(t, chainPlan, verifyTOML)
	h.verifyResults(failWith(1, "boom\n"))
	h.resignal(2) // the session tries twice more
	h.onEvent = func(prev func(Event)) func(Event) {
		return func(ev Event) {
			prev(ev)
			switch {
			case ev.Kind == VerifyFailed && h.count(VerifyFailed) == 3:
				h.eng.Send(Command{Kind: CmdDone, Text: "owner checked it"})
			case ev.Kind == TaskDone:
				h.eng.Send(Command{Kind: CmdStop})
			}
		}
	}(h.onEvent)
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Only the first failure goes back; the limit (2) stops the rest.
	if got := h.be.Prompts("A-1"); len(got) != 2 {
		t.Errorf("A-1 got %d prompts, want the task and one failure", len(got))
	}
	if got := h.count(VerifyLimit); got != 2 {
		t.Errorf("%d verify_limit events, want one per failure at or over the limit", got)
	}
	if got := h.event(TaskDone, "A-1").Detail; got != "owner checked it" {
		t.Errorf("A-1 done note %q", got)
	}
	// The owner's done is not verified: three verify runs, all for the signals.
	if got := len(h.calls("sh")); got != 3 {
		t.Errorf("%d verify runs, want 3", got)
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=done A-2=ready") {
		t.Errorf("statuses = %s", got)
	}
	limitToasts := 0
	for _, toast := range h.toasts() {
		if strings.Contains(toast, "A-1 One: verification keeps failing") {
			limitToasts++
		}
	}
	if limitToasts != 2 {
		t.Errorf("toasts = %q, want two verify-limit toasts for A-1", h.toasts())
	}
}

func TestVerifyTimeoutIsAFailure(t *testing.T) {
	h := newHarness(t, chainPlan, verifyTOML)
	h.verifyResults(func() (runner.Result, error) {
		return runner.Result{Stdout: []byte("partial output\n"), ExitCode: -1}, fmt.Errorf("run sh: %w after 15m0s", runner.ErrTimeout)
	}, pass)
	h.resignal(1)
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	fb := h.be.Prompts("A-1")
	if len(fb) != 2 || !strings.Contains(fb[1], "timed out after 15m0s") || !strings.Contains(fb[1], "partial output") {
		t.Errorf("prompts = %q", fb)
	}
}

func TestVerifyThatCannotRunFailsTheRun(t *testing.T) {
	h := newHarness(t, chainPlan, verifyTOML)
	h.verifyResults(func() (runner.Result, error) { return runner.Result{}, errors.New(`"sh" not found in PATH`) })
	_, err := h.run()
	if err == nil || !strings.Contains(err.Error(), "not found in PATH") {
		t.Fatalf("err = %v", err)
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=in progress") {
		t.Errorf("statuses = %s", got)
	}
}

// A retry gives the new session the full verify budget again.
func TestRetryResetsVerifyAttempts(t *testing.T) {
	h := newHarness(t, chainPlan, verifyTOML)
	h.verifyResults(failWith(1, "boom\n"), failWith(1, "boom\n"), pass)
	h.resignal(1)
	h.onEvent = func(prev func(Event)) func(Event) {
		return func(ev Event) {
			prev(ev)
			if ev.Kind == VerifyLimit {
				h.eng.Send(Command{Kind: CmdRetry})
			}
		}
	}(h.onEvent)
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.opened(); got != "A-1 A-1 A-2 A-3" {
		t.Errorf("sessions opened for %q", got)
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=done") {
		t.Errorf("statuses = %s", got)
	}
}

func TestTail(t *testing.T) {
	tests := []struct {
		out  string
		n    int
		want string
	}{
		{"", 3, "(no output)"},
		{" \n\n", 3, "(no output)"},
		{"a\nb\n", 3, "a\nb"},
		{"a\nb\nc\nd", 2, "c\nd"},
		{"a\nb\nc\n\n\n", 2, "b\nc"},
	}
	for _, tt := range tests {
		if got := tail(tt.out, tt.n); got != tt.want {
			t.Errorf("tail(%q, %d) = %q, want %q", tt.out, tt.n, got, tt.want)
		}
	}
}

// Found in gate M5-06 with verify_max_attempts = 1: "verify failed 1 times".
func TestTimes(t *testing.T) {
	for n, want := range map[int]string{1: "once", 2: "2 times", 3: "3 times"} {
		if got := times(n); got != want {
			t.Errorf("times(%d) = %q, want %q", n, got, want)
		}
	}
}

// profilePlan has a Verify column: A-1 names a profile, A-2 falls back to
// the phase default, A-3 turns verification off, and B-1 falls back to the
// project default.
const profilePlan = `## A — First phase

| ID | Task | Deps | Status | Model | Verify |
|---|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet | ` + "`Fast`" + ` |
| A-2 | **Two** | A-1 | blocked | opus | — |
| A-3 | **Three** | A-2 | blocked | fable | none |

## B — Second phase

| ID | Task | Deps | Status | Model | Verify |
|---|---|---|---|---|---|
| B-1 | **Four** | A-3 | blocked | sonnet | |
`

func TestVerifyProfiles(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want []string // the verify commands run, in order
		log  []string // the run log's verify details, in order
	}{
		{"cell, phase, none, default",
			"[run]\nverify = \"make full\"\n[verify]\nfast = \"make fast\"\nslow = \"make slow\"\n[phases.a]\nverify = \"slow\"\n",
			[]string{"make fast", "make slow", "make full"},
			[]string{"profile fast", "profile slow", "profile default"}},
		{"phase none, no default",
			"[verify]\nfast = \"make fast\"\n[phases.A]\nverify = \"none\"\n",
			[]string{"make fast"},
			[]string{"profile fast"}},
		{"default from [verify]",
			"[verify]\nfast = \"make fast\"\ndefault = \"make all\"\n",
			[]string{"make fast", "make all", "make all"},
			[]string{"profile fast", "profile default", "profile default"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, profilePlan, tt.toml)
			h.verifyResults(pass)
			if _, err := h.run(func(o *Options) { o.Through = "B" }); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=done" {
				t.Errorf("statuses = %s", got)
			}
			var got []string
			for _, c := range h.calls("sh") {
				got = append(got, strings.TrimPrefix(strings.Join(c.Args, " "), "-c "))
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("verify commands = %q, want %q", got, tt.want)
			}
			events, err := h.dir.Events()
			if err != nil {
				t.Fatal(err)
			}
			var log []string
			for _, e := range events {
				if e.Type == state.EventVerifyPassed {
					log = append(log, e.Detail)
				}
			}
			if strings.Join(log, "|") != strings.Join(tt.log, "|") {
				t.Errorf("verify_passed details = %q, want %q", log, tt.log)
			}
		})
	}
}

func TestVerifyProfileFailureNamesIt(t *testing.T) {
	h := newHarness(t, profilePlan, "[verify]\nfast = \"make fast\"\n[phases.A]\nverify = \"none\"\n")
	h.verifyResults(failWith(1, "boom\n"), pass)
	h.resignal(1)
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fb := h.be.Prompts("A-1"); len(fb) != 2 || !strings.Contains(fb[1], "igris verification `fast` (`make fast`) failed (exit status 1)") {
		t.Errorf("failure prompt = %q", fb)
	}
	if ev := h.event(VerifyStarted, "A-1"); ev.Verify != "fast" || ev.Detail != "make fast" {
		t.Errorf("verify_started = %+v", ev)
	}
	if ev := h.event(SessionOpened, "A-2"); ev.Verify != "" {
		t.Errorf("A-2 has profile %q, want none", ev.Verify)
	}
	events, err := h.dir.Events()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == state.EventVerifyFailed && e.Detail != "profile fast: attempt 1 of 3: exit status 1" {
			t.Errorf("verify_failed detail %q", e.Detail)
		}
	}
}

func TestUnknownVerifyProfileStopsTheRun(t *testing.T) {
	h := newHarness(t, strings.Replace(profilePlan, "`Fast`", "fsat", 1), "[verify]\nfast = \"make fast\"\n")
	_, err := h.run()
	if err == nil || !strings.Contains(err.Error(), `A-1: unknown verify profile "fsat"; define it under [verify] in igris.toml or use one of: fast, none`) {
		t.Fatalf("Run error = %v", err)
	}
	if got := h.calls("sh"); len(got) != 0 {
		t.Errorf("verify ran: %+v", got)
	}
}
