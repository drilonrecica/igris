package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/drilonrecica/igris/internal/plan"
)

func TestFindRoot(t *testing.T) {
	tests := []struct {
		name   string
		marker string // created in the root; "" = none
	}{
		{"igris.toml", ConfigFile},
		{"state dir", DirName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, tt.marker)
			if tt.marker == DirName {
				must(t, os.Mkdir(path, 0o700))
			} else {
				must(t, os.WriteFile(path, nil, 0o600))
			}
			sub := filepath.Join(root, "a", "b")
			must(t, os.MkdirAll(sub, 0o750))
			got, err := FindRoot(sub)
			if err != nil || got != root {
				t.Errorf("FindRoot = %q, %v; want %q", got, err, root)
			}
		})
	}
	t.Run("nearest wins", func(t *testing.T) {
		root := t.TempDir()
		inner := filepath.Join(root, "inner")
		must(t, os.WriteFile(filepath.Join(root, ConfigFile), nil, 0o600))
		must(t, os.MkdirAll(filepath.Join(inner, DirName), 0o700))
		if got, err := FindRoot(inner); err != nil || got != inner {
			t.Errorf("FindRoot = %q, %v; want %q", got, err, inner)
		}
	})
	t.Run("none", func(t *testing.T) {
		_, err := FindRoot(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "igris init") {
			t.Errorf("err = %v, want hint to run igris init", err)
		}
	})
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSignalRoundTrip(t *testing.T) {
	d := testDir(t)
	must(t, d.WriteSignal(Signal{ID: "M0-01", Action: ActionDone, Note: "ok"}))

	got, err := d.ReadSignal("M0-01")
	if err != nil || got == nil {
		t.Fatalf("ReadSignal = %v, %v", got, err)
	}
	if got.ID != "M0-01" || got.Action != ActionDone || got.Note != "ok" || !got.At.Equal(t0) {
		t.Errorf("signal = %+v", got)
	}
	if p := perm(t, d.signalPath("M0-01")); p != 0o600 {
		t.Errorf("mode = %v, want 0600", p)
	}

	// A later signal replaces the earlier one.
	must(t, d.WriteSignal(Signal{ID: "M0-01", Action: ActionSkip, Note: "no"}))
	if got, _ := d.ReadSignal("M0-01"); got.Action != ActionSkip || got.Note != "no" {
		t.Errorf("after overwrite = %+v", got)
	}

	must(t, d.RemoveSignal("M0-01"))
	must(t, d.RemoveSignal("M0-01")) // already gone is fine
	if got, err := d.ReadSignal("M0-01"); got != nil || err != nil {
		t.Errorf("after remove = %v, %v; want nil, nil", got, err)
	}
}

// A reset signal has its own slot, signals/<ID>.reset.json (SPEC §6.2):
// it always records whether it was forced, and a done or skip for the same
// task neither replaces nor removes it, nor the other way round. Done and
// skip signals keep their old shape.
func TestResetSignal(t *testing.T) {
	d := testDir(t)
	must(t, d.WriteSignal(Signal{ID: "M0-01", Action: ActionDone, Note: "ok"}))
	data, err := os.ReadFile(d.signalPath("M0-01"))
	must(t, err)
	if strings.Contains(string(data), "force") {
		t.Errorf("done signal = %s, want no force field", data)
	}
	for _, force := range []bool{false, true} {
		must(t, d.WriteSignal(Signal{ID: "M0-01", Action: ActionReset, Force: force}))
		data, err := os.ReadFile(filepath.Join(d.SignalsDir(), "M0-01.reset.json"))
		must(t, err)
		if want := fmt.Sprintf(`"force":%v`, force); !strings.Contains(string(data), want) || !strings.Contains(string(data), `"action":"reset"`) {
			t.Errorf("reset signal = %s, want %s", data, want)
		}
		got, err := d.ReadReset("M0-01")
		if err != nil || got == nil || got.Action != ActionReset || got.Force != force {
			t.Errorf("ReadReset = %+v, %v", got, err)
		}
		if got, err := d.ReadSignal("M0-01"); err != nil || got == nil || got.Action != ActionDone {
			t.Errorf("ReadSignal after the reset = %+v, %v; want the done signal kept", got, err)
		}
	}
	// The session's done after the owner's reset leaves the reset pending.
	must(t, d.WriteSignal(Signal{ID: "M0-01", Action: ActionDone, Note: "again"}))
	must(t, d.RemoveSignal("M0-01"))
	if got, err := d.ReadReset("M0-01"); err != nil || got == nil {
		t.Errorf("ReadReset after done + RemoveSignal = %+v, %v; want the reset kept", got, err)
	}
	sigs, bad, err := d.ListSignals()
	if err != nil || len(bad) != 0 || len(sigs) != 1 || sigs[0].Action != ActionReset || sigs[0].ID != "M0-01" {
		t.Errorf("ListSignals = %+v, %v, %v; want the reset only", sigs, bad, err)
	}
	must(t, d.RemoveReset("M0-01"))
	must(t, d.RemoveReset("M0-01")) // already gone is fine
	if got, err := d.ReadReset("M0-01"); got != nil || err != nil {
		t.Errorf("after RemoveReset = %+v, %v", got, err)
	}
	if err := d.WriteSignal(Signal{ID: "M0-01", Action: "undo"}); err == nil {
		t.Error("an unknown action was written")
	}
}

