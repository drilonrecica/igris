package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/tui"
)

func openFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // a device or the test's own file
	if err != nil {
		t.Skipf("no %s here: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestInteractive(t *testing.T) {
	// /dev/zero is a character device that is not /dev/null: it stands in
	// for a terminal.
	zero := openFile(t, "/dev/zero")
	null := openFile(t, os.DevNull)
	regular, err := os.Create(filepath.Join(t.TempDir(), "regular"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = regular.Close() })
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })

	tests := []struct {
		name          string
		stdin, stdout *os.File
		term          string
		want          bool
	}{
		{"both terminals", zero, zero, "xterm-256color", true},
		{"empty TERM", zero, zero, "", true},
		{"stdin is /dev/null", null, zero, "xterm", false},
		{"TERM=dumb", zero, zero, "dumb", false},
		{"stdin is a pipe", pr, zero, "xterm", false},
		{"stdin is a file", regular, zero, "xterm", false},
		{"stdout is a pipe", zero, pw, "xterm", false},
		{"stdout is a file", zero, regular, "xterm", false},
		{"stdout is /dev/null", zero, null, "xterm", true}, // only stdin is held to the /dev/null rule
		{"nil stdin", nil, zero, "xterm", false},
		{"nil stdout", zero, nil, "xterm", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := interactive(tt.stdin, tt.stdout, tt.term); got != tt.want {
				t.Errorf("interactive = %v, want %v", got, tt.want)
			}
		})
	}
}

// fakeApp swaps the terminal check and the app for a recorder.
func fakeApp(t *testing.T, tty bool, res tui.AppResult, err error) *[]tui.AppOptions {
	t.Helper()
	savedTerm, savedUI := interactiveTerm, appUI
	t.Cleanup(func() { interactiveTerm, appUI = savedTerm, savedUI })
	interactiveTerm = func() bool { return tty }
	var calls []tui.AppOptions
	appUI = func(_ context.Context, o tui.AppOptions) (tui.AppResult, error) {
		calls = append(calls, o)
		return res, err
	}
	return &calls
}

func TestBareIgrisOnATerminalOpensHome(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": dryPlan})
	calls := fakeApp(t, true, tui.AppResult{}, nil)
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("the app opened %d times", len(*calls))
	}
	o := (*calls)[0]
	if _, ok := o.Start.(tui.Home); !ok || o.Services == nil {
		t.Errorf("options = %+v, want Home with services", o)
	}
	if out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("output %q / %q, want none", out.String(), errb.String())
	}
}

func TestBareIgrisTUIFailure(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": dryPlan})
	fakeApp(t, true, tui.AppResult{}, errors.New("no tty"))
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != exitFail || !strings.Contains(errb.String(), "the TUI failed: no tty") {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
}

func TestBareIgrisStoppedRunSaysItsSessionKeepsRunning(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": dryPlan})
	fakeApp(t, true, tui.AppResult{Stopped: true}, nil)
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != exitOK || !strings.Contains(out.String(), "`igris arise` resumes") {
		t.Errorf("exit %d, stdout %q", code, out.String())
	}
}

// Without a terminal (a pipe, /dev/null, TERM=dumb: interactive says so)
// bare igris prints the help to stderr and exits 2, as before.
func TestBareIgrisWithoutATerminalPrintsHelp(t *testing.T) {
	calls := fakeApp(t, false, tui.AppResult{}, nil)
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != exitUsage {
		t.Errorf("exit %d, want %d", code, exitUsage)
	}
	if out.Len() != 0 || !strings.HasPrefix(errb.String(), "igris - run a markdown task plan") || !strings.Contains(errb.String(), "Usage:") {
		t.Errorf("stdout %q, stderr %q", out.String(), errb.String())
	}
	if len(*calls) != 0 {
		t.Error("the app was opened")
	}
}

// The real detection, end to end: /dev/null as stdin and TERM=dumb each
// keep home closed.
func TestBareIgrisRealDetection(t *testing.T) {
	zero := openFile(t, "/dev/zero")
	null := openFile(t, os.DevNull)
	savedTerm, savedUI := interactiveTerm, appUI
	t.Cleanup(func() { interactiveTerm, appUI = savedTerm, savedUI })
	opened := false
	appUI = func(context.Context, tui.AppOptions) (tui.AppResult, error) {
		opened = true
		return tui.AppResult{}, nil
	}
	for _, tt := range []struct {
		name  string
		stdin *os.File
		term  string
		want  bool
	}{
		{"terminal", zero, "xterm", true},
		{"dev null", null, "xterm", false},
		{"dumb", zero, "dumb", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writeProject(t, map[string]string{"tasks.md": dryPlan})
			opened = false
			interactiveTerm = func() bool { return interactive(tt.stdin, zero, tt.term) }
			var out, errb bytes.Buffer
			code := run(nil, &out, &errb)
			if opened != tt.want || (code == exitUsage) == tt.want {
				t.Errorf("opened %v, exit %d", opened, code)
			}
		})
	}
}

