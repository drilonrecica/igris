package usage

import "github.com/drilonrecica/igris/internal/look"

// Short writes a token count short, rounded down: 950, 1.2k, 41k, 2.3M,
// 41M, 1.2G. The CLI and the run card show counts this way (SPEC §14,
// §15.1); the format is look.Tokens.
func Short(n int64) string { return look.Tokens(n) }