// Each slot holds its own actions only: a reset written into the done
// slot (a session forging one) or a done in the reset slot is unreadable,
// never applied. A task whose ID itself ends in ".reset" keeps its done
// slot.
func TestSignalSlots(t *testing.T) {
	d := testDir(t)
	dir := d.SignalsDir()
	must(t, os.WriteFile(filepath.Join(dir, "A-1.json"), []byte(`{"id":"A-1","action":"reset","force":true}`), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "A-2.reset.json"), []byte(`{"id":"A-2","action":"done"}`), 0o600))
	must(t, d.WriteSignal(Signal{ID: "X.reset", Action: ActionDone}))
	must(t, d.WriteSignal(Signal{ID: "A-3", Action: ActionReset}))

	sigs, bad, err := d.ListSignals()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range sigs {
		got = append(got, s.ID+":"+s.Action)
	}
	if strings.Join(got, " ") != "A-3:reset X.reset:done" {
		t.Errorf("sigs = %v, want A-3:reset X.reset:done", got)
	}
	if len(bad) != 2 {
		t.Errorf("bad = %v, want 2 (reset in the done slot, done in the reset slot)", bad)
	}
	if s, err := d.ReadSignal("A-1"); err == nil || s != nil {
		t.Errorf("ReadSignal(A-1) = %+v, %v; want an error", s, err)
	}
}

func TestSignalIDValidation(t *testing.T) {
	d := testDir(t)
	for _, id := range []string{"", "..", "../x", "a/b", ".hidden", "a b"} {
		if err := d.WriteSignal(Signal{ID: id, Action: ActionDone}); err == nil {
			t.Errorf("WriteSignal(%q) succeeded, want error", id)
		}
		if _, err := d.ReadSignal(id); err == nil {
			t.Errorf("ReadSignal(%q) succeeded, want error", id)
		}
		if err := d.RemoveSignal(id); err == nil {
			t.Errorf("RemoveSignal(%q) succeeded, want error", id)
		}
	}
	if err := d.WriteSignal(Signal{ID: "M0-01", Action: "explode"}); err == nil {
		t.Error("unknown action accepted")
	}
}

func TestListSignals(t *testing.T) {
	d := testDir(t)
	must(t, d.WriteSignal(Signal{ID: "M1-02", Action: ActionDone}))
	must(t, d.WriteSignal(Signal{ID: "M0-01", Action: ActionSkip, Note: "r"}))
	// Leftover temp file, corrupt file, foreign file and a mismatched file.
	must(t, os.WriteFile(filepath.Join(d.SignalsDir(), ".M0-03.json.tmp-123"), []byte("{"), 0o600))
	must(t, os.WriteFile(filepath.Join(d.SignalsDir(), "M0-04.json"), []byte("{not json"), 0o600))
	must(t, os.WriteFile(filepath.Join(d.SignalsDir(), "M0-05.json"), []byte(`{"id":"OTHER","action":"done"}`), 0o600))
	must(t, os.WriteFile(filepath.Join(d.SignalsDir(), "notes.txt"), nil, 0o600))

	sigs, bad, err := d.ListSignals()
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 2 || sigs[0].ID != "M0-01" || sigs[1].ID != "M1-02" {
		t.Errorf("sigs = %+v, want M0-01 then M1-02", sigs)
	}
	if len(bad) != 2 {
		t.Errorf("bad = %v, want 2 errors (corrupt, mismatched)", bad)
	}
}

