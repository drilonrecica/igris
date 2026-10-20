package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
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
	if code := run([]string{"usage"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "No usage was recorded") {
		t.Errorf("run without usage: code %d, out %q, err %q", code, out.String(), errb.String())
	}
}

// The quota line shows what igris's status line recorded, with its time and
// reset times, and `quota unavailable` otherwise (SPEC §6.3, §14).
func TestUsageQuotaLine(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	d, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = d
	writeLog(t, root, []byte(`{"v":1,"at":"2026-10-01T09:00:00Z","type":"run_started","run":"20261001-090000-3fa2","detail":"phase A"}`+"\n"))
	var out, errb bytes.Buffer
	if code := run([]string{"usage"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "Quota: quota unavailable") {
		t.Fatalf("no quota: code %d, out %q, err %q", code, out.String(), errb.String())
	}
	q := state.Quota{At: time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local),
		FiveHour: &state.QuotaWindow{Used: 4, ResetsAt: time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local).Unix()},
		SevenDay: &state.QuotaWindow{Used: 52, ResetsAt: time.Date(2026, 10, 25, 9, 30, 0, 0, time.Local).Unix()}}
	if err := state.WriteQuota(root, q); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run([]string{"usage"}, &out, &errb); code != exitOK {
		t.Fatalf("code %d, err %q", code, errb.String())
	}
	want := "Quota: 5h 4% (resets 2026-10-20 15:00) · 7d 52% (resets 2026-10-25 09:30) · recorded 2026-10-20 12:00\n"
	if !strings.Contains(out.String(), want) {
		t.Errorf("out %q lacks %q", out.String(), want)
	}
	out.Reset()
	if code := run([]string{"usage", "--json"}, &out, &errb); code != exitOK {
		t.Fatalf("json: code %d", code)
	}
	var got struct {
		Quota *struct {
			FiveHour struct{ Used float64 } `json:"five_hour"`
		} `json:"quota"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Quota == nil || got.Quota.FiveHour.Used != 4 {
		t.Errorf("json quota = %+v err %v\n%s", got, err, out.String())
	}
}

// Side sessions get a table of their own, also when no attempt has usage,
// and a `side` array in --json (SPEC §14).
func TestUsageSideTable(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if _, err := state.Open(root, state.Options{}); err != nil {
		t.Fatal(err)
	}
	writeLog(t, root, []byte(`{"v":1,"at":"2026-10-01T09:00:00Z","type":"run_started","run":"20261001-090000-3fa2","detail":"phase A"}`+"\n"+
		`{"v":1,"at":"2026-10-01T09:30:00Z","type":"side_usage","run":"20261001-090000-3fa2","side":"a1b2c3","rank":"sonnet","model":"sonnet",`+
		`"usage":{"input":5,"output":40,"cache_read":1000,"cache_write":0,"messages":2},"models":["claude-sonnet-5-5"]}`+"\n"))
	var out, errb bytes.Buffer
	if code := run([]string{"usage"}, &out, &errb); code != exitOK {
		t.Fatalf("code %d, err %q", code, errb.String())
	}
	for _, want := range []string{"Side sessions", "a1b2c3", "claude-sonnet-5-5", "not attempts"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if code := run([]string{"usage", "--json"}, &out, &errb); code != exitOK {
		t.Fatalf("json: code %d", code)
	}
	var got struct {
		Side []struct {
			Side   string `json:"side"`
			In     int64  `json:"in"`
			Output int64  `json:"output"`
		} `json:"side"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got.Side) != 1 || got.Side[0].Side != "a1b2c3" || got.Side[0].In != 1005 || got.Side[0].Output != 40 {
		t.Errorf("json side = %+v err %v\n%s", got, err, out.String())
	}
}
