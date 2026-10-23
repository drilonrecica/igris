package look

import "strings"

// The helm (docs/brand, SPEC §15.4): the shadow knight's helm in half
// blocks, six rows, for the two brand moments that show it, GET STARTED
// and the end-of-run summary. The armor is drawn dim, the visor in glow,
// the plume in plume; a surface without color writes the blocks plain.

// Tagline is what GET STARTED says beside the helm (SPEC §15.6).
const Tagline = "Arise. One task, one fresh session, the right rank."

// HelmRole is what a part of the helm is drawn as.
type HelmRole int

// The helm's parts.
const (
	HelmArmor HelmRole = iota // dim
	HelmVisor                 // glow: the seam across the face
	HelmPlume                 // plume: the brand's one other red
)

// HelmPart is a run of cells of one role.
type HelmPart struct {
	Text string
	Role HelmRole
}

// Helm is the helm, row by row, as parts; HelmLines draws it.
var Helm = [][]HelmPart{
	{{" ▄█▄   ", HelmArmor}, {"▄▄", HelmPlume}},
	{{"▄███▄ ", HelmArmor}, {"▄▀", HelmPlume}},
	{{"▟█████▙", HelmArmor}, {"▀", HelmPlume}},
	{{"█", HelmArmor}, {"▀▀▀▀▀", HelmVisor}, {"█", HelmArmor}},
	{{"▜█████▛", HelmArmor}},
	{{"  ▀███▀", HelmArmor}},
}

// HelmWidth is the helm's width in cells, and HelmRows its height.
const (
	HelmWidth = 9
	HelmRows  = 6
)

// HelmMinWidth is the narrowest screen the helm is drawn on (SPEC §15.4):
// below it the helm is dropped and the words stay.
const HelmMinWidth = 60

// HelmLines draws the helm as HelmRows lines, each part painted by paint
// (the identity for plain text). Every line is padded to HelmWidth cells
// of plain text, so the helm sits in a column.
func HelmLines(paint func(HelmRole, string) string) []string {
	out := make([]string, 0, len(Helm))
	for _, row := range Helm {
		var b strings.Builder
		width := 0
		for _, p := range row {
			b.WriteString(paint(p.Role, p.Text))
			width += len([]rune(p.Text)) // every block is one cell wide
		}
		b.WriteString(strings.Repeat(" ", max(HelmWidth-width, 0)))
		out = append(out, b.String())
	}
	return out
}
