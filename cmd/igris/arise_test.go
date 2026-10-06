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
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"arise", "A"}, "the TUI is not part of this build yet"},
		{[]string{"arise", "A", "--no-tui"}, "herdr backend is not part of this build yet"},
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

func TestAriseNoTUI(t *testing.T) {
	const noTUIPlan = `## A

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet | agent |
| A-2 | **Buy a domain** | A-1 | blocked | — | user |
`
	root := writeProject(t, map[string]string{
		"tasks.md":   noTUIPlan,
		"igris.toml": "poll_interval = \"5ms\"\n[notify.backend]\nenabled = false\n",
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
	git.Func(func(c runner.Cmd) (runner.Result, error) {
		if c.Args[0] == "status" {
			return runner.Result{Stdout: []byte(" M x\n")}, nil
		}
		return runner.Result{}, nil
	})
	stdinR, stdinW := io.Pipe()
	defer func() { _ = stdinW.Close() }()
	saved := []any{ariseStdin, ariseBackend, ariseRunner}
	t.Cleanup(func() {
		ariseStdin = saved[0].(io.Reader)
		ariseBackend = saved[1].(func(*config.Config) (backend.Backend, error))
		ariseRunner, _ = saved[2].(runner.Runner)
	})
	ariseStdin, ariseRunner = stdinR, git
	ariseBackend = func(*config.Config) (backend.Backend, error) { return be, nil }

	out := &triggerWriter{stdin: stdinW, replies: [][2]string{
		{"A-1 session open", "bogus"},
		{"? commit the changes of A-1", "y"},
		{"A-2 YOUR TURN", "done bought it"},
	}}
	var errb bytes.Buffer
	done := make(chan int)
	go func() { done <- run([]string{"arise", "A", "--no-tui"}, out, &errb) }()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("exit %d, stderr: %s\noutput:\n%s", code, errb.String(), out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("igris arise --no-tui did not finish; output:\n%s", out.String())
	}
	got := out.String()
	for _, w := range []string{
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
	p, err := plan.Load(filepath.Join(root, "tasks.md"), plan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range p.Tasks {
		if task.Status != plan.Done {
			t.Errorf("%s is %s", task.ID, task.Status)
		}
	}
}
