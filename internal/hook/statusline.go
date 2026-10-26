package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/usage"
)

// StatusLineCommand is the event argument of the status line command: with
// [usage] statusline = true the settings file has igris print the session's
// status line, which is how the subscription quota reaches igris (SPEC
// §6.3, V11-P2).
const StatusLineCommand = "statusline"

// statusPayload is what the status line command reads from Claude Code;
// Claude Code sends more, which is ignored (the cost among it).
type statusPayload struct {
	Model struct {
		ID string `json:"id"`
	} `json:"model"`
	Context struct {
		Used *float64 `json:"used_percentage"`
	} `json:"context_window"`
	RateLimits struct {
		FiveHour   *rateWindow `json:"five_hour"`
		SevenDay   *rateWindow `json:"seven_day"`
		SpendLimit *rateWindow `json:"spend_limit"` // behind a Claude gateway (since 1.8)
	} `json:"rate_limits"`
}

type rateWindow struct {
	Used     *float64 `json:"used_percentage"`
	ResetsAt *float64 `json:"resets_at"`
	// The spend window's amounts, when Claude Code sends them (SPEC §13).
	UsedUSD  *float64 `json:"used_usd"`
	LimitUSD *float64 `json:"limit_usd"`
	Period   string   `json:"period"`
}

// quotaWindow converts w, nil unless it holds a usable percentage and reset
// time.
func (w *rateWindow) quotaWindow() *state.QuotaWindow {
	if w == nil || w.Used == nil || w.ResetsAt == nil || math.IsNaN(*w.ResetsAt) ||
		*w.ResetsAt <= 0 || *w.ResetsAt > math.MaxInt32*4 {
		return nil
	}
	return &state.QuotaWindow{Used: *w.Used, ResetsAt: int64(*w.ResetsAt)}
}

// spendWindow is quotaWindow for the spend window, with its amounts.
func (w *rateWindow) spendWindow() *state.QuotaWindow {
	q := w.quotaWindow()
	if q == nil {
		return nil
	}
	q.UsedUSD, q.LimitUSD, q.Period = w.UsedUSD, w.LimitUSD, w.Period
	return q
}

// StatusLine reads the status line payload on in, prints the one line igris
// shows in task panes to out, and records the payload's rate limits in
// root's .igris/quota.json when they changed (SPEC §6.3). Like Run it gives
// up after Deadline, and only the fields below are used: nothing else of
// the payload is kept. task is the task ID the settings file passed.
func StatusLine(ctx context.Context, root, task string, in io.Reader, out io.Writer, now func() time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- statusLine(root, task, in, out, now) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("hook: %w", ctx.Err())
	}
}

func statusLine(root, task string, in io.Reader, out io.Writer, now func() time.Time) error {
	data, err := io.ReadAll(io.LimitReader(in, maxInput+1))
	if err != nil {
		return fmt.Errorf("hook: read status line: %w", err)
	}
	if len(data) > maxInput {
		return errors.New("hook: status line larger than 64 KiB")
	}
	var p statusPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("hook: decode status line: %w", err)
	}
	q := state.Quota{
		At:       now().UTC(),
		FiveHour: p.RateLimits.FiveHour.quotaWindow(),
		SevenDay: p.RateLimits.SevenDay.quotaWindow(),
		// Recorded for the hold (SPEC §6.9); the line leaves it out.
		SpendLimit: p.RateLimits.SpendLimit.spendWindow(),
	}
	_, err = fmt.Fprintln(out, statusText(task, p, q))
	// The line is printed first: a quota that can't be saved never blanks
	// the status line.
	if werr := state.WriteQuota(root, q); err == nil {
		err = werr
	}
	return err
}

// statusText is `<ID> · <model id> · ctx 5% · 5h 4% · 7d 52%`; parts
// Claude Code didn't send are left out. The ID is shape-checked and the
// model id must look like one, since both end up in a terminal.
func statusText(task string, p statusPayload, q state.Quota) string {
	var parts []string
	if t := strings.TrimSpace(task); plan.ValidID(t) {
		parts = append(parts, t)
	}
	if usage.ValidModel(p.Model.ID) {
		parts = append(parts, p.Model.ID)
	}
	if c := p.Context.Used; c != nil && *c >= 0 && *c <= 100 {
		parts = append(parts, fmt.Sprintf("ctx %s%%", percent(*c)))
	}
	if w := q.FiveHour; w != nil {
		parts = append(parts, "5h "+percent(w.Used)+"%")
	}
	if w := q.SevenDay; w != nil {
		parts = append(parts, "7d "+percent(w.Used)+"%")
	}
	return strings.Join(parts, " · ")
}

// percent is a percentage rounded to a whole number.
func percent(v float64) string { return fmt.Sprintf("%.0f", math.Round(v)) }
