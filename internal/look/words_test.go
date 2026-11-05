package look

import (
	"strings"
	"testing"
)

func TestWords(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"! M1-03 Pick a license", "needs you M1-03 Pick a license"},
		{"! M1-04 needs you · commit?", "M1-04 needs you · commit?"},
		{"✓ M1-01 done", "M1-01 done"},
		{"✓ M1-01 committed · abc", "done M1-01 committed · abc"},
		{"✗ M1-02 verify failed · lint", "M1-02 verify failed · lint"},
		{"▲ phase M1 is stuck", "warning phase M1 is stuck"},
		{"◌ paused before M1-04", "waiting paused before M1-04"},
		{"· M1-03", "next M1-03"},
		{"  | ✓ output kept", "  | ✓ output kept"},
		{"M1-01 started · sonnet", "M1-01 started · sonnet"},
		{"✓", "done"},
		{"✓x", "✓x"},
	} {
		if got := Words(tc.in); got != tc.want {
			t.Errorf("Words(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCellSpoken(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Cell
		want Cell
	}{
		{"mark", Mark(Needs, GlyphNeedsYou, "M1-03"), Cell{Text: "needs you M1-03", Sev: Needs, Span: len("needs you")}},
		{"word", MarkAs(Accent, GlyphDone, "passed", "M1"), Cell{Text: "passed M1", Sev: Accent, Span: len("passed"), Word: "passed"}},
		{"unsaid", MarkAs(Accent, GlyphDone, Unsaid, "completed"), Cell{Text: "completed", Sev: Accent, Word: Unsaid}},
		{"whole cell painted", Cell{Text: "✓ ok", Sev: Accent}, Cell{Text: "done ok", Sev: Accent}},
		{"wide span", Cell{Text: "✗ FAILED: boom", Sev: Fail, Span: len("✗ FAILED"), Word: Unsaid}, Cell{Text: "FAILED: boom", Sev: Fail, Span: len("FAILED"), Word: Unsaid}},
		{"no glyph", TextCell("sonnet"), TextCell("sonnet")},
		{"dash is not a mark", TextCell("— x"), TextCell("— x")},
	} {
		if got := tc.in.Spoken(); got != tc.want {
			t.Errorf("%s: Spoken() = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// A spoken table is laid out on the words, not on the glyphs it replaced.
func TestTableSpoken(t *testing.T) {
	tb := &Table{Indent: "  ", Header: []string{"ID", "STATUS"}, Spoken: true}
	tb.Row(Mark(Accent, GlyphDone, "M1-01"), TextCell("done"))
	tb.Row(Mark(Needs, GlyphNeedsYou, "M1-02"), TextCell("in progress"))
	tb.Line(Mark(Accent, GlyphDone, "phase M1 complete"))
	want := "  ID               STATUS\n  done M1-01       done\n  needs you M1-02  in progress\n  done phase M1 complete\n"
	if got := tb.String(); got != want {
		t.Errorf("table:\n%s\nwant:\n%s", got, want)
	}
	tb.Spoken = false
	if got := tb.String(); got == want || !strings.Contains(got, "✓ M1-01") {
		t.Errorf("unspoken table:\n%s", got)
	}
}

func TestProgressWords(t *testing.T) {
	if got := ProgressWords(3, 7); got != "3 of 7 done" {
		t.Errorf("ProgressWords = %q", got)
	}
}

func TestGlyphWords(t *testing.T) {
	for _, g := range []string{GlyphDone, GlyphRunning, GlyphNeedsYou, GlyphReady, GlyphBlocked, GlyphSkipped, GlyphWarn, GlyphFail, GlyphWaiting} {
		if GlyphWord(g) == "" {
			t.Errorf("glyph %q has no word", g)
		}
	}
}
