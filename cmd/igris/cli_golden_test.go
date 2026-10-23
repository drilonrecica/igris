package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
	upd "github.com/drilonrecica/igris/internal/update"
)

// cliGoldenDir is taken before any test changes the working directory.
var cliGoldenDir = mustAbs("testdata/golden/cli")

// checkGolden compares a command's exit code and output with
// testdata/golden/cli/NAME.golden, or rewrites it under -update. Each
// replacement pair turns a value that differs per run (a temp dir, a
// token) into a placeholder.
func checkGolden(t *testing.T, name string, code int, stdout, stderr string, replace ...string) {
	t.Helper()
	got := fmt.Sprintf("exit: %d\n--- stdout\n%s--- stderr\n%s", code, stdout, stderr)
	got = strings.NewReplacer(replace...).Replace(got)
	golden := filepath.Join(cliGoldenDir, name+".golden")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden) //nolint:gosec // a golden under testdata/
	if err != nil {
		t.Fatalf("%v; run `go test ./cmd/igris -run %s -update`", err, t.Name())
	}
	if got != string(want) {
		t.Errorf("%s differs from %s; review and run with -update:\n--- got\n%s--- want\n%s", t.Name(), golden, got, want)
	}
}

// goldenPlan is a small valid plan for the goldens of the commands that
// need a project.
const goldenPlan = `## M1 — Basics

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M1-01 | **Config loader** | — | done | sonnet | agent |
| M1-02 | **Lock file** | M1-01 | ready | opus | agent |
| M1-03 | **Pick a license** | — | ready | — | user |
`

func TestDoctorGolden(t *testing.T) {
	t.Run("outside a project", func(t *testing.T) {
		dir := t.TempDir()
		code, out, errb := doctorIn(t, dir)
		checkGolden(t, "doctor_outside", code, out, errb, dir, "<dir>")
	})
	t.Run("project", func(t *testing.T) {
		dir := t.TempDir()
		if code, _, errb := initIn(t, dir); code != exitOK {
			t.Fatalf("init: %s", errb)
		}
		if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(goldenPlan), 0o600); err != nil {
			t.Fatal(err)
		}
		code, out, errb := doctorIn(t, dir)
		checkGolden(t, "doctor_project", code, out, errb, dir, "<dir>")
	})
	t.Run("invalid plan", func(t *testing.T) {
		dir := t.TempDir()
		if code, _, errb := initIn(t, dir); code != exitOK {
			t.Fatalf("init: %s", errb)
		}
		if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(strings.Replace(goldenPlan, "| M1-01 |", "| M1-02 |", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		code, out, errb := doctorIn(t, dir)
		checkGolden(t, "doctor_invalid", code, out, errb, dir, "<dir>")
	})
}

func TestInitGolden(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		dir := t.TempDir()
		code, out, errb := initIn(t, dir)
		checkGolden(t, "init", code, out, errb, dir, "<dir>")
	})
	t.Run("again", func(t *testing.T) {
		dir := t.TempDir()
		initIn(t, dir)
		code, out, errb := initIn(t, dir)
		checkGolden(t, "init_again", code, out, errb, dir, "<dir>")
	})
	t.Run("example", func(t *testing.T) {
		dir := t.TempDir()
		saved := ariseGetenv
		t.Cleanup(func() { ariseGetenv = saved })
		ariseGetenv = func(string) string { return "" }
		t.Chdir(dir)
		code, out, errb := runCmd("init", "--example")
		checkGolden(t, "init_example", code, out, errb, dir, "<dir>")
	})
}

func TestUpdateCheckGolden(t *testing.T) {
	for _, tc := range []struct {
		name, tag string
		build     upd.Build
	}{
		{"newer_brew", "v1.2.0", brewBuild},
		{"newer_go", "v1.2.0", goBuild},
		{"equal", "v1.1.0", brewBuild},
		{"dev", "v1.2.0", devBuild},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inDir(t)
			withUpdate(t, tc.tag, tc.build, &runner.Fake{}, "/usr/local/bin/igris", nil)
			code, out, errb := runCmd("update", "--check")
			checkGolden(t, "update_check_"+tc.name, code, out, errb)
		})
	}
}

// qrLines are the lines of a QR code drawn black on white.
var qrLines = regexp.MustCompile("(?m)^\x1b\\[30;107m.*\n")

