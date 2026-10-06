package adapt

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// render writes a diff as "-a +b  c" for compact comparisons.
func render(d []Line) string {
	var parts []string
	for _, l := range d {
		parts = append(parts, [...]string{" ", "-", "+"}[l.Op]+l.Text)
	}
	return strings.Join(parts, " ")
}

func TestDiff(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want string
	}{
		{"both empty", "", "", ""},
		{"identical", "a b c", "a b c", " a  b  c"},
		{"from nothing", "", "a b", "+a +b"},
		{"to nothing", "a b", "", "-a -b"},
		{"all changed", "a b", "c d", "-a -b +c +d"},
		{"insert in middle", "a c", "a b c", " a +b  c"},
		{"delete in middle", "a b c", "a c", " a -b  c"},
		{"replace in middle", "a b c", "a x c", " a -b +x  c"},
		{"common tail", "x a b", "y a b", "-x +y  a  b"},
		{"moved line", "a b c", "b c a", "-a  b  c +a"},
		{"interleaved", "a x b y c", "a b c", " a -x  b -y  c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := render(Diff(strings.Fields(tt.a), strings.Fields(tt.b))); got != tt.want {
				t.Errorf("Diff(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestDiffRebuildsBothSides checks the invariants on a bigger input: the
// kept and removed lines are the original, the kept and added lines the
// proposal, with the right line numbers.
func TestDiffRebuildsBothSides(t *testing.T) {
	var a, b []string
	for i := range 200 {
		if i%7 != 0 {
			a = append(a, fmt.Sprintf("line %d", i))
		}
		if i%5 != 0 {
			b = append(b, fmt.Sprintf("line %d", i))
		}
	}
	var gotA, gotB []string
	for _, l := range Diff(a, b) {
		if l.Op != Add {
			gotA = append(gotA, l.Text)
			if l.Old != len(gotA) {
				t.Fatalf("old line number %d, want %d", l.Old, len(gotA))
			}
		}
		if l.Op != Del {
			gotB = append(gotB, l.Text)
			if l.New != len(gotB) {
				t.Fatalf("new line number %d, want %d", l.New, len(gotB))
			}
		}
	}
	if !slices.Equal(gotA, a) || !slices.Equal(gotB, b) {
		t.Error("the diff does not rebuild both sides")
	}
}

func TestDiffTooBigFallsBack(t *testing.T) {
	a := make([]string, 5000)
	b := make([]string, 5000)
	for i := range a {
		a[i], b[i] = fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i)
	}
	d := Diff(a, b)
	if added, removed := Counts(d); added != 5000 || removed != 5000 || d[0].Op != Del || d[5000].Op != Add {
		t.Errorf("added %d removed %d", added, removed)
	}
}

func TestLines(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"\n", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\r\nb\r\n", []string{"a", "b"}},
		{"a\n\nb", []string{"a", "", "b"}},
	}
	for _, tt := range tests {
		if got := Lines([]byte(tt.in)); !slices.Equal(got, tt.want) {
			t.Errorf("Lines(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
