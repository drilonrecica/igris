package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// settingsFix is a project whose igris.toml sets a few keys, two secrets
// among them, and has a warning.
func settingsFix() homeFixture {
	f := readyFix()
	cfg := f.snap.Config
	cfg.Run.Verify = "make test"
	cfg.Notify.Ntfy.Token = "env:NTFY_TOKEN"
	cfg.Notify.Discord.WebhookURL = "https://discord.example/api/webhooks/1/s3cret"
	cfg.Columns = map[string]string{"Depends on": "Deps"}
	keys := config.Keys{}
	for _, k := range [][]string{{"run"}, {"run", "verify"}, {"notify"}, {"notify", "ntfy"}, {"notify", "ntfy", "topic"}, {"notify", "ntfy", "token"}, {"notify", "discord", "webhook_url"}, {"columns", "Depends on"}} {
		keys[strings.Join(k, "\x00")] = true
	}
	getenv := func(k string) string {
		if k == "NTFY_TOKEN" {
			return "tk_hidden"
		}
		return ""
	}
	f.snap.Settings = report.NewSettings(cfg, keys, getenv)
	f.snap.ConfigWarnings = []string{`claude.command = "claudex" is ignored: herdr always starts claude from PATH; remove it from igris.toml`}
	return f
}

// settingsInvalidFix is a project whose igris.toml has problems: igris
// shows the defaults meanwhile.
func settingsInvalidFix() homeFixture {
	f := readyFix()
	f.snap.ConfigProblems = []string{`default_mode = "fast" is invalid; use one of: default, accept, auto, plan, yolo`, `run.verify_max_attempts must be at least 1`}
	f.snap.Settings = report.NewSettings(config.Default(), nil, nil)
	return f
}

func TestSettingsGolden(t *testing.T) {
	pages := []struct {
		name string
		fix  func() homeFixture
	}{
		{"settings", settingsFix},
		{"settings_invalid", settingsInvalidFix},
		{"settings_noconfig", func() homeFixture {
			f := homeStates[0].fix()
			f.snap.Settings = report.NewSettings(f.snap.Config, nil, nil)
			return f
		}},
	}
	for _, pg := range pages {
		for _, size := range homeSizes {
			w, h := size[0], size[1]
			t.Run(fmt.Sprintf("%s_%dx%d", pg.name, w, h), func(t *testing.T) {
				s := newSettingsScreen(planPagesHome(w, h, pg.fix()))
				sized(s, w, h)
				v := s.View()
				checkFits(t, v, w, h)
				golden(t, fmt.Sprintf("%s_%dx%d", pg.name, w, h), v)
			})
		}
	}
}

