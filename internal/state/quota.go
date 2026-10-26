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

// The quota windows' names, as Claude Code's status line and the run log
// name them (SPEC §6.9).
const (
	WindowFiveHour   = "five_hour"
	WindowSevenDay   = "seven_day"
	WindowSpendLimit = "spend_limit"
)

// QuotaWindow is how much of one subscription rate-limit window is used.
type QuotaWindow struct {
	Used     float64 `json:"used"`      // percent, 0 to 100 (spend_limit: above 100 once exceeded)
	ResetsAt int64   `json:"resets_at"` // Unix seconds
	// UsedUSD, LimitUSD and Period are what Claude Code sends with
	// spend_limit when it does (SPEC §13, since 1.8); nil or "" otherwise.
	UsedUSD  *float64 `json:"used_usd,omitempty"`
	LimitUSD *float64 `json:"limit_usd,omitempty"`
	Period   string   `json:"period,omitempty"`
}

// Quota is the subscription quota Claude Code last reported to igris's
// status line (SPEC §6.3). A window Claude Code didn't send is nil.
type Quota struct {
	At       time.Time    `json:"at"`
	FiveHour *QuotaWindow `json:"five_hour,omitempty"`
	SevenDay *QuotaWindow `json:"seven_day,omitempty"`
	// SpendLimit is the spend window behind a Claude gateway (since 1.8).
	SpendLimit *QuotaWindow `json:"spend_limit,omitempty"`
}

// Empty reports whether q holds no window.
func (q Quota) Empty() bool { return q.FiveHour == nil && q.SevenDay == nil && q.SpendLimit == nil }

// SameWindows reports whether q and o hold the same windows, whenever they
// were recorded.
func (q Quota) SameWindows(o Quota) bool {
	return sameWindow(q.FiveHour, o.FiveHour) && sameWindow(q.SevenDay, o.SevenDay) && sameWindow(q.SpendLimit, o.SpendLimit)
}

// NamedWindow is one of q's windows with its name.
type NamedWindow struct {
	Name string
	QuotaWindow
}

// Windows lists q's windows with their names, five_hour, seven_day and
// spend_limit in that order, leaving out the ones not recorded.
func (q Quota) Windows() []NamedWindow {
	var out []NamedWindow
	for _, w := range []struct {
		name string
		w    *QuotaWindow
	}{{WindowFiveHour, q.FiveHour}, {WindowSevenDay, q.SevenDay}, {WindowSpendLimit, q.SpendLimit}} {
		if w.w != nil {
			out = append(out, NamedWindow{Name: w.name, QuotaWindow: *w.w})
		}
	}
	return out
}

func sameWindow(a, b *QuotaWindow) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Used == b.Used && a.ResetsAt == b.ResetsAt && sameAmount(a.UsedUSD, b.UsedUSD) &&
		sameAmount(a.LimitUSD, b.LimitUSD) && a.Period == b.Period
}

func sameAmount(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// maxPeriod bounds the period text, which a session could write.
const maxPeriod = 32

// valid reports whether w has the shape Claude Code sends; spend says
// whether w is the spend window, whose use passes 100% once exceeded.
func (w *QuotaWindow) valid(spend bool) bool {
	if w == nil || math.IsNaN(w.Used) || w.Used < 0 || w.ResetsAt <= 0 {
		return false
	}
	if !spend {
		return w.Used <= 100
	}
	if w.Used > 1e6 || badAmount(w.UsedUSD) || badAmount(w.LimitUSD) || len(w.Period) > maxPeriod {
		return false
	}
	return true
}

func badAmount(a *float64) bool {
	return a != nil && (math.IsNaN(*a) || math.IsInf(*a, 0) || *a < 0)
}

// check drops q's windows that are out of shape.
func (q *Quota) check() {
	if !q.FiveHour.valid(false) {
		q.FiveHour = nil
	}
	if !q.SevenDay.valid(false) {
		q.SevenDay = nil
	}
	if !q.SpendLimit.valid(true) {
		q.SpendLimit = nil
	}
}

// QuotaPath returns root/.igris/quota.json.
func QuotaPath(root string) string { return filepath.Join(root, DirName, QuotaFile) }

// WriteQuota records q in root's .igris/ unless the file already holds the
// same windows. It never creates .igris/ itself, like WriteAgentState. A
// quota without a window, or a window out of shape, is not recorded.
func WriteQuota(root string, q Quota) error {
	q.check()
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
	q.check()
	if q.Empty() || q.At.IsZero() {
		return Quota{}, false, nil
	}
	return q, true, nil
}
