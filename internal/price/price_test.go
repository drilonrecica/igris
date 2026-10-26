package price

import (
	"math"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/usage"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestBase(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5-5":          "claude-opus-5-5",
		"claude-opus-5-5-20261001": "claude-opus-5-5",
		"Claude-Opus-5-5[1m]":      "claude-opus-5-5",
		" claude-sonnet-4-5 ":      "claude-sonnet-4-5",
		"claude-haiku-4-5-2025":    "claude-haiku-4-5-2025", // not a date suffix
	} {
		if got := Base(in); got != want {
			t.Errorf("Base(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmbeddedTable(t *testing.T) {
	for id, r := range embedded {
		if id != Base(id) {
			t.Errorf("table key %q is not a base id", id)
		}
		if r.Input <= 0 || r.Output <= r.Input || r.CacheRead >= r.Input && id != "claude-fable-5" || r.CacheWrite1h < r.CacheWrite5m {
			t.Errorf("%s: implausible rates %+v", id, r)
		}
	}
	r, _, ok := Table{}.Lookup("claude-sonnet-5-5-20261001[1m]")
	if !ok || r.Output != 10 {
		t.Fatalf("lookup = %+v, %v", r, ok)
	}
	if _, _, ok := (Table{}).Lookup("claude-unknown-1"); ok {
		t.Fatal("unknown model has a price")
	}
}

func TestCost(t *testing.T) {
	var tab Table
	c := usage.Counts{Input: 1_000_000, Output: 100_000, CacheRead: 2_000_000, CacheWrite: 600_000, CacheWrite5m: 400_000, CacheWrite1h: 200_000, Messages: 3}
	got := tab.Cost(c, []string{"claude-opus-5-5"})
	want := 4.0 + 2.0 + 0.4 + 2.0 + 1.6 // in, out, read, 5m write, 1h write
	if !near(got.USD, want) || got.Unpriced != 0 || got.Source() != SourceEmbedded {
		t.Fatalf("cost = %+v, want %v", got, want)
	}
}

func TestCostUnsplitWritesArePricedAt5m(t *testing.T) {
	c := usage.Counts{CacheWrite: 1_000_000, Messages: 1}
	got := Table{}.Cost(c, []string{"claude-sonnet-5-5"})
	if !near(got.USD, 2.5) {
		t.Fatalf("cost = %v, want 2.5", got.USD)
	}
}

func TestCostUnpriced(t *testing.T) {
	c := usage.Counts{Input: 10, Output: 5, CacheRead: 100, Messages: 1}
	for _, models := range [][]string{nil, {"claude-unknown-1"}, {"default"}} {
		got := Table{}.Cost(c, models)
		if got.USD != 0 || got.Unpriced != 15 {
			t.Errorf("models %v: cost = %+v", models, got)
		}
	}
	if got := (Table{}).Cost(usage.Counts{}, nil); got != (Sum{}) {
		t.Errorf("nothing counted costs %+v", got)
	}
}

func TestCostSeveralModelsUsesTheDearest(t *testing.T) {
	c := usage.Counts{Output: 1_000_000, Messages: 1}
	got := Table{}.Cost(c, []string{"claude-sonnet-5-5", "claude-opus-5-5", "claude-nope"})
	if !near(got.USD, 20) {
		t.Fatalf("cost = %v, want 20 (opus)", got.USD)
	}
}

func TestOverrides(t *testing.T) {
	tab := Table{}.WithOverrides(map[string]Rates{
		"claude-opus-5-5-20261001": {Output: 100},
		"my-model":                 {Input: 1, Output: 2},
	})
	c := usage.Counts{Output: 1_000_000, Messages: 1}
	got := tab.Cost(c, []string{"claude-opus-5-5"})
	if !near(got.USD, 100) || got.Source() != SourceConfig {
		t.Fatalf("override cost = %+v", got)
	}
	if got := tab.Cost(c, []string{"my-model"}); !near(got.USD, 2) {
		t.Fatalf("custom model cost = %+v", got)
	}
	// Untouched models keep the embedded price and source.
	got = tab.Cost(c, []string{"claude-sonnet-5-5"})
	if !near(got.USD, 10) || got.Source() != SourceEmbedded {
		t.Fatalf("other model = %+v", got)
	}
	if (Table{}).WithOverrides(nil).overrides != nil {
		t.Fatal("no overrides should be the zero table")
	}
}

func TestSumAddSaturates(t *testing.T) {
	s := Sum{Unpriced: 1 << 62}
	s.Add(Sum{Unpriced: 5, USD: 1, Config: true})
	if s.Unpriced != 1<<62 || s.USD != 1 || !s.Config {
		t.Fatalf("sum = %+v", s)
	}
}

func TestLegend(t *testing.T) {
	l := Legend()
	if !strings.Contains(l, AsOf) || !strings.HasPrefix(l, "≈ $ is an estimate") || !strings.Contains(l, "not a bill") {
		t.Fatalf("legend = %q", l)
	}
}