// Sessions can write into signals/: only regular files of a sane size are
// read, and the note can't carry escape sequences into the TUI.
func TestSignalsFromSessionsAreGuarded(t *testing.T) {
	d := testDir(t)
	must(t, d.WriteSignal(Signal{ID: "M0-01", Action: ActionDone, Note: "fine\x1b[2J\x1b[H\nspoof"}))
	target := filepath.Join(d.Path(), "target.json")
	must(t, os.WriteFile(target, []byte(`{"id":"M0-02","action":"done"}`), 0o600))
	must(t, os.Symlink(target, filepath.Join(d.SignalsDir(), "M0-02.json")))
	must(t, os.WriteFile(filepath.Join(d.SignalsDir(), "M0-03.json"), make([]byte, maxSignalSize+1), 0o600))
	must(t, syscall.Mkfifo(filepath.Join(d.SignalsDir(), "M0-04.json"), 0o600))

	sigs, bad, err := d.ListSignals()
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 1 || sigs[0].ID != "M0-01" || sigs[0].Note != "fine spoof" {
		t.Errorf("sigs = %+v, want only M0-01 with a cleaned note", sigs)
	}
	if len(bad) != 3 {
		t.Errorf("bad = %v, want 3 errors (symlink, oversized, fifo)", bad)
	}
	for _, id := range []string{"M0-02", "M0-03", "M0-04"} {
		if s, err := d.ReadSignal(id); err == nil || s != nil {
			t.Errorf("ReadSignal(%s) = %+v, %v; want an error", id, s, err)
		}
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name    string
		sig     Signal
		current string
		owner   plan.Owner
		want    Disposition
	}{
		{"done current agent", Signal{ID: "A", Action: ActionDone}, "A", plan.OwnerAgent, Apply},
		{"done current agent+user", Signal{ID: "A", Action: ActionDone}, "A", plan.OwnerAgentUser, Apply},
		{"done current user", Signal{ID: "A", Action: ActionDone}, "A", plan.OwnerUser, Apply},
		{"skip current agent needs owner", Signal{ID: "A", Action: ActionSkip}, "A", plan.OwnerAgent, ConfirmSkip},
		{"skip current agent+user needs owner", Signal{ID: "A", Action: ActionSkip}, "A", plan.OwnerAgentUser, ConfirmSkip},
		{"skip current user applies", Signal{ID: "A", Action: ActionSkip}, "A", plan.OwnerUser, Apply},
		{"stray done", Signal{ID: "B", Action: ActionDone}, "A", plan.OwnerAgent, Stray},
		{"stray skip", Signal{ID: "B", Action: ActionSkip}, "A", plan.OwnerUser, Stray},
		{"no current task", Signal{ID: "B", Action: ActionDone}, "", plan.OwnerAgent, Stray},
		{"reset current", Signal{ID: "A", Action: ActionReset}, "A", plan.OwnerAgent, Reset},
		{"reset other task", Signal{ID: "B", Action: ActionReset, Force: true}, "A", plan.OwnerUser, Reset},
		{"reset between tasks", Signal{ID: "B", Action: ActionReset}, "", plan.OwnerAgent, Reset},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.sig, tt.current, tt.owner); got != tt.want {
				t.Errorf("Classify = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWriteRules(t *testing.T) {
	d := testDir(t)
	path, err := d.WriteRules("M0-01", "be good\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(d.PromptsDir(), "M0-01.rules.md"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if got, _ := os.ReadFile(path); string(got) != "be good\n" { //nolint:gosec // test temp dir
		t.Errorf("content = %q", got)
	}
	if p := perm(t, path); p != 0o600 {
		t.Errorf("mode = %v, want 0600", p)
	}
	if _, err := d.WriteRules("../x", "x"); err == nil {
		t.Error("path-like ID accepted")
	}
}
