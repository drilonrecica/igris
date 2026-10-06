package main

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		line    string
		want    engine.Command
		act     commandAction
		wantErr string
	}{
		{"", engine.Command{}, actNone, ""},
		{"   ", engine.Command{}, actNone, ""},
		{"y", engine.Command{Kind: engine.CmdAnswer, Yes: true}, actSend, ""},
		{"YES", engine.Command{Kind: engine.CmdAnswer, Yes: true}, actSend, ""},
		{"n", engine.Command{Kind: engine.CmdAnswer}, actSend, ""},
		{"no thanks", engine.Command{}, actNone, "takes no argument"},
		{"done", engine.Command{Kind: engine.CmdDone}, actSend, ""},
		{"done  bought it, all fine ", engine.Command{Kind: engine.CmdDone, Text: "bought it, all fine"}, actSend, ""},
		{"skip", engine.Command{}, actNone, "needs a reason"},
		{"skip not needed", engine.Command{Kind: engine.CmdSkip, Text: "not needed"}, actSend, ""},
		{"retry", engine.Command{Kind: engine.CmdRetry}, actSend, ""},
		{"retry fresh", engine.Command{Kind: engine.CmdRetry}, actSend, ""},
		{"retry Continue", engine.Command{Kind: engine.CmdRetry, Continue: true}, actSend, ""},
		{"retry later", engine.Command{}, actNone, "continue or fresh"},
		{"pause", engine.Command{Kind: engine.CmdPause}, actSend, ""},
		{"stop", engine.Command{Kind: engine.CmdStop}, actSend, ""},
		{"stop now", engine.Command{}, actNone, "takes no argument"},
		{"mode accept", engine.Command{Kind: engine.CmdMode, Text: "accept"}, actSend, ""},
		{"mode YOLO", engine.Command{Kind: engine.CmdMode, Text: "yolo"}, actConfirmYolo, ""},
		{"mode wild", engine.Command{}, actNone, "default|accept"},
		{"help", engine.Command{}, actHelp, ""},
		{"?", engine.Command{}, actHelp, ""},
		{"launch", engine.Command{}, actNone, "unknown command"},
	}
	for _, tt := range tests {
		got, act, err := parseCommand(tt.line)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("parseCommand(%q) err = %v, want %q", tt.line, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got != tt.want || act != tt.act {
			t.Errorf("parseCommand(%q) = %+v, %d, %v; want %+v, %d", tt.line, got, act, err, tt.want, tt.act)
		}
	}
}

func TestFormatEvent(t *testing.T) {
	tests := []struct {
		ev   engine.Event
		want string
	}{
		{engine.Event{Kind: engine.TaskStarted, Task: "A-1", Title: "One", Rank: "opus", Model: "opus", Mode: "plan"}, "A-1 started (opus → opus, mode plan) · One"},
		{engine.Event{Kind: engine.TaskStarted, Task: "A-2", Title: "Buy"}, "A-2 started (user task) · Buy"},
		{engine.Event{Kind: engine.SessionOpened, Task: "A-1", Mode: "yolo"}, "A-1 session open [SKIP PERMISSIONS]"},
		{engine.Event{Kind: engine.Asked, Question: engine.QuestionCommit, Detail: "commit?"}, "? commit? [y/n]"},
		{engine.Event{Kind: engine.Asked, Question: engine.QuestionSessionLost, Detail: "gone"}, "? gone\n  type `retry continue`, `retry fresh`, `done [note]`, `skip <reason>` or `stop`"},
		{engine.Event{Kind: engine.TaskDone, Task: "A-1"}, "A-1 done"},
		{engine.Event{Kind: engine.TaskSkipped, Task: "A-1", Detail: "why"}, "A-1 skipped · why"},
		{engine.Event{Kind: engine.Warning, Detail: "hm"}, "warning: hm"},
	}
	for _, tt := range tests {
		if got := strings.Join(formatEvent(tt.ev), "\n"); got != tt.want {
			t.Errorf("formatEvent(%s) = %q, want %q", tt.ev.Kind, got, tt.want)
		}
	}
	// Every kind renders as something.
	for _, k := range []engine.EventKind{engine.RunStarted, engine.PhaseStuck, engine.Paused, engine.VerifyFailed, engine.NeedsYou, "brand_new"} {
		if lines := formatEvent(engine.Event{Kind: k, Task: "A-1"}); len(lines) == 0 || lines[0] == "" {
			t.Errorf("formatEvent(%s) is empty", k)
		}
	}
}

