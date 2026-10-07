package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
)

// homePlan is the synthetic plan of the home screen's goldens: two phases
// done, one with work, two waiting on it.
const homePlan = `## M0 — Foundations

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M0-01 | **Go module** | — | done | sonnet | agent |
| M0-02 | **Entrypoint** | M0-01 | done | sonnet | agent |
| M0-03 | **Makefile** | M0-01 | done | haiku | agent |

## M1 — Plan parser

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M1-01 | **Parser** | M0-01 | done | opus | agent |
| M1-02 | **Validator** | M1-01 | skipped | sonnet | agent |

## M2 — Config & state

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M2-01 | **Config loader** | M1-01 | done | sonnet | agent |
| M2-02 | **Lock file** with a title long enough to be cut in a narrow row | M2-01 | STATUS | opus | agent |
| M2-03 | **State file** | M2-02 | ready | sonnet | agent |
| M2-04 | **Signals** | M2-02, M2-03 | ready | haiku | agent |

## M3 — Engine

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M3-01 | **Scheduler** | M2-04 | ready | opus | agent |
| M3-02 | **Run loop** | M3-01 | ready | opus | agent |
| M3-03 | **Verify** | M3-02 | ready | sonnet | agent |

## M4 — TUI

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M4-01 | **Layout** | M3-03 | ready | fable | agent |
| M4-02 | **Dialogs** | M4-01 | ready | sonnet | agent |
`

// homeInvalidPlan has three kinds of problems.
const homeInvalidPlan = `## M9 — Broken

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M9-02 | **Depends on nothing** | M9-01 | ready | sonnet | agent |
| M9-03 | **Odd status** | — | wip | sonnet | agent |
| M9-04 | **Odd model** | — | ready | gpt | agent |
| M9-05 | **Odd status again** | — | doing | sonnet | agent |
`

// homeNow is the home goldens' clock: the morning after the last run.
var homeNow = time.Date(2026, 10, 7, 9, 41, 0, 0, time.UTC)

// homeSnap is the project sinjal with the plan text, parsed and validated
// as Snapshot does it; status is "ready" unless the M2-02 placeholder says
// otherwise.
func homeSnap(status string) *report.Snapshot {
	cfg := config.Default()
	cfg.Notify.Ntfy.Topic = "igris" // a channel is set up
	text := strings.ReplaceAll(homePlan, "STATUS", status)
	s := &report.Snapshot{
		Root: "/src/sinjal", Project: "sinjal", Found: true, Config: cfg, PlanPath: "/src/sinjal/tasks.md", ConfigPath: "/src/sinjal/igris.toml",
		Lock:   state.LockState{Path: "/src/sinjal/.igris/igris.lock"},
		Recent: homeRecent(),
	}
	s.Plan = plan.Parse("tasks.md", []byte(text), plan.Options{})
	s.Issues = report.Issues(s.Plan.Validate(cfg.Models))
	if len(s.Issues) == 0 {
		st, err := report.Status(s.Plan, "")
		if err != nil {
			panic(err)
		}
		s.Status = &st
	}
	return s
}

func homeRecent() []report.HistoryRun {
	return []report.HistoryRun{
		{StartedAt: "2026-10-06T21:10:00Z", EndedAt: "2026-10-06T22:40:00Z", Phases: []string{"M2"}, End: "stopped", Done: 3, Skipped: 1},
		{StartedAt: "2026-10-05T18:02:00Z", EndedAt: "2026-10-05T19:30:00Z", Phases: []string{"M1"}, End: "completed", Done: 6},
	}
}

// homeRun is a run on M2 through M3 that is on M2-02 (or between tasks,
// with task "").
func homeRun(task string, since time.Time) *report.RunInfo {
	r := &report.RunInfo{
		Phases: []string{"M2"}, Through: "M3", StartedAt: "2026-10-07T07:12:00Z",
		Task: task, Lock: report.LockNone, Signals: []string{},
	}
	if task != "" {
		r.Mode, r.Since, r.Session = "plan", since.UTC().Format(time.RFC3339), "herdr tab-3"
	}
	return r
}

var homeDoctor = []checks.Result{
	{ID: "git", Level: checks.OK, Message: "git 2.50"},
	{ID: "uncommitted", Level: checks.Warn, Message: "uncommitted changes"},
}

