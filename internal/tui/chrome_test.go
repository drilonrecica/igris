package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
)

// The open screen's pieces (chrome.go): the header keeps its status word
// and cuts the facts, the rules carry their labels, the columns share one
// divider, a dialog floats over the dimmed screen.

func TestHeader(t *testing.T) {
	plain := &theme{}
	tests := []struct {
		name          string
		w             int
		facts, status string
		want          string
	}{
		{"word at the right", 40, "sinjal · phase M0", "● Working", "igris · sinjal · phase M0      ● Working"},
		{"no word", 20, "sinjal", "", "igris · sinjal"},
		{"facts cut, the word stays", 28, "sinjal · phase M0 · mode plan", "Idle", "igris · sinjal · phas…  Idle"},
		{"too narrow for the word", 12, "sinjal", "Idle", "igris · sin…"},
	}
	for _, tt := range tests {
		got := plain.header(tt.w, tt.facts, tt.status)
		if got != tt.want || textWidth(got) > tt.w {
			t.Errorf("%s: %q (%d cells), want %q", tt.name, got, textWidth(got), tt.want)
		}
	}
	if room := headerRoom(40, "● Working"); room != 40-8-9-headerGap {
		t.Errorf("headerRoom = %d", room)
	}
	if got := plain.wordmark(); got != "igris" {
		t.Errorf("plain wordmark = %q", got)
	}
	// With color the second i is the brand's red; the word reads the same.
	th := colorTheme(t, true, nil)
	if got := strip(th.wordmark()); got != "igris" {
		t.Errorf("colored wordmark reads %q", got)
	}
	if !colored(th.wordmark()) || colored(th.paint(lookTitle, "igr")) {
		t.Errorf("the i isn't the colored part: %q", th.wordmark())
	}
}

func TestRuleAndColumns(t *testing.T) {
	plain := &theme{}
	if got := plain.rule(10, ""); got != "──────────" {
		t.Errorf("rule = %q", got)
	}
	if got := plain.rule(12, "LOG"); got != "── LOG ─────" {
		t.Errorf("labelled rule = %q", got)
	}
	if got := plain.rule(4, "TASKS"); textWidth(got) > 4 {
		t.Errorf("rule wider than the screen: %q", got)
	}
	left := []string{"TASKS", " ✓ M0-01"}
	right := []string{"card line 1", "card line 2", "card line 3"}
	got := plain.columns(left, right, 10, 30, 4, func(i int) bool { return i < 3 })
	want := []string{
		"TASKS      │ card line 1",
		" ✓ M0-01   │ card line 2",
		"           │ card line 3",
		"",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("columns = %q, want %q", got, want)
	}
	for _, l := range got {
		if textWidth(l) > 30 {
			t.Errorf("column row too wide: %q", l)
		}
	}
	if rw := columnsRight(10, 30); rw != 17 {
		t.Errorf("columnsRight = %d", rw)
	}
	// The divider stays on every row when asked.
	got = plain.columns(left, nil, 10, 30, 3, func(int) bool { return true })
	if got[2] != "           │" {
		t.Errorf("row without right text = %q", got[2])
	}
}

func TestCells(t *testing.T) {
	tests := []struct {
		s        string
		from, to int
		want     string
	}{
		{"abcdef", 0, 3, "abc"},
		{"abcdef", 2, 4, "cd"},
		{"abcdef", 4, 10, "ef"},
		{"abcdef", 8, 10, ""},
		// A wide grapheme cut by the edge becomes spaces inside it.
		{"a⚠️b", 0, 2, "a "},
		{"a⚠️b", 2, 4, " b"},
		// Styles pass through and the result is reset.
		{"\x1b[1mab\x1b[0mcd", 1, 3, "\x1b[1mb\x1b[0mc\x1b[0m"},
		{"\x1b[1mab\x1b[0mcd", 3, 4, "\x1b[1m\x1b[0md\x1b[0m"},
	}
	for _, tt := range tests {
		if got := cells(tt.s, tt.from, tt.to); got != tt.want {
			t.Errorf("cells(%q, %d, %d) = %q, want %q", tt.s, tt.from, tt.to, got, tt.want)
		}
	}
}