// Secrets never show, defaults say so in words, problems come first.
func TestSettingsPageHidesSecrets(t *testing.T) {
	th := noColorTheme(t)
	s := newSettingsScreen(homeAt(th, 120, 80, settingsFix()))
	sized(s, 120, 80)
	v := textsafe.Clean(s.View()) // the words, without the dimming
	for _, bad := range []string{"tk_hidden", "s3cret", "discord.example"} {
		if strings.Contains(v, bad) {
			t.Errorf("the settings page shows a secret (%q):\n%s", bad, v)
		}
	}
	for _, want := range []string{"token  = set (env:NTFY_TOKEN)", "webhook_url = set (hidden)", `verify              = "make test"`, `plan              = "tasks.md" · default`, `"Depends on" = "Deps"`, "! warn", "[notify.ntfy]"} {
		if !strings.Contains(v, want) {
			t.Errorf("the settings page lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, `verify              = "make test" · default`) {
		t.Error("a key igris.toml sets is marked default")
	}

	inv := newSettingsScreen(homeAt(th, 120, 80, settingsInvalidFix()))
	sized(inv, 120, 80)
	v = inv.View()
	if p, d := strings.Index(v, "PROBLEMS"), strings.Index(v, "plan "); p < 0 || d < p || !strings.Contains(v, "⨯ fail") {
		t.Errorf("problems don't come first:\n%s", v)
	}
}

// fakeExec is an editor that doesn't run.
type fakeExec struct{}

func (fakeExec) Run() error          { return nil }
func (fakeExec) SetStdin(io.Reader)  {}
func (fakeExec) SetStdout(io.Writer) {}
func (fakeExec) SetStderr(io.Writer) {}

// editApp is an app on fix whose Services record the paths opened.
func editApp(t *testing.T, fix homeFixture, editor, vi bool) (*appModel, *homeScreen, *fakeServices, *[]string) {
	t.Helper()
	var opened []string
	a, svc := planApp(t, fix)
	svc.edit = func(path string) (tea.ExecCommand, error) {
		if !editor {
			return nil, ErrNoEditor
		}
		opened = append(opened, path)
		return fakeExec{}, nil
	}
	if vi {
		svc.vi = func(string) tea.ExecCommand { return fakeExec{} }
	}
	return a, a.stack[0].(*homeScreen), svc, &opened
}

// Settings' e pops to home, which opens igris.toml in the editor; `e` on
// home opens the plan.
func TestEditOpensTheFile(t *testing.T) {
	a, home, _, opened := editApp(t, readyFix(), true, false)
	press(a, ",")
	if _, ok := a.stack[len(a.stack)-1].(*settingsScreen); !ok {
		t.Fatalf("`,` doesn't open Settings: %T", a.stack[len(a.stack)-1])
	}
	_, cmd := a.Update(keyMsg("e"))
	drive(a, cmd)
	if len(a.stack) != 1 || len(*opened) != 1 || (*opened)[0] != "/src/sinjal/igris.toml" {
		t.Errorf("Settings' e: %d screens, opened %q", len(a.stack), *opened)
	}
	if cmd := home.activate(actEdit); cmd == nil || (*opened)[1] != "/src/sinjal/tasks.md" {
		t.Errorf("home's e opened %q", *opened)
	}
}

// With no editor set, a dialog shows the path: Close first, vi only when
// it is on PATH, copy with y. Nothing opens on its own.
func TestEditWithoutEditorAsks(t *testing.T) {
	for _, vi := range []bool{false, true} {
		_, home, _, opened := editApp(t, readyFix(), false, vi)
		var out bytes.Buffer
		home.out = &out
		home.activate(actEditConfig)
		d := home.dialog
		if d == nil || d.title != "No editor set" || !strings.Contains(d.detail, "/src/sinjal/igris.toml") {
			t.Fatalf("vi %v: dialog %+v", vi, d)
		}
		if d.options[0].act != actClose || hasOption(d, actEditVi) != vi || !hasOption(d, actCopy) {
			t.Errorf("vi %v: options %+v", vi, d.options)
		}
		if len(*opened) != 0 {
			t.Errorf("vi %v: opened %q without asking", vi, *opened)
		}
		home.Update(keyMsg("y"))
		if home.dialog != nil || !strings.Contains(home.status, "copied /src/sinjal/igris.toml") || out.Len() == 0 {
			t.Errorf("vi %v: y: dialog %v, status %q", vi, home.dialog != nil, home.status)
		}
		if !vi {
			continue
		}
		home.activate(actEditConfig)
		if cmd := home.pick(actEditVi); cmd == nil || home.dialog != nil {
			t.Error("Open with vi doesn't run vi")
		}
		home.activate(actEditConfig)
		home.Update(keyMsg("esc"))
		if home.dialog != nil || home.editing != nil {
			t.Error("esc doesn't close the dialog")
		}
	}
}

// While another igris holds the lock, editing the plan asks first, with
// Cancel the default; igris.toml opens without asking.
func TestEditPlanUnderALiveLockAsks(t *testing.T) {
	fix := readyFix()
	fix.snap.Lock.Held, fix.snap.Lock.Alive = true, true
	_, home, _, opened := editApp(t, fix, true, false)
	home.activate(actEdit)
	d := home.dialog
	if d == nil || d.options[d.selected].act != actClose || !hasOption(d, actEditAnyway) || len(*opened) != 0 {
		t.Fatalf("no question before editing the plan: %+v, opened %q", d, *opened)
	}
	home.Update(keyMsg("enter")) // Cancel
	if home.dialog != nil || len(*opened) != 0 {
		t.Fatalf("Cancel opened the plan: %q", *opened)
	}
	home.activate(actEdit)
	if cmd := home.pick(actEditAnyway); cmd == nil || len(*opened) != 1 {
		t.Errorf("Edit anyway: opened %q", *opened)
	}
	home.activate(actEditConfig)
	if home.dialog != nil || len(*opened) != 2 {
		t.Errorf("igris.toml asked too: %q", *opened)
	}
}

// exitErr is how an editor that failed ends.
type exitErr int

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitErr) ExitCode() int { return int(e) }

// When the editor closes, home reads the project again and says how the
// file is now; a non-zero exit is reported.
func TestEditedRevalidates(t *testing.T) {
	cfgPath, planPath := "/src/sinjal/igris.toml", "/src/sinjal/tasks.md"
	tests := []struct {
		name string
		fix  homeFixture
		t    editTarget
		err  error
		want string
	}{
		{"config ok", readyFix(), editTarget{path: cfgPath}, nil, "✓ igris.toml valid"},
		{"config bad", settingsInvalidFix(), editTarget{path: cfgPath}, nil, "⨯ igris.toml: 2 problems — Settings"},
		{"config exit", settingsInvalidFix(), editTarget{path: cfgPath}, exitErr(3), "editor exited with status 3; file reloaded · ⨯ igris.toml: 2 problems — Settings"},
		{"plan ok", readyFix(), editTarget{path: planPath, plan: true}, nil, "✓ tasks.md valid"},
		{"plan bad", invalidFix(), editTarget{path: planPath, plan: true}, nil, "⨯ tasks.md: "},
		{"not run", readyFix(), editTarget{path: planPath, plan: true}, errors.New("exec: \"nvim\": not found\x1b[31m"), "the editor didn't run: exec: \"nvim\": not found; check $VISUAL / $EDITOR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, home, svc, _ := editApp(t, tt.fix, true, false)
			reads, doctors := svc.called("Snapshot"), svc.called("Doctor")
			drive(a, func() tea.Msg { return ownedMsg{home, editedMsg{tt.t, tt.err}} })
			if svc.called("Snapshot") != reads+1 || svc.called("Doctor") != doctors+1 {
				t.Errorf("not read again: %d reads, %d doctors", svc.called("Snapshot")-reads, svc.called("Doctor")-doctors)
			}
			if !strings.HasPrefix(home.status, tt.want) || home.afterEdit != nil {
				t.Errorf("status %q, want %q", home.status, tt.want)
			}
			if strings.ContainsRune(home.status, 0x1b) {
				t.Error("the error's escape sequence reached the status line")
			}
		})
	}
}