// homeFixture is one state of the home screen.
type homeFixture struct {
	snap    *report.Snapshot
	snapErr error
	// backend is the herdr check's answer; unknown says it hasn't come yet.
	backend    error
	unknown    bool
	doctor     []checks.Result
	doctorDone bool
}

// homeStates are the states the NOW card has (SPEC §15.6), each as the
// data it is read from.
var homeStates = []struct {
	name string
	fix  func() homeFixture
}{
	{"checking", func() homeFixture { // herdr and doctor not back yet
		return homeFixture{snap: homeSnap("ready"), unknown: true}
	}},
	{"ready", func() homeFixture {
		return homeFixture{snap: homeSnap("ready"), doctor: homeDoctor, doctorDone: true}
	}},
	{"herdr_absent", func() homeFixture {
		return homeFixture{snap: homeSnap("ready"), backend: errors.New("herdr not found on PATH"), doctor: homeDoctor, doctorDone: true}
	}},
	{"get_started", func() homeFixture {
		s := &report.Snapshot{Root: "/home/me/code/newproj", Project: "newproj", Config: config.Default(), NoConfig: true, ConfigPath: "/home/me/code/newproj/igris.toml", PlanPath: "/home/me/code/newproj/tasks.md", PlanMissing: true}
		return homeFixture{snap: s, doctor: []checks.Result{{ID: "config", Level: checks.Fail, Message: "igris.toml not found", Next: "igris init"}}, doctorDone: true}
	}},
	{"plan_missing", func() homeFixture {
		s := homeSnap("ready")
		s.Plan, s.Status, s.Issues, s.PlanMissing, s.Recent = nil, nil, nil, true, nil
		return homeFixture{snap: s, doctor: homeDoctor, doctorDone: true}
	}},
	{"plan_invalid", func() homeFixture {
		s := homeSnap("ready")
		s.Plan = plan.Parse("tasks.md", []byte(homeInvalidPlan), plan.Options{})
		s.Issues = report.Issues(s.Plan.Validate(s.Config.Models))
		s.Status, s.Recent = nil, nil
		return homeFixture{snap: s, doctor: homeDoctor, doctorDone: true}
	}},
	{"plan_invalid_no_herdr", func() homeFixture {
		s := homeSnap("ready")
		s.Plan = plan.Parse("tasks.md", []byte(homeInvalidPlan), plan.Options{})
		s.Issues = report.Issues(s.Plan.Validate(s.Config.Models))
		s.Status, s.Recent = nil, nil
		return homeFixture{snap: s, backend: errors.New("herdr not found on PATH"), doctor: homeDoctor, doctorDone: true}
	}},
	{"config_invalid", func() homeFixture {
		s := homeSnap("ready")
		s.ConfigProblems = []string{`igris.toml:3: default_mode: unknown mode "fast"`, `igris.toml:9: notify.ntfy.events: unknown event "foo"`}
		return homeFixture{snap: s, doctor: []checks.Result{{ID: "config", Level: checks.Fail, Message: "igris.toml: 2 problems"}}, doctorDone: true}
	}},
	{"interrupted", func() homeFixture {
		s := homeSnap("in progress")
		s.Run = homeRun("M2-02", homeNow.Add(-2*time.Hour))
		s.Recent = append([]report.HistoryRun{{StartedAt: "2026-10-07T07:12:00Z", Phases: []string{"M2", "M3"}, End: report.EndInterrupted, Done: 1,
			Tasks: []report.TaskRun{{ID: "M2-01", Result: report.ResultDone}, {ID: "M2-02", Result: report.ResultOpen}}}}, s.Recent...)
		return homeFixture{snap: s, doctor: homeDoctor, doctorDone: true}
	}},
	{"stopped", func() homeFixture {
		s := homeSnap("ready")
		s.Run = homeRun("", time.Time{})
		return homeFixture{snap: s, doctor: homeDoctor, doctorDone: true}
	}},
	{"running_elsewhere", func() homeFixture {
		s := homeSnap("in progress")
		s.Run = homeRun("M2-02", homeNow.Add(-14*time.Minute))
		s.Run.Lock, s.Run.LockDetail = report.LockHere, "pid 4242"
		s.Lock = state.LockState{Held: true, Alive: true, Info: state.LockInfo{PID: 4242, Host: "this-host", StartedAt: homeNow.Add(-29 * time.Minute)}, Path: "/src/sinjal/.igris/igris.lock"}
		s.Recent = append([]report.HistoryRun{{StartedAt: "2026-10-07T09:12:00Z", Phases: []string{"M2", "M3"}, End: report.EndRunning, Done: 1,
			Tasks: []report.TaskRun{{ID: "M2-01", Result: report.ResultDone}, {ID: "M2-02", Result: report.ResultOpen}}}}, s.Recent...)
		return homeFixture{snap: s, doctor: homeDoctor, doctorDone: true}
	}},
	{"stale_lock", func() homeFixture {
		s := homeSnap("in progress")
		s.Run = homeRun("M2-02", homeNow.Add(-2*time.Hour))
		s.Run.Lock, s.Run.LockDetail = report.LockStale, "pid 4242: process no longer running"
		s.Lock = state.LockState{Held: true, Stale: true, Info: state.LockInfo{PID: 4242, Host: "this-host", StartedAt: homeNow.Add(-3 * time.Hour)}, Reason: "process no longer running", Path: "/src/sinjal/.igris/igris.lock"}
		return homeFixture{snap: s, doctor: homeDoctor, doctorDone: true}
	}},
	{"remote_lock", func() homeFixture {
		s := homeSnap("ready")
		s.Lock = state.LockState{Held: true, Remote: true, Info: state.LockInfo{PID: 4242, Host: "mbp", StartedAt: time.Date(2026, 10, 6, 21, 10, 0, 0, time.UTC)}, Path: "/src/sinjal/.igris/igris.lock"}
		s.Run = &report.RunInfo{Lock: report.LockElsewhere, LockDetail: "pid 4242 on mbp", Signals: []string{}}
		return homeFixture{snap: s, doctor: homeDoctor, doctorDone: true}
	}},
	{"unreadable", func() homeFixture {
		return homeFixture{snapErr: errors.New("permission denied"), doctor: homeDoctor, doctorDone: true}
	}},
}