func TestFloatBox(t *testing.T) {
	plain := &theme{}
	screen := []string{"0123456789", "abcdefghij", "ABCDEFGHIJ", "short"}
	box := []string{"╭──╮", "╰──╯"}
	got := plain.floatBox(screen, box, 3, 1, 10)
	want := []string{"0123456789", "abc╭──╮hij", "ABC╰──╯HIJ", "short"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("floatBox = %q, want %q", got, want)
	}
	// Past the screen's end the box is dropped, not drawn out of range.
	if got := plain.floatBox(screen[:1], box, 3, 0, 10); len(got) != 1 || got[0] != "012╭──╮789" {
		t.Errorf("floatBox past the end = %q", got)
	}
	// The plain theme dims nothing; a theme dims every line and turns the
	// faint back on after each reset inside it.
	if got := plain.dim("abc"); got != "abc" {
		t.Errorf("plain dim = %q", got)
	}
	th := noColorTheme(t)
	if got := th.dim("a" + th.paint(lookTitle, "b") + "c"); got != "\x1b[2ma\x1b[1mb\x1b[0m\x1b[2mc\x1b[0m" {
		t.Errorf("dim = %q", got)
	}
	if got := th.dim(""); got != "" {
		t.Errorf("dim of nothing = %q", got)
	}
}

func TestFloatDialogZonesLandOnTheOptions(t *testing.T) {
	plain := &theme{}
	screen := make([]string, 20)
	for i := range screen {
		screen[i] = strings.Repeat("x", 100)
	}
	d := stopDialog()
	out, z := plain.floatDialog(screen, d, 100, 2, 16)
	if len(out) != 20 {
		t.Fatalf("%d lines", len(out))
	}
	found := false
	for y, l := range out {
		if i := strings.Index(l, "2. Stop igris"); i >= 0 {
			found = true
			if tg, ok := z.at(textWidth(l[:i]), y); !ok || tg.act != actOption || tg.option != 1 {
				t.Errorf("zone at the second option: %+v %v", tg, ok)
			}
			if !strings.HasPrefix(l, "xx") || !strings.HasSuffix(l, "xx") {
				t.Errorf("the screen doesn't show beside the box: %q", l)
			}
		}
	}
	if !found {
		t.Fatalf("option not drawn:\n%s", strings.Join(out, "\n"))
	}
	// Narrow: the dialog alone, cut to the screen.
	box, _ := plain.fullScreenDialog(d, 30, 6)
	if len(box) != 6 || !strings.HasPrefix(box[0], "╭") {
		t.Errorf("full-screen dialog = %q", box)
	}
}

func TestDialogHint(t *testing.T) {
	tests := []struct {
		name string
		d    *dialog
		want string
	}{
		{"pending question", questionDialog(engine.Event{Question: engine.QuestionCommit, Task: "M0-03"}), "enter choose · 1–2 pick · esc keep asking"},
		{"keep is the safe choice", stopDialog(), "enter choose · 1–2 pick · esc keep running"},
		{"cancel", retryDialog("M0-03"), "enter choose · 1–2 pick · esc close"},
		{"a field with a submit", doneDialog("M0-05"), "type · enter done · esc cancel"},
		{"a field without one", skipDialog("M0-03", false), "type · enter next · esc cancel"},
		{"one option", &dialog{title: "x", options: []option{{"Close", actClose}}, cancel: actClose}, "enter choose · esc close"},
	}
	for _, tt := range tests {
		if got := tt.d.hint(); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
	plain := &theme{}
	box, _ := stopDialog().render(plain, 60, 20)
	if last := box[len(box)-2]; !strings.Contains(last, "esc keep running") {
		t.Errorf("the hint isn't the dialog's last line: %q", last)
	}
}

func TestButtonText(t *testing.T) {
	tests := []struct {
		b     button
		short bool
		want  string
	}{
		{button{"Open session", "Open", "o", actOpen}, false, "o Open session"},
		{button{"Open session", "Open", "o", actOpen}, true, "o Open"},
		{button{label: "Mode", key: "m", act: actMode}, true, "m Mode"},
		{button{key: "?", act: actHelp}, false, "?"},
		{button{label: "More…", act: actMore}, false, "More…"},
	}
	for _, tt := range tests {
		if got := tt.b.text(tt.short); got != tt.want {
			t.Errorf("%+v short=%v: %q, want %q", tt.b, tt.short, got, tt.want)
		}
	}
	if d := moreDialog([]button{{label: "Pause", key: "p", act: actPause}}); len(d.options) != 1 || d.options[0] != (option{"Pause", actPause}) {
		t.Errorf("moreDialog options = %+v", d.options)
	}
}

// q leaves the screen it is on (SPEC §15.5): on a page in the run view
// it closes the page, as its [q Close] says; the run goes on.
func TestQClosesTheRunViewsPage(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.key("t")
	if hs.m.page == nil || !strings.Contains(hs.m.View(), "[q Close]") {
		t.Fatalf("no page after t:\n%s", hs.m.View())
	}
	hs.key("q")
	if hs.m.page != nil || hs.m.dialog != nil {
		t.Fatal("q didn't close the page, or asked to leave")
	}
	if len(hs.s.take()) != 0 {
		t.Error("q on a page sent a command")
	}
}
