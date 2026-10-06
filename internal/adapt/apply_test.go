package adapt

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
)

func TestAccept(t *testing.T) {
	h := newHarness(t, oldPlan)
	if err := os.Chmod(h.plan, 0o640); err != nil { //nolint:gosec // test file: a mode Accept must keep
		t.Fatal(err)
	}
	res := &Result{PlanPath: h.plan, Original: []byte(oldPlan), Proposed: []byte(goodProposal)}

	backup, err := Accept(h.dir, res, time.Date(2026, 10, 6, 14, 3, 9, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(h.root, ".igris", "adapt", "plan.20261006-140309.bak.md"); backup != want {
		t.Errorf("backup = %s, want %s", backup, want)
	}
	if data, _ := os.ReadFile(backup); string(data) != oldPlan { //nolint:gosec // test temp dir
		t.Errorf("backup = %q, want the original", data)
	}
	if data, _ := os.ReadFile(h.plan); string(data) != goodProposal { //nolint:gosec // test temp dir
		t.Errorf("plan = %q, want the proposal", data)
	}
	if fi, _ := os.Stat(h.plan); fi.Mode().Perm() != 0o640 {
		t.Errorf("plan mode = %v, want 0640 kept", fi.Mode().Perm())
	}
}

func TestAcceptPlanChangedSinceReview(t *testing.T) {
	h := newHarness(t, oldPlan)
	res := &Result{PlanPath: h.plan, Original: []byte("what the owner reviewed\n"), Proposed: []byte(goodProposal)}

	if _, err := Accept(h.dir, res, t0); !errors.Is(err, plan.ErrConcurrentEdit) {
		t.Fatalf("err = %v, want ErrConcurrentEdit", err)
	}
	if data, _ := os.ReadFile(h.plan); string(data) != oldPlan { //nolint:gosec // test temp dir
		t.Error("the plan was replaced although it changed since the review")
	}
}

func TestBackupName(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("CEST", 2*3600))
	if got := BackupName("/p/imp-docs/tasks.md", at); got != "tasks.20260102-010405.bak.md" {
		t.Errorf("BackupName = %s", got)
	}
}
