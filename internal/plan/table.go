// Package plan parses, validates, schedules and surgically rewrites the
// markdown task plan (SPEC §3–§5).
package plan

import (
	"bytes"
	"strings"

	"github.com/drilonrecica/igris/internal/textsafe"
)

// utf8BOM is the UTF-8 byte order mark.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// line is one physical line of the plan file.
type line struct {
	text  string // content without the line ending ("\n" or "\r\n")
	start int    // byte offset of text in the file
	num   int    // 1-based line number
}

// splitLines splits data into lines. Line endings are not part of text but
// stay in the file at their offsets, so splicing within text never touches
// them. A final line without a trailing newline is kept as is. A UTF-8 byte
// order mark at the start of the file (Windows editors write one) is skipped
// the same way: it stays in the file but is not part of the first line.
func splitLines(data []byte) []line {
	var lines []line
	start := 0
	if bytes.HasPrefix(data, utf8BOM) {
		start = len(utf8BOM)
	}
	for num := 1; start < len(data); num++ {
		end := start
		for end < len(data) && data[end] != '\n' {
			end++
		}
		next := end + 1
		if end > start && data[end-1] == '\r' && end < len(data) {
			end-- // CRLF: the CR belongs to the line ending
		}
		lines = append(lines, line{text: string(data[start:end]), start: start, num: num})
		start = next
	}
	return lines
}

// cell is one table cell.
type cell struct {
	value string // trimmed content with \| unescaped to | and control characters removed
	start int    // byte offset of the trimmed content within the line
	end   int    // byte offset just past the trimmed content
}

// hasPipe reports whether s contains an unescaped pipe.
func hasPipe(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++ // the next byte is escaped
		case '|':
			return true
		}
	}
	return false
}

// splitRow splits a table row on unescaped pipes. A backslash escapes the
// byte after it, so `\|` is literal text and `\\|` is a backslash followed
// by a separator. Leading and trailing pipes are optional.
func splitRow(s string) []cell {
	type seg struct{ from, to int }
	var segs []seg
	from := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '|':
			segs = append(segs, seg{from, i})
			from = i + 1
		}
	}
	segs = append(segs, seg{from, len(s)})

	blank := func(sg seg) bool { return strings.TrimSpace(s[sg.from:sg.to]) == "" }
	if len(segs) > 1 && blank(segs[0]) {
		segs = segs[1:]
	}
	if len(segs) > 1 && blank(segs[len(segs)-1]) {
		segs = segs[:len(segs)-1]
	}

	cells := make([]cell, len(segs))
	for i, sg := range segs {
		a, b := sg.from, sg.to
		for a < b && isSpace(s[a]) {
			a++
		}
		for b > a && isSpace(s[b-1]) {
			b--
		}
		if a == b && sg.to > sg.from {
			// Empty cell: anchor after the first padding byte, matching
			// where content would go in "| x |".
			a, b = sg.from+1, sg.from+1
		}
		// Cell text is shown in the TUI and sent to sessions; it never
		// carries escape sequences (the row is a validation error too).
		cells[i] = cell{value: textsafe.Line(strings.ReplaceAll(s[a:b], `\|`, "|")), start: a, end: b}
	}
	return cells
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' }

// isSeparator reports whether cells form a table delimiter row such as
// "|---|:--:|".
func isSeparator(cells []cell) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		v := strings.TrimSuffix(strings.TrimPrefix(c.value, ":"), ":")
		if v == "" || strings.Trim(v, "-") != "" {
			return false
		}
	}
	return true
}

// isTableStart reports whether lines[i] is a table header row, i.e. it has a
// pipe and the next line is a delimiter row with the same number of cells.
func isTableStart(lines []line, i int) bool {
	if i+1 >= len(lines) || !hasPipe(lines[i].text) || !hasPipe(lines[i+1].text) {
		return false
	}
	sep := splitRow(lines[i+1].text)
	return isSeparator(sep) && len(sep) == len(splitRow(lines[i].text))
}
