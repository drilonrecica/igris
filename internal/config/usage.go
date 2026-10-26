package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/drilonrecica/igris/internal/price"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// DefaultHoldAt is usage.hold_at when it is not set (SPEC §6.9, V18-P1):
// a quota window at or above this percentage holds the next task.
const DefaultHoldAt = 95

// HoldAtOrDefault is usage.hold_at, or DefaultHoldAt when unset; 0 turns
// the hold-ahead off (a hit still holds).
func (u Usage) HoldAtOrDefault() int {
	if u.HoldAt == nil {
		return DefaultHoldAt
	}
	return *u.HoldAt
}

// normalized is u with the keys at their defaults unset, so the hash of a
// config that writes them out is the hash of one that doesn't.
func (u Usage) normalized() Usage {
	if u.HoldAt != nil && *u.HoldAt == DefaultHoldAt {
		u.HoldAt = nil
	}
	// An empty table sets nothing.
	if len(u.Prices) == 0 {
		u.Prices = nil
	}
	if len(u.Budget.Phases) == 0 {
		u.Budget.Phases = nil
	}
	if len(u.Budget.Ranks) == 0 {
		u.Budget.Ranks = nil
	}
	return u
}

// Budgets is the [usage.budget] table (SPEC §12, V18-P1): the budget of
// the run, of each phase and of each rank. Every key is empty when not
// set.
type Budgets struct {
	Run Budget `toml:"run,omitempty" json:",omitempty"`
	// Phases is [usage.budget.phases], by phase ID (case-insensitive).
	Phases map[string]Budget `toml:"phases,omitempty" json:",omitempty"`
	// Ranks is [usage.budget.ranks], by [models] key.
	Ranks map[string]Budget `toml:"ranks,omitempty" json:",omitempty"`
}

// IsZero reports that no budget is set.
func (b Budgets) IsZero() bool { return b.Run == "" && len(b.Phases) == 0 && len(b.Ranks) == 0 }

// Budget is a budget as igris.toml writes it: a token count ("20M",
// "500k", 1500000) or an estimated amount in US dollars ("$40"). Limit
// parses it.
type Budget string

// UnmarshalTOML implements toml.Unmarshaler: a string, or an integer,
// which is a token count. Any other value is kept as text, so Validate
// reports it with the file's other problems.
func (b *Budget) UnmarshalTOML(v any) error {
	switch x := v.(type) {
	case string:
		*b = Budget(x)
	case int64:
		*b = Budget(strconv.FormatInt(x, 10))
	default:
		*b = Budget(fmt.Sprint(x))
	}
	return nil
}

// BudgetLimit is a parsed budget: either Tokens (input + output, as `igris
// usage` totals them) or Cents (an estimate by the price table) is set.
type BudgetLimit struct {
	Tokens int64
	Cents  int64
}

// Budget limits: far above any run, low enough that sums don't overflow.
const (
	maxBudgetTokens = 1_000_000_000_000_000 // 10^15
	maxBudgetCents  = 100_000_000_000       // $1,000,000,000
)

const budgetHint = `use a token count such as "20M", "500k" or 1500000, or an amount such as "$40"`

// Limit parses b: "$" and an amount with at most two decimals, or a whole
// number of tokens with an optional k (thousand) or M (million) suffix,
// which may have decimals ("1.5M"). Both must be greater than zero.
func (b Budget) Limit() (BudgetLimit, error) {
	s := strings.TrimSpace(string(b))
	if amount, ok := strings.CutPrefix(s, "$"); ok {
		cents, err := parseScaled(amount, 2)
		switch {
		case err != nil:
			return BudgetLimit{}, err
		case cents <= 0 || cents > maxBudgetCents:
			return BudgetLimit{}, fmt.Errorf("the amount must be more than $0 and at most $%d", maxBudgetCents/100)
		}
		return BudgetLimit{Cents: cents}, nil
	}
	scale := 0
	switch {
	case strings.HasSuffix(s, "k"), strings.HasSuffix(s, "K"):
		scale = 3
	case strings.HasSuffix(s, "M"):
		scale = 6
	}
	if scale > 0 {
		s = s[:len(s)-1]
	}
	tokens, err := parseScaled(s, scale)
	switch {
	case err != nil:
		return BudgetLimit{}, err
	case tokens <= 0 || tokens > maxBudgetTokens:
		return BudgetLimit{}, fmt.Errorf("the token count must be more than 0 and at most %d", int64(maxBudgetTokens))
	}
	return BudgetLimit{Tokens: tokens}, nil
}

// parseScaled parses a non-negative decimal "12" or "12.5" times
// 10^scale, which must come out whole: at most scale decimals.
func parseScaled(s string, scale int) (int64, error) {
	whole, frac, dot := strings.Cut(s, ".")
	if whole == "" || !allDigits(whole) || (dot && (frac == "" || !allDigits(frac))) {
		return 0, fmt.Errorf("%s", budgetHint)
	}
	if len(frac) > scale {
		if scale == 0 {
			return 0, fmt.Errorf("not a whole number of tokens; %s", budgetHint)
		}
		return 0, fmt.Errorf("too many decimals; %s", budgetHint)
	}
	digits := whole + frac + strings.Repeat("0", scale-len(frac))
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("too large; %s", budgetHint)
	}
	return n, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Price is one model's [usage.prices] entry (SPEC §12, V18-P1): US dollars
