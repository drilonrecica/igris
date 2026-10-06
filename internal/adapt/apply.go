package adapt

import (
	"fmt"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// backupStamp is the timestamp in a backup's name: sortable, no colons.
const backupStamp = "20060102-150405"

// BackupName is the backup of the plan at planPath taken at now:
// <plan-name>.<timestamp>.bak.md (SPEC §9).
func BackupName(planPath string, now time.Time) string {
	return planName(planPath) + "." + now.UTC().Format(backupStamp) + ".bak.md"
}

// Accept replaces the plan with the reviewed proposal of res after backing
// the original up to .igris/adapt/, and returns the backup's path. The
// backup is written first, so a failed replace leaves the original in
// place and backed up. If the plan changed since the review, nothing is
// replaced.
func Accept(d *state.Dir, res *Result, now time.Time) (string, error) {
	backup, err := d.WriteAdaptFile(BackupName(res.PlanPath, now), res.Original)
	if err != nil {
		return "", fmt.Errorf("back up the plan: %w; the plan is unchanged", err)
	}
	if err := plan.Replace(res.PlanPath, res.Original, res.Proposed); err != nil {
		return backup, err
	}
	return backup, nil
}
