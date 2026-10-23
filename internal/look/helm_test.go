package look

import (
	"os"
	"strings"
	"testing"
)

// The grids are docs/brand/helm-pixels.txt, pixel for pixel: the
// reference is drawn exactly.
func TestHelmGridsAreTheReference(t *testing.T) {
	b, err := os.ReadFile("../../docs/brand/helm-pixels.txt")
	if err != nil {
		t.Fatal(err)
	}
	ref := map[string][]string{}
	section := ""
	for _, l := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(l, "## L "):
			section = "L"
		case strings.HasPrefix(l, "## S-tile "):
			section = "S"
		case strings.HasPrefix(l, "    ") && section != "":
			ref[section] = append(ref[section], strings.TrimSpace(l))
		case strings.TrimSpace(l) != "" && section != "":
			section = ""
		}
	}
	for name, grid := range map[string][]string{"L": HelmL, "S": HelmS} {
		if strings.Join(grid, "\n") != strings.Join(ref[name], "\n") {
			t.Errorf("%s isn't the reference:\n%s\nwant\n%s", name, strings.Join(grid, "\n"), strings.Join(ref[name], "\n"))
		}
	}
	if HelmWidth(HelmL) != 24 || HelmRows(HelmL) != 13 || HelmWidth(HelmS) != 19 || HelmRows(HelmS) != 6 {
		t.Errorf("L is %d×%d cells, S %d×%d; want 24×13 and 19×6", HelmWidth(HelmL), HelmRows(HelmL), HelmWidth(HelmS), HelmRows(HelmS))
	}
}

// Every pixel is a letter with a shade, or '.', and every row of a grid is
// as wide as the first.
func TestHelmLettersHaveShades(t *testing.T) {
	for _, grid := range [][]string{HelmL, HelmS} {
		for y, row := range grid {
			if len(row) != len(grid[0]) {
				t.Errorf("row %d is %d pixels, want %d", y, len(row), len(grid[0]))
			}
			for x := range len(row) {
				if _, ok := HelmShade(row[x]); !ok && row[x] != '.' {
					t.Errorf("pixel %d,%d is %q, which has no shade", x, y, row[x])
				}
			}
		}
	}
	if s, _ := HelmShade('R'); s != Plume {
		t.Errorf("the plume is %v, want Plume", s)
	}
	if s, _ := HelmShade('O'); s != Glow {
		t.Errorf("the outline is %v, want Glow", s)
	}
}

// Two pixel rows make one row of cells.
func TestHelmCells(t *testing.T) {
	got := HelmCells([]string{"aR", ".b", "c."})
	want := [][]HelmCell{{{'a', '.'}, {'R', 'b'}}, {{'c', '.'}, {'.', '.'}}}
	if len(got) != len(want) {
		t.Fatalf("%d rows, want %d", len(got), len(want))
	}
	for i := range want {
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Errorf("cell %d,%d = %+v, want %+v", j, i, got[i][j], want[i][j])
			}
		}
	}
}

// Without color the S shape is the visor and the plume in blocks, without
// the tile.
func TestHelmPlain(t *testing.T) {
	want := []string{
		"            ▄▄▄    ",
		"         ▄██▀▀▀▀   ",
		"  ▄▄▄▄       ▄▄▄▄  ",
		"  ▀██████▄██████▀  ",
		"      ▀▀███▀▀      ",
		"         ▀         ",
	}
	got := HelmPlain()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("plain helm:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// L on a large screen, S from 40 columns and on a shorter one or where L
// doesn't fit, none below 40; without color always S.
func TestHelmFor(t *testing.T) {
	cases := []struct {
		w, h, room int
		color      bool
		want       []string
	}{
		{120, 40, 20, true, HelmL},
		{80, 32, 13, true, HelmL},
		{79, 40, 20, true, HelmS},
		{120, 31, 20, true, HelmS},
		{120, 40, 12, true, HelmS},
		{120, 40, 20, false, HelmS},
		{40, 20, 10, true, HelmS},
		{40, 20, 5, true, nil},
		{39, 40, 20, true, nil},
	}
	for _, c := range cases {
		got := HelmFor(c.w, c.h, c.room, c.color)
		if len(got) != len(c.want) {
			t.Errorf("HelmFor(%d, %d, %d, %v) is %d pixel rows, want %d", c.w, c.h, c.room, c.color, len(got), len(c.want))
		}
	}
}