// homeAt is a home screen at w×h, painted with th, holding fix's data.
func homeAt(th *theme, w, h int, fix homeFixture) *homeScreen {
	m := newHome(context.Background(), &fakeServices{}, th)
	m.now = func() time.Time { return homeNow }
	m.loc = time.UTC
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.Update(snapshotMsg{fix.snap, fix.snapErr})
	if !fix.unknown {
		m.Update(backendMsg{fix.backend})
	}
	if fix.doctorDone {
		m.Update(doctorMsg{fix.doctor})
	}
	return m
}

func readyHome(w, h int) *homeScreen {
	return homeAt(&theme{}, w, h, homeStates[1].fix())
}

var homeSizes = [][2]int{{120, 40}, {80, 24}, {50, 20}}

func TestHomeGolden(t *testing.T) {
	for _, st := range homeStates {
		for _, size := range homeSizes {
			w, h := size[0], size[1]
			t.Run(fmt.Sprintf("%s_%dx%d", st.name, w, h), func(t *testing.T) {
				m := homeAt(&theme{}, w, h, st.fix())
				view := m.View()
				checkFits(t, view, w, h)
				if n := strings.Count(view, "\n") + 1; n != h {
					t.Errorf("%d lines, want the whole screen (%d)", n, h)
				}
				golden(t, fmt.Sprintf("home_%s_%dx%d", st.name, w, h), view)
			})
		}
	}
}

// Under NO_COLOR the screen is the plain one plus bold, faint and reverse
// video: no color, and nothing told by color alone (SPEC §15.4).
func TestHomeNoColorGolden(t *testing.T) {
	for _, st := range homeStates {
		t.Run(st.name, func(t *testing.T) {
			th := noColorTheme(t)
			view := homeAt(th, 120, 40, st.fix()).View()
			checkFits(t, view, 120, 40)
			golden(t, "home_nocolor_"+st.name, view)
			plain := homeAt(&theme{}, 120, 40, st.fix()).View()
			if strip(view) != plain {
				t.Errorf("NO_COLOR changes the text:\n--- no color, stripped\n%s\n--- plain\n%s", strip(view), plain)
			}
			for _, p := range sgr.FindAllStringSubmatch(view, -1) {
				for _, n := range strings.Split(p[1], ";") {
					if n != "" && n != sgrBold && n != sgrFaint && n != sgrReverse && n != "0" && n != "22" && n != "27" {
						t.Errorf("color under NO_COLOR: %q", p[0])
					}
				}
			}
		})
	}
}

