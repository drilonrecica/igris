package main

import (
	"github.com/drilonrecica/igris/internal/report"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// IGRIS_SCREEN_READER wins over igris.toml; unset, the config decides; a
// config that can't be read is off (SPEC §15.4).
func TestScreenReaderSwitch(t *testing.T) {
	cfgOn, cfgOff := func() bool { return true }, func() bool { return false }
	env := func(v string) func(string) string { return func(string) string { return v } }
	for _, tc := range []struct {
		env  string
		cfg  func() bool
		want bool
	}{
		{"", cfgOff, false}, {"", cfgOn, true},
		{"1", cfgOff, true}, {"true", cfgOff, true}, {"yes", cfgOff, true},
		{"0", cfgOn, false}, {"false", cfgOn, false}, {"OFF", cfgOn, false}, {" no ", cfgOn, false},
	} {
		if got := screenReader(env(tc.env), tc.cfg); got != tc.want {
			t.Errorf("IGRIS_SCREEN_READER=%q, config %v: %v, want %v", tc.env, tc.cfg(), got, tc.want)
		}
	}
	// The config is not read when the variable decides.
	if !screenReader(env("1"), func() bool { t.Error("config read"); return false }) {
		t.Error("1 is off")
	}
}

func TestConfigScreenReader(t *testing.T) {
	t.Setenv("IGRIS_SCREEN_READER", "")
	dir := t.TempDir()
	t.Chdir(dir)
	if configScreenReader() {
		t.Error("on without an igris.toml")
	}
	for src, want := range map[string]bool{
		"[tui]\nscreen_reader = true\n":  true,
		"[tui]\nscreen_reader = false\n": false,
		"[tui]\nscreen_reader = ":        false, // unreadable
	} {
		if err := os.WriteFile(filepath.Join(dir, "igris.toml"), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := configScreenReader(); got != want {
			t.Errorf("%q: %v, want %v", src, got, want)
		}
	}
}

// status and doctor with the screen reader on: words for glyphs, one fact
// per line in the Run block, no color (SPEC §15.4).
func TestScreenReaderGoldens(t *testing.T) {
	t.Run("status working", func(t *testing.T) {
		t.Setenv("IGRIS_SCREEN_READER", "1")
		pid := statusProject(t, nil)
		onTerminal(t, 0, true)
		code, out, errb := runCmd("status")
		checkGolden(t, "status_working_screen_reader", code, out, errb, "pid "+pid, "pid <pid>")
	})
	t.Run("status needs you", func(t *testing.T) {
		t.Setenv("IGRIS_SCREEN_READER", "1")
		pid := statusProject(t, needsYou)
		onTerminal(t, 0, true)
		code, out, errb := runCmd("status")
		checkGolden(t, "status_needs_you_screen_reader", code, out, errb, "pid "+pid, "pid <pid>")
	})
	t.Run("doctor", func(t *testing.T) {
		t.Setenv("IGRIS_SCREEN_READER", "1")
		dir := t.TempDir()
		if code, _, errb := initIn(t, dir); code != exitOK {
			t.Fatalf("init: %s", errb)
		}
		if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(strings.Replace(goldenPlan, "| M1-01 |", "| M1-02 |", 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		onTerminal(t, 0, true)
		code, out, errb := doctorIn(t, dir)
		checkGolden(t, "doctor_screen_reader", code, out, errb, dir, "<dir>")
	})
	// The config key does what the variable does, and 0 turns it off again.
	t.Run("config key and variable", func(t *testing.T) {
		statusProject(t, nil)
		if err := os.WriteFile("igris.toml", []byte("[tui]\nscreen_reader = true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		onTerminal(t, 0, true)
		t.Setenv("IGRIS_SCREEN_READER", "")
		if _, out, _ := runCmd("status"); !strings.Contains(out, "running M1-02") || strings.Contains(out, "●") {
			t.Errorf("[tui] screen_reader = true:\n%s", out)
		}
		t.Setenv("IGRIS_SCREEN_READER", "0")
		if _, out, _ := runCmd("status"); !strings.Contains(out, "● M1-02") {
			t.Errorf("IGRIS_SCREEN_READER=0:\n%s", out)
		}
	})
}

// phases and the report's verify marks speak too; with the switch off they
// keep their glyphs.
func TestScreenReaderPhasesAndVerifyMarks(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": goldenPlan})
	onTerminal(t, 0, true)
	t.Setenv("IGRIS_SCREEN_READER", "1")
	if _, out, _ := runCmd("phases"); !strings.Contains(out, "next M1") || strings.ContainsAny(out, "✓●·◌") {
		t.Errorf("phases spoken:\n%s", out)
	}
	t.Setenv("IGRIS_SCREEN_READER", "0")
	if _, out, _ := runCmd("phases"); !strings.Contains(out, "· M1") {
		t.Errorf("phases with the switch off:\n%s", out)
	}
	vs := []report.ReportVerify{{Profile: "fast"}, {Profile: "fast", Passed: true}}
	if got := verifyMarks(vs, true); got != "fast failed passed" {
		t.Errorf("spoken verify marks = %q", got)
	}
	if got := verifyMarks(vs, false); got != "fast ✗ ✓" {
		t.Errorf("verify marks = %q", got)
	}
}
