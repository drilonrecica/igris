package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUsageGoldens pins the text and --json output of `igris usage` on the
// report fixture: a v0 run (no usage), a run with two attempts of one task
// on a mismatch, an unreadable one and a user task, and a run that logged
// nothing more. The clock is fixed so --since is the same every day.
func TestUsageGoldens(t *testing.T) {
	usageNow = func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, time.Local) }
	t.Cleanup(func() { usageNow = time.Now })
	goldenDir := mustAbs("testdata/golden")
	log, err := os.ReadFile(filepath.Join("testdata", "report/runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"usage"}, {"usage", "--json"},
		{"usage", "2"}, {"usage", "2", "--json"},
		{"usage", "20261002-100000-00ff"}, {"usage", "4"},
		{"usage", "--since", "7d"}, {"usage", "--since", "7d", "--json"},
		{"usage", "--since", "2026-10-02"}, {"usage", "--since", "2026-10-03"},
		{"usage", "--since", "30h"},
	} {
		historyGolden(t, goldenDir, "usage", log, args)
	}
}

func TestUsageReadOnlyAndArgs(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	var out, errb bytes.Buffer
	if code := run([]string{"usage"}, &out, &errb); code != exitFail || !strings.Contains(errb.String(), "no runs recorded yet") {
		t.Errorf("no project: code %d, out %q, err %q", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".igris")); !os.IsNotExist(err) {
		t.Errorf("usage created .igris/: %v", err)
	}
	for _, args := range [][]string{
		{"usage", "0"}, {"usage", "last"}, {"usage", "1", "2"},
		{"usage", "--since", "soon"}, {"usage", "--since", ""}, {"usage", "--since", "0d"}, {"usage", "--since", "-3d"},
		{"usage", "--since", "2026-13-01"}, {"usage", "--since", "7d", "1"}, {"usage", "--since"},
	} {
		errb.Reset()
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: code %d, want usage error (err %q)", args, code, errb.String())
		}
	}
	writeLog(t, root, []byte(`{"v":1,"at":"2026-10-01T09:00:00Z","type":"run_started","run":"20261001-090000-3fa2","detail":"phase A"}`+"\n"))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"usage", "2"}, "only 1 run recorded"},
		{[]string{"usage", "--since", "1h"}, "no run since 1h"},
	} {
		errb.Reset()
		if code := run(tc.args, &out, &errb); code != exitFail || !strings.Contains(errb.String(), tc.want) {
			t.Errorf("%v: code %d, err %q; want exit 1, %q", tc.args, code, errb.String(), tc.want)
		}
	}
	// A run without usage lines still reports, with nothing counted.
	errb.Reset()
	out.Reset()
	if code := run([]string{"usage"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "0 attempts") {
		t.Errorf("run without usage: code %d, out %q, err %q", code, out.String(), errb.String())
	}
}
