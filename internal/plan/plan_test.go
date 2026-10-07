package plan

import (
	"reflect"
	"strings"
	"testing"
)

// specExample is the canonical example from SPEC §3.6.
const specExample = "# Example plan\n\n" +
	"## Legend\n\n| Value | Meaning |\n|---|---|\n| `ready` | can start |\n\n" +
	"## M0 — Repository foundation\n\n" +
	"| ID | Task | Deps | Status | Model | Owner |\n" +
	"|---|---|---|---|---|---|\n" +
	"| M0-01 | **Go module** — init module, pin Go version | — | ready | sonnet | agent |\n" +
	"| M0-02 | **Entrypoint** — main.go with subcommands | M0-01 | blocked | sonnet | agent |\n" +
	"| M0-03 | **Crypto envelope** — `v1 \\| nonce \\| ciphertext` | M0-01 | blocked | opus | agent |\n" +
	"| M0-G | **M0 gate** — owner smoke test | M0-02…M0-03 | blocked | sonnet | agent + user |\n"

func phaseIDs(p *Plan) []string {
	var ids []string
	for _, ph := range p.Phases {
		ids = append(ids, ph.ID)
	}
	return ids
}

func issueMsgs(issues []Issue) []string {
	var out []string
	for _, i := range issues {
		out = append(out, i.Error())
	}
	return out
}

func TestParsePhasesSpecExample(t *testing.T) {
	p := Parse("tasks.md", []byte(specExample), Options{})
	if len(p.issues) > 0 {
		t.Fatalf("unexpected issues: %v", issueMsgs(p.issues))
	}
	if got := phaseIDs(p); !reflect.DeepEqual(got, []string{"M0"}) {
		t.Fatalf("phases = %v", got)
	}
	ph := p.Phases[0]
	if ph.Title != "Repository foundation" || ph.Line != 9 || ph.tableLine != 11 || len(ph.rows) != 4 {
		t.Fatalf("phase = %+v", ph)
	}
	if want := []string{"ID", "Task", "Deps", "Status", "Model", "Owner"}; !reflect.DeepEqual(ph.Columns, want) {
		t.Fatalf("columns = %v", ph.Columns)
	}
	if got := ph.rows[2].cells[1].value; got != "**Crypto envelope** — `v1 | nonce | ciphertext`" {
		t.Fatalf("escaped pipes: %q", got)
	}
	if p.Phase("m0") != ph || p.Phase("M1") != nil {
		t.Fatal("Phase lookup must be case-insensitive")
	}
}

func TestPhaseID(t *testing.T) {
	tests := []struct {
		heading, id, title string
	}{
		{"M0 — Repository foundation", "M0", "Repository foundation"},
		{"P0 - Decisions", "P0", "Decisions"},
		{"Phase 2 — API", "Phase-2", "API"},
		{"Phase 2: API", "Phase", "2: API"}, // second token must be a bare number
		{"Phase 2b — API", "Phase", "2b — API"},
		{"M1", "M1", ""},
		{"M1.5 · Extras", "M1.5", "Extras"},
		{"Étape 3 — Tests", "Étape-3", "Tests"},
		{"2 Phase", "2", "Phase"},
	}
	for _, tt := range tests {
		ph := newPhase(tt.heading, 1)
		if ph.ID != tt.id || ph.Title != tt.title {
			t.Errorf("%q: got (%q, %q), want (%q, %q)", tt.heading, ph.ID, ph.Title, tt.id, tt.title)
		}
	}
}

