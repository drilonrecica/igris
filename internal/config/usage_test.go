package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The keys of 1.8 (SPEC §12; decisions.md V18-P1–P3): the hold-ahead
// threshold, budgets and prices, commit = "batch", dep_notes, the
// resolver, and the two new events.
func TestV18Keys(t *testing.T) {
	src := `[run]
commit = "batch"
dep_notes = false

[parallel]
on_conflict = "resolve"
resolver_rank = "opus"

[usage]
statusline = true
hold_at = 90

[usage.prices]
"claude-opus-5-5" = { input = 4.0, cache_write_5m = 5, cache_write_1h = 8.0, cache_read = 0.20, output = 20.0 }

[usage.budget]
run = "20M"

[usage.budget.phases]
V18 = "$40"

[usage.budget.ranks]
fable = 1500000

[notify.ntfy]
topic = "t"
events = ["usage_limit", "budget_exceeded"]
`
	c, err := Parse([]byte(src), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Run.Commit != CommitBatch || c.Run.DepNotesOn() {
		t.Errorf("run = %+v", c.Run)
	}
	if c.Parallel.OnConflictOrDefault() != OnConflictResolve || c.Parallel.ResolverRank != "opus" {
		t.Errorf("parallel = %+v", c.Parallel)
	}
	if c.Usage.HoldAtOrDefault() != 90 {
		t.Errorf("hold_at = %d", c.Usage.HoldAtOrDefault())
	}
	p := c.Usage.Prices["claude-opus-5-5"]
	if p.Input == nil || *p.Input != 4 || *p.CacheWrite5m != 5 || *p.CacheWrite1h != 8 || *p.CacheRead != 0.2 || *p.Output != 20 {
		t.Errorf("prices = %+v", p)
	}
	b := c.Usage.Budget
	for _, tt := range []struct {
		b    Budget
		want BudgetLimit
	}{{b.Run, BudgetLimit{Tokens: 20_000_000}}, {b.Phases["V18"], BudgetLimit{Cents: 4000}}, {b.Ranks["fable"], BudgetLimit{Tokens: 1_500_000}}} {
		if got, err := tt.b.Limit(); err != nil || got != tt.want {
			t.Errorf("%q: %+v, %v; want %+v", tt.b, got, err, tt.want)
		}
	}
	if len(c.Warnings()) != 0 {
		t.Errorf("warnings: %q", c.Warnings())
	}

	d := Default()
	if d.Run.Commit != CommitAsk || !d.Run.DepNotesOn() || d.Usage.HoldAtOrDefault() != DefaultHoldAt || !d.Usage.Budget.IsZero() || d.Usage.Prices != nil || d.Parallel.ResolverRank != "" {
		t.Errorf("defaults: run %+v usage %+v parallel %+v", d.Run, d.Usage, d.Parallel)
	}
	for _, ev := range []string{"usage_limit", "budget_exceeded"} {
		if !slices.Contains(d.Notify.Ntfy.Events, ev) || slices.Contains(d.Notify.BreakThrough, ev) {
			t.Errorf("%s: in the default events %v, not in break_through %v", ev, d.Notify.Ntfy.Events, d.Notify.BreakThrough)
		}
	}
}

func TestBudgetLimit(t *testing.T) {
	tests := []struct {
		in      Budget
		want    BudgetLimit
		wantErr string
	}{
		{"20M", BudgetLimit{Tokens: 20_000_000}, ""},
		{"1.5M", BudgetLimit{Tokens: 1_500_000}, ""},
		{"500k", BudgetLimit{Tokens: 500_000}, ""},
		{"2K", BudgetLimit{Tokens: 2000}, ""},
		{" 1500000 ", BudgetLimit{Tokens: 1_500_000}, ""},
		{"$40", BudgetLimit{Cents: 4000}, ""},
		{"$12.5", BudgetLimit{Cents: 1250}, ""},
		{"$0.99", BudgetLimit{Cents: 99}, ""},
		{"0", BudgetLimit{}, "more than 0"},
		{"$0", BudgetLimit{}, "more than $0"},
		{"$1.234", BudgetLimit{}, "too many decimals"},
		{"1.0000005k", BudgetLimit{}, "too many decimals"},
		{"1.5", BudgetLimit{}, "not a whole number of tokens"},
		{"20m", BudgetLimit{}, "use a token count"},
		{"20 M", BudgetLimit{}, "use a token count"},
		{"-5", BudgetLimit{}, "use a token count"},
		{"$", BudgetLimit{}, "use a token count"},
		{"40$", BudgetLimit{}, "use a token count"},
		{"1e6", BudgetLimit{}, "use a token count"},
		{"", BudgetLimit{}, "use a token count"},
		{"true", BudgetLimit{}, "use a token count"},
		{"99999999999999999999", BudgetLimit{}, "too large"},
		{"2000000000M", BudgetLimit{}, "at most"},
	}
	for _, tt := range tests {
		got, err := tt.in.Limit()
		switch {
		case tt.wantErr == "" && (err != nil || got != tt.want):
			t.Errorf("%q: %+v, %v; want %+v", tt.in, got, err, tt.want)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("%q: err = %v, want %q", tt.in, err, tt.wantErr)
		}
	}
}

func TestV18Validation(t *testing.T) {
	tests := []struct{ src, want string }{
		{"[run]\ncommit = \"later\"\n", "run.commit = \"later\" is invalid; use one of: ask, auto, never, batch"},
		{"[parallel]\non_conflict = \"merge\"\n", "parallel.on_conflict = \"merge\" is invalid; use one of: rerun, ask, resolve"},
		{"[parallel]\nresolver_rank = \"turbo\"\n", "parallel.resolver_rank = \"turbo\" is not a [models] rank; use one of: fable, haiku, opus, sonnet"},
		{"[usage]\nhold_at = 101\n", "usage.hold_at = 101 is out of range"},
		{"[usage]\nhold_at = -1\n", "usage.hold_at = -1 is out of range"},
		{"[usage.budget]\nrun = \"lots\"\n", "usage.budget.run = \"lots\" is not a budget: use a token count"},
		{"[usage.budget]\nrun = 1.5\n", "usage.budget.run = \"1.5\" is not a budget: not a whole number of tokens"},
		{"[usage.budget]\nrun = 0\n", "usage.budget.run = \"0\" is not a budget"},
		{"[usage.budget.phases]\nM1 = \"$0\"\n", "usage.budget.phases.M1 = \"$0\" is not a budget"},
		{"[usage.budget.phases]\nM1 = \"1M\"\nm1 = \"2M\"\n", "name the same phase"},
		{"[usage.budget.ranks]\nturbo = \"1M\"\n", "usage.budget.ranks.turbo is not a [models] rank"},
		{"[usage.budget.ranks]\nopus = \"x\"\n", "usage.budget.ranks.opus = \"x\" is not a budget"},
		{"[usage.prices]\n\"m\" = { input = 1.0 }\n", "usage.prices.\"m\" needs cache_write_5m, cache_write_1h, cache_read, output too"},
		{"[usage.prices]\n\"m\" = { input = -1.0, cache_write_5m = 1, cache_write_1h = 1, cache_read = 1, output = 1 }\n", "usage.prices.\"m\".input is out of range"},
		{"[usage.prices]\n\"\" = { input = 1, cache_write_5m = 1, cache_write_1h = 1, cache_read = 1, output = 1 }\n", "usage.prices has a model id that is empty"},
		{"[usage.prices]\n\"m\" = { input = 1, cache_write_5m = 1, cache_write_1h = 1, cache_read = 1, output = 1, batch = 1 }\n", "unknown key"},
		{"[run]\ndep_notes = \"yes\"\n", "dep_notes"},
		{"[notify.ntfy]\nevents = [\"usage_limits\"]\n", "notify.ntfy.events contains unknown event \"usage_limits\""},
	}
	for _, tt := range tests {
		_, err := Parse([]byte(tt.src), "igris.toml")
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: err = %v, want %q", tt.src, err, tt.want)
		}
	}
}