// The wide layout needs 100×13; anything smaller is one column.
func TestHomeLayoutSwitch(t *testing.T) {
	for _, tc := range []struct {
		w, h int
		wide bool
	}{{100, 13, true}, {99, 13, false}, {100, 12, false}, {120, 40, true}, {60, 20, false}} {
		view := readyHome(tc.w, tc.h).View()
		checkFits(t, view, tc.w, tc.h)
		if got := strings.Contains(view, "┬"); got != tc.wide {
			t.Errorf("%dx%d: wide = %v, want %v", tc.w, tc.h, got, tc.wide)
		}
	}
}

// Below 60 columns the progress bars go and the numbers stay.
func TestHomeBarsDropBelow60(t *testing.T) {
	if v := readyHome(60, 20).View(); !strings.Contains(v, "█") || !strings.Contains(v, "3/3 done") {
		t.Errorf("60 columns: want bars and counts:\n%s", v)
	}
	if v := readyHome(59, 20).View(); strings.Contains(v, "█") || !strings.Contains(v, "3/3 done") {
		t.Errorf("59 columns: want counts without bars:\n%s", v)
	}
}

// The bar shows only the actions that apply (SPEC §15.6), the default
// action first.
func TestHomeButtonsOnlyWhenTheyApply(t *testing.T) {
	want := map[string]string{
		"checking":              "Preview Check Doctor History Edit plan Settings Notify ? Quit",
		"ready":                 "Arise… Preview Check Doctor History Edit plan Settings Notify ? Quit",
		"herdr_absent":          "Preview Check Doctor History Edit plan Settings Notify ? Quit",
		"get_started":           "Init Doctor Settings ? Quit",
		"plan_missing":          "Example plan Doctor Settings Notify ? Quit",
		"plan_invalid":          "Check Adapt Doctor Edit plan Settings Notify ? Quit",
		"plan_invalid_no_herdr": "Check Doctor Edit plan Settings Notify ? Quit",
		"config_invalid":        "Settings Preview Check Doctor History Edit plan Notify ? Quit",
		"interrupted":           "Resume… Preview Check Doctor History Edit plan Settings Notify ? Quit",
		"stopped":               "Resume… Preview Check Doctor History Edit plan Settings Notify ? Quit",
		"running_elsewhere":     "Open session Preview Check Doctor History Edit plan Settings Notify ? Quit",
		"stale_lock":            "Resume… Preview Check Doctor History Edit plan Settings Notify ? Quit",
		"remote_lock":           "Arise… Preview Check Doctor History Edit plan Settings Notify ? Quit",
		"unreadable":            "Doctor Settings ? Quit",
	}
	for _, st := range homeStates {
		m := homeAt(&theme{}, 120, 40, st.fix())
		var labels []string
		for _, b := range m.buttons() {
			labels = append(labels, b.label)
		}
		if got := strings.Join(labels, " "); got != want[st.name] {
			t.Errorf("%s: buttons %q, want %q", st.name, got, want[st.name])
		}
	}
}

// Focus goes PHASES → action bar → HEALTH/RECENT and starts on the bar's
// default action; the selected phase is the one with work.
func TestHomeFocusRegions(t *testing.T) {
	m := readyHome(120, 40)
	if m.focus != homeBar || m.selectedPhase() != "M2" {
		t.Fatalf("start: focus %v, phase %q; want the bar and M2", m.focus, m.selectedPhase())
	}
	if v := m.View(); !strings.Contains(v, "[›Arise…‹]") {
		t.Errorf("the default action isn't focused:\n%s", v)
	}
	press := func(k string) { m.Update(keyMsg(k)) }
	press("tab")
	if m.focus != homeLines {
		t.Errorf("after tab: focus %v, want HEALTH/RECENT", m.focus)
	}
	press("tab")
	if m.focus != homePhases {
		t.Errorf("after 2 tabs: focus %v, want PHASES", m.focus)
	}
	press("down")
	if m.selectedPhase() != "M3" {
		t.Errorf("down: phase %q, want M3", m.selectedPhase())
	}
	press("k")
	press("k")
	if m.selectedPhase() != "M1" {
		t.Errorf("k k: phase %q, want M1", m.selectedPhase())
	}
	if v := m.View(); !strings.Contains(v, "› PHASES") {
		t.Errorf("the focused region isn't marked:\n%s", v)
	}
	press("shift+tab")
	if m.focus != homeLines {
		t.Errorf("shift+tab from PHASES: focus %v, want HEALTH/RECENT", m.focus)
	}
	press("tab")
	press("tab")
	if m.focus != homeBar {
		t.Errorf("back on the bar: focus %v", m.focus)
	}
	press("right")
	if v := m.View(); !strings.Contains(v, "[›Preview‹]") {
		t.Errorf("right doesn't move along the bar:\n%s", v)
	}
	press("left")
	press("left")
	if v := m.View(); !strings.Contains(v, "[›Quit‹]") {
		t.Errorf("left doesn't wrap around the bar:\n%s", v)
	}
}

