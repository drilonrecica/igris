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
func TestHistoryGoldens(t *testing.T) {
	log, err := os.ReadFile("testdata/history/runs.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	goldenDir := mustAbs("testdata/golden")
	for _, args := range [][]string{
		{"history"}, {"history", "--json"},
		{"history", "-n", "1"},
		{"history", "B-1"}, {"history", "B-1", "--json"},
		{"history", "A-9"},
	} {
		name := "history_" + strings.ReplaceAll(strings.Join(args[1:], "_"), "--", "")
		name = strings.TrimSuffix(name, "_")
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
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
	if code := run([]string{"history"}, &out, &errb); code != exitFail {
		t.Errorf("a damaged line in the middle: code %d, want 1 (err %q)", code, errb.String())
	}
}