func TestServeTokenGolden(t *testing.T) {
	t.Run("address", func(t *testing.T) {
		root := writeProject(t, map[string]string{"tasks.md": servePlan, "igris.toml": "[serve]\nhttp = \"100.101.102.103:7777\"\n"})
		code, out, errb := runCmd("serve", "token")
		tok, err := state.ReadServeToken(root)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(qrLines.FindAllString(out, -1)); n < 10 {
			t.Errorf("the QR code has %d lines", n)
		}
		out = qrLines.ReplaceAllString(out, "")
		checkGolden(t, "serve_token", code, out, errb, tok, "<token>")
	})
	t.Run("no address", func(t *testing.T) {
		root := writeProject(t, map[string]string{"tasks.md": servePlan})
		code, out, errb := runCmd("serve", "token", "--rotate")
		tok, _ := state.ReadServeToken(root)
		checkGolden(t, "serve_token_no_address", code, out, errb, tok, "<token>")
	})
}

func TestSignalGolden(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		root := signalProject(t)
		var out, errb bytes.Buffer
		code := run([]string{"done", "M0-01", "--note", "tests added"}, &out, &errb)
		checkGolden(t, "done", code, out.String(), errb.String(), root, "<dir>")
		out.Reset()
		code = run([]string{"skip", "M0-01", "--reason", "not needed"}, &out, &errb)
		checkGolden(t, "skip", code, out.String(), errb.String(), root, "<dir>")
	})
	t.Run("unknown task", func(t *testing.T) {
		root := signalProject(t)
		code, out, errb := runCmd("done", "M9-01")
		checkGolden(t, "done_unknown", code, out, errb, root, "<dir>")
	})
}

func TestNotifyTestGolden(t *testing.T) {
	t.Run("sent", func(t *testing.T) {
		ntfy := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body) }))
		defer ntfy.Close()
		notifyProject(t, "[notify.backend]\nenabled = false\n[notify.ntfy]\ntopic = \"igris\"\nserver = \""+ntfy.URL+"\"\n")
		code, out, errb := runCmd("notify", "test", "--event", "needs_input")
		checkGolden(t, "notify_test", code, out, errb, ntfy.URL, "<ntfy>")
	})
	t.Run("failed", func(t *testing.T) {
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
		defer bad.Close()
		notifyProject(t, "[notify.backend]\nenabled = false\n[notify.ntfy]\ntopic = \"igris\"\nserver = \""+bad.URL+"\"\n")
		code, out, errb := runCmd("notify", "test", "--event", "phase_done")
		checkGolden(t, "notify_test_failed", code, out, errb, bad.URL, "<ntfy>")
	})
	t.Run("no channels", func(t *testing.T) {
		notifyProject(t, "[notify.backend]\nenabled = false\n")
		code, out, errb := runCmd("notify", "test")
		checkGolden(t, "notify_test_none", code, out, errb)
	})
}

// onTerminal makes the commands' stdout a terminal width columns wide (0:
// as wide as the text), with colors unless noColor.
func onTerminal(t *testing.T, width int, noColor bool) {
	t.Helper()
	saved := outTerminal
	t.Cleanup(func() { outTerminal = saved })
	outTerminal = func(io.Writer) (bool, int) { return true, width }
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORFGBG", "")
	t.Setenv("NO_COLOR", "")
	if noColor {
		t.Setenv("NO_COLOR", "1")
	}
}

// statusProject is a project running goldenPlan's M1-02 in a run whose
// lock this test holds; live, when set, is what its socket answers.
func statusProject(t *testing.T, live *serve.Snapshot) (pid string) {
	t.Helper()
	root := writeProject(t, map[string]string{"tasks.md": strings.Replace(goldenPlan, "| M1-02 | **Lock file** | M1-01 | ready |", "| M1-02 | **Lock file** | M1-01 | in progress |", 1)})
	writeRunState(t, root, "state.json", `{"version":1,"started_at":"2026-10-23T09:30:00Z","phases":["M1"],"config_hash":"h",
"current":{"task_id":"M1-02","mode":"plan","claude_session":"s","session":{"backend":"tmux","pane_id":"%3"},"started_at":"2026-10-23T09:41:00Z"}}`)
	d, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := d.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	saved := peekRun
	t.Cleanup(func() { peekRun = saved })
	peekRun = func(string) (*serve.Snapshot, bool) { return live, live != nil }
	return fmt.Sprint(os.Getpid())
}