// Every action reaches its screen the same way: enter on its button, its
// key, or a click. Pages that later tasks add report that they are not
// there yet on the status line.
func TestHomeActionsThreeWays(t *testing.T) {
	m := readyHome(120, 40)
	send := func(msg tea.Msg) tea.Msg {
		_, cmd := m.Update(msg)
		return run(cmd)
	}
	// Its key.
	if p, ok := send(keyMsg("c")).(pushMsg); !ok {
		t.Error("c didn't push the check page")
	} else if _, ok := p.s.(*checkScreen); !ok {
		t.Errorf("c pushed %T", p.s)
	}
	if p, ok := send(keyMsg("h")).(pushMsg); !ok {
		t.Error("h didn't push the history page")
	} else if _, ok := p.s.(*historyScreen); !ok {
		t.Errorf("h pushed %T", p.s)
	}
	m.setStatus("Settings: not available yet")
	if v := m.View(); !strings.Contains(v, m.status) || !strings.Contains(v, "09:41") {
		t.Errorf("the status line isn't drawn with its time:\n%s", v)
	}
	// The focused button.
	m.status = ""
	send(keyMsg("right"))
	if _, ok := send(keyMsg("enter")).(pushMsg); !ok {
		t.Errorf("enter on Preview didn't push the preview page (status %q)", m.status)
	}
	// A click.
	m.status = ""
	m.View()
	var clicked tea.Msg
	for _, z := range m.zones.list {
		if z.t.act == actDoctor {
			clicked = send(tea.MouseMsg{X: z.r.x, Y: z.r.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		}
	}
	if p, ok := clicked.(pushMsg); !ok {
		t.Errorf("click on Doctor didn't push its page (status %q)", m.status)
	} else if _, ok := p.s.(*doctorScreen); !ok {
		t.Errorf("click on Doctor pushed %T", p.s)
	}
	// A key whose action doesn't apply does nothing.
	m.status = ""
	send(keyMsg("A"))
	send(keyMsg("I"))
	send(keyMsg("o"))
	if m.status != "" {
		t.Errorf("hidden actions ran: %q", m.status)
	}
	// ? and q go to the app.
	if _, ok := send(keyMsg("?")).(helpMsg); !ok {
		t.Error("? doesn't ask for help")
	}
	if _, ok := send(keyMsg("q")).(quitMsg); !ok {
		t.Error("q doesn't quit")
	}
}

// A click selects a phase; a second click opens it. A click on a HEALTH or
// RECENT line opens its page.
func TestHomeMouse(t *testing.T) {
	m := readyHome(120, 40)
	click := func(want func(target) bool) bool {
		m.View()
		for _, z := range m.zones.list {
			if want(z.t) {
				_, cmd := m.Update(tea.MouseMsg{X: z.r.x, Y: z.r.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
				run(cmd)
				return true
			}
		}
		return false
	}
	row := func(i int) func(target) bool {
		return func(t target) bool { return t.act == actPhaseRow && t.option == i }
	}
	if !click(row(3)) {
		t.Fatal("no phase row to click")
	}
	if m.selectedPhase() != "M3" || m.focus != homePhases || m.status != "" {
		t.Errorf("one click: phase %q, focus %v, status %q; want M3 selected, nothing opened", m.selectedPhase(), m.focus, m.status)
	}
	m.View()
	var open tea.Msg
	for _, z := range m.zones.list {
		if row(3)(z.t) {
			_, cmd := m.Update(tea.MouseMsg{X: z.r.x, Y: z.r.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			open = run(cmd)
		}
	}
	if p, ok := open.(pushMsg); !ok {
		t.Errorf("second click didn't push a page: %T", open)
	} else if ps, ok := p.s.(*phaseScreen); !ok || ps.id != "M3" {
		t.Errorf("second click pushed %T, want M3's page", p.s)
	}
	clickMsg := func(want func(target) bool) tea.Msg {
		m.View()
		for _, z := range m.zones.list {
			if want(z.t) {
				_, cmd := m.Update(tea.MouseMsg{X: z.r.x, Y: z.r.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
				return run(cmd)
			}
		}
		t.Fatal("no line to click")
		return nil
	}
	if p, ok := clickMsg(func(t target) bool { return t.act == actLine && t.option == 1 }).(pushMsg); !ok {
		t.Error("click on the doctor line didn't push a page")
	} else if _, ok := p.s.(*doctorScreen); !ok || m.focus != homeLines {
		t.Errorf("click on the doctor line pushed %T, focus %v", p.s, m.focus)
	}
	if p, ok := clickMsg(func(t target) bool { return t.act == actLine && t.option == 2 }).(pushMsg); !ok {
		t.Error("click on a recent run didn't push a page")
	} else if _, ok := p.s.(*historyScreen); !ok {
		t.Errorf("click on a recent run pushed %T", p.s)
	}
}

// The wheel scrolls the phase list under the pointer, and the selection
// stays in view when moved with the keys.
func TestHomePhasesScroll(t *testing.T) {
	var b strings.Builder
	for i := range 12 {
		fmt.Fprintf(&b, "## P%d — Phase %d\n\n| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n| P%d-01 | **Task** | — | ready | sonnet | agent |\n\n", i, i, i)
	}
	snap := homeSnap("ready")
	snap.Plan = plan.Parse("tasks.md", []byte(b.String()), plan.Options{})
	st, err := report.Status(snap.Plan, "")
	if err != nil {
		t.Fatal(err)
	}
	snap.Status = &st
	m := homeAt(&theme{}, 100, 13, homeFixture{snap: snap, doctor: homeDoctor, doctorDone: true})
	view := m.View()
	checkFits(t, view, 100, 13)
	if strings.Contains(view, "P11 ") {
		t.Fatalf("every phase fits; the test needs more than fit:\n%s", view)
	}
	var phases rect
	for _, z := range m.zones.list {
		if z.t.region == regionPhases {
			phases = z.r
		}
	}
	if phases.h == 0 {
		t.Fatal("no PHASES region")
	}
	for range 3 {
		m.Update(tea.MouseMsg{X: phases.x, Y: phases.y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	}
	if v := m.View(); !strings.Contains(v, "P11 ") {
		t.Errorf("the wheel doesn't scroll the phases:\n%s", v)
	}
	m.Update(keyMsg("tab"))
	m.Update(keyMsg("tab")) // PHASES
	for m.selectedPhase() != "P11" {
		m.Update(keyMsg("down"))
	}
	if v := m.View(); !strings.Contains(v, "P11 ") {
		t.Errorf("the selection left the view:\n%s", v)
	}
	m.Update(keyMsg("pgup"))
	if v := m.View(); !strings.Contains(v, "P0 ") || m.selectedPhase() == "P11" {
		t.Errorf("pgup doesn't move up a screenful (phase %s):\n%s", m.selectedPhase(), v)
	}
}

// Narrow, the bar folds into More…, which lists what was folded.
func TestHomeMoreFolds(t *testing.T) {
	m := readyHome(44, 20)
	v := m.View()
	if !strings.Contains(v, "[More…]") || !strings.Contains(v, "[›Arise…‹]") || !strings.Contains(v, "[Quit]") {
		t.Fatalf("want Arise…, More… and Quit on the bar:\n%s", v)
	}
	if strings.Contains(v, "[Settings]") {
		t.Fatalf("Settings should be folded:\n%s", v)
	}
	_, cmd := m.Update(keyMsg(","))
	if msg, ok := run(cmd).(pushMsg); !ok {
		t.Errorf("a folded action's key still works: got %T", msg.s)
	}
	m.status = ""
	for m.focusedButton() != actMore {
		m.Update(keyMsg("right"))
	}
	m.Update(keyMsg("enter"))
	v = m.View()
	if m.dialog == nil || !strings.Contains(v, "More actions") || !strings.Contains(v, "Settings") {
		t.Fatalf("More… doesn't open the dialog:\n%s", v)
	}
	checkFits(t, v, 44, 20)
	m.Update(keyMsg("esc"))
	if m.dialog != nil {
		t.Error("esc doesn't close the dialog")
	}
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("down"))
	_, cmd = m.Update(keyMsg("enter"))
	if m.dialog != nil || (run(cmd) == nil && m.status == "") {
		t.Errorf("picking from More… doesn't run the action: dialog %v, status %q", m.dialog != nil, m.status)
	}
}

// The More… dialog on the wide layout is drawn over the body.
func TestHomeMoreWide(t *testing.T) {
	m := readyHome(100, 13)
	if v := m.View(); !strings.Contains(v, "[More…]") {
		t.Skipf("the bar fits at 100 columns:\n%s", v)
	}
	for m.focusedButton() != actMore {
		m.Update(keyMsg("right"))
	}
	m.Update(keyMsg("enter"))
	v := m.View()
	checkFits(t, v, 100, 13)
	if !strings.Contains(v, "More actions") {
		t.Errorf("no dialog:\n%s", v)
	}
}

// Home reads the project, then asks herdr and doctor off the loop.
func TestHomeInitAsksForTheSlowChecks(t *testing.T) {
	svc := &fakeServices{backendErr: errors.New("no herdr"), doctor: homeDoctor}
	a := testApp(svc, 120, 40)
	drive(a, a.Init())
	home := a.stack[0].(*homeScreen)
	if home.snap == nil || !home.backendKnown || !home.doctorKnown {
		t.Errorf("after Init: snapshot %v, herdr known %v, doctor known %v", home.snap != nil, home.backendKnown, home.doctorKnown)
	}
	if v := a.View(); !strings.Contains(v, "herdr ⨯") || !strings.Contains(v, "doctor: 1 warning") {
		t.Errorf("the slow checks aren't shown:\n%s", v)
	}
	for _, name := range []string{"Snapshot", "BackendAvailable", "Doctor"} {
		if n := svc.called(name); n != 1 {
			t.Errorf("%s called %d times, want 1", name, n)
		}
	}
}

// The help page lists home's keys.
func TestHomeHelpKeys(t *testing.T) {
	m := readyHome(120, 40)
	var keys []string
	for _, e := range m.helpKeys() {
		keys = append(keys, e.key)
	}
	want := "a v c i h e , n A I o ? q"
	if got := strings.Join(keys, " "); got != want {
		t.Errorf("help keys %q, want %q", got, want)
	}
}

// Until the owner moves along the bar, the focus follows the state's own
// action: the first frame has no project yet, the next one has.
func TestHomeFocusFollowsTheDefaultAction(t *testing.T) {
	m := newHome(context.Background(), &fakeServices{}, &theme{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if v := m.View(); !strings.Contains(v, "[›Doctor‹]") {
		t.Fatalf("while reading, Doctor should be focused:\n%s", v)
	}
	m.Update(snapshotMsg{homeSnap("ready"), nil})
	m.Update(backendMsg{nil})
	if v := m.View(); !strings.Contains(v, "[›Arise…‹]") {
		t.Errorf("once ready, Arise… should be focused:\n%s", v)
	}
	m.Update(keyMsg("right"))
	m.Update(snapshotMsg{homeSnap("ready"), nil})
	if v := m.View(); !strings.Contains(v, "[›Preview‹]") {
		t.Errorf("after moving, a refresh shouldn't move the focus back:\n%s", v)
	}
}

// A user task has no session and so no mode; the NOW card leaves the mode out
// instead of printing a bare "mode" (found at gate V02-G).
func TestHomeRunningUserTaskHasNoMode(t *testing.T) {
	fix := homeStates[0].fix()
	for _, st := range homeStates {
		if st.name == "running_elsewhere" {
			fix = st.fix()
		}
	}
	fix.snap.Run.Mode = ""
	m := homeAt(&theme{}, 120, 40, fix)
	var line string
	for _, l := range strings.Split(m.View(), "\n") {
		if strings.Contains(l, "M2-02") && strings.Contains(l, "14m") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no NOW card line for M2-02 in:\n%s", m.View())
	}
	if strings.Contains(line, "mode") {
		t.Errorf("user task line shows a mode: %q", line)
	}
}
