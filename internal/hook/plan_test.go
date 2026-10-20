package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

// recordedPlanRequest is the PermissionRequest for ExitPlanMode recorded
// with Claude Code 2.1.304 (V11-P1).
func recordedPlanRequest(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "permissionrequest_exitplanmode.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// answerWith runs ApprovePlan on the recorded request and plays igris:
// once the request is there, decide writes the decision (nil: never).
// It returns what the hook printed.
func answerWith(t *testing.T, root string, opt PlanOptions, decide func(state.PlanRequest) *state.PlanDecision) string {
	t.Helper()
	if opt.Mode == "" {
		opt.Mode = "auto"
	}
	if opt.Wait == 0 {
		opt.Wait = time.Minute
	}
	if opt.Alive == nil {
		opt.Alive = func() bool { return true }
	}
	opt.Poll = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			r, ok, _ := state.ReadPlanRequest(root, fixtureUUID)
			if ok {
				if d := decide(r); d != nil {
					if err := state.WritePlanDecision(root, fixtureUUID, *d); err != nil {
						t.Error(err)
					}
				}
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	var out bytes.Buffer
	if err := ApprovePlan(ctx, root, bytes.NewReader(recordedPlanRequest(t)), &out, opt); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("ApprovePlan did not return")
	}
	return out.String()
}

// golden compares got with testdata/name, or rewrites it with -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // testdata golden file
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s:\n%s\nwant:\n%s", path, got, want)
	}
}