// per million tokens. Every field must be set; nil is a missing one.
type Price struct {
	Input        *float64 `toml:"input"`
	CacheWrite5m *float64 `toml:"cache_write_5m"`
	CacheWrite1h *float64 `toml:"cache_write_1h"`
	CacheRead    *float64 `toml:"cache_read"`
	Output       *float64 `toml:"output"`
}

// priceFields are p's fields by key, in igris.toml's order.
func (p Price) priceFields() []struct {
	key string
	v   *float64
} {
	return []struct {
		key string
		v   *float64
	}{{"input", p.Input}, {"cache_write_5m", p.CacheWrite5m}, {"cache_write_1h", p.CacheWrite1h}, {"cache_read", p.CacheRead}, {"output", p.Output}}
}

// maxPrice bounds a price per million tokens; far above any list price.
const maxPrice = 1_000_000

// validateUsage checks [usage], [usage.budget] and [usage.prices] (SPEC
// §12): hold_at a whole percent, every budget a token count or an amount,
// budget ranks [models] keys, every price set and not negative.
func (c *Config) validateUsage(add func(string, ...any)) {
	u := c.Usage
	if u.HoldAt != nil && (*u.HoldAt < 0 || *u.HoldAt > 100) {
		add("usage.hold_at = %d is out of range; use a whole percent from 1 to 100, or 0 to hold only after a session hits a limit", *u.HoldAt)
	}
	check := func(key string, b Budget) {
		if _, err := b.Limit(); err != nil {
			add("%s = %q is not a budget: %v", key, textsafe.Line(string(b)), err)
		}
	}
	if u.Budget.Run != "" {
		check("usage.budget.run", u.Budget.Run)
	}
	seen := map[string]string{}
	for _, id := range sortedKeys(u.Budget.Phases) {
		if first, dup := seen[strings.ToLower(id)]; dup {
			add("usage.budget.phases.%s and usage.budget.phases.%s name the same phase (phase IDs match case-insensitively); keep one of them", textsafe.Line(first), textsafe.Line(id))
			continue
		}
		seen[strings.ToLower(id)] = id
		check("usage.budget.phases."+textsafe.Line(id), u.Budget.Phases[id])
	}
	for _, rank := range sortedKeys(u.Budget.Ranks) {
		if _, ok := c.Models[rank]; !ok {
			add("usage.budget.ranks.%s is not a [models] rank; use one of: %s", textsafe.Line(rank), strings.Join(sortedKeys(c.Models), ", "))
			continue
		}
		check("usage.budget.ranks."+rank, u.Budget.Ranks[rank])
	}
	for _, model := range sortedKeys(u.Prices) {
		if strings.TrimSpace(model) == "" || strings.ContainsFunc(model, unicode.IsControl) {
			add("usage.prices has a model id that is empty or has a control character; use the model id, e.g. \"claude-opus-5-5\"")
			continue
		}
		var missing []string
		for _, f := range u.Prices[model].priceFields() {
			switch {
			case f.v == nil:
				missing = append(missing, f.key)
			case math.IsNaN(*f.v) || *f.v < 0 || *f.v > maxPrice:
				add("usage.prices.%q.%s is out of range; use US dollars per million tokens, from 0 to %d", textsafe.Line(model), f.key, maxPrice)
			}
		}
		if len(missing) > 0 {
			add("usage.prices.%q needs %s too; give all five prices in US dollars per million tokens (input, cache_write_5m, cache_write_1h, cache_read, output)", textsafe.Line(model), strings.Join(missing, ", "))
		}
	}
}

// usageWarnings are the [usage] settings that have no effect (SPEC §12).
func (c *Config) usageWarnings() []string {
	var out []string
	u := c.Usage
	if !u.On() && !u.Budget.IsZero() {
		out = append(out, "usage.budget is set, but usage.enabled = false, so igris reads no usage and no budget holds; set usage.enabled = true, or remove [usage.budget]")
	}
	if u.On() && u.HoldAt != nil && *u.HoldAt > 0 && !u.Statusline {
		out = append(out, fmt.Sprintf("usage.hold_at = %d needs the quota, which only igris's status line records: set [usage] statusline = true, or igris holds a task only after a session hits a usage limit", *u.HoldAt))
	}
	return out
}

// PriceTable is the table of the estimate (SPEC §6.9): the embedded one
// with [usage.prices] applied.
func (u Usage) PriceTable() price.Table {
	over := make(map[string]price.Rates, len(u.Prices))
	v := func(f *float64) float64 { // validation guarantees every field is set
		if f == nil {
			return 0
		}
		return *f
	}
	for id, p := range u.Prices {
		over[id] = price.Rates{Input: v(p.Input), CacheWrite5m: v(p.CacheWrite5m), CacheWrite1h: v(p.CacheWrite1h), CacheRead: v(p.CacheRead), Output: v(p.Output)}
	}
	return price.Table{}.WithOverrides(over)
}
