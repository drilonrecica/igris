// Package price estimates what a session's tokens would cost at the
// Anthropic API list prices (SPEC §6.9, V18-P1). The table is embedded and
// dated, never fetched, and overridable per model by [usage.prices]. Every
// figure it yields is an estimate: igris never meters or bills.
package price

import (
	"fmt"
	"strings"

	"github.com/drilonrecica/igris/internal/usage"
)

// AsOf is the date of the embedded table's figures; a release that changes
// them changes it.
const AsOf = "2026-10-25"

// Source says where an estimate's prices came from.
const (
	SourceEmbedded = "embedded"
	SourceConfig   = "config"
)

// Rates are one model's prices in US dollars per million tokens.
type Rates struct {
	Input        float64
	CacheWrite5m float64
	CacheWrite1h float64
	CacheRead    float64
	Output       float64
}

// embedded is the list-price table (platform.claude.com pricing, read on
// AsOf), by model id without a -YYYYMMDD suffix. The long-context tier of
// Haiku 5.5, the batch discount and fast mode are not modelled: the
// estimate is flat.
var embedded = map[string]Rates{
	"claude-fable-5-1":  {10, 12.50, 20, 0.25, 50},
	"claude-opus-5-5":   {4, 5, 8, 0.20, 20},
	"claude-sonnet-5-5": {2, 2.50, 4, 0.10, 10},
	"claude-haiku-5-5":  {0.10, 0.125, 0.20, 0.01, 0.50},
	"claude-fable-5":    {10, 12.50, 20, 1, 50},
	"claude-opus-5":     {5, 6.25, 10, 0.50, 25},
	"claude-opus-4-8":   {5, 6.25, 10, 0.50, 25},
	"claude-opus-4-7":   {5, 6.25, 10, 0.50, 25},
	"claude-opus-4-6":   {5, 6.25, 10, 0.50, 25},
	"claude-opus-4-5":   {5, 6.25, 10, 0.50, 25},
	"claude-sonnet-5":   {2, 2.50, 4, 0.20, 10},
	"claude-sonnet-4-6": {3, 3.75, 6, 0.30, 15},
	"claude-sonnet-4-5": {3, 3.75, 6, 0.30, 15},
	"claude-haiku-4-5":  {1, 1.25, 2, 0.10, 5},
}

// Table is the embedded table with the owner's overrides. The zero Table
// is the embedded one.
type Table struct {
	overrides map[string]Rates
}

// WithOverrides returns the table with [usage.prices] applied: each key is
// matched by its base, like a model id.
func (t Table) WithOverrides(o map[string]Rates) Table {
	if len(o) == 0 {
		return Table{}
	}
	m := make(map[string]Rates, len(o))
	for id, r := range o {
		m[Base(id)] = r
	}
	return Table{overrides: m}
}

// Base is a model id as the table keys it: lower case, without a bracketed
// suffix (`[1m]`) and without a trailing -YYYYMMDD.
func Base(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if i := strings.IndexByte(id, '['); i >= 0 {
		id = id[:i]
	}
	if n := len(id); n > 9 && id[n-9] == '-' && allDigits(id[n-8:]) {
		id = id[:n-9]
	}
	return id
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// Lookup returns the prices of model, and whether they came from the
// config. An override wins over the embedded table.
func (t Table) Lookup(model string) (r Rates, fromConfig, ok bool) {
	b := Base(model)
	if r, ok = t.overrides[b]; ok {
		return r, true, true
	}
	r, ok = embedded[b]
	return r, false, ok
}

// Sum is an estimate being added up: dollars, the tokens that had no price
// (input + output of attempts on a model without one) and whether any
// price came from the config.
type Sum struct {
	USD      float64
	Unpriced int64
	Config   bool
}

// Add folds o into s.
func (s *Sum) Add(o Sum) {
	s.USD += o.USD
	s.Unpriced = sat(s.Unpriced, o.Unpriced)
	s.Config = s.Config || o.Config
}

func sat(a, b int64) int64 {
	if a < 0 || b < 0 || a > 1<<62-b {
		return 1 << 62
	}
	return a + b
}

// Cost prices c on the models a thread ran on. A thread that ran on
// several models can't be split (the transcript doesn't say which tokens
// were whose), so it is priced at the dearest of those with a price (by
// input + output); on none it is unpriced. Cache writes the transcript
// doesn't split by TTL are priced at the 5-minute rate.
func (t Table) Cost(c usage.Counts, models []string) Sum {
	if c.IsZero() {
		return Sum{}
	}
	var best Rates
	var cfg, found bool
	for _, m := range models {
		r, fromCfg, ok := t.Lookup(m)
		if ok && (!found || r.Input+r.Output > best.Input+best.Output) {
			best, cfg, found = r, fromCfg, true
		}
	}
	if !found {
		return Sum{Unpriced: sat(c.Input, c.Output)}
	}
	w5, w1 := c.CacheWrite5m, c.CacheWrite1h
	if rest := c.CacheWrite - w5 - w1; rest > 0 {
		w5 += rest
	}
	usd := (float64(c.Input)*best.Input + float64(c.Output)*best.Output + float64(c.CacheRead)*best.CacheRead +
		float64(w5)*best.CacheWrite5m + float64(w1)*best.CacheWrite1h) / 1e6
	return Sum{USD: usd, Config: cfg}
}

// Source is where s's prices came from.
func (s Sum) Source() string {
	if s.Config {
		return SourceConfig
	}
	return SourceEmbedded
}

// Legend is the line shown wherever an estimate is (SPEC §6.9).
func Legend() string {
	return fmt.Sprintf("≈ $ is an estimate at API list prices (as of %s; igris.toml overrides), not a bill: a subscription includes this usage.", AsOf)
}