const dryPlan = `## A — First

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| A-0 | **Half done** | — | in progress | opus | agent | — |
| A-1 | **One** | A-0 | blocked | sonnet | agent | plan |
| A-2 | **Buy a domain** | A-1 | blocked | — | user | — |
| A-3 | **Gate** | A-2 | ready | fable | agent + user | yolo |

## B — Second

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| B-1 | **Later** | A-3 | blocked | sonnet |
`

// snapshot returns every file under root with its content.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // the test's own temp tree
		files[path] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	return root
}

func TestDryRun(t *testing.T) {
	root := writeProject(t, map[string]string{
		"plans/tasks.md": dryPlan,
		"igris.toml":     "plan = \"plans/tasks.md\"\n[run]\nverify = \"false\"\ncommit = \"auto\"\nprompt_template = \"p.tmpl\"\n",
		"p.tmpl":         "do {{.ID}}\n",
	})
	before := snapshot(t, root)

	var out, errb bytes.Buffer
	if code := run([]string{"arise", "A", "--through", "B", "--dry-run"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	want := []string{
		"warning: drift: A-3: ready → blocked",
		"dry run of phase A through B",
		"  1. A-0        opus    → model opus    mode default Half done  (resumed: fresh session)",
		"  2. A-1        sonnet  → model sonnet  mode plan    One",
		"  3. A-2        user task: waits for you  Buy a domain",
		"  4. A-3        fable   → model fable   mode yolo    Gate [SKIP PERMISSIONS]",
		"     phase A complete",
		"  5. B-1        sonnet  → model sonnet  mode default Later",
		"     phase B complete",
		"dry run: 4 session(s), 1 user task(s)",
	}
	last := -1
	for _, w := range want {
		i := strings.Index(got, w)
		if i < 0 || i < last {
			t.Errorf("output lacks %q (in order):\n%s", w, got)
			continue
		}
		last = i
	}
	if strings.Contains(got, "igris.toml changed") {
		t.Errorf("the dry run's own config was reported as changed:\n%s", got)
	}
	if after := snapshot(t, root); len(after) != len(before) {
		t.Errorf("files after the dry run: %d, before: %d", len(after), len(before))
	} else {
		for path, content := range before {
			if after[path] != content {
				t.Errorf("%s changed", path)
			}
		}
	}
}

func TestDryRunUnknownPhase(t *testing.T) {
	root := writeProject(t, map[string]string{"tasks.md": dryPlan})
	var out, errb bytes.Buffer
	if code := run([]string{"arise", "Z", "--dry-run"}, &out, &errb); code != exitFail {
		t.Fatalf("exit %d", code)
	}
	if want := filepath.Join(root, "tasks.md") + "; phases are: A, B"; !strings.Contains(errb.String(), want) {
		t.Errorf("stderr = %q, want it to name %q", errb.String(), want)
	}
	if strings.Contains(out.String(), "dry run of phase") {
		t.Errorf("header printed for a phase that doesn't exist:\n%s", out.String())
	}
}

func TestDryRunResumesTheLastRunsPhases(t *testing.T) {
	root := writeProject(t, map[string]string{"tasks.md": dryPlan})
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.SaveRun(&state.Run{Phases: []string{"B"}, Current: &state.Current{TaskID: "B-1"}}); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, root)
	var out, errb bytes.Buffer
	if code := run([]string{"arise", "--dry-run"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	for _, w := range []string{"note: the last run stopped during B-1", "dry run of phase B:", "phase B is STUCK"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out.String())
		}
	}
	if after := snapshot(t, root); len(after) != len(before) {
		t.Errorf("the dry run added files: %d → %d", len(before), len(after))
	}

	t.Chdir(t.TempDir())
	if err := os.WriteFile("tasks.md", []byte(dryPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"arise", "--dry-run"}, &out, &errb); code != exitFail || !strings.Contains(errb.String(), "no earlier run") {
		t.Errorf("without an earlier run: exit %d, stderr %q", code, errb.String())
	}
}

func TestAriseRefusesWhatIsNotBuiltYet(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": dryPlan})
	savedRunner, savedGetenv := ariseRunner, ariseGetenv
	t.Cleanup(func() { ariseRunner, ariseGetenv = savedRunner, savedGetenv })
	ariseRunner = &runner.Fake{} // any herdr call would fail the run
	ariseGetenv = func(string) string { return "" }
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"arise", "A"}, "the TUI is not part of this build yet"},
		{[]string{"arise", "A", "--no-tui"}, "must run inside a herdr pane"},
	}
	for _, tt := range tests {
		var out, errb bytes.Buffer
		if code := run(tt.args, &out, &errb); code != exitFail || !strings.Contains(errb.String(), tt.want) {
			t.Errorf("%v: exit %d, stderr %q; want %q", tt.args, code, errb.String(), tt.want)
		}
	}
}

