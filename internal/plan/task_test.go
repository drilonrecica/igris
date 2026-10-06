package plan

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseStatus(t *testing.T) {
	tests := []struct {
		in     string
		want   Status
		suffix string
		ok     bool
	}{
		{"ready", Ready, "", true},
		{"READY", Ready, "", true},
		{"  Blocked ", Blocked, "", true},
		{"in progress", InProgress, "", true},
		{"In Progress", InProgress, "", true},
		{"done", Done, "", true},
		{"skipped", Skipped, "", true},
		{"`done`", Done, "", true},
		{"`skipped` (not needed)", Skipped, "(not needed)", true},
		{"`skipped (not needed)`", Skipped, "(not needed)", true},
		{"skipped (not needed)", Skipped, "(not needed)", true},
		{"done — see notes", Done, "— see notes", true},
		{"done.", Done, ".", true},
		{"", StatusUnknown, "", false},
		{"todo", StatusUnknown, "", false},
		{"readyish", StatusUnknown, "", false},
		{"done2", StatusUnknown, "", false},
		{"in-progress", StatusUnknown, "", false},
		{"inprogress", StatusUnknown, "", false},
	}
	for _, tt := range tests {
		s, suffix, ok := ParseStatus(tt.in)
		if s != tt.want || suffix != tt.suffix || ok != tt.ok {
			t.Errorf("ParseStatus(%q) = (%v, %q, %v), want (%v, %q, %v)", tt.in, s, suffix, ok, tt.want, tt.suffix, tt.ok)
		}
	}
}

func TestStatusSatisfied(t *testing.T) {
	for s, want := range map[Status]bool{Ready: false, Blocked: false, InProgress: false, Done: true, Skipped: true, StatusUnknown: false} {
		if s.Satisfied() != want {
			t.Errorf("%v.Satisfied() = %v", s, !want)
		}
	}
	if InProgress.String() != "in progress" || StatusUnknown.String() != "unknown" {
		t.Error("String")
	}
}

