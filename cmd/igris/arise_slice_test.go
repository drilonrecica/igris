package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/state"
)

// Malformed selection flags are usage errors (SPEC §14).
func TestAriseSelectionUsage(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"arise", "--only", ""}, "--only needs at least one task ID, e.g. --only M1-03,M1-05"},
		{[]string{"arise", "--only", " , "}, "--only needs at least one task ID, e.g. --only M1-03,M1-05"},
		{[]string{"arise", "--only", "A-1,,A-2"}, `invalid task ID "" in --only`},
		{[]string{"arise", "--only", "A-1", "--from", "A-1"}, "--only can't be combined with --from or --until; use one or the other"},
		{[]string{"arise", "A", "--until", "B-1", "--only", "A-1"}, "--only can't be combined with --from or --until"},
		{[]string{"arise", "--from", "a b"}, `invalid task ID "a b" in --from: IDs are letters, digits, '.', '_' and '-' (e.g. M0-01)`},
		{[]string{"arise", "--until", ""}, `invalid task ID "" in --until`},
	}
	for _, tt := range tests {
		var out, errb bytes.Buffer
		if code := run(tt.args, &out, &errb); code != exitUsage || !strings.Contains(errb.String(), tt.want) {
			t.Errorf("%q: exit %d, stderr %q; want exit 2 and %q", tt.args, code, errb.String(), tt.want)
		}
	}
}

func TestParseSelectionDedupes(t *testing.T) {
	fs := flag.NewFlagSet("arise", flag.ContinueOnError)
	fs.String("only", "", "")
	fs.String("from", "", "")
	fs.String("until", "", "")
	if err := fs.Parse([]string{"--only", " A-2, A-1 ,A-2"}); err != nil {
		t.Fatal(err)
	}
	sel, err := parseSelection(fs)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sel.Only, " "); got != "A-2 A-1" || sel.From != "" || sel.Until != "" {
		t.Errorf("selection = %+v", sel)
	}
}

// A selection that doesn't fit the plan fails before arise asks anything or
// writes a file.
func TestAriseSelectionChecked(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--only", "A-9", "--no-tui"}, "igris arise: --only A-9 is not a task in "},
		{[]string{"A", "--until", "A-9", "--no-tui"}, "--until A-9 is not a task in "},
		{[]string{"--from", "A-2", "--until", "A-1", "--no-tui"}, "igris arise: --from A-2 comes after --until A-1 in the plan; swap them"},
	}
	for _, tt := range tests {
		code, got, root := noTUIRun{args: tt.args, apiKey: "sk-test"}.run(t)
		if code != exitFail || !strings.Contains(got, tt.want) {
			t.Errorf("%q: exit %d, output:\n%s\nwant %q", tt.args, code, got, tt.want)
		}
		if strings.Contains(got, "Start the run anyway?") {
			t.Errorf("%q: asked before checking the selection:\n%s", tt.args, got)
		}
		if _, err := state.PeekRun(root); !errors.Is(err, state.ErrNoRun) {
			t.Errorf("%q: state.json written: %v", tt.args, err)
		}
		if got := planStatuses(t, root); got != "A-1=ready A-2=blocked" {
			t.Errorf("%q: statuses = %s", tt.args, got)
		}
	}
}

// A slice task whose deps are unmet is reported, in the log and in the
// final summary, and not run; the run still completes.
func TestAriseNoTUINotRun(t *testing.T) {
	code, got, root := noTUIRun{args: []string{"--only", "A-2", "--no-tui"}}.run(t)
	if code != exitOK {
		t.Fatalf("exit %d; output:\n%s", code, got)
	}
	for _, w := range []string{
		"run started: phase A; only A-2",
		"phase A started",
		"not run: A-2 waits on A-1 (ready, phase A)",
		"run stopped: completed",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
	if n := strings.Count(got, "not run: A-2 waits on A-1"); n != 2 {
		t.Errorf("not run reported %d times, want in the log and the summary:\n%s", n, got)
	}
	if strings.Contains(got, "STUCK") || strings.Contains(got, "phase A complete") {
		t.Errorf("a slice reported a stuck or complete phase:\n%s", got)
	}
	if got := planStatuses(t, root); got != "A-1=ready A-2=blocked" {
		t.Errorf("statuses = %s", got)
	}
	run, err := state.PeekRun(root)
	if err != nil || run.Selection == nil || strings.Join(run.Selection.Only, ",") != "A-2" {
		t.Errorf("state.json = %+v, %v", run, err)
	}
}

func TestDryRunWalksTheSlice(t *testing.T) {
	withVersions(t, "2.1.291 (Claude Code)\n", "herdr 0.9.1\n")
	root := writeProject(t, map[string]string{"tasks.md": dryPlan})
	before := snapshot(t, root)
	var out, errb bytes.Buffer
	if code := run([]string{"arise", "--only", "A-0,A-1,B-1", "--dry-run"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	want := []string{
		"dry run of phase A through B; only A-0, A-1, B-1: nothing is written and no session starts",
		"  1. A-0        opus    → model opus    mode auto    Half done  (resumed: fresh session)",
		"  2. A-1        sonnet  → model sonnet  mode plan    One",
		"     not run: B-1 waits on A-3 (blocked, phase A)",
		"dry run: 2 session(s), 0 user task(s)",
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
	for _, no := range []string{"A-2        ", "A-3        ", "phase A complete", "phase B complete", "STUCK"} {
		if strings.Contains(got, no) {
			t.Errorf("output has %q:\n%s", no, got)
		}
	}
	if after := snapshot(t, root); len(after) != len(before) {
		t.Errorf("the dry run added files: %d → %d", len(before), len(after))
	}

	// A bare dry run walks the last run's slice.
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.SaveRun(&state.Run{Phases: []string{"A"}, Selection: &state.Selection{Until: "A-1"}}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run([]string{"arise", "--dry-run"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if w := "dry run of phase A; until A-1:"; !strings.Contains(out.String(), w) {
		t.Errorf("output lacks %q:\n%s", w, out.String())
	}

	// A wrong ID fails the dry run too.
	out.Reset()
	errb.Reset()
	if code := run([]string{"arise", "A", "--until", "B-1", "--dry-run"}, &out, &errb); code != exitFail ||
		!strings.Contains(errb.String(), "--until B-1 is in phase B, outside the run's phase A; widen --through or drop it") {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
}
