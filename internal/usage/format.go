package usage

import (
	"fmt"
	"strings"
)

// Short writes a token count short, rounded down: 950, 1.2k, 41k, 2.3M,
// 41M, 1.2G. The CLI and the run card show counts this way (SPEC §14, §15.1).
func Short(n int64) string {
	for _, u := range []struct {
		at   int64
		unit string
	}{{1_000_000_000, "G"}, {1_000_000, "M"}, {1_000, "k"}} {
		if n < u.at {
			continue
		}
		if n >= 10*u.at {
			return fmt.Sprintf("%d%s", n/u.at, u.unit)
		}
		tenths := n * 10 / u.at
		return strings.TrimSuffix(fmt.Sprintf("%d.%d", tenths/10, tenths%10), ".0") + u.unit
	}
	return fmt.Sprint(n)
}
