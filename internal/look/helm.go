package look

import "strings"

// The helm (docs/brand/helm-pixels.txt, SPEC §15.4): the mark and the app
// icon drawn by hand in pixels, for the two brand moments that show it,
// GET STARTED and the end-of-run summary. One letter is one pixel and
// names its color role; a terminal cell holds two pixels stacked, drawn
// with ▀ (the top in the foreground color, the bottom in the background
// color). A surface without color draws the S shape in plain blocks.

// Tagline is what GET STARTED says beside the helm (SPEC §15.6).
const Tagline = "Arise. One task, one fresh session, the right rank."

// The helm's shades beyond the palette: the mark's face, ridges, visor
// core, chin bars and the plume's inner stroke (docs/brand). The outline
// and the visor are Glow, the plume is Plume. The light ground takes the
// light mark's face and visor core, and the ridges and chin bars mixed
// from its Glow as the dark ones are.
var (
	HelmFace        = Shade{"#0A1124", "#0E1730"} // the face's left half
	HelmFaceShadow  = Shade{"#05070F", "#05070F"} // the face's right half
	HelmRidge       = Shade{"#236C92", "#0C4F7C"}
	HelmVisorCore   = Shade{"#B8F0FF", "#8FE3FF"}
	HelmChin        = Shade{"#32A3D3", "#0B70AA"}
	HelmPlumeStroke = Shade{"#F05A6E", "#F05A6E"}
)

// HelmL is the mark, 24 × 26 pixels (24 × 13 cells): glow outline, the
// navy face split by a ridge, side ridges, the V-visor with its bright
// core, three chin bars, and the plume with its pink stroke.
var HelmL = []string{
	"...........RRRR.........",
	"........OOORRRRRR.......",
	"......OOnrmOO.RRRRR.....",
	"....OOnnnrmmmOOpRRRRR...",
	"..OOnnnnnrmmmmmOOpRRRR..",
	".OnnnnnnnrmmmmmmmO.pRRR.",
	"OnnrnnnnnrmmmmmrmmO.pRRR",
	"OnnrnnnnnrmmmmmrmmO.pRRR",
	"OnnnrnnnnrmmmmrmmmO.pRRR",
	"OnnnrnnnnrmmmmrmmmO.pRRR",
	"OnnnnrnnnrmmmrmmmmO.p.RR",
	"OVVVnnnnnrmmmmmVVVO.p.RR",
	"OCCCVVVnnrmmVVVCCCO.p..R",
	"OVVVCCCVVVVVCCCVVVO....R",
	"OnnnVVVCCVCCVVVmmmO.....",
	"OnnnnnnVVCVVmmmmmmO.....",
	"OnnnnnnnnVmmmmmmmmO.....",
	".OnnnnnnnrmmmmmmmO......",
	".OnbbnnnnrmmmmbbmO......",
	"..OnnbbbbbbbbbmmO.......",
	"..OnbbnnnrmmmbbmO.......",
	"...OnnbbbbbbbmmO........",
	"...OnnnnnrmmmmmO........",
	"....OnbbbbbbbmO.........",
	".....OOnnrmmOO..........",
	".......OOOOO............",
}

// HelmS is the app icon, 19 × 12 pixels (19 × 6 cells): the visor and the
// plume's arc on a dark rounded tile.
var HelmS = []string{
	".nnnnnnnnnnnnnnnnn.",
	"nnnnnnnnnnnnRRRnnnn",
	"nnnnnnnnnnRRRRRRnnn",
	"nnnnnnnnnRRRnnnnnnn",
	"nnnnnnnnnnnnnnnnnnn",
	"nnVVVVnnnnnnnVVVVnn",
	"nnVCCCVVVnVVVCCCVnn",
	"nnnVVVCCCVCCCVVVnnn",
	"nnnnnnVVVCVVVnnnnnn",
	"nnnnnnnnVVVnnnnnnnn",
	"nnnnnnnnnVnnnnnnnnn",
	".nnnnnnnnnnnnnnnnn.",
}

// HelmShade is the shade a helm letter is drawn in; false for '.', the
// terminal's own background.
func HelmShade(letter byte) (Shade, bool) {
	switch letter {
	case 'O', 'V':
		return Glow, true
	case 'n':
		return HelmFace, true
	case 'm':
		return HelmFaceShadow, true
	case 'r':
		return HelmRidge, true
	case 'C':
		return HelmVisorCore, true
	case 'b':
		return HelmChin, true
	case 'R':
		return Plume, true
	case 'p':
		return HelmPlumeStroke, true
	}
	return Shade{}, false
}

// HelmCell is one terminal cell of a helm: its top and bottom pixels.
type HelmCell struct{ Top, Bottom byte }

// HelmCells is grid as rows of cells, two pixel rows to one.
func HelmCells(grid []string) [][]HelmCell {
	out := make([][]HelmCell, 0, (len(grid)+1)/2)
	for y := 0; y < len(grid); y += 2 {
		row := make([]HelmCell, len(grid[y]))
		for x := range row {
			row[x] = HelmCell{grid[y][x], '.'}
			if y+1 < len(grid) {
				row[x].Bottom = grid[y+1][x]
			}
		}
		out = append(out, row)
	}
	return out
}

// HelmPlain is the S shape without color: HelmS's visor and plume pixels
// as blocks, every other pixel blank, without the tile. Every line is
// HelmS's width.
func HelmPlain() []string {
	lit := func(p byte) bool { return p == 'V' || p == 'C' || p == 'R' }
	var out []string
	for _, row := range HelmCells(HelmS) {
		var b strings.Builder
		for _, c := range row {
			switch top, bottom := lit(c.Top), lit(c.Bottom); {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		out = append(out, b.String())
	}
	return out
}

// HelmWidth is a grid's width in cells, and HelmRows its height.
func HelmWidth(grid []string) int { return len(grid[0]) }
func HelmRows(grid []string) int  { return (len(grid) + 1) / 2 }

// The screens the helm is drawn on (SPEC §15.4): L from HelmLargeWidth
// columns by HelmLargeHeight rows, S from HelmMinWidth columns, nothing
// below.
const (
	HelmLargeWidth  = 80
	HelmLargeHeight = 32
	HelmMinWidth    = 40
)

// HelmFor is the grid for a screen of w columns by h rows, with room rows
// left for it: L on a large screen, S on a smaller or shorter one, nil
// below HelmMinWidth columns or when not even S fits the room. Without
// color it is always S, drawn as HelmPlain.
func HelmFor(w, h, room int, color bool) []string {
	if w < HelmMinWidth {
		return nil
	}
	if color && w >= HelmLargeWidth && h >= HelmLargeHeight && room >= HelmRows(HelmL) {
		return HelmL
	}
	if room >= HelmRows(HelmS) {
		return HelmS
	}
	return nil
}
