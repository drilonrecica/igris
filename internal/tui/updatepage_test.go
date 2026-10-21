package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/update"
)

// Checks as project.CheckUpdate returns them.
var (
	brewCheck = update.Check{Current: "1.1.0", Latest: "1.2.0", Status: update.Newer, Install: update.Install{
		Method: update.Brew, Argv: []string{"brew", "upgrade", "igris"},
	}}
	goCheck = update.Check{Current: "1.1.0", Latest: "1.2.0", Status: update.Newer, Install: update.Install{
		Method: update.GoInstall, Argv: []string{"go", "install", "github.com/drilonrecica/igris/cmd/igris@v1.2.0"},
		Env: []string{"GOBIN=/home/u/go/bin"},
	}}
	archiveCheck = update.Check{Current: "1.1.0", Latest: "1.2.0", Status: update.Newer, Install: update.Install{
		Method: update.Archive, Steps: update.ArchiveSteps("1.2.0", "linux", "amd64", "/home/u/.local/bin/igris"),
	}}
)

// updateHome is home in fixture state with Check for updates at t.
func updateHome(w, h int, state string, t updateTarget) *homeScreen {
	var fix homeFixture
	for _, st := range homeStates {
		if st.name == state {
			fix = st.fix()
		}
	}
	m := homeAt(&theme{}, w, h, fix)
	m.svc = &fakeServices{}
	m.updating = &t
	m.showUpdate()
	return m
}

// Every state of the dialog at the three sizes (SPEC §17).
func TestUpdateGolden(t *testing.T) {
	failed := update.Result{
		Err:  errors.New("go install github.com/drilonrecica/igris/cmd/igris@v1.2.0 exited with status 1"),
		Tail: []string{"go: github.com/drilonrecica/igris/cmd/igris@v1.2.0: reading github.com/drilonrecica/igris/go.mod at revision v1.2.0: unknown revision v1.2.0"},
		Hint: "The Go checksum database can lag a new tag by about 30 minutes. Try again later, or now with: GOPROXY=direct GONOSUMDB=github.com/drilonrecica/igris GOBIN=/home/u/go/bin go install github.com/drilonrecica/igris/cmd/igris@v1.2.0",
	}
	cases := []struct {
		name, state string
		t           updateTarget
	}{
		{"checking", "ready", updateTarget{step: updChecking}},
		{"up_to_date", "ready", updateTarget{step: updChecked, check: update.Check{Current: "1.2.0", Latest: "1.2.0"}}},
		{"dev", "ready", updateTarget{step: updChecked, check: update.Check{Current: "dev", Latest: "1.2.0", Status: update.Dev}}},
		{"newer", "ready", updateTarget{step: updChecked, check: brewCheck}},
		{"newer_go", "ready", updateTarget{step: updChecked, check: goCheck}},
		{"newer_run", "running_elsewhere", updateTarget{step: updChecked, check: brewCheck}},
		{"archive", "ready", updateTarget{step: updChecked, check: archiveCheck}},
		{"network", "ready", updateTarget{step: updChecked, err: errors.New("ask GitHub for the latest release: no answer within 10s; check your connection and try again")}},
		{"updating", "ready", updateTarget{step: updRunning, check: brewCheck}},
		{"updated", "ready", updateTarget{step: updDone, check: brewCheck, res: update.Result{Message: "Updated: igris in PATH is now 1.2.0."}}},
		{"failed", "ready", updateTarget{step: updDone, check: goCheck, res: failed}},
	}
	for _, c := range cases {
		for _, size := range homeSizes {
			w, h := size[0], size[1]
			t.Run(fmt.Sprintf("%s_%dx%d", c.name, w, h), func(t *testing.T) {
				view := updateHome(w, h, c.state, c.t).View()
				checkFits(t, view, w, h)
				golden(t, fmt.Sprintf("update_%s_%dx%d", c.name, w, h), view)
			})
		}
	}
}

// u checks, the dialog offers Update, Update runs the command the check
// found and the result says to restart.
func TestUpdateFlow(t *testing.T) {
	var got update.Check
	svc := &fakeServices{check: brewCheck, updateFn: func(_ context.Context, c update.Check) update.Result {
		got = c
		return update.Result{Message: "Updated: igris in PATH is now 1.2.0."}
	}}
	a := testApp(svc, 120, 40)
	drive(a, a.Init())
	home := a.stack[0].(*homeScreen)
	drive(a, func() tea.Msg { return keyMsg("u") })
	if n := svc.called("CheckUpdate"); n != 1 {
		t.Fatalf("CheckUpdate called %d times, want 1", n)
	}
	v := a.View()
	for _, want := range []string{"igris 1.2.0 is out", "1.1.0 → 1.2.0 · installed with Homebrew", "brew upgrade igris", "Runs in other projects keep going", "1. Update", "2. Not now"} {
		if !strings.Contains(v, want) {
			t.Errorf("the dialog lacks %q:\n%s", want, v)
		}
	}
	drive(a, func() tea.Msg { return keyMsg("enter") })
	if svc.called("Update") != 1 || got.Latest != "1.2.0" {
		t.Fatalf("Update called %d times with %+v", svc.called("Update"), got)
	}
	v = a.View()
	if !strings.Contains(v, "Updated: igris in PATH is now 1.2.0. Quit and start igris again.") {
		t.Errorf("no restart message:\n%s", v)
	}
	if !strings.Contains(home.status, "Quit and start igris again") {
		t.Errorf("status %q", home.status)
	}
	if quit := drive(a, func() tea.Msg { return keyMsg("enter") }); !quit {
		t.Error("Quit igris doesn't quit")
	}
}

