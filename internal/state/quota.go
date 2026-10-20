package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/drilonrecica/igris/internal/fsafe"
)

// maxQuotaSize bounds quota.json. igris writes ~150 bytes.
const maxQuotaSize = 4 << 10

// QuotaFile is the name of the file in .igris/ that holds the last
// subscription quota the status line reported (SPEC §6.3, §13).
const QuotaFile = "quota.json"

// QuotaWindow is how much of one subscription rate-limit window is used.
type QuotaWindow struct {
	Used     float64 `json:"used"`      // percent, 0 to 100
	ResetsAt int64   `json:"resets_at"` // Unix seconds
}

// Quota is the subscription quota Claude Code last reported to igris's
// status line (SPEC §6.3). A window Claude Code didn't send is nil.
type Quota struct {
	At       time.Time    `json:"at"`
	FiveHour *QuotaWindow `json:"five_hour,omitempty"`
	SevenDay *QuotaWindow `json:"seven_day,omitempty"`
}

// Empty reports whether q holds no window.
func (q Quota) Empty() bool { return q.FiveHour == nil && q.SevenDay == nil }

// SameWindows reports whether q and o hold the same windows, whenever they
// were recorded.
func (q Quota) SameWindows(o Quota) bool {
	return sameWindow(q.FiveHour, o.FiveHour) && sameWindow(q.SevenDay, o.SevenDay)
}

func sameWindow(a, b *QuotaWindow) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// valid reports whether w has the shape Claude Code sends.
func (w *QuotaWindow) valid() bool {
	return w != nil && !math.IsNaN(w.Used) && w.Used >= 0 && w.Used <= 100 && w.ResetsAt > 0
}

// QuotaPath returns root/.igris/quota.json.
func QuotaPath(root string) string { return filepath.Join(root, DirName, QuotaFile) }

// WriteQuota records q in root's .igris/ unless the file already holds the
// same windows. It never creates .igris/ itself, like WriteAgentState. A
// quota without a window, or a window out of shape, is not recorded.
func WriteQuota(root string, q Quota) error {
	if !q.FiveHour.valid() {
		q.FiveHour = nil
	}
	if !q.SevenDay.valid() {
		q.SevenDay = nil
	}
	if q.Empty() {
		return nil
	}
	if _, err := os.Stat(filepath.Join(root, DirName)); err != nil {
		return fmt.Errorf("write quota: %w", err)
	}
	if cur, ok, _ := ReadQuota(root); ok && cur.SameWindows(q) {
		return nil
	}
	data, err := json.Marshal(q)
	if err != nil {
		return fmt.Errorf("write quota: %w", err)
	}
	return writeFileAtomic(QuotaPath(root), append(data, '\n'))
}

// ReadQuota returns the quota last recorded, and false if there is none or
// the file is not usable. A session can write quota.json, so it is read as
// a regular file of at most 4 KiB and shape-checked: a window out of shape
// is dropped, and a file without a window or a time counts as none (SPEC
// §16).
func ReadQuota(root string) (Quota, bool, error) {
	path := QuotaPath(root)
	data, err := fsafe.ReadFile(path, maxQuotaSize, false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Quota{}, false, nil
		}
		return Quota{}, false, fmt.Errorf("read quota: %w", err)
	}
	var q Quota
	if err := json.Unmarshal(data, &q); err != nil {
		return Quota{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	if !q.FiveHour.valid() {
		q.FiveHour = nil
	}
	if !q.SevenDay.valid() {
		q.SevenDay = nil
	}
	if q.Empty() || q.At.IsZero() {
		return Quota{}, false, nil
	}
	return q, true, nil
}
