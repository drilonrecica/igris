package plan

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var fuzzStatuses = []Status{Ready, Blocked, InProgress, Done, Skipped}

// checkSplice is the writer's byte-preservation property: changing task
// pick's Status to s changes nothing but that task's Status cell. For a
// valid plan (the only kind Writer.Update writes) it also checks that the
// result parses to the same plan with only that status changed, and that
// writing the same status again changes nothing.
func checkSplice(t *testing.T, data []byte, pick uint, s Status) {
	t.Helper()
	p := Parse("tasks.md", data, Options{})
	if len(p.Tasks) == 0 {
		return
	}
	target := p.Tasks[pick%uint(len(p.Tasks))]
	if p.Task(target.ID) != target {
		return // an empty or duplicate ID: splice addresses tasks by ID
	}
	out, err := splice(data, p, []Change{{ID: target.ID, To: s}})
	if err != nil {
		t.Fatalf("splice %s: %v", target.ID, err)
	}
	cell := statusCell(string(data[target.statusStart:target.statusEnd]), s)
	want := slicesConcat(data[:target.statusStart], []byte(cell), data[target.statusEnd:])
	if !bytes.Equal(out, want) {
		t.Fatalf("splice %s → %s changed bytes outside its Status cell [%d,%d)", target.ID, s, target.statusStart, target.statusEnd)
	}

	if p.Check(testRules) != nil {
		return
	}
	q := Parse("tasks.md", out, Options{})
	if len(q.Tasks) != len(p.Tasks) || len(q.Phases) != len(p.Phases) {
		t.Fatalf("after setting %s to %s: %d phases / %d tasks, was %d / %d", target.ID, s, len(q.Phases), len(q.Tasks), len(p.Phases), len(p.Tasks))
	}
	for i, a := range p.Tasks {
		b := q.Tasks[i]
		if a.ID != b.ID || a.Text != b.Text || a.DepsText != b.DepsText || a.Rank != b.Rank || a.OwnerText != b.OwnerText || a.Mode != b.Mode || a.Line != b.Line || fmt.Sprint(a.Extra) != fmt.Sprint(b.Extra) {
			t.Fatalf("after setting %s to %s, task %d changed: %+v → %+v", target.ID, s, i, a, b)
		}
		wantStatus := a.Status
		if a == target {
			wantStatus = s
		}
		if b.Status != wantStatus {
			t.Fatalf("after setting %s to %s, task %s is %s, want %s", target.ID, s, b.ID, b.Status, wantStatus)
		}
	}
	again, err := splice(out, q, []Change{{ID: target.ID, To: s}})
	if err != nil || !bytes.Equal(again, out) {
		t.Fatalf("setting %s to %s twice is not a no-op (err %v)", target.ID, s, err)
	}
}

func slicesConcat(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

// FuzzSplice: arbitrary plan bytes, any task, any status.
func FuzzSplice(f *testing.F) {
	for i, s := range fuzzPlanSeeds(f) {
		f.Add(s, uint(i), uint8(i))
	}
	f.Fuzz(func(t *testing.T, data []byte, pick uint, status uint8) {
		checkSplice(t, data, pick, fuzzStatuses[int(status)%len(fuzzStatuses)])
	})
}

// escapeCell makes fuzzed text usable as one table cell: one line, literal
// pipes escaped. A trailing backslash would escape the separator after it.
func escapeCell(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", `\`, `\\`, "|", `\|`).Replace(s)
	return s
}

// generatedPlan builds a valid two-phase plan around fuzzed cell text, so
// the checked half of checkSplice runs on most inputs.
func generatedPlan(title, deps, extra, status string, crlf, trailingNL bool) []byte {
	title, extra = escapeCell(title), escapeCell(extra)
	if strings.TrimSpace(title) == "" {
		title = "x"
	}
	if st, _, ok := ParseStatus(status); !ok || st == StatusUnknown || strings.ContainsAny(status, "|\\\r\n") {
		status = "ready"
	}
	if strings.ContainsAny(deps, "|\\\r\n") {
		deps = "—"
	}
	lines := []string{
		"# Plan " + extra,
		"",
		"## P1 — " + title,
		"",
		"| ID | Task | Deps | Status | Model | Owner | Notes |",
		"|---|---|---|:---:|---|---|---|",
		"| P1-01 | **" + title + "** " + extra + " | — | " + status + " | sonnet | agent | " + extra + " |",
		"| P1-02 | `code` " + title + " | " + deps + " | `blocked` | opus | agent + user | |",
		"|P1-03|" + extra + "|P1-01, P1-02| blocked |  —  |user|x|",
		"",
		"```",
		"| ID | Status | Model |",
		"|---|---|---|",
		"| P1-01 | done | opus |",
		"```",
		"",
		"## P2",
		"",
		"| ID | Deps | Status | Model |",
		"| --- | --- | --- | --- |",
		"| P2-01 | P1-01…P1-03 | blocked | haiku |",
	}
	nl := "\n"
	if crlf {
		nl = "\r\n"
	}
	s := strings.Join(lines, nl)
	if trailingNL {
		s += nl
	}
	return []byte(s)
}

// FuzzSpliceGenerated: valid plans with fuzzed cell text, line endings and
// trailing newline; any task, any status.
func FuzzSpliceGenerated(f *testing.F) {
	f.Add("One", "P1-01", "note", "ready", false, true, uint(0), uint8(3))
	f.Add("Größe ✓ 日本", "—", "a | b", "`ready` (soon)", true, false, uint(1), uint8(0))
	f.Add("`x`", "P1-01, P1-01", `back\slash`, "in progress", true, true, uint(4), uint8(4))
	f.Add(strings.Repeat("long ", 500), "", "", "done", false, false, uint(2), uint8(1))
	f.Fuzz(func(t *testing.T, title, deps, extra, status string, crlf, trailingNL bool, pick uint, st uint8) {
		data := generatedPlan(title, deps, extra, status, crlf, trailingNL)
		checkSplice(t, data, pick, fuzzStatuses[int(st)%len(fuzzStatuses)])
	})
}

// The generated plan is valid for the seeds, so FuzzSpliceGenerated really
// exercises the checked path; and the file-based writer gives the same
// bytes as splice.
func TestGeneratedPlanThroughWriter(t *testing.T) {
	for _, crlf := range []bool{false, true} {
		for _, nl := range []bool{false, true} {
			data := generatedPlan("Größe | ✓", "P1-01", "a | b", "`ready` (soon)", crlf, nl)
			p := Parse("tasks.md", data, Options{})
			if err := p.Check(testRules); err != nil {
				t.Fatalf("crlf=%v nl=%v: generated plan invalid: %v", crlf, nl, err)
			}
			path := filepath.Join(t.TempDir(), "tasks.md")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewWriter(path, Options{}, testRules).Update(context.Background(), set("P1-01", Done)); err != nil {
				t.Fatal(err)
			}
			want, err := splice(data, p, []Change{{ID: "P1-01", To: Done}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path) //nolint:gosec // test temp file
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("crlf=%v nl=%v: Writer and splice differ:\n%q\n%q", crlf, nl, got, want)
			}
			checkSplice(t, data, 0, Done)
		}
	}
}