func TestHeadingText(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"## M0 — x", "M0 — x", true},
		{"  ## M0", "M0", true},
		{"## M0 ##", "M0", true},
		{"##", "", true},
		{"### M0", "", false},
		{"# M0", "", false},
		{"##M0", "", false},
		{"    ## M0", "", false}, // indented code
	}
	for _, tt := range tests {
		got, ok := headingText(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("headingText(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

const taskHeader = "| ID | Task | Status | Model |\n|---|---|---|---|\n"

func TestParseStructure(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		opts   Options
		phases []string
		issues []string
	}{
		{
			name:   "sections without task tables are ignored",
			in:     "## Legend\n\n| Value | Meaning |\n|---|---|\n| a | b |\n\n## Notes\n\ntext\n\n## M0\n\n" + taskHeader + "| a | x | ready | sonnet |\n",
			phases: []string{"M0"},
		},
		{
			name:   "phase order is file order",
			in:     "## B\n\n" + taskHeader + "| b | x | ready | sonnet |\n\n## A\n\n" + taskHeader + "| a | x | ready | sonnet |\n",
			phases: []string{"B", "A"},
		},
		{
			name:   "fenced code is ignored",
			in:     "## M0\n\n" + taskHeader + "| a | x | ready | sonnet |\n\n```markdown\n## M9 — fake\n\n" + taskHeader + "| z | x | ready | sonnet |\n```\n\n~~~~\n```\n## M8\n~~~~\n",
			phases: []string{"M0"},
		},
		{
			name:   "subheadings stay in the phase",
			in:     "## M0\n\n### Details\n\n" + taskHeader + "| a | x | ready | sonnet |\n",
			phases: []string{"M0"},
		},
		{
			name:   "table ends at blank line",
			in:     "## M0\n\n" + taskHeader + "| a | x | ready | sonnet |\n\n| b | x | ready | sonnet |\n",
			phases: []string{"M0"},
		},
		{
			name:   "second table in a phase",
			in:     "## M0\n\n" + taskHeader + "| a | x | ready | sonnet |\n\n" + taskHeader + "| b | x | ready | sonnet |\n",
			phases: []string{"M0"},
			issues: []string{"tasks.md:7: phase M0 has a second task table (first at line 3); a phase may contain only one"},
		},
		{
			name:   "non-task table next to a task table is fine",
			in:     "## M0\n\n" + taskHeader + "| a | x | ready | sonnet |\n\n| Key | Value |\n|---|---|\n| k | v |\n",
			phases: []string{"M0"},
		},
		{
			name:   "task table outside a phase",
			in:     "# Plan\n\n" + taskHeader + "| a | x | ready | sonnet |\n",
			issues: []string{`tasks.md:3: task table outside a phase; put it under a "## <phase ID> — <title>" heading`},
		},
		{
			name:   "duplicate phase IDs, case-insensitive",
			in:     "## M0 — a\n\n" + taskHeader + "| a | x | ready | sonnet |\n\n## m0 — b\n\n" + taskHeader + "| b | x | ready | sonnet |\n",
			phases: []string{"M0"},
			issues: []string{"tasks.md:7: duplicate phase ID m0 (first at line 1); phase IDs must be unique"},
		},
		{
			name:   "duplicate non-task sections are fine",
			in:     "## Notes\n\n## Notes\n\n## M0\n\n" + taskHeader + "| a | x | ready | sonnet |\n",
			phases: []string{"M0"},
		},
		{
			name:   "empty heading",
			in:     "##\n\n" + taskHeader + "| a | x | ready | sonnet |\n",
			issues: []string{`tasks.md:3: task table under an empty "##" heading; give the heading a phase ID`},
		},
		{
			name:   "duplicate column",
			in:     "## M0\n\n| ID | Status | Model | status |\n|---|---|---|---|\n",
			phases: []string{"M0"},
			issues: []string{"tasks.md:3: column Status appears more than once in the table header"},
		},
		{
			name:   "crlf",
			in:     strings.ReplaceAll("## M0\n\n"+taskHeader+"| a | x | ready | sonnet |\n", "\n", "\r\n"),
			phases: []string{"M0"},
		},
		{
			// Windows editors such as Notepad may start the file with a
			// UTF-8 byte order mark.
			name:   "bom before the first heading",
			in:     "\ufeff## M0\r\n\r\n" + strings.ReplaceAll(taskHeader+"| a | x | ready | sonnet |\n", "\n", "\r\n"),
			phases: []string{"M0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Parse("tasks.md", []byte(tt.in), tt.opts)
			if got := phaseIDs(p); !reflect.DeepEqual(got, tt.phases) {
				t.Errorf("phases = %v, want %v", got, tt.phases)
			}
			if got := issueMsgs(p.issues); !reflect.DeepEqual(got, tt.issues) {
				t.Errorf("issues = %q, want %q", got, tt.issues)
			}
		})
	}
}

func TestParseColumns(t *testing.T) {
	tests := []struct {
		name   string
		header string
		opts   Options
		want   []string // nil = not a task table
	}{
		{"canonical", "| ID | Task | Deps | Status | Model | Owner | Mode |", Options{}, []string{"ID", "Task", "Deps", "Status", "Model", "Owner", "Mode"}},
		{"case and decoration", "| **id** | `TASK` | *status* | MODEL |", Options{}, []string{"ID", "Task", "Status", "Model"}},
		{"extra columns kept", "| ID | Spec | Status | Model | Notes |", Options{}, []string{"ID", "Spec", "Status", "Model", "Notes"}},
		{"aliases", "| ID | Depends on | Status | **Agent** |", Options{Columns: map[string]string{"Depends on": "Deps", "agent": "Model"}}, []string{"ID", "Deps", "Status", "Model"}},
		{"alias target case-insensitive", "| ID | State | Status2 | Model |", Options{Columns: map[string]string{"state": "status"}}, []string{"ID", "Status", "Status2", "Model"}},
		{"missing model", "| ID | Task | Status |", Options{}, nil},
		{"missing status", "| ID | Model |", Options{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sep := strings.Repeat("|---", len(splitRow(tt.header))) + "|\n"
			p := Parse("tasks.md", []byte("## M0\n\n"+tt.header+"\n"+sep), tt.opts)
			if tt.want == nil {
				if len(p.Phases) != 0 {
					t.Fatalf("expected no task table, got columns %v", p.Phases[0].Columns)
				}
				return
			}
			if len(p.Phases) != 1 {
				t.Fatalf("expected a task table; issues %v", issueMsgs(p.issues))
			}
			if !reflect.DeepEqual(p.Phases[0].Columns, tt.want) {
				t.Fatalf("columns = %v, want %v", p.Phases[0].Columns, tt.want)
			}
		})
	}
}

func TestFences(t *testing.T) {
	tests := []struct {
		open, close string
		closes      bool
	}{
		{"```", "```", true},
		{"```go", "```", true},
		{"````", "```", false},
		{"```", "````", true},
		{"~~~", "```", false},
		{"~~~", "~~~ ", true},
		{"```", "``` x", false},
	}
	for _, tt := range tests {
		f := fenceOpen(tt.open)
		if f == "" {
			t.Fatalf("%q should open a fence", tt.open)
		}
		if got := isFenceClose(tt.close, f); got != tt.closes {
			t.Errorf("open %q close %q = %v, want %v", tt.open, tt.close, got, tt.closes)
		}
	}
	if fenceOpen("``` a`b") != "" || fenceOpen("``") != "" {
		t.Error("not fences")
	}
}

func TestHeaderCellsAndTableLines(t *testing.T) {
	src := "# P\n\n## A — x\n\n| Status | **ID** | Model | Depends on | Spec |\n|---|---|---|---|---|\n| ready | A-1 | sonnet | — | 3 |\n| todo | A-2 | | A-1 |\n\nafter\n\n## B\n\n| ID | Status | Model |\n|---|---|---|\n"
	p := Parse("p.md", []byte(src), Options{Columns: map[string]string{"Depends on": "Deps"}})
	a, b := p.Phase("A"), p.Phase("B")
	if a == nil || b == nil {
		t.Fatalf("phases %+v", p.Phases)
	}
	if got, want := strings.Join(a.Columns, ","), "Status,ID,Model,Deps,Spec"; got != want {
		t.Errorf("Columns = %s, want %s", got, want)
	}
	if got, want := strings.Join(a.Header, ","), "Status,ID,Model,Depends on,Spec"; got != want {
		t.Errorf("Header = %s, want %s", got, want)
	}
	if got, want := strings.Join(p.Task("A-1").Cells, ","), "ready,A-1,sonnet,—,3"; got != want {
		t.Errorf("A-1 Cells = %s, want %s", got, want)
	}
	if got, want := strings.Join(p.Task("A-2").Cells, ","), "todo,A-2,,A-1,"; got != want {
		t.Errorf("A-2 Cells = %s, want %s (a missing cell is empty)", got, want)
	}
	if first, last := a.TableLines(); first != 5 || last != 8 {
		t.Errorf("A TableLines = %d, %d, want 5, 8", first, last)
	}
	if first, last := b.TableLines(); first != 14 || last != 15 {
		t.Errorf("B TableLines = %d, %d, want 14, 15 (no rows)", first, last)
	}
}
