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

// Any positive integer is an index; past the end is a plain failure.
func TestReportLargeIndex(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeLog(t, root, []byte(`{"v":1,"at":"2026-10-01T09:00:00Z","type":"run_started","run":"20261001-090000-3fa2","detail":"phase A"}`+"\n"))
	for _, sel := range []string{"1234567890", "99999999999999999999999"} {
		var out, errb bytes.Buffer
		if code := run([]string{"report", sel}, &out, &errb); code != exitFail || !strings.Contains(errb.String(), "only 1 run recorded") {
			t.Errorf("report %s: code %d, err %q; want exit 1, only 1 run recorded", sel, code, errb.String())
		}
	}
}

// Text from the log can't make markdown: links, images, emphasis, HTML.
func TestReportEscapesMarkdown(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	const run1 = `"run":"20261001-090000-3fa2"`
	writeLog(t, root, []byte(strings.Join([]string{
		`{"v":1,"at":"2026-10-01T09:00:00Z","type":"run_started",` + run1 + `,"detail":"phase A"}`,
		`{"v":1,"at":"2026-10-01T09:00:01Z","type":"task_started",` + run1 + `,"task":"A-1","phase":"A","owner":"agent","title":"[click](https://evil.example) **now** <b>x</b>"}`,
		`{"v":1,"at":"2026-10-01T09:01:00Z","type":"task_done",` + run1 + `,"task":"A-1","detail":"see ![img](https://evil.example/p.png) and ` + "`code`" + ` # not a heading"}`,
		`{"v":1,"at":"2026-10-01T09:02:00Z","type":"run_stopped",` + run1 + `,"detail":"completed"}`,
	}, "\n")+"\n"))
	var out, errb bytes.Buffer
	if code := run([]string{"report"}, &out, &errb); code != exitOK {
		t.Fatalf("code %d, err %q", code, errb.String())
	}
	got := out.String()
	for _, want := range []string{
		`| A-1 \[click\]\(https://evil.example\) \*\*now\*\* \<b\>x\</b\> |`,
		"- A-1 done: see \\!\\[img\\]\\(https://evil.example/p.png\\) and \\`code\\` \\# not a heading",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	out.Reset()
	if code := run([]string{"report", "--json"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), `"title": "[click](https://evil.example) **now** \u003cb\u003ex\u003c/b\u003e"`) {
		t.Errorf("--json keeps the text as cleaned: code %d\n%s", code, out.String())
	}
}

// `report -h` and `history -h` show the positional arguments, not only
// the flags, and exit 0.
func TestReportHistoryHelp(t *testing.T) {
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"report", "-h"}, []string{"igris report [RUN] [--json]", "-json"}},
		{[]string{"history", "--help"}, []string{"igris history [TASK-ID] [-n N] [--json]", "-n int"}},
	} {
		var out, errb bytes.Buffer
		if code := run(c.args, &out, &errb); code != exitOK {
			t.Errorf("%v: exit %d", c.args, code)
		}
		for _, w := range c.want {
			if !strings.Contains(errb.String(), w) {
				t.Errorf("%v: help lacks %q:\n%s", c.args, w, errb.String())
			}
		}
	}
}