// triggerWriter collects the output and, the first time it contains a
// pattern, writes the paired reply to stdin.
type triggerWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	stdin   io.Writer
	replies [][2]string
}

func (w *triggerWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, _ := w.buf.Write(p)
	for i, r := range w.replies {
		if r[0] != "" && strings.Contains(w.buf.String(), r[0]) {
			w.replies[i][0] = ""
			go func(line string) { _, _ = io.WriteString(w.stdin, line+"\n") }(r[1])
		}
	}
	return n, nil
}

func (w *triggerWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

const noTUIPlan = `## A

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet | agent |
| A-2 | **Buy a domain** | A-1 | blocked | — | user |
`

// noTUIRun is one `igris arise --no-tui` with a fake backend whose sessions
// signal done at once, git answered by gitFn, and stdin replies triggered
// by the output.
type noTUIRun struct {
	plan, toml string
	args       []string
	apiKey     string
	gitFn      func(runner.Cmd) (runner.Result, error)
	replies    [][2]string // output pattern → stdin line
}

func (r noTUIRun) run(t *testing.T) (code int, output, root string) {
	t.Helper()
	if r.plan == "" {
		r.plan = noTUIPlan
	}
	if r.gitFn == nil {
		r.gitFn = func(c runner.Cmd) (runner.Result, error) {
			if c.Args[0] == "rev-parse" {
				return runner.Result{Stdout: []byte("true\n")}, nil
			}
			return runner.Result{}, nil
		}
	}
	root = writeProject(t, map[string]string{
		"tasks.md":   r.plan,
		"igris.toml": "poll_interval = \"5ms\"\n[notify.backend]\nenabled = false\n" + r.toml,
	})
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	be := fake.New()
	be.SetAutoSignal(func(_ context.Context, id string) error {
		return dir.WriteSignal(state.Signal{ID: id, Action: state.ActionDone, Note: "did " + id})
	})
	git := &runner.Fake{}
	git.Func(r.gitFn)
	stdinR, stdinW := io.Pipe()
	t.Cleanup(func() { _ = stdinW.Close() })
	savedStdin, savedBackend, savedRunner, savedGetenv := ariseStdin, ariseBackend, ariseRunner, ariseGetenv
	t.Cleanup(func() {
		ariseStdin, ariseBackend, ariseRunner, ariseGetenv = savedStdin, savedBackend, savedRunner, savedGetenv
	})
	ariseStdin, ariseRunner = stdinR, git
	ariseBackend = func(*config.Config) (backend.Backend, error) { return be, nil }
	ariseGetenv = func(k string) string {
		if k == engine.APIKeyVar {
			return r.apiKey
		}
		return ""
	}

	out := &triggerWriter{stdin: stdinW, replies: r.replies}
	var errb bytes.Buffer
	done := make(chan int)
	go func() { done <- run(append([]string{"arise"}, r.args...), out, &errb) }()
	select {
	case code = <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("igris arise --no-tui did not finish; output:\n%s", out.String())
	}
	return code, out.String() + errb.String(), root
}

func planStatuses(t *testing.T, root string) string {
	t.Helper()
	p, err := plan.Load(filepath.Join(root, "tasks.md"), plan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, task := range p.Tasks {
		out = append(out, task.ID+"="+task.Status.String())
	}
	return strings.Join(out, " ")
}

func TestAriseNoTUI(t *testing.T) {
	code, got, root := noTUIRun{
		args: []string{"A", "--no-tui"},
		gitFn: func(c runner.Cmd) (runner.Result, error) {
			switch c.Args[0] {
			case "rev-parse":
				return runner.Result{Stdout: []byte("true\n")}, nil
			case "status":
				return runner.Result{Stdout: []byte(" M x\n")}, nil
			}
			return runner.Result{}, nil
		},
		replies: [][2]string{
			{"A-1 session open", "bogus"},
			{"? commit the changes of A-1", "y"},
			{"A-2 YOUR TURN", "done bought it"},
		},
	}.run(t)
	if code != exitOK {
		t.Fatalf("exit %d; output:\n%s", code, got)
	}
	for _, w := range []string{
		"warning: the working tree has uncommitted changes",
		"run started: phase A",
		"A-1 started (sonnet → sonnet, mode default) · One",
		`unknown command "bogus"; type help`,
		"A-1 committed: A-1: One",
		"A-1 done · did A-1",
		"A-2 YOUR TURN: **Buy a domain**",
		"A-2 done · bought it",
		"phase A complete",
		"run stopped: completed",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
	if got := planStatuses(t, root); got != "A-1=done A-2=done" {
		t.Errorf("statuses = %s", got)
	}
}

func TestAriseStartUpConfirmations(t *testing.T) {
	const driftPlan = `## A

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet | agent |
| A-2 | **Two** | A-1 | ready | sonnet | agent |
`
	tests := []struct {
		name       string
		r          noTUIRun
		wantCode   int
		wantOut    []string
		wantStatus string
	}{
		{
			name:       "api key confirmed",
			r:          noTUIRun{apiKey: "sk-x", replies: [][2]string{{"Start the run anyway?", "y"}, {"A-2 YOUR TURN", "done"}}},
			wantCode:   exitOK,
			wantOut:    []string{"warning: ANTHROPIC_API_KEY is set"},
			wantStatus: "A-1=done A-2=done",
		},
		{
			name:       "api key declined",
			r:          noTUIRun{apiKey: "sk-x", replies: [][2]string{{"Start the run anyway?", "n"}}},
			wantCode:   exitFail,
			wantOut:    []string{"not confirmed; nothing was started"},
			wantStatus: "A-1=ready A-2=blocked",
		},
		{
			name:       "drift confirmed",
			r:          noTUIRun{plan: driftPlan, replies: [][2]string{{"Let igris fix them?", "yes"}}},
			wantCode:   exitOK,
			wantOut:    []string{"  A-2: ready → blocked"},
			wantStatus: "A-1=done A-2=done",
		},
		{
			name:       "drift declined",
			r:          noTUIRun{plan: driftPlan, replies: [][2]string{{"Let igris fix them?", "n"}}},
			wantCode:   exitFail,
			wantStatus: "A-1=ready A-2=ready",
		},
		{
			name:       "yolo typed",
			r:          noTUIRun{args: []string{"--mode", "yolo"}, plan: driftPlan[:strings.Index(driftPlan, "| A-2")], replies: [][2]string{{"Type \"skip permissions\"", "skip permissions"}}},
			wantCode:   exitOK,
			wantOut:    []string{"A-1 session open [SKIP PERMISSIONS]"},
			wantStatus: "A-1=done",
		},
		{
			name:       "yolo not typed",
			r:          noTUIRun{args: []string{"--mode", "yolo"}, replies: [][2]string{{"Type \"skip permissions\"", "y"}}},
			wantCode:   exitFail,
			wantOut:    []string{"skip-permissions mode not confirmed"},
			wantStatus: "A-1=ready A-2=blocked",
		},
		{
			name: "not a git repo only warns",
			r: noTUIRun{
				gitFn:   func(runner.Cmd) (runner.Result, error) { return runner.Result{ExitCode: 128}, nil },
				toml:    "[run]\ncommit = \"never\"\n",
				replies: [][2]string{{"A-2 YOUR TURN", "done"}},
			},
			wantCode:   exitOK,
			wantOut:    []string{"warning: this is not a git repository"},
			wantStatus: "A-1=done A-2=done",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.r.args = append([]string{"A", "--no-tui"}, tt.r.args...)
			code, got, root := tt.r.run(t)
			if code != tt.wantCode {
				t.Errorf("exit %d, want %d; output:\n%s", code, tt.wantCode, got)
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(got, w) {
					t.Errorf("output lacks %q:\n%s", w, got)
				}
			}
			if strings.Contains(got, "sk-x") {
				t.Errorf("output leaks the API key:\n%s", got)
			}
			if got := planStatuses(t, root); got != tt.wantStatus {
				t.Errorf("statuses = %s, want %s", got, tt.wantStatus)
			}
		})
	}
}
