package plan

import (
	"reflect"
	"testing"
)

// depsPlan has two phases; task IDs deliberately don't sort in file order
// (M0-G comes last, v1.2 contains dots) to prove ranges follow the file.
const depsPlanHead = "## P0\n\n| ID | Status | Model |\n|---|---|---|\n" +
	"| P0-01 | done | sonnet |\n" +
	"| P0-02 | done | sonnet |\n" +
	"\n## M0\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n" +
	"| M0-01 | | ready | sonnet |\n" +
	"| M0-03 | | ready | sonnet |\n" +
	"| M0-02 | | ready | sonnet |\n" +
	"| v1..2 | | ready | sonnet |\n" +
	"| M0-G | | ready | sonnet |\n"

func TestResolveDeps(t *testing.T) {
	tests := []struct {
		name string
		deps string
		want []string
	}{
		{"empty", "", nil},
		{"em dash", "—", nil},
		{"hyphen", "-", nil},
		{"none", "None", nil},
		{"single", "M0-01", []string{"M0-01"}},
		{"list", "M0-01, P0-02,M0-03", []string{"M0-01", "P0-02", "M0-03"}},
		{"backticks", "`M0-01`, `M0-02`", []string{"M0-01", "M0-02"}},
		{"ellipsis range in file order", "M0-01…M0-02", []string{"M0-01", "M0-03", "M0-02"}},
		{"three dots", "M0-03...M0-G", []string{"M0-03", "M0-02", "v1..2", "M0-G"}},
		{"two dots", "M0-02..M0-G", []string{"M0-02", "v1..2", "M0-G"}},
		{"spaced range", "M0-01 … M0-03", []string{"M0-01", "M0-03"}},
		{"cross-phase range", "P0-02…M0-01", []string{"P0-02", "M0-01"}},
		{"id containing dots is not a range", "v1..2", []string{"v1..2"}},
		{"duplicates removed, order kept", "M0-02, M0-01…M0-02, M0-01", []string{"M0-02", "M0-01", "M0-03"}},
		{"unknown ids kept for validation", "M9-99, M0-01", []string{"M9-99", "M0-01"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := depsPlanHead + "| X-1 | " + tt.deps + " | blocked | sonnet |\n"
			p := Parse("tasks.md", []byte(in), Options{})
			if len(p.issues) > 0 {
				t.Fatalf("issues: %v", issueMsgs(p.issues))
			}
			if got := p.Task("X-1").Deps; !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("deps = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveDepsErrors(t *testing.T) {
	tests := []struct {
		name  string
		deps  string
		want  []string // resolved deps that survive
		issue string
	}{
		{"open range", "M0-01…", nil, `X-1: malformed Deps range "M0-01…"; write it as A…B with two task IDs`},
		{"open start", "...M0-01", nil, `X-1: malformed Deps range "...M0-01"; write it as A…B with two task IDs`},
		{"three endpoints", "M0-01…M0-03…M0-02", nil, `X-1: malformed Deps range "M0-01…M0-03…M0-02"; write it as A…B with two task IDs`},
		{"unknown start", "M9-01…M0-02, P0-01", []string{"P0-01"}, `X-1: Deps range "M9-01…M0-02" refers to unknown task M9-01`},
		{"unknown end", "M0-01..M9-02", nil, `X-1: Deps range "M0-01..M9-02" refers to unknown task M9-02`},
		{"reversed", "M0-02…M0-01", nil, `X-1: Deps range "M0-02…M0-01" is not in file order (M0-02 does not come before M0-01); swap the endpoints`},
		{"same endpoint", "M0-01…M0-01", nil, `X-1: Deps range "M0-01…M0-01" is not in file order (M0-01 does not come before M0-01); swap the endpoints`},
		{"empty entry", "M0-01,,M0-02", []string{"M0-01", "M0-02"}, `X-1: empty entry in Deps "M0-01,,M0-02"; separate task IDs with single commas`},
		{"trailing comma", "M0-01,", []string{"M0-01"}, `X-1: empty entry in Deps "M0-01,"; separate task IDs with single commas`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := depsPlanHead + "| X-1 | " + tt.deps + " | blocked | sonnet |\n"
			p := Parse("tasks.md", []byte(in), Options{})
			want := []string{"tasks.md:17: " + tt.issue}
			if got := issueMsgs(p.issues); !reflect.DeepEqual(got, want) {
				t.Fatalf("issues = %q, want %q", got, want)
			}
			if got := p.Task("X-1").Deps; !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("deps = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveDepsMissingColumn(t *testing.T) {
	p := Parse("tasks.md", []byte("## M0\n\n| ID | Status | Model |\n|---|---|---|\n| a | ready | sonnet |\n"), Options{})
	if p.Task("a").Deps != nil {
		t.Fatal("missing Deps column means no deps")
	}
}

func TestResolveDepsAlias(t *testing.T) {
	in := "## M0\n\n| ID | Depends on | Status | Model |\n|---|---|---|---|\n| a | | ready | sonnet |\n| b | a | blocked | sonnet |\n"
	p := Parse("tasks.md", []byte(in), Options{Columns: map[string]string{"Depends on": "Deps"}})
	if got := p.Task("b").Deps; !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("deps = %q", got)
	}
}