// While a run holds this project's lock (another igris's, or this one's),
// the dialog offers no Update and the action does nothing.
func TestUpdateNotDuringARun(t *testing.T) {
	for _, name := range []string{"running_elsewhere", "own run"} {
		t.Run(name, func(t *testing.T) {
			state := "running_elsewhere"
			if name == "own run" {
				state = "ready"
			}
			m := updateHome(120, 40, state, updateTarget{step: updChecked, check: brewCheck})
			if name == "own run" {
				m.run = &liveRun{}
				m.showUpdate()
			}
			svc := m.svc.(*fakeServices)
			for _, o := range m.dialog.options {
				if o.act == actUpdateRun {
					t.Fatalf("Update offered: %+v", m.dialog.options)
				}
			}
			if v := m.View(); !strings.Contains(v, "Update after the run ends") {
				t.Errorf("no reason given:\n%s", v)
			}
			if cmd := m.updatePick(actUpdateRun); cmd != nil || svc.called("Update") != 0 {
				t.Error("Update ran during a run")
			}
		})
	}
}

// A failed check says why and offers Try again; Cancel while checking
// drops the answer.
func TestUpdateCheckFailsAndCancels(t *testing.T) {
	svc := &fakeServices{checkErr: errors.New("ask GitHub for the latest release: can't resolve api.github.com; check your connection and try again")}
	a := testApp(svc, 120, 40)
	drive(a, a.Init())
	drive(a, func() tea.Msg { return keyMsg("u") })
	if v := a.View(); !strings.Contains(v, "Check for updates failed") || !strings.Contains(v, "check your connection") || !strings.Contains(v, "Try again") {
		t.Fatalf("no failure dialog:\n%s", a.View())
	}
	drive(a, func() tea.Msg { return keyMsg("enter") }) // Try again
	if n := svc.called("CheckUpdate"); n != 2 {
		t.Errorf("Try again: CheckUpdate called %d times, want 2", n)
	}

	m := readyHome(120, 40)
	m.svc = &fakeServices{check: brewCheck}
	m.openUpdate()
	m.Update(keyMsg("esc"))
	if m.dialog != nil || m.updating != nil {
		t.Fatal("esc doesn't cancel the check")
	}
	m.Update(updateCheckedMsg{c: brewCheck})
	if m.dialog != nil {
		t.Error("a cancelled check's answer opened the dialog")
	}
}

// Closing the dialog while the command runs leaves it running; the result
// opens the dialog again, or goes to the status line when another dialog
// is open. A failure shows the cleaned output and the hint.
func TestUpdateRunsOnAfterClose(t *testing.T) {
	m := updateHome(120, 40, "ready", updateTarget{step: updRunning, check: goCheck})
	m.Update(keyMsg("esc"))
	if m.dialog != nil || m.updating == nil || m.updating.open {
		t.Fatal("closing the running update's dialog dropped it")
	}
	res := update.Result{Err: errors.New("go install exited with status 1"), Tail: []string{"bad \x1b[31mred\x1b[0m line"}, Hint: "try GOPROXY=direct"}
	m.Update(updateDoneMsg{res})
	v := m.View()
	for _, want := range []string{"Update failed", "bad red line", "try GOPROXY=direct"} {
		if !strings.Contains(v, want) {
			t.Errorf("the failure lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "\x1b[31m") {
		t.Error("the output tail isn't cleaned")
	}

	m = updateHome(120, 40, "ready", updateTarget{step: updRunning, check: brewCheck})
	m.Update(keyMsg("esc"))
	m.dialog = moreDialog([]option{{"Settings", actSettings}})
	m.Update(updateDoneMsg{update.Result{Message: "Updated: igris in PATH is now 1.2.0."}})
	if m.updating != nil || !strings.Contains(m.status, "Updated: igris in PATH is now 1.2.0.") {
		t.Errorf("result with another dialog open: updating %v, status %q", m.updating != nil, m.status)
	}
}

// The run view never offers it: u means nothing there.
func TestUpdateNotInTheRunView(t *testing.T) {
	if _, ok := shortcuts["u"]; ok {
		t.Error("the run view maps u")
	}
	if homeShortcuts["u"] != actUpdate {
		t.Error("home doesn't map u to Check for updates")
	}
}