func TestV18Warnings(t *testing.T) {
	tests := []struct{ src, want string }{
		{"[parallel]\nresolver_rank = \"opus\"\n", `parallel.resolver_rank is set, but on_conflict = "rerun"; no resolver runs`},
		{"[parallel]\non_conflict = \"ask\"\nresolver_rank = \"opus\"\n", `parallel.resolver_rank is set, but on_conflict = "ask"; no resolver runs`},
		{"[usage]\nhold_at = 90\n", "usage.hold_at = 90 needs the quota, which only igris's status line records: set [usage] statusline = true"},
		{"[usage]\nenabled = false\n\n[usage.budget]\nrun = \"1M\"\n", "usage.budget is set, but usage.enabled = false"},
	}
	for _, tt := range tests {
		c, err := Parse([]byte(tt.src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		if ws := c.Warnings(); !slices.ContainsFunc(ws, func(w string) bool { return strings.Contains(w, tt.want) }) {
			t.Errorf("%q: warnings %q, want %q", tt.src, ws, tt.want)
		}
	}
	for _, src := range []string{
		"[usage]\nhold_at = 0\n", // hold-ahead off: nothing needs the quota
		"[usage]\nhold_at = 90\nstatusline = true\n",
		"[usage]\nenabled = false\nhold_at = 90\n", // no usage at all: the hold has nothing to read either way
		"[parallel]\non_conflict = \"resolve\"\nresolver_rank = \"opus\"\n",
		"[parallel]\non_conflict = \"resolve\"\n",
	} {
		c, err := Parse([]byte(src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		if ws := c.Warnings(); len(ws) != 0 {
			t.Errorf("%q: warnings %q", src, ws)
		}
	}
}

// The keys of 1.8 stay out of the hash while unset or at their defaults
// (SPEC §12, §19), so a run interrupted under 1.7 resumes without the
// config-changed notice; and the two new default events don't change the
// hash of a config that doesn't set events, nor of one that lists 1.7's
// defaults itself.
func TestHashWithoutV18KeysIsUnchanged(t *testing.T) {
	parse := func(src string) *Config {
		t.Helper()
		c, err := Parse([]byte(src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if got := Default().Hash(); got != v04DefaultHash {
		t.Errorf("default hash %s, want %s", got, v04DefaultHash)
	}
	for _, src := range []string{
		"[run]\ndep_notes = true\n",
		"[usage]\nhold_at = 95\n",
		"[usage.budget]\n",
		"[usage.prices]\n",
		"[parallel]\non_conflict = \"rerun\"\n",
		"[notify.ntfy]\n" + `events = ["needs_input", "session_lost", "task_overdue", "rank_mismatch", "phase_done", "phase_stuck", "run_error", "verify_failed_limit"]` + "\n",
	} {
		if parse(src).Hash() != v04DefaultHash {
			t.Errorf("%q changed the hash", src)
		}
	}
	for _, src := range []string{
		"[run]\ndep_notes = false\n",
		"[run]\ncommit = \"batch\"\n",
		"[usage]\nhold_at = 90\n",
		"[usage]\nhold_at = 0\n",
		"[usage.budget]\nrun = \"1M\"\n",
		"[usage.budget.ranks]\nopus = \"$5\"\n",
		"[usage.prices]\n\"m\" = { input = 1, cache_write_5m = 1, cache_write_1h = 1, cache_read = 1, output = 1 }\n",
		"[parallel]\non_conflict = \"resolve\"\n",
		"[parallel]\non_conflict = \"resolve\"\nresolver_rank = \"opus\"\n",
		"[notify.ntfy]\n" + `events = ["needs_input", "session_lost", "task_overdue", "rank_mismatch", "phase_done", "phase_stuck", "run_error", "verify_failed_limit", "usage_limit"]` + "\n",
	} {
		if parse(src).Hash() == v04DefaultHash {
			t.Errorf("%q left the hash unchanged", src)
		}
	}

	// Write and Load keep every key and the hash, an integer budget as text.
	c := parse("[run]\ncommit = \"batch\"\ndep_notes = false\n\n[parallel]\non_conflict = \"resolve\"\nresolver_rank = \"opus\"\n\n[usage]\nhold_at = 80\n\n" +
		"[usage.prices]\n\"claude-x\" = { input = 1.5, cache_write_5m = 2, cache_write_1h = 3, cache_read = 0.1, output = 9 }\n\n" +
		"[usage.budget]\nrun = 1500000\n\n[usage.budget.phases]\nM1 = \"$40\"\n")
	path := filepath.Join(t.TempDir(), "igris.toml")
	if err := Write(path, c); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		data, _ := os.ReadFile(path) //nolint:gosec // the test's own temp file
		t.Fatalf("load written config: %v\n%s", err, data)
	}
	if back.Hash() != c.Hash() || back.Usage.Budget.Run != "1500000" || back.Usage.Budget.Phases["M1"] != "$40" || back.Parallel.ResolverRank != "opus" || back.Usage.HoldAtOrDefault() != 80 {
		t.Errorf("round trip: %+v %+v", back.Usage, back.Parallel)
	}
	if err := Write(path, Default()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || strings.Contains(string(data), "usage") || strings.Contains(string(data), "dep_notes") { //nolint:gosec // the test's own temp file
		t.Errorf("written default config has 1.8 keys (%v):\n%s", err, data)
	}
}
