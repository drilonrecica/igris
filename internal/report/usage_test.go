package report

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/usage"
)

func usageLine(run, task, rank string, main, sub *usage.Counts, models, subModels []string, audit string) state.Event {
	return state.Event{V: 1, At: time.Date(2026, 10, 1, 9, 5, 0, 0, time.UTC), Type: state.EventUsage, Run: run, Task: task,
		Rank: rank, Model: rank, Usage: main, Sub: sub, Models: models, SubModels: subModels, Audit: audit}
}

func runStart(run string, at time.Time) state.Event {
	return state.Event{V: 1, At: at, Type: state.EventRunStarted, Run: run, Detail: "phase A"}
}

func TestNewUsage(t *testing.T) {
	day1 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	day2 := day1.Add(48 * time.Hour)
	events := []state.Event{
		runStart("r1", day1),
		usageLine("r1", "A-1", "sonnet", &usage.Counts{Input: 10, Output: 100, CacheRead: 1000, CacheWrite: 50, Messages: 4},
			&usage.Counts{Input: 1, Output: 5, CacheRead: 20, CacheWrite: 2, Messages: 1},
			[]string{"claude-sonnet-5-5"}, []string{"claude-haiku-5-5"}, state.AuditOK),
		usageLine("r1", "A-2", "opus", nil, nil, nil, nil, state.AuditUnavailable),
		runStart("r2", day2),
		usageLine("r2", "A-1", "sonnet", &usage.Counts{Input: 5, Output: 50, CacheRead: 500, CacheWrite: 5, Messages: 2}, nil,
			[]string{"claude-opus-5-5"}, nil, state.AuditMismatch),
		{V: 1, At: day2, Type: state.EventRankMismatch, Run: "r2", Task: "A-1", Rank: "sonnet", Model: "sonnet", Models: []string{"claude-opus-5-5"}},
		usageLine("r2", "A-3", "", &usage.Counts{Input: 1, Output: 1, Messages: 1}, nil, nil, nil, state.AuditUnchecked),
	}
	in := HistoryInput{Events: events}

	// The newest run alone.
	u, err := NewUsage(in, "demo", "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Runs) != 1 || u.Runs[0].Run != "r2" {
		t.Fatalf("runs = %+v", u.Runs)
	}
	if got := u.Totals.In; got != 5+500+5+1 {
		t.Errorf("totals in = %d", got)
	}
	if u.Totals.Attempts != 2 || u.Unavailable != 0 {
		t.Errorf("attempts = %d, unavailable = %d", u.Totals.Attempts, u.Unavailable)
	}
	if got := u.Ranks[len(u.Ranks)-1].Rank; got != unknownModel {
		t.Errorf("a task with no rank is %q, want %q", got, unknownModel)
	}
	if want := (UsageAudit{Unchecked: 1, Mismatch: 1, Mismatches: []UsageMismatch{
		{Run: "r2", Task: "A-1", Rank: "sonnet", Model: "sonnet", Models: []string{"claude-opus-5-5"}},
	}}); !reflect.DeepEqual(u.Audit, want) {
		t.Errorf("audit = %+v, want %+v", u.Audit, want)
	}

	// Both runs: tasks merge, the unreadable attempt is counted apart.
	u, err = NewUsage(in, "demo", "", day1.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Runs) != 2 || u.Totals.Attempts != 3 || u.Unavailable != 1 {
		t.Fatalf("runs %d, attempts %d, unavailable %d", len(u.Runs), u.Totals.Attempts, u.Unavailable)
	}
	a1 := u.Tasks[0]
	if a1.ID != "A-1" || a1.Attempts != 2 || a1.Audit != state.AuditMismatch ||
		!reflect.DeepEqual(a1.Models, []string{"claude-sonnet-5-5", "claude-opus-5-5"}) ||
		!reflect.DeepEqual(a1.SubModels, []string{"claude-haiku-5-5"}) {
		t.Errorf("A-1 = %+v", a1)
	}
	// Subagents are in the task's tokens, and apart in Sub.
	if a1.In != 10+1000+50+1+20+2+5+500+5 || a1.Output != 100+5+50 || a1.Sub.In != 23 || a1.Sub.Output != 5 {
		t.Errorf("A-1 tokens = %+v sub %+v", a1.Tokens, a1.Sub)
	}
	if a2 := u.Tasks[1]; a2.ID != "A-2" || a2.Attempts != 0 || a2.Unavailable != 1 || a2.Audit != state.AuditUnavailable {
		t.Errorf("A-2 = %+v", a2)
	}
	// By model: the main thread's ids and the subagents' each have a row.
	models := map[string]UsageRow{}
	for _, m := range u.Models {
		models[m.Model] = m.UsageRow
	}
	if m := models["claude-haiku-5-5"]; m.In != 23 || m.Output != 5 || m.Sub.In != 23 || m.Attempts != 1 {
		t.Errorf("haiku row = %+v", m)
	}
	if m := models["claude-sonnet-5-5"]; m.In != 1060 || m.Output != 100 || m.Sub.Messages != 0 {
		t.Errorf("sonnet row = %+v", m)
	}
	// An attempt on two models is counted under each, not as one row.
	for _, id := range []string{"claude-sonnet-5-5", "claude-opus-5-5"} {
		if _, ok := models[id]; !ok {
			t.Errorf("no row for %s", id)
		}
	}
	if len(u.Top) == 0 || u.Top[0].ID != "A-1" {
		t.Errorf("top = %+v", u.Top)
	}

	// --since and RUN selection errors.
	if _, err := NewUsage(in, "demo", "", day2.Add(time.Hour)); !errors.Is(err, ErrNoRunsSince) {
		t.Errorf("no run since: %v", err)
	}
	if _, err := NewUsage(in, "demo", "3", time.Time{}); err == nil {
		t.Error("run 3 of 2: want an error")
	}
	if u, err := NewUsage(in, "demo", "r1", time.Time{}); err != nil || u.Runs[0].Run != "r1" {
		t.Errorf("run by ID: %+v, %v", u.Runs, err)
	}
	if _, err := NewUsage(HistoryInput{}, "demo", "", time.Time{}); !errors.Is(err, ErrNoRuns) {
		t.Errorf("no log: %v", err)
	}
}