func TestParseOwner(t *testing.T) {
	tests := []struct {
		in   string
		want Owner
		ok   bool
	}{
		{"agent", OwnerAgent, true},
		{"", OwnerAgent, true},
		{"User", OwnerUser, true},
		{"agent + user", OwnerAgentUser, true},
		{"agent+user", OwnerAgentUser, true},
		{"Agent  +  User", OwnerAgentUser, true},
		{"`user`", OwnerUser, true},
		{"robot", "", false},
		{"user + agent", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseOwner(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseOwner(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
	if !OwnerAgentUser.IsAgent() || !OwnerAgent.IsAgent() || OwnerUser.IsAgent() {
		t.Error("IsAgent")
	}
}

func TestTitle(t *testing.T) {
	long := strings.Repeat("é", 100)
	tests := []struct{ in, want string }{
		{"**Go module** — init", "Go module"},
		{"Set up **CI** and **lint**", "CI"},
		{"plain text", "plain text"},
		{"** ** then text", "** ** then text"},
		{"**unclosed", "**unclosed"},
		{long, strings.Repeat("é", 80)},
		{"", ""},
	}
	for _, tt := range tests {
		if got := title(tt.in); got != tt.want {
			t.Errorf("title(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseTasks(t *testing.T) {
	in := "## M0 — Foundation\n\n" +
		"| ID | Task | Deps | Spec | Status | Model | Owner | Mode |\n" +
		"|---|---|---|---|---|---|---|---|\n" +
		"| M0-01 | **Go module** — `a \\| b` | — | 18 | `done` | sonnet | agent | — |\n" +
		"| M0-02 | Plain task | M0-01 | 3.2 | Skipped (not needed) | `opus` | Agent + User | Plan |\n" +
		"| M0-03 | **Owner call** | M0-01, M0-02 | | blocked | — | user | |\n" +
		"| M0-04 | short row | none | 1 | ready |\n" +
		"\n## M1\n\n| ID | Status | Model |\n|---|---|---|\n| M1-01 | in progress | fable |\n"
	p := Parse("tasks.md", []byte(in), Options{})
	if len(p.issues) > 0 {
		t.Fatalf("issues: %v", issueMsgs(p.issues))
	}
	if len(p.Tasks) != 5 || len(p.Phases[0].Tasks) != 4 || len(p.Phases[1].Tasks) != 1 {
		t.Fatalf("task counts: %d", len(p.Tasks))
	}

	m0 := p.Phases[0]
	want := []Task{
		{ID: "M0-01", Title: "Go module", Text: "**Go module** — `a | b`", Status: Done, StatusText: "`done`",
			Owner: OwnerAgent, OwnerText: "agent", Rank: "sonnet", DepsText: "—", Extra: map[string]string{"Spec": "18"}, Phase: m0, Line: 5},
		{ID: "M0-02", Title: "Plain task", Text: "Plain task", Status: Skipped, StatusText: "Skipped (not needed)", Suffix: "(not needed)",
			Owner: OwnerAgentUser, OwnerText: "Agent + User", Rank: "opus", Mode: "plan", DepsText: "M0-01", Extra: map[string]string{"Spec": "3.2"}, Phase: m0, Line: 6},
		{ID: "M0-03", Title: "Owner call", Text: "**Owner call**", Status: Blocked, StatusText: "blocked",
			Owner: OwnerUser, OwnerText: "user", DepsText: "M0-01, M0-02", Extra: map[string]string{"Spec": ""}, Phase: m0, Line: 7},
		{ID: "M0-04", Title: "short row", Text: "short row", Status: Ready, StatusText: "ready",
			Owner: OwnerAgent, DepsText: "none", Extra: map[string]string{"Spec": "1"}, Phase: m0, Line: 8},
	}
	for i, w := range want {
		got := *p.Tasks[i]
		got.statusStart, got.statusEnd, got.Deps = 0, 0, nil
		if !reflect.DeepEqual(got, w) {
			t.Errorf("task %d:\n got %+v\nwant %+v", i, got, w)
		}
	}
	m1 := p.Task("M1-01")
	if m1 == nil || m1.Owner != OwnerAgent || m1.OwnerText != "" || m1.Status != InProgress || m1.Rank != "fable" || m1.Phase.ID != "M1" {
		t.Fatalf("M1-01 = %+v", m1)
	}
	if p.Task("nope") != nil {
		t.Fatal("unknown task")
	}

	// The Status span points at the cell content in the original bytes.
	for _, task := range p.Tasks {
		if got := in[task.statusStart:task.statusEnd]; got != task.StatusText {
			t.Errorf("%s status span = %q, want %q", task.ID, got, task.StatusText)
		}
	}
}

func TestParseTasksUnknownValuesKept(t *testing.T) {
	in := "## M0\n\n| ID | Status | Model | Owner |\n|---|---|---|---|\n| a | todo | sonnet | robot |\n"
	p := Parse("tasks.md", []byte(in), Options{})
	a := p.Task("a")
	if a.Status != StatusUnknown || a.StatusText != "todo" || a.Owner != "" || a.OwnerText != "robot" {
		t.Fatalf("a = %+v", a)
	}
}

func TestParseTasksTooManyCells(t *testing.T) {
	in := "## M0\n\n| ID | Status | Model |\n|---|---|---|\n| a | ready | sonnet | x | y |\n"
	p := Parse("tasks.md", []byte(in), Options{})
	want := []string{`tasks.md:5: row has 5 cells but the table header has 3; escape literal pipes as \|`}
	if got := issueMsgs(p.issues); !reflect.DeepEqual(got, want) {
		t.Fatalf("issues = %q", got)
	}
}

func TestParseTasksDuplicateIDLookup(t *testing.T) {
	in := "## M0\n\n| ID | Status | Model |\n|---|---|---|\n| a | ready | sonnet |\n| a | done | opus |\n"
	p := Parse("tasks.md", []byte(in), Options{})
	if len(p.Tasks) != 2 || p.Task("a").Line != 5 {
		t.Fatalf("lookup must return the first occurrence")
	}
}

func TestIsNone(t *testing.T) {
	for _, s := range []string{"", " ", "—", "-", "none", "None", "`—`"} {
		if !isNone(s) {
			t.Errorf("isNone(%q) = false", s)
		}
	}
	for _, s := range []string{"sonnet", "--", "–", "n/a"} {
		if isNone(s) {
			t.Errorf("isNone(%q) = true", s)
		}
	}
}
