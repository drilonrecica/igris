package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHistoryGoldens pins the text and --json output of `igris history` on a
// synthetic log: finished, errored and interrupted runs, a resume, an
// unknown event type and a truncated last line.
//
// runs_v1.jsonl mixes v0 lines (igris v0.2–v0.4) with v1 ones and a few
// from a "v2": runs grouped by ID and by start/stop, run IDs shown, a line
// outside a run, a bad run ID, unknown types, fields and reasons, and the
// newer-version note (SPEC §13).
func TestHistoryGoldens(t *testing.T) {
	goldenDir := mustAbs("testdata/golden")
	for _, tc := range []struct {
		log, prefix string
		args        [][]string
	}{
		{"runs.jsonl", "history", [][]string{
			{"history"}, {"history", "--json"},
			{"history", "-n", "1"},
			{"history", "B-1"}, {"history", "B-1", "--json"},
			{"history", "A-9"},
		}},
		{"runs_v1.jsonl", "history_v1", [][]string{
			{"history"}, {"history", "--json"},
			{"history", "A-2"}, {"history", "A-2", "--json"},
		}},
	} {
		log, err := os.ReadFile(filepath.Join("testdata/history", tc.log))
		if err != nil {
			t.Fatal(err)
		}
		for _, args := range tc.args {
			historyGolden(t, goldenDir, tc.prefix, log, args)
		}
	}
}

// historyGolden runs `igris` with args on log and compares the output with
// its golden file.
func historyGolden(t *testing.T, goldenDir, prefix string, log []byte, args []string) {
	t.Helper()
	name := strings.TrimSuffix(prefix+"_"+strings.ReplaceAll(strings.Join(args[1:], "_"), "--", ""), "_")
	t.Run(name, func(t *testing.T) {
		// A fixed directory name: `report` names the project after it.
		root := filepath.Join(t.TempDir(), "demo")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Chdir(root)
		writeLog(t, root, log)
		var out, errb bytes.Buffer
		code := run(args, &out, &errb)
		got := fmt.Sprintf("exit: %d\n--- stdout\n%s--- stderr\n%s", code, out.String(), errb.String())
		golden := filepath.Join(goldenDir, name+".golden")
		if *update {
			if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
				t.Fatal(err)
			}
			return
		}
		want, err := os.ReadFile(golden) //nolint:gosec // a golden under testdata/
		if err != nil {
			t.Fatalf("%v; run `go test ./cmd/igris -run TestHistoryGoldens -update`", err)
		}
		if got != string(want) {
			t.Errorf("output changed:\n%s\nwant:\n%s", got, want)
		}
	})
}

func writeLog(t *testing.T, root string, log []byte) {
	t.Helper()
	dir := filepath.Join(root, ".igris")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // a log written into the test's own temp dir
	if err := os.WriteFile(filepath.Join(dir, "runs.jsonl"), log, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryReadOnlyAndUsage(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	var out, errb bytes.Buffer
	if code := run([]string{"history"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "no runs recorded yet") {
		t.Errorf("no project: code %d, out %q, err %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"history", "--json"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), `"runs": []`) {
		t.Errorf("no project --json: code %d, out %q", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".igris")); !os.IsNotExist(err) {
		t.Errorf("history created .igris/: %v", err)
	}
	for _, args := range [][]string{{"history", "-n", "0"}, {"history", "bad id"}, {"history", "A-1", "A-2"}} {
		errb.Reset()
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: code %d, want usage error", args, code)
		}
	}
	writeLog(t, root, []byte("{not json}\n{\"at\":\"2026-10-01T09:00:00Z\",\"type\":\"run_started\"}\n"))
	errb.Reset()
	out.Reset()
	// One bad line never hides the rest (SPEC §13).
	if code := run([]string{"history"}, &out, &errb); code != exitOK ||
		!strings.Contains(out.String(), "note: 1 unreadable line in runs.jsonl skipped") || !strings.Contains(out.String(), "Run 2026-10-01T09:00:00Z") {
		t.Errorf("a damaged line in the middle: code %d, out %q, err %q", code, out.String(), errb.String())
	}
}

// A run whose run_started line is lost has no phases: "phases": [] and no
// dangling "phase" in the text.
func TestHistoryRunWithoutStart(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeLog(t, root, []byte(`{"v":1,"at":"2026-10-01T09:00:00Z","type":"task_started","run":"20261001-090000-3fa2","task":"A-1"}`+"\n"))
	var out, errb bytes.Buffer
	if code := run([]string{"history"}, &out, &errb); code != exitOK || strings.Contains(out.String(), "phase") {
		t.Errorf("code %d, out %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"history", "--json"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), `"phases": []`) {
		t.Errorf("--json: code %d, out %q", code, out.String())
	}
}
