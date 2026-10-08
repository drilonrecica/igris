package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunReportGoldens pins the markdown and --json output of `igris report`
// on synthetic logs. report/runs.jsonl has a v0 run cut short, a sliced
// two-phase v1 run (a retry, overlapping needs-you waits, a run-wide wait,
// a user task, a `|` and an escape sequence in titles, a hook error, a wait
// still open at the stop) and an interrupted v1 run with a resume; the
// history log adds a newer-version note and a bad session UUID.
func TestRunReportGoldens(t *testing.T) {
	goldenDir := mustAbs("testdata/golden")
	for _, tc := range []struct {
		log, prefix string
		args        [][]string
	}{
		{"report/runs.jsonl", "report", [][]string{
			{"report"}, {"report", "--json"},
			{"report", "2"}, {"report", "2", "--json"},
			{"report", "3"}, {"report", "3", "--json"},
			{"report", "20261001-090000-3fa2"},
			{"report", "4"}, {"report", "20991231-000000-0000"},
		}},
		{"history/runs_v1.jsonl", "report_v1", [][]string{
			{"report"}, {"report", "2"}, {"report", "2", "--json"}, {"report", "3"}, {"report", "4"},
		}},
	} {
		log, err := os.ReadFile(filepath.Join("testdata", tc.log))
		if err != nil {
			t.Fatal(err)
		}
		for _, args := range tc.args {
			historyGolden(t, goldenDir, tc.prefix, log, args)
		}
	}
}

func TestReportReadOnlyAndUsage(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	var out, errb bytes.Buffer
	if code := run([]string{"report"}, &out, &errb); code != exitFail || !strings.Contains(errb.String(), "no runs recorded yet") {
		t.Errorf("no project: code %d, out %q, err %q", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".igris")); !os.IsNotExist(err) {
		t.Errorf("report created .igris/: %v", err)
	}
	for _, args := range [][]string{
		{"report", "0"}, {"report", "-1"}, {"report", "01"}, {"report", "+1"}, {"report", "last"},
		{"report", "20261001-090000-3FA2"}, {"report", "1", "2"},
	} {
		errb.Reset()
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: code %d, want usage error (err %q)", args, code, errb.String())
		}
	}
	writeLog(t, root, nil)
	errb.Reset()
	if code := run([]string{"report", "--json"}, &out, &errb); code != exitFail || !strings.Contains(errb.String(), "no runs recorded yet") {
		t.Errorf("empty log: code %d, err %q", code, errb.String())
	}
}
