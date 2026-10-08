package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
)

// Emoji with a variation selector (⚠️ ✔️ ❤️) are two cells wide as one
// grapheme; fit must count them that way and never split one.
func TestFitGraphemes(t *testing.T) {
	for _, s := range []string{"⚠️ overflow text", "✔️ overflow text", "❤️ overflow text", "a⚠️b✔️c❤️d overflow", "\x1b[1m⚠️ bold\x1b[0m text"} {
		for w := 1; w <= 12; w++ {
			got := fit(s, w)
			if textWidth(got) > w {
				t.Errorf("fit(%q, %d) = %q, %d cells", s, w, got, textWidth(got))
			}
			if strings.ContainsRune(got, '️') && !strings.Contains(got, "️") {
				t.Errorf("fit(%q, %d) = %q split a grapheme", s, w, got)
			}
		}
	}
	if got := fit("⚠️⚠️⚠️", 4); got != "⚠️…" {
		t.Errorf("fit = %q, want one emoji and the ellipsis", got)
	}
	if got := expandTabs("⚠️\tx"); got != "⚠️      x" {
		t.Errorf("expandTabs = %q, want the tab to stop at 8 after a 2-cell emoji", got)
	}
}

const emojiPlan = `## M0 — Repository ⚠️ foundation

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M0-01 | **Go module ✔️** — module path | — | done | sonnet | agent |
| M0-03 | **Makefile ⚠️⚠️⚠️ with ❤️ and a title long enough to be cut** — fmt, lint, test | M0-01 | in progress | sonnet | agent |
| M0-04 | **Config ❤️ loader** | M0-01 | ready | opus | agent |
`

// No line of the run view is wider than the terminal, whatever emoji the
// plan and the tail hold.
func TestEmojiFitsEveryWidth(t *testing.T) {
	for w := 10; w <= 140; w++ {
		for _, h := range []int{3, 10, 24, 40} {
			hs := newHarness(t, w, h)
			hs.withPlan(emojiPlan)
			hs.m.opts.Tail = func(context.Context, int) ([]string, error) { return nil, nil }
			ev := started("M0-03")
			ev.Title = "Makefile ⚠️⚠️⚠️ with ❤️ and a title long enough to be cut"
			hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0", Detail: "phase M0"}, ev, opened("M0-03"))
			hs.m.tail = tailLines([]string{"✔️ tests passed ⚠️ one warning ❤️ " + strings.Repeat("x", 200), "⚠️\tindented"}, tailWide)
			for i, l := range strings.Split(hs.m.View(), "\n") {
				if textWidth(l) > w {
					t.Errorf("w=%d h=%d line %d is %d cells: %q", w, h, i, textWidth(l), l)
				}
			}
		}
	}
}