// The top consumers are at most five, biggest first, and a task that
// used nothing is not one.
func TestUsageTopFive(t *testing.T) {
	events := []state.Event{runStart("r1", time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))}
	for i := 1; i <= 7; i++ {
		events = append(events, usageLine("r1", fmt.Sprintf("A-%d", i), "sonnet",
			&usage.Counts{Input: int64(i * 10), Messages: 1}, nil, []string{"claude-sonnet-5-5"}, nil, state.AuditOK))
	}
	events = append(events, usageLine("r1", "A-8", "sonnet", &usage.Counts{}, nil, nil, nil, state.AuditOK))
	u, err := NewUsage(HistoryInput{Events: events}, "demo", "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range u.Top {
		ids = append(ids, r.ID)
	}
	if want := []string{"A-7", "A-6", "A-5", "A-4", "A-3"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("top = %v, want %v", ids, want)
	}
}

func TestUsageManyModelsAndNoUsage(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	events := []state.Event{runStart("r1", at),
		usageLine("r1", "A-1", "sonnet", &usage.Counts{Input: 10, Output: 1, Messages: 1}, nil,
			[]string{"claude-sonnet-5-5", "claude-opus-5-5"}, nil, state.AuditMismatch)}
	u, err := NewUsage(HistoryInput{Events: events}, "demo", "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Models) != 2 || u.Models[0].Attempts != 1 || u.Models[1].Attempts != 1 || u.Models[0].In != 10 || u.NoUsage != "" {
		t.Errorf("models = %+v, no_usage %q", u.Models, u.NoUsage)
	}
	for _, off := range []bool{false, true} {
		u, err := NewUsage(HistoryInput{Events: events[:1], UsageOff: off}, "demo", "", time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if u.NoUsage == "" || (off != strings.Contains(u.NoUsage, "turns it off")) {
			t.Errorf("off=%v: no_usage %q", off, u.NoUsage)
		}
	}
}

func sideLine(run, side string, at time.Time, main *usage.Counts) state.Event {
	return state.Event{V: 1, At: at, Type: state.EventSideUsage, Run: run, Side: side, Rank: "sonnet", Model: "sonnet",
		Usage: main, Models: []string{"claude-sonnet-5-5"}}
}

// Side sessions are listed apart: by run for a run, by time for --since,
// and they are in none of the figures nor a task's attempts (SPEC §6.10).
func TestUsageSide(t *testing.T) {
	day1 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	day2 := day1.Add(48 * time.Hour)
	events := []state.Event{
		runStart("r1", day1),
		usageLine("r1", "A-1", "sonnet", &usage.Counts{Input: 10, Output: 100, Messages: 1}, nil, []string{"claude-sonnet-5-5"}, nil, state.AuditOK),
		sideLine("r1", "aaaaaa", day1.Add(time.Hour), &usage.Counts{Input: 1, Output: 2, CacheRead: 30, Messages: 1}),
		sideLine("", "bbbbbb", day1.Add(3*time.Hour), nil), // no run, unreadable
		{V: 1, At: day1.Add(time.Hour), Type: state.EventSideClosed, Run: "r1", Side: "aaaaaa"},
		runStart("r2", day2),
		sideLine("r2", "cccccc", day2.Add(time.Hour), &usage.Counts{Output: 9, Messages: 1}),
	}
	in := HistoryInput{Events: events}

	u, err := NewUsage(in, "demo", "2", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Side) != 1 || u.Side[0].Side != "aaaaaa" || u.Side[0].In != 31 || u.Side[0].Output != 2 || u.Side[0].Run != "r1" {
		t.Errorf("run 1 side = %+v", u.Side)
	}
	if u.Totals.Attempts != 1 || u.Totals.In != 10 || len(u.Tasks) != 1 {
		t.Errorf("side sessions changed the figures: %+v tasks %d", u.Totals, len(u.Tasks))
	}
	if u, _ = NewUsage(in, "demo", "", time.Time{}); len(u.Side) != 1 || u.Side[0].Side != "cccccc" {
		t.Errorf("newest run side = %+v", u.Side)
	}
	u, err = NewUsage(in, "demo", "", day1.Add(2*time.Hour))
	if err != nil || len(u.Side) != 2 || u.Side[0].Side != "bbbbbb" || !u.Side[0].Unavailable || u.Side[1].Side != "cccccc" {
		t.Errorf("since side = %+v, %v", u.Side, err)
	}
	if u, _ = NewUsage(HistoryInput{Events: events[:2]}, "demo", "", time.Time{}); u.Side == nil || len(u.Side) != 0 {
		t.Errorf("no side sessions: %#v, want an empty array", u.Side)
	}
	// Side lines are no part of a run's history.
	if h := readRuns(events); len(h) != 2 || len(h[0].events) != 2 {
		t.Errorf("runs hold side lines: %d runs", len(h))
	}
}
