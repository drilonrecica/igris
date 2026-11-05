package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/fsafe"
	"github.com/drilonrecica/igris/internal/plan"
)

// The plan baseline (SPEC §5.4, §13): .igris/plan-cells.json, the cells of
// every task row as the last run left them, written when a run starts and
// when it ends. `igris status --since-last-run` compares the plan with it.
// A session can write the file, so it is read bounded and shape-checked;
// one that doesn't pass is no baseline at all.

// PlanCellsFile is the baseline's name under .igris/.
const PlanCellsFile = "plan-cells.json"

// PlanCellsVersion is the "v" igris writes and reads.
const PlanCellsVersion = 1

const (
	// maxPlanCellsSize bounds the file igris reads.
	maxPlanCellsSize = 16 << 20
	// maxPlanCellsTasks bounds how many rows it holds.
	maxPlanCellsTasks = 20000
	// maxPlanCell bounds one cell's text.
	maxPlanCell = 4 << 10
)

// PlanCells is the baseline: the run that wrote it, when, and each task's
// cells by ID.
type PlanCells struct {
	V     int                 `json:"v"`
	Run   string              `json:"run"`
	At    time.Time           `json:"at"`
	Tasks map[string]PlanCell `json:"tasks"`
}

// PlanCell is what igris reads from a task row (planwatch's facts): the
// Status keyword, the Model, Mode, Owner, Deps and Verify cells, and the
// sha256 of the Task text, never the text itself.
type PlanCell struct {
	Status     string `json:"status"`
	Rank       string `json:"rank"`
	Mode       string `json:"mode"`
	Owner      string `json:"owner"`
	Deps       string `json:"deps"`
	Verify     string `json:"verify"`
	TextSHA256 string `json:"text_sha256"`
}

// CellsOf returns the cells of every task of p by ID.
func CellsOf(p *plan.Plan) map[string]PlanCell {
	out := make(map[string]PlanCell, len(p.Tasks))
	for _, t := range p.Tasks {
		out[t.ID] = CellOf(t)
	}
	return out
}

// CellOf returns t's cells.
func CellOf(t *plan.Task) PlanCell {
	sum := sha256.Sum256([]byte(t.Text))
	return PlanCell{
		Status: t.Status.String(), Rank: t.Rank, Mode: t.Mode, Owner: string(t.Owner),
		Deps: t.DepsText, Verify: t.Verify, TextSHA256: hex.EncodeToString(sum[:]),
	}
}

// PlanCellsPath returns root/.igris/plan-cells.json.
func PlanCellsPath(root string) string { return filepath.Join(root, DirName, PlanCellsFile) }

// WritePlanCells replaces the baseline with c, atomically.
func (d *Dir) WritePlanCells(c PlanCells) error {
	c.V, c.At = PlanCellsVersion, c.At.UTC()
	if err := checkPlanCells(c); err != nil {
		return fmt.Errorf("write plan baseline: %w", err)
	}
	data, err := marshalPlain(c)
	if err != nil {
		return fmt.Errorf("write plan baseline: %w", err)
	}
	return writeFileAtomic(PlanCellsPath(d.root), data)
}

// ReadPlanCells returns the baseline under root, and false when there is
// none: missing, unreadable (a FIFO, a symlink, too large) or not of
// igris's shape. why says what was wrong with a file that is there.
func ReadPlanCells(root string) (c PlanCells, ok bool, why string) {
	data, err := fsafe.ReadFile(PlanCellsPath(root), maxPlanCellsSize, false)
	if errors.Is(err, fs.ErrNotExist) {
		return PlanCells{}, false, ""
	}
	if err != nil {
		return PlanCells{}, false, err.Error()
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return PlanCells{}, false, fmt.Sprintf("%s: %v", PlanCellsFile, err)
	}
	if err := checkPlanCells(c); err != nil {
		return PlanCells{}, false, fmt.Sprintf("%s: %v", PlanCellsFile, err)
	}
	return c, true, ""
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

func checkPlanCells(c PlanCells) error {
	switch {
	case c.V != PlanCellsVersion:
		return fmt.Errorf("version %d, not %d", c.V, PlanCellsVersion)
	case !ValidRunID(c.Run):
		return errors.New("the run ID is not igris's")
	case c.At.IsZero():
		return errors.New("no time")
	case c.Tasks == nil:
		return errors.New("no tasks")
	case len(c.Tasks) > maxPlanCellsTasks:
		return fmt.Errorf("over %d tasks", maxPlanCellsTasks)
	}
	for id, t := range c.Tasks {
		if !plan.ValidID(id) {
			return fmt.Errorf("task ID %q is not a task ID", id)
		}
		if s, suffix, ok := plan.ParseStatus(t.Status); !ok || suffix != "" || s.String() != t.Status {
			return fmt.Errorf("task %s: unknown status %q", id, t.Status)
		}
		if !sha256Hex.MatchString(t.TextSHA256) {
			return fmt.Errorf("task %s: text_sha256 is not a sha256", id)
		}
		for _, v := range []string{t.Rank, t.Mode, t.Owner, t.Deps, t.Verify} {
			if len(v) > maxPlanCell || !utf8.ValidString(v) {
				return fmt.Errorf("task %s: a cell is not text of at most %d bytes", id, maxPlanCell)
			}
		}
	}
	return nil
}
