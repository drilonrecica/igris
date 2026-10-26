package look

import "testing"

func TestDollars(t *testing.T) {
	for in, want := range map[float64]string{0: "≈ $0.00", -1: "≈ $0.00", 0.001: "≈ <$0.01", 0.005: "≈ $0.01", 12.4: "≈ $12.40", 1234.567: "≈ $1234.57"} {
		if got := Dollars(in); got != want {
			t.Errorf("Dollars(%v) = %q, want %q", in, got, want)
		}
	}
}
