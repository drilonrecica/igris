package look

import (
	"strings"
	"testing"
)

// The helm is HelmRows rows of at most HelmWidth cells, each row padded
// to HelmWidth, with a plume and a visor.
func TestHelmLines(t *testing.T) {
	plain := HelmLines(func(_ HelmRole, s string) string { return s })
	if len(plain) != HelmRows || len(Helm) != HelmRows {
		t.Fatalf("%d rows, want %d", len(plain), HelmRows)
	}
	for i, l := range plain {
		if n := len([]rune(l)); n != HelmWidth {
			t.Errorf("row %d is %d cells, want %d: %q", i, n, HelmWidth, l)
		}
	}
	want := []string{" ▄█▄   ▄▄", "▄███▄ ▄▀ ", "▟█████▙▀ ", "█▀▀▀▀▀█  ", "▜█████▛  ", "  ▀███▀  "}
	for i := range want {
		if plain[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, plain[i], want[i])
		}
	}
	roles := map[HelmRole]bool{}
	painted := HelmLines(func(r HelmRole, s string) string {
		roles[r] = true
		return "<" + s + ">"
	})
	if !roles[HelmArmor] || !roles[HelmVisor] || !roles[HelmPlume] {
		t.Errorf("roles drawn: %v, want armor, visor and plume", roles)
	}
	if !strings.Contains(painted[3], "<▀▀▀▀▀>") {
		t.Errorf("the visor row isn't its own part: %q", painted[3])
	}
}
