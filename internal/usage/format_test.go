package usage

import (
	"math"
	"testing"
)

func TestShort(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0", 950: "950", 999: "999", 1000: "1k", 1234: "1.2k", 9999: "9.9k", 10_000: "10k", 41_900: "41k",
		999_999: "999k", 1_000_000: "1M", 2_345_678: "2.3M", 41_000_000: "41M", 1_200_000_000: "1.2G",
		250_000_000_000: "250G", math.MaxInt64: "9223372036G",
	} {
		if got := Short(n); got != want {
			t.Errorf("Short(%d) = %q, want %q", n, got, want)
		}
	}
}