// needsYou is a served run whose M1-02 waits on the commit question.
var needsYou = &serve.Snapshot{Type: serve.TypeSnapshot, Run: "20261023-093000-abcd", Phase: "M1", Mode: "plan",
	Active:    []serve.Current{{Task: "M1-02", Rank: "opus", Mode: "plan", State: serve.StateQuestion}},
	Questions: []serve.Event{{Type: "asked", Task: "M1-02", Question: "commit"}}}

// status and doctor drawn on a terminal: with colors, and the same text
// without them under NO_COLOR (SPEC §14).
func TestColorGoldens(t *testing.T) {
	for _, tc := range []struct {
		name    string
		noColor bool
	}{{"color", false}, {"no_color", true}} {
		t.Run("status working "+tc.name, func(t *testing.T) {
			pid := statusProject(t, nil)
			onTerminal(t, 0, tc.noColor)
			code, out, errb := runCmd("status")
			checkGolden(t, "status_working_"+tc.name, code, out, errb, "pid "+pid, "pid <pid>")
		})
		t.Run("status needs you "+tc.name, func(t *testing.T) {
			pid := statusProject(t, needsYou)
			onTerminal(t, 0, tc.noColor)
			code, out, errb := runCmd("status")
			checkGolden(t, "status_needs_you_"+tc.name, code, out, errb, "pid "+pid, "pid <pid>")
		})
		t.Run("doctor "+tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if code, _, errb := initIn(t, dir); code != exitOK {
				t.Fatalf("init: %s", errb)
			}
			if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(strings.Replace(goldenPlan, "| M1-01 |", "| M1-02 |", 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			onTerminal(t, 0, tc.noColor)
			code, out, errb := doctorIn(t, dir)
			checkGolden(t, "doctor_"+tc.name, code, out, errb, dir, "<dir>")
		})
	}
	// Without colors the text is the plain text byte for byte.
	for _, name := range []string{"status_working", "status_needs_you", "doctor"} {
		color, err := os.ReadFile(filepath.Join(cliGoldenDir, name+"_color.golden")) //nolint:gosec // a golden under testdata/
		if err != nil {
			t.Fatal(err)
		}
		plain, err := os.ReadFile(filepath.Join(cliGoldenDir, name+"_no_color.golden")) //nolint:gosec // a golden under testdata/
		if err != nil {
			t.Fatal(err)
		}
		if got := sgr.ReplaceAll(color, nil); !bytes.Equal(got, plain) {
			t.Errorf("%s: the colored golden without its SGR sequences is not the NO_COLOR one:\n%s", name, got)
		}
		if bytes.Equal(color, plain) {
			t.Errorf("%s: no colors in the colored golden", name)
		}
		if bytes.ContainsRune(plain, 0x1b) {
			t.Errorf("%s: an escape sequence under NO_COLOR", name)
		}
	}
}

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Plume is drawn only for Needs you (SPEC §14, rule 2: red means you).
func TestColorPlumeOnlyForNeedsYou(t *testing.T) {
	plume := "38;2;255;92;116" // look.PlumeText on a dark ground
	for _, name := range []string{"status_working_color", "doctor_color"} {
		b, err := os.ReadFile(filepath.Join(cliGoldenDir, name+".golden")) //nolint:gosec // a golden under testdata/
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(plume)) {
			t.Errorf("%s draws plume without anything needing you", name)
		}
	}
	b, err := os.ReadFile(filepath.Join(cliGoldenDir, "status_needs_you_color.golden")) //nolint:gosec // a golden under testdata/
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("\x1b[1;"+plume+"m! Needs you\x1b[0m")) {
		t.Errorf("Needs you is not drawn in plume:\n%q", b)
	}
}

// On a narrow terminal the tables fit: long cells are cut and WAITS ON
// folds onto a line of its own; off a terminal nothing is cut.
func TestStatusFitsTheTerminal(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": goldenPlan + "| M1-04 | **Release** | M1-02, M1-03 | blocked | sonnet | agent + user |\n"})
	onTerminal(t, 44, true)
	code, out, errb := runCmd("status")
	for _, l := range strings.Split(out, "\n") {
		if w := look.Width(l); w > 44 {
			t.Errorf("%q is %d cells wide", l, w)
		}
	}
	checkGolden(t, "status_narrow", code, out, errb)
}