func TestWantWizard(t *testing.T) {
	saveRun := func(t *testing.T, root string, phases []string) {
		t.Helper()
		d, err := state.Open(root, state.Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := d.SaveRun(&state.Run{Phases: phases}); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name  string
		flags ariseFlags
		tty   bool
		setup func(t *testing.T, root string)
		want  bool
	}{
		{"no run", ariseFlags{}, true, nil, true},
		{"run without phases", ariseFlags{}, true, func(t *testing.T, r string) { saveRun(t, r, nil) }, true},
		{"run to resume", ariseFlags{}, true, func(t *testing.T, r string) { saveRun(t, r, []string{"A"}) }, false},
		{"unreadable state", ariseFlags{}, true, func(t *testing.T, r string) {
			if err := os.MkdirAll(filepath.Join(r, ".igris"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(r, ".igris", "state.json"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"phase named", ariseFlags{phase: "A"}, true, nil, false},
		{"no-tui", ariseFlags{noTUI: true}, true, nil, false},
		{"dry-run", ariseFlags{dryRun: true}, true, nil, false},
		{"not a terminal", ariseFlags{}, false, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeProject(t, map[string]string{"tasks.md": dryPlan})
			if tt.setup != nil {
				tt.setup(t, root)
			}
			fakeApp(t, tt.tty, tui.AppResult{}, nil)
			if got := wantWizard(tt.flags, root); got != tt.want {
				t.Errorf("wantWizard = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAriseWithNothingToResumeOpensTheWizard(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": dryPlan})
	calls := fakeApp(t, true, tui.AppResult{}, nil)
	var out, errb bytes.Buffer
	args := []string{"arise", "--mode", "plan", "--through", "B", "--force-unlock"}
	if code := run(args, &out, &errb); code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("the app opened %d times", len(*calls))
	}
	want := tui.Wizard{Mode: "plan", Through: "B", ForceUnlock: true}
	if got, ok := (*calls)[0].Start.(tui.Wizard); !ok || got != want {
		t.Errorf("start = %#v, want %#v", (*calls)[0].Start, want)
	}
}

// Every other arise case goes on as before: the app stays closed.
func TestAriseOtherCasesDoNotOpenTheApp(t *testing.T) {
	for _, tt := range []struct {
		args []string
		code int
	}{{[]string{"arise", "A", "--dry-run"}, exitOK}, {[]string{"arise", "--dry-run"}, exitFail}, {[]string{"arise", "A", "--no-tui", "--dry-run"}, exitOK}} {
		args := tt.args
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			writeProject(t, map[string]string{"tasks.md": dryPlan})
			calls := fakeApp(t, true, tui.AppResult{}, nil)
			var out, errb bytes.Buffer
			if code := run(args, &out, &errb); code != tt.code {
				t.Fatalf("exit %d, want %d, stderr %q", code, tt.code, errb.String())
			}
			if len(*calls) != 0 {
				t.Error("the app was opened")
			}
		})
	}
}

func TestAriseWithoutATerminalDoesNotOpenTheApp(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": dryPlan})
	calls := fakeApp(t, false, tui.AppResult{}, nil)
	var out, errb bytes.Buffer
	run([]string{"arise", "--dry-run"}, &out, &errb)
	if len(*calls) != 0 {
		t.Error("the app was opened")
	}
}

// Outside a project arise keeps refusing, terminal or not.
func TestAriseOutsideAProjectOnATerminal(t *testing.T) {
	t.Chdir(t.TempDir())
	calls := fakeApp(t, true, tui.AppResult{}, nil)
	var out, errb bytes.Buffer
	if code := run([]string{"arise"}, &out, &errb); code != exitFail || !strings.Contains(errb.String(), "no igris project in") {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
	if len(*calls) != 0 {
		t.Error("the app was opened")
	}
}
