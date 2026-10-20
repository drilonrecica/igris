package state

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const planUUID = "2b7f3c1e-8d4a-4f6b-9c2e-5a1d0e9f8b7c"

func planRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, DirName), 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPlanRequestRoundTrip(t *testing.T) {
	root := planRoot(t)
	n, err := NewPlanN()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ReadPlanRequest(root, planUUID); ok || err != nil {
		t.Fatalf("before writing: %v %v", ok, err)
	}
	// A decision left from before never answers the new request.
	if err := WritePlanDecision(root, planUUID, PlanDecision{N: n, Behavior: PlanAllow}); err != nil {
		t.Fatal(err)
	}
	r := PlanRequest{N: n, At: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Plan: "1. <b> & \"c\"\n", PID: 42}
	if err := WritePlanRequest(root, planUUID, r); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ReadPlanDecision(root, planUUID); ok {
		t.Error("the old decision survived a new request")
	}
	got, ok, err := ReadPlanRequest(root, planUUID)
	if err != nil || !ok || got != r {
		t.Errorf("read %+v %v %v, want %+v", got, ok, err, r)
	}
	data, _ := os.ReadFile(filepath.Join(PlanApprovalDir(root), planUUID+".json"))
	if !strings.Contains(string(data), `"plan":"1. <b> & \"c\"\n"`) {
		t.Errorf("file %s", data)
	}
	for _, p := range []string{PlanApprovalDir(root), filepath.Join(PlanApprovalDir(root), planUUID+".json")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if want := map[bool]os.FileMode{true: 0o700, false: 0o600}[info.IsDir()]; info.Mode().Perm() != want {
			t.Errorf("%s mode %v", p, info.Mode().Perm())
		}
	}

	d := PlanDecision{N: n, Behavior: PlanDeny, Message: "use Go"}
	if err := WritePlanDecision(root, planUUID, d); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := ReadPlanDecision(root, planUUID); err != nil || !ok || got != d {
		t.Errorf("decision %+v %v %v", got, ok, err)
	}

	// A newer request survives the old hook's cleanup; its own goes.
	other, _ := NewPlanN()
	if err := RemovePlanApproval(root, planUUID, other); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ReadPlanRequest(root, planUUID); !ok {
		t.Error("removed a request that isn't the hook's")
	}
	if err := RemovePlanApproval(root, planUUID, n); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(PlanApprovalDir(root), "*"))
	if len(files) != 0 {
		t.Errorf("left %v", files)
	}
}

// Sessions can write the exchange files: anything igris didn't write is
// refused, never blocks and never follows a link.
func TestPlanExchangeUntrusted(t *testing.T) {
	n := strings.Repeat("a", 32)
	for _, tc := range []struct {
		name, file, content string
	}{
		{"request not JSON", ".json", "{"},
		{"request bad n", ".json", `{"n":"x","plan":"p"}`},
		{"request negative pid", ".json", `{"n":"` + n + `","plan":"p","pid":-1}`},
		{"request not UTF-8", ".json", `{"n":"` + n + `","plan":"\xff"}`},
		{"decision unknown behavior", ".decision.json", `{"n":"` + n + `","behavior":"yolo"}`},
		{"decision long feedback", ".decision.json", `{"n":"` + n + `","behavior":"deny","message":"` + strings.Repeat("x", MaxPlanFeedback+1) + `"}`},
		{"decision too large", ".decision.json", strings.Repeat(" ", maxPlanDecisionSize+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := planRoot(t)
			_ = os.MkdirAll(PlanApprovalDir(root), 0o700)
			if err := os.WriteFile(filepath.Join(PlanApprovalDir(root), planUUID+tc.file), []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			var ok bool
			var err error
			if tc.file == ".json" {
				_, ok, err = ReadPlanRequest(root, planUUID)
			} else {
				_, ok, err = ReadPlanDecision(root, planUUID)
			}
			if ok || err == nil {
				t.Errorf("accepted: %v %v", ok, err)
			}
		})
	}
	t.Run("fifo and symlink", func(t *testing.T) {
		root := planRoot(t)
		_ = os.MkdirAll(PlanApprovalDir(root), 0o700)
		if err := syscall.Mkfifo(filepath.Join(PlanApprovalDir(root), planUUID+".json"), 0o600); err != nil {
			t.Skip(err)
		}
		if _, ok, err := ReadPlanRequest(root, planUUID); ok || err == nil {
			t.Errorf("fifo: %v %v", ok, err)
		}
		target := filepath.Join(root, "elsewhere")
		_ = os.WriteFile(target, []byte(`{"n":"`+n+`","behavior":"allow"}`), 0o600)
		if err := os.Symlink(target, filepath.Join(PlanApprovalDir(root), planUUID+".decision.json")); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := ReadPlanDecision(root, planUUID); ok || err == nil {
			t.Errorf("symlink: %v %v", ok, err)
		}
	})
	t.Run("bad uuid", func(t *testing.T) {
		root := planRoot(t)
		if err := WritePlanRequest(root, "../x", PlanRequest{N: n}); err == nil {
			t.Error("wrote for a bad uuid")
		}
		if _, _, err := ReadPlanDecision(root, "../x"); err == nil {
			t.Error("read for a bad uuid")
		}
	})
	t.Run("no .igris", func(t *testing.T) {
		if err := WritePlanRequest(t.TempDir(), planUUID, PlanRequest{N: n}); err == nil {
			t.Error("created .igris")
		}
	})
}

func TestSavePlan(t *testing.T) {
	root := planRoot(t)
	d, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, text := range []string{"first", "second\n"} {
		p, err := d.SavePlan("M1-03", 2, text)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	if got := strings.Join(paths, " "); got != ".igris/plans/M1-03-a2-1.md .igris/plans/M1-03-a2-2.md" {
		t.Errorf("paths %s", got)
	}
	for i, p := range paths {
		if !ValidPlanFile(p, "M1-03") {
			t.Errorf("%s not valid", p)
		}
		text, err := ReadPlanFile(root, p)
		if want := []string{"first\n", "second\n"}[i]; err != nil || text != want {
			t.Errorf("%s: %q %v", p, text, err)
		}
		info, _ := os.Stat(filepath.Join(root, p))
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", p, info.Mode().Perm())
		}
	}
	if info, _ := os.Stat(PlansDir(root)); info.Mode().Perm() != 0o700 {
		t.Errorf("plans dir mode %v", info.Mode().Perm())
	}
	if _, err := d.SavePlan("../x", 1, "p"); err == nil {
		t.Error("saved for a bad task ID")
	}
	for _, bad := range []string{
		".igris/plans/M1-04-a1-1.md", ".igris/plans/M1-03-a0-1.md", "plans/M1-03-a1-1.md",
		".igris/plans/M1-03-a1-1.md/../../x", "/etc/passwd", ".igris/plans/M1-03-a1-1.txt",
	} {
		if ValidPlanFile(bad, "M1-03") {
			t.Errorf("%s accepted", bad)
		}
	}
}

// state.json's approved plan is shape-checked like the other values igris
// uses from it.
func TestApprovedPlanReadBack(t *testing.T) {
	ok := &Run{Current: &Current{TaskID: "A-1", ApprovedPlan: ".igris/plans/A-1-a1-2.md"}}
	if why := checkReadBack(ok); why != "" {
		t.Errorf("valid: %s", why)
	}
	bad := &Run{Current: &Current{TaskID: "A-1", ApprovedPlan: "../../etc/passwd"}}
	if why := checkReadBack(bad); why == "" {
		t.Error("a path outside .igris/plans accepted")
	}
}
