package look

import (
	"strings"

	"github.com/rivo/uniseg"
)

// Width is how many terminal cells s takes.
func Width(s string) int { return uniseg.StringWidth(s) }

// Cut shortens s to at most w cells, ending it with "…" when something
// was cut; w below 1 is "".
func Cut(s string, w int) string {
	if Width(s) <= w {
		return s
	}
	if w < 1 {
		return ""
	}
	var b strings.Builder
	used := 0
	state := -1
	for rest := s; rest != ""; {
		var g string
		var gw int
		g, rest, gw, state = uniseg.FirstGraphemeClusterInString(rest, state)
		if used+gw > w-1 {
			break
		}
		b.WriteString(g)
		used += gw
	}
	return b.String() + "…"
}

// Cell is one cell of a table: its text and how loud it is drawn. Span
// is how many bytes from the start get the severity (a glyph and its
// word); 0 paints the whole text.
type Cell struct {
	Text string
	Sev  Severity
	Span int
}

// TextCell is a cell drawn plain.
func TextCell(s string) Cell { return Cell{Text: s} }

// Mark is a cell led by a glyph drawn with sev: "✓ M1-02".
func Mark(sev Severity, glyph, text string) Cell {
	if glyph == "" {
		return Cell{Text: text}
	}
	return Cell{Text: glyph + " " + text, Sev: sev, Span: len(glyph)}
}

// Painter draws a piece of text with a severity: Paint for a terminal
// with color, the text as it is without.
type Painter func(sev Severity, s string) string

// Draw is the cell's text with its severity painted.
func (c Cell) Draw(p Painter) string {
	if c.Sev == Plain || p == nil || c.Text == "" {
		return c.Text
	}
	n := c.Span
	if n <= 0 || n > len(c.Text) {
		n = len(c.Text)
	}
	return p(c.Sev, c.Text[:n]) + c.Text[n:]
}

// cut is the cell shortened to w cells; the painted span shrinks with it.
func (c Cell) cut(w int) Cell {
	s := Cut(c.Text, w)
	if s == c.Text {
		return c
	}
	if c.Span > 0 && c.Span > len(s) {
		c.Span = len(s)
	}
	c.Text = s
	return c
}

// Empty is what an empty cell shows (SPEC §14).
const Empty = "—"

// Table is the one table style of the text output (SPEC §14): a header
// row in capitals, two spaces between columns, no trailing spaces, Empty
// for an empty cell. Fitted to a width it cuts long cells with "…" and
// folds the last column onto a line of its own first, when Fold is set.
type Table struct {
	Indent string
	Header []string
	// Fold lets the last column move onto a second line under its row,
	// as "<header in lower case> <text>", when the table is too wide. Its
	// empty cells stay blank instead of showing Empty.
	Fold bool
	rows []tableRow
}

type tableRow struct {
	cells []Cell
	line  *Cell // a line of free text in place of the cells
}

// Row adds a row of cells; missing cells are empty.
func (t *Table) Row(cells ...Cell) { t.rows = append(t.rows, tableRow{cells: cells}) }

// Line adds a line of free text between the rows, indented like them; it
// takes no part in the columns' widths and is cut to the width.
func (t *Table) Line(c Cell) { t.rows = append(t.rows, tableRow{line: &c}) }

// Len is the number of rows and lines.
func (t *Table) Len() int { return len(t.rows) }

// Lines renders the table fitted to width cells (0: no fitting), its
// cells painted by p (nil: plain).
func (t *Table) Lines(width int, p Painter) []string {
	n := len(t.Header)
	cell := func(r tableRow, i int) Cell {
		var c Cell
		if i < len(r.cells) {
			c = r.cells[i]
		}
		c.Text = strings.TrimRight(c.Text, " ")
		if folds := t.Fold && i == n-1; c.Text == "" && !folds {
			c = TextCell(Empty)
		}
		return c
	}
	widths := make([]int, n)
	for i, h := range t.Header {
		widths[i] = Width(h)
	}
	for _, r := range t.rows {
		if r.line != nil {
			continue
		}
		for i := range n {
			widths[i] = max(widths[i], Width(cell(r, i).Text))
		}
	}
	cols := n // the columns drawn in the row; a folded last one is not
	total := func() int {
		w := Width(t.Indent)
		for i := range cols {
			w += widths[i]
		}
		return w + 2*(cols-1)
	}
	if width > 0 && t.Fold && n > 1 && total() > width {
		cols = n - 1
	}
	// Shorten the widest column a cell at a time until the table fits:
	// first down to its header, then down to a few cells.
	const least = 4
	for _, floor := range []func(i int) int{
		func(i int) int { return max(least, Width(t.Header[i])) },
		func(int) int { return least },
	} {
		for width > 0 && total() > width {
			widest := -1
			for i := range cols {
				if widths[i] > floor(i) && (widest < 0 || widths[i] > widths[widest]) {
					widest = i
				}
			}
			if widest < 0 {
				break
			}
			widths[widest]--
		}
	}

	draw := func(cells []Cell) string {
		var b strings.Builder
		b.WriteString(t.Indent)
		for i := range cols {
			c := cells[i].cut(widths[i])
			b.WriteString(c.Draw(p))
			if i < cols-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-Width(c.Text)+2))
			}
		}
		return strings.TrimRight(b.String(), " ")
	}
	header := make([]Cell, n)
	for i, h := range t.Header {
		header[i] = TextCell(h)
	}
	// A table of free lines only has no header.
	var out []string
	for _, r := range t.rows {
		if r.line == nil {
			out = []string{draw(header)}
			break
		}
	}
	// A folded column goes under the second column, where the row's words
	// start.
	foldAt := Width(t.Indent)
	if cols > 0 {
		foldAt += widths[0] + 2
	}
	for _, r := range t.rows {
		if r.line != nil {
			c := *r.line
			if width > 0 {
				c = c.cut(width - Width(t.Indent))
			}
			out = append(out, strings.TrimRight(t.Indent+c.Draw(p), " "))
			continue
		}
		cells := make([]Cell, n)
		for i := range n {
			cells[i] = cell(r, i)
		}
		out = append(out, draw(cells))
		if cols < n && cells[n-1].Text != "" {
			c := cells[n-1]
			label := strings.ToLower(t.Header[n-1]) + " "
			c.Text, c.Span = label+c.Text, 0
			if width > 0 {
				c = c.cut(width - foldAt)
			}
			out = append(out, strings.Repeat(" ", foldAt)+c.Draw(p))
		}
	}
	return out
}

// String is the table unfitted and plain, one line per row.
func (t *Table) String() string { return strings.Join(t.Lines(0, nil), "\n") + "\n" }