// An approval echoes the tool input as updatedInput (Claude Code ignores
// an allow for ExitPlanMode without it) and switches the mode.
func TestApprovePlanAllow(t *testing.T) {
	root := project(t)
	var plan string
	out := answerWith(t, root, PlanOptions{Mode: "acceptEdits"}, func(r state.PlanRequest) *state.PlanDecision {
		plan = r.Plan
		if r.PID != os.Getpid() {
			t.Errorf("request pid %d", r.PID)
		}
		return &state.PlanDecision{N: r.N, Behavior: state.PlanAllow}
	})
	if !strings.HasPrefix(plan, "1. Create `hello.txt`") {
		t.Errorf("request plan %q", plan)
	}
	golden(t, "plan_allow.out.json", out)
	var o struct {
		HookSpecificOutput struct {
			HookEventName string `json:"hookEventName"`
			Decision      struct {
				Behavior     string          `json:"behavior"`
				UpdatedInput json.RawMessage `json:"updatedInput"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatal(err)
	}
	var in struct {
		ToolInput json.RawMessage `json:"tool_input"`
	}
	_ = json.Unmarshal(recordedPlanRequest(t), &in)
	var a, b any
	_ = json.Unmarshal(in.ToolInput, &a)
	_ = json.Unmarshal(o.HookSpecificOutput.Decision.UpdatedInput, &b)
	if !jsonEqual(a, b) || o.HookSpecificOutput.HookEventName != "PermissionRequest" {
		t.Errorf("updatedInput %s, want the tool input %s", o.HookSpecificOutput.Decision.UpdatedInput, in.ToolInput)
	}
	assertCleanedUp(t, root)
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func TestApprovePlanDeny(t *testing.T) {
	root := project(t)
	out := answerWith(t, root, PlanOptions{}, func(r state.PlanRequest) *state.PlanDecision {
		return &state.PlanDecision{N: r.N, Behavior: state.PlanDeny, Message: "Use <Go> & keep it small"}
	})
	golden(t, "plan_deny.out.json", out)
	assertCleanedUp(t, root)
}

// The pane decides: on `pane`, after the wait, when igris is gone, when
// Claude Code cancels the hook. A decision for another request is ignored.
func TestApprovePlanLeavesItToThePane(t *testing.T) {
	t.Run("pane", func(t *testing.T) {
		root := project(t)
		out := answerWith(t, root, PlanOptions{}, func(r state.PlanRequest) *state.PlanDecision {
			return &state.PlanDecision{N: r.N, Behavior: state.PlanPane}
		})
		if out != "" {
			t.Errorf("printed %q", out)
		}
		assertCleanedUp(t, root)
	})
	t.Run("wait over, other request's decision ignored", func(t *testing.T) {
		root := project(t)
		var now atomic.Int64
		start := time.Now()
		opt := PlanOptions{Wait: time.Minute, Now: func() time.Time {
			return start.Add(time.Duration(now.Add(int64(time.Second))))
		}}
		out := answerWith(t, root, opt, func(state.PlanRequest) *state.PlanDecision {
			return &state.PlanDecision{N: strings.Repeat("0", 32), Behavior: state.PlanAllow}
		})
		if out != "" {
			t.Errorf("printed %q", out)
		}
		assertCleanedUp(t, root)
	})
	t.Run("igris gone", func(t *testing.T) {
		root := project(t)
		var polls atomic.Int32
		alive := func() bool { return polls.Add(1) < 3 }
		out := answerWith(t, root, PlanOptions{Alive: alive}, func(state.PlanRequest) *state.PlanDecision { return nil })
		if out != "" {
			t.Errorf("printed %q", out)
		}
		assertCleanedUp(t, root)
	})
	t.Run("igris not running", func(t *testing.T) {
		root := project(t)
		var out bytes.Buffer
		err := ApprovePlan(context.Background(), root, bytes.NewReader(recordedPlanRequest(t)), &out, PlanOptions{Mode: "auto", Wait: time.Minute})
		if err != nil || out.Len() != 0 {
			t.Errorf("printed %q, %v", out.String(), err)
		}
		if _, err := os.Stat(state.PlanApprovalDir(root)); !os.IsNotExist(err) {
			t.Errorf("a request was written without a running igris: %v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		root := project(t)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			for {
				if _, ok, _ := state.ReadPlanRequest(root, fixtureUUID); ok {
					cancel()
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
		var out bytes.Buffer
		opt := PlanOptions{Mode: "auto", Wait: time.Hour, Poll: time.Millisecond, Alive: func() bool { return true }}
		if err := ApprovePlan(ctx, root, bytes.NewReader(recordedPlanRequest(t)), &out, opt); err != nil || out.Len() != 0 {
			t.Errorf("printed %q, %v", out.String(), err)
		}
		assertCleanedUp(t, root)
	})
}

// Anything but a plan from a session with an igris UUID is refused before
// a request is written.
func TestApprovePlanRefuses(t *testing.T) {
	rec := string(recordedPlanRequest(t))
	for _, tc := range []struct {
		name, in string
		opt      PlanOptions
	}{
		{"bad uuid", strings.Replace(rec, fixtureUUID, "../x", 1), PlanOptions{}},
		{"other tool", strings.Replace(rec, `"ExitPlanMode"`, `"Bash"`, 1), PlanOptions{}},
		{"other event", strings.Replace(rec, `"PermissionRequest"`, `"PreToolUse"`, 1), PlanOptions{}},
		{"input not an object", `{"session_id":"` + fixtureUUID + `","hook_event_name":"PermissionRequest","tool_name":"ExitPlanMode","tool_input":"x"}`, PlanOptions{}},
		{"too large", rec[:len(rec)-2] + `,"x":"` + strings.Repeat("a", maxPlanInput) + `"}`, PlanOptions{}},
		{"unknown mode", rec, PlanOptions{Mode: "dontAsk"}},
		{"no wait", rec, PlanOptions{Wait: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t)
			opt := tc.opt
			if opt.Mode == "" {
				opt.Mode = "auto"
			}
			if opt.Wait == 0 {
				opt.Wait = time.Minute
			}
			opt.Alive = func() bool { return true }
			var out bytes.Buffer
			if err := ApprovePlan(context.Background(), root, strings.NewReader(tc.in), &out, opt); err == nil {
				t.Error("accepted")
			}
			if out.Len() != 0 {
				t.Errorf("printed %q", out.String())
			}
			if _, err := os.Stat(state.PlanApprovalDir(root)); !os.IsNotExist(err) {
				t.Errorf("a request was written: %v", err)
			}
		})
	}
}

func TestSettingsPlanApproval(t *testing.T) {
	data, err := Settings("/bin/igris", "/p", nil, &PlanApproval{Mode: "auto", Wait: 30 * time.Minute}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"command": "'/bin/igris' hook --root '/p' plan-approve --mode 'auto' --wait '30m0s'"`) || !strings.Contains(string(data), `"timeout": 1830`) {
		t.Errorf("settings:\n%s", data)
	}
	for _, bad := range []PlanApproval{{Mode: "dontAsk", Wait: time.Minute}, {Mode: "plan", Wait: time.Minute}, {Mode: "auto"}} {
		if _, err := Settings("/bin/igris", "/p", nil, &bad, ""); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// plan:yolo's approval switches to bypassPermissions (V11-02).
func TestApprovePlanAllowBypass(t *testing.T) {
	root := project(t)
	out := answerWith(t, root, PlanOptions{Mode: "bypassPermissions"}, func(r state.PlanRequest) *state.PlanDecision {
		return &state.PlanDecision{N: r.N, Behavior: state.PlanAllow}
	})
	if !strings.Contains(out, `{"type":"setMode","mode":"bypassPermissions","destination":"session"}`) {
		t.Errorf("output %s", out)
	}
	data, err := Settings("/bin/igris", "/p", nil, &PlanApproval{Mode: "bypassPermissions", Wait: time.Minute}, "")
	if err != nil || !strings.Contains(string(data), "plan-approve --mode 'bypassPermissions'") {
		t.Errorf("settings %s, %v", data, err)
	}
}

func assertCleanedUp(t *testing.T, root string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(state.PlanApprovalDir(root), "*"))
	if len(files) != 0 {
		t.Errorf("left behind: %v", files)
	}
}
