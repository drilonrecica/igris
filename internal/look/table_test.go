package look

import (
	"strings"
	"testing"
)

func TestCut(t *testing.T) {
	for _, tc := range []struct {
		in   string
		w    int
		want string
	}{
		{"Config loader", 20, "Config loader"},
		{"Config loader", 13, "Config loader"},
		{"Config loader", 8, "Config …"},
		{"Config loader", 1, "…"},
		{"Config loader", 0, ""},
		{"日本語のタイトル", 7, "日本語…"}, // wide runes take two cells
		{"✓ done", 4, "✓ d…"},
	} {
		if got := Cut(tc.in, tc.w); got != tc.want {
			t.Errorf("Cut(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
		if got := Width(Cut(tc.in, tc.w)); got > max(tc.w, 0) {
			t.Errorf("Cut(%q, %d) is %d cells wide", tc.in, tc.w, got)
		}
	}
}

func statusTable() *Table {
	tb := &Table{Indent: "  ", Header: []string{"ID", "STATUS", "OWNER", "WAITS ON"}, Fold: true}
	tb.Row(Mark(Accent, GlyphDone, "M2-01"), TextCell("done"), TextCell("agent"))
	tb.Row(Mark(Plain, GlyphBlocked, "M2-04"), TextCell("blocked"), TextCell("agent + user"), TextCell("M2-02, M2-03 (phase M1)"))
	return tb
}

func TestTable(t *testing.T) {
	want := strings.Join([]string{
		"  ID       STATUS   OWNER         WAITS ON",
		"  ✓ M2-01  done     agent",
		"  ⨯ M2-04  blocked  agent + user  M2-02, M2-03 (phase M1)",
	}, "\n") + "\n"
	if got := statusTable().String(); got != want {
		t.Errorf("table:\n%s\nwant:\n%s", got, want)
	}
}

// Too wide, the last column folds onto a line of its own first; then
// the widest cells are cut.
func TestTableFits(t *testing.T) {
	tb := statusTable()
	got := tb.Lines(40, nil)
	want := []string{
		"  ID       STATUS   OWNER",
		"  ✓ M2-01  done     agent",
		"  ⨯ M2-04  blocked  agent + user",
		"           waits on M2-02, M2-03 (phase…",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("at 40:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, w := range []int{40, 30, 24} {
		for _, l := range tb.Lines(w, nil) {
			if Width(l) > w {
				t.Errorf("at %d: %q is %d cells wide", w, l, Width(l))
			}
			if strings.TrimRight(l, " ") != l {
				t.Errorf("at %d: %q has trailing spaces", w, l)
			}
		}
	}
	if got := tb.Lines(24, nil)[2]; got != "  ⨯ M2-…  block…  agent…" {
		t.Errorf("at 24: %q", got)
	}
}

// An empty cell shows Empty; lines of free text don't size the columns.
func TestTableEmptyAndLines(t *testing.T) {
	tb := &Table{Header: []string{"TASK", "RANK", "MODE"}}
	tb.Row(TextCell("A-1"), TextCell(""), TextCell("plan"))
	tb.Line(TextCell("phase A complete, and this line is longer than the table"))
	tb.Row(TextCell("A-2"), TextCell("opus"))
	want := "TASK  RANK  MODE\nA-1   —     plan\nphase A complete, and this line is longer than the table\nA-2   opus  —\n"
	if got := tb.String(); got != want {
		t.Errorf("table:\n%q\nwant:\n%q", got, want)
	}
}

// The painter gets the glyph only, and the padding is measured without
// the escape sequences.
func TestTablePaints(t *testing.T) {
	paint := func(sev Severity, s string) string { return Paint(true, true, sev, s) }
	got := statusTable().Lines(0, paint)
	if want := "  \x1b[38;2;60;200;255m✓\x1b[0m M2-01  done     agent"; got[1] != want {
		t.Errorf("painted row = %q, want %q", got[1], want)
	}
	if got[2] != "  ⨯ M2-04  blocked  agent + user  M2-02, M2-03 (phase M1)" {
		t.Errorf("a plain glyph was painted: %q", got[2])
	}
}

// Cells give way down to their header's width before a header is cut.
func TestTableCutsCellsBeforeHeaders(t *testing.T) {
	tb := &Table{Header: []string{"PHASE", "TITLE", "IN PROGRESS"}}
	tb.Row(TextCell("P1"), TextCell("Hello CLI and more"), TextCell("0"))
	got := tb.Lines(25, nil)
	if got[0] != "PHASE  TITLE  IN PROGRESS" || got[1] != "P1     Hell…  0" {
		t.Errorf("at 25:\n%s", strings.Join(got, "\n"))
	}
}
