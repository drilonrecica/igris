package project

import (
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
)

// SinceLastRun compares p with the baseline the last run left under root
// (.igris/plan-cells.json, SPEC §5.4), crediting igris with the Status
// writes its run log tells. Read-only; a baseline that can't be read is
// none, and a run log that can't be read credits igris with nothing.
func SinceLastRun(root string, p *plan.Plan) report.SinceLastRun {
	base, ok, why := state.ReadPlanCells(root)
	var events []state.Event
	if ok {
		events, _ = state.PeekEvents(root)
	}
	return report.NewSinceLastRun(base, ok, why, events, p)
}
