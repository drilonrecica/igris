package tui

import (
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
)

func TestModePicker(t *testing.T) {
	tests := []struct {
		name  string
		keys  []string
		click int // option to click, when keys is empty
		want  string
	}{
		{name: "number", keys: []string{"2"}, want: "accept"},
		{name: "arrows", keys: []string{"up", "enter"}, want: "auto"}, // from plan, the current one
		{name: "k and space", keys: []string{"k", "k", "k", " "}, want: "default"},
		{name: "click", click: 2, want: "auto"},
		{name: "esc", keys: []string{"esc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := newHarness(t, 80, 24)
			hs.key("m")
			d := hs.m.dialog
			if d == nil || d.options[d.selected].act != actModePlan || !strings.Contains(hs.m.View(), "plan — plan first, you approve, then it implements (current)") {
				t.Fatalf("m opened %+v, want the picker on the current mode:\n%s", d, hs.m.View())
			}
			if len(tt.keys) == 0 {
				hs.click(isOption(tt.click))
			}
			hs.keys(tt.keys...)
			got := hs.s.take()
			if tt.want == "" {
				if len(got) != 0 {
					t.Errorf("sent %+v", got)
				}
			} else if len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdMode, Text: tt.want}) {
				t.Errorf("sent %+v, want mode %s", got, tt.want)
			}
			if hs.m.dialog != nil {
				t.Error("picker still open")
			}
		})
	}
}

func TestYoloNeedsTheTypedPhrase(t *testing.T) {
	tests := []struct {
		name string
		keys []string // after picking yolo
		sent bool
	}{
		{name: "enter twice cancels", keys: []string{"enter", "enter"}},
		{name: "nothing typed", keys: []string{"enter", "2"}},
		{name: "wrong phrase", keys: append(typing("skip"), "enter", "2")},
		{name: "phrase then esc", keys: append(typing("skip permissions"), "esc")},
		{name: "phrase then cancel", keys: append(typing("skip permissions"), "enter", "1")},
		{name: "phrase then confirm", keys: append(typing("skip permissions"), "enter", "2"), sent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := newHarness(t, 80, 24)
			hs.keys("m", "5")
			if len(hs.s.take()) != 0 || hs.m.dialog == nil || hs.m.dialog.input == nil || !hs.m.dialog.inField {
				t.Fatalf("picking yolo didn't ask for the phrase: %+v", hs.m.dialog)
			}
			hs.keys(tt.keys...)
			got := hs.s.take()
			if tt.sent {
				if len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdMode, Text: "yolo", Yes: true}) {
					t.Errorf("sent %+v, want confirmed yolo", got)
				}
			} else if len(got) != 0 {
				t.Errorf("sent %+v, want nothing", got)
			}
		})
	}
}

func TestYoloIsNeverOneClick(t *testing.T) {
	hs := newHarness(t, 80, 24)
	hs.click(isAct(actMode))
	hs.click(isOption(4))
	if got := hs.s.take(); len(got) != 0 {
		t.Fatalf("one click on yolo sent %+v", got)
	}
	hs.click(isOption(1)) // Skip permissions, with nothing typed
	if got := hs.s.take(); len(got) != 0 || hs.m.dialog == nil || !strings.Contains(hs.m.View(), "Type exactly: skip permissions") {
		t.Fatalf("confirm without the phrase sent %+v:\n%s", got, hs.m.View())
	}
	// Even when the run is already in yolo mode.
	hs.events(engine.Event{Kind: engine.ModeChanged, Detail: "yolo"})
	hs.m.dialog = nil
	hs.keys("m", "enter")
	if got := hs.s.take(); len(got) != 0 || hs.m.dialog == nil || hs.m.dialog.input == nil {
		t.Errorf("re-picking yolo sent %+v without the phrase", got)
	}
}

func TestTaskModePicker(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"))
	hs.keys("tab", "tab", "down") // the task list, from M0-03 to M0-04
	hs.key("M")
	d := hs.m.dialog
	if d == nil || d.title != "Mode for M0-04's next session" {
		t.Fatalf("M opened %+v", d)
	}
	hs.key("3")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdTaskMode, Task: "M0-04", Text: "auto"}) {
		t.Fatalf("sent %+v", got)
	}
	hs.events(engine.Event{Kind: engine.TaskModeChanged, Task: "M0-04", Detail: "auto"})
	if hs.m.mode != "plan" || hs.m.overrides["M0-04"] != "auto" {
		t.Errorf("run mode %q, override %q", hs.m.mode, hs.m.overrides["M0-04"])
	}
	if !strings.Contains(hs.m.View(), "mode for M0-04's next session: auto") {
		t.Errorf("override not logged:\n%s", hs.m.View())
	}
	hs.key("M")
	if d := hs.m.dialog; d.options[d.selected].act != actModeAuto {
		t.Errorf("picker doesn't start on the override")
	}
	hs.keys("5")
	hs.keys(typing("skip permissions")...)
	hs.keys("enter", "2")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdTaskMode, Task: "M0-04", Text: "yolo", Yes: true}) {
		t.Fatalf("sent %+v", got)
	}

	// Without a selection, M is about the current task.
	cur := newHarness(t, 120, 30)
	cur.withPlan(demoPlan)
	cur.events(started("M0-03"))
	cur.click(isAct(actTaskMode))
	if d := cur.m.dialog; d == nil || d.task != "M0-03" {
		t.Errorf("Task mode button opened %+v, want M0-03", d)
	}
}

func TestSkipPermissionsBadge(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.events(started("M0-03"), engine.Event{Kind: engine.SessionOpened, Task: "M0-03", Mode: "yolo"})
	v := hs.m.View()
	if !strings.Contains(strings.Split(v, "\n")[0], "SKIP PERMISSIONS") {
		t.Errorf("header lacks the badge while a session skips permissions:\n%s", v)
	}
	hs.events(engine.Event{Kind: engine.ModeChanged, Detail: "yolo"})
	if top := strings.Split(hs.m.View(), "\n")[0]; strings.Count(top, "SKIP PERMISSIONS") != 1 {
		t.Errorf("header badge %q, want it once", top)
	}
}

// typing is the key presses that type s.
func typing(s string) []string {
	var out []string
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}
