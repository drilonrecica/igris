// Package qr encodes a QR code (ISO/IEC 18004) for `igris serve token`'s
// pairing URL (SPEC §14): byte mode, error correction level M, versions 1–10,
// drawn with half blocks. Standard library only (decisions.md V16-P1).
package qr

import (
	"errors"
	"fmt"
	"strings"
)

// MaxBytes is the most data a code holds: version 10 at level M.
const MaxBytes = 213

// ErrTooLong is returned for data beyond MaxBytes.
var ErrTooLong = errors.New("qr: data too long for version 10")

// Code is an encoded symbol: Size×Size modules.
type Code struct {
	Version int
	Mask    int
	Size    int
	dark    []bool
}

// Dark reports whether the module in column x, row y is dark; outside the
// symbol (the quiet zone) it is light.
func (c *Code) Dark(x, y int) bool {
	if x < 0 || y < 0 || x >= c.Size || y >= c.Size {
		return false
	}
	return c.dark[y*c.Size+x]
}

// quiet is the light border around a symbol, in modules (ISO/IEC 18004 §6.3.8).
const quiet = 4

// HalfBlocks draws the code with its quiet zone, two module rows per line:
// '█' both dark, '▀' top dark, '▄' bottom dark, ' ' both light. The glyphs
// are ink, so the lines scan with a dark foreground on a light background
// (the caller sets the colours; a dark terminal's defaults show a negative).
func (c *Code) HalfBlocks() []string {
	var lines []string
	for y := -quiet; y < c.Size+quiet; y += 2 {
		var b strings.Builder
		for x := -quiet; x < c.Size+quiet; x++ {
			switch top, bot := c.Dark(x, y), c.Dark(x, y+1); {
			case top && bot:
				b.WriteRune('█')
			case top:
				b.WriteRune('▀')
			case bot:
				b.WriteRune('▄')
			default:
				b.WriteByte(' ')
			}
		}
		lines = append(lines, b.String())
	}
	return lines
}

// Encode returns data as a code of the smallest version that holds it, with
// the mask of the lowest penalty.
func Encode(data []byte) (*Code, error) {
	return encode(data, -1)
}

// encode is Encode with the mask fixed when mask is 0–7.
func encode(data []byte, mask int) (*Code, error) {
	for v := 1; v <= 10; v++ {
		if len(data) <= capacity(v) {
			return build(v, interleave(v, dataCodewords(v, data)), mask), nil
		}
	}
	return nil, fmt.Errorf("%w: %d bytes, at most %d", ErrTooLong, len(data), MaxBytes)
}

// blocks is a row of ISO/IEC 18004 Table 9 for level M: ec check codewords
// per block, n1 blocks of d1 data codewords, then n2 blocks of d1+1.
type blocks struct{ ec, n1, d1, n2 int }

var levelM = [11]blocks{
	1:  {10, 1, 16, 0},
	2:  {16, 1, 28, 0},
	3:  {26, 1, 44, 0},
	4:  {18, 2, 32, 0},
	5:  {24, 2, 43, 0},
	6:  {16, 4, 27, 0},
	7:  {18, 4, 31, 0},
	8:  {22, 2, 38, 2},
	9:  {22, 3, 36, 2},
	10: {26, 4, 43, 1},
}

// dataLen is the number of data codewords of version v at level M.
func dataLen(v int) int {
	b := levelM[v]
	return b.n1*b.d1 + b.n2*(b.d1+1)
}

// countBits is the width of byte mode's character count (Table 3).
func countBits(v int) int {
	if v <= 9 {
		return 8
	}
	return 16
}

// capacity is the most bytes version v holds: the data codewords less the
// mode indicator (4 bits) and the character count.
func capacity(v int) int {
	return (dataLen(v)*8 - 4 - countBits(v)) / 8
}

// bitWriter appends bits most significant first.
type bitWriter struct {
	buf []byte
	n   int
}

func (w *bitWriter) write(val uint, nbits int) {
	for i := nbits - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.buf = append(w.buf, 0)
		}
		if val>>uint(i)&1 == 1 {
			w.buf[w.n/8] |= 0x80 >> uint(w.n%8)
		}
		w.n++
	}
}

// dataCodewords is data in byte mode (§7.4.5) with the terminator, padding
// to a byte, and pad codewords 0xEC 0x11 up to version v's data length
// (§7.4.10). The caller checked that data fits.
func dataCodewords(v int, data []byte) []byte {
	total := dataLen(v) * 8
	var w bitWriter
	w.write(0b0100, 4)
	w.write(uint(len(data)), countBits(v))
	for _, b := range data {
		w.write(uint(b), 8)
	}
	w.write(0, min(4, total-w.n))
	if r := w.n % 8; r != 0 {
		w.write(0, 8-r)
	}
	for pad := byte(0xEC); w.n < total; pad ^= 0xEC ^ 0x11 {
		w.write(uint(pad), 8)
	}
	return w.buf
}

// interleave splits data into version v's blocks, adds each block's
// Reed-Solomon check codewords and interleaves them (§7.6): the data
// codewords column by column across the blocks, then the check codewords.
func interleave(v int, data []byte) []byte {
	b := levelM[v]
	var dblocks, eblocks [][]byte
	for i := 0; i < b.n1+b.n2; i++ {
		n := b.d1
		if i >= b.n1 {
			n++
		}
		dblocks = append(dblocks, data[:n])
		eblocks = append(eblocks, rsCheck(data[:n], b.ec))
		data = data[n:]
	}
	var out []byte
	for i := 0; i <= b.d1; i++ {
		for _, d := range dblocks {
			if i < len(d) {
				out = append(out, d[i])
			}
		}
	}
	for i := 0; i < b.ec; i++ {
		for _, e := range eblocks {
			out = append(out, e[i])
		}
	}
	return out
}

// GF(256) with the field polynomial x⁸+x⁴+x³+x²+1 (0x11D, §7.5.2).
var gfExp, gfLog = func() (exp [510]byte, log [256]byte) {
	x := 1
	for i := 0; i < 255; i++ {
		exp[i], exp[i+255] = byte(x), byte(x)
		log[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	return exp, log
}()

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// rsGenerator is the product of (x − αⁱ) for i < n, highest degree first,
// without its leading 1.
func rsGenerator(n int) []byte {
	g := []byte{1}
	for i := 0; i < n; i++ {
		next := make([]byte, len(g)+1)
		for j, c := range g {
			next[j] ^= c
			next[j+1] ^= gfMul(c, gfExp[i])
		}
		g = next
	}
	return g[1:]
}

// rsCheck is the n check codewords of data: the remainder of data·xⁿ
// divided by the generator polynomial.
func rsCheck(data []byte, n int) []byte {
	gen := rsGenerator(n)
	rem := make([]byte, n)
	for _, d := range data {
		f := d ^ rem[0]
		copy(rem, rem[1:])
		rem[n-1] = 0
		for i, g := range gen {
			rem[i] ^= gfMul(g, f)
		}
	}
	return rem
}

// alignment is the row and column centres of the alignment patterns
// (Annex E, Table E.1).
var alignment = [11][]int{
	2:  {6, 18},
	3:  {6, 22},
	4:  {6, 26},
	5:  {6, 30},
	6:  {6, 34},
	7:  {6, 22, 38},
	8:  {6, 24, 42},
	9:  {6, 26, 46},
	10: {6, 28, 50},
}

// matrix is a symbol under construction: the modules and which of them
// are function patterns (not data).
type matrix struct {
	size     int
	dark     []bool
	function []bool
}

func (m *matrix) set(x, y int, dark bool) {
	m.dark[y*m.size+x] = dark
	m.function[y*m.size+x] = true
}

func (m *matrix) isFunction(x, y int) bool { return m.function[y*m.size+x] }

// build lays out version v's function patterns and codewords, masks the
// data with mask (0–7), or with each mask and keeps the lowest penalty
// (§7.8.3) when mask is out of range, then writes the format and version
// information.
func build(v int, codewords []byte, mask int) *Code {
	size := 17 + 4*v
	m := &matrix{size: size, dark: make([]bool, size*size), function: make([]bool, size*size)}
	m.functionPatterns(v)
	m.placeData(codewords)
	if mask < 0 || mask > 7 {
		best := -1
		for k := 0; k < 8; k++ {
			if p := m.masked(v, k).penalty(); best < 0 || p < best {
				best, mask = p, k
			}
		}
	}
	return &Code{Version: v, Mask: mask, Size: size, dark: m.masked(v, mask).dark}
}

// functionPatterns draws the finder patterns with their separators, the
// timing patterns, the alignment patterns and the dark module, and reserves
// the format and version information areas (§6.3).
func (m *matrix) functionPatterns(v int) {
	n := m.size
	for _, c := range [][2]int{{3, 3}, {n - 4, 3}, {3, n - 4}} {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := c[0]+dx, c[1]+dy
				if x < 0 || y < 0 || x >= n || y >= n {
					continue
				}
				d := max(abs(dx), abs(dy))
				m.set(x, y, d != 2 && d != 4)
			}
		}
	}
	for i := 8; i < n-8; i++ {
		m.set(i, 6, i%2 == 0)
		m.set(6, i, i%2 == 0)
	}
	pos := alignment[v]
	last := len(pos) - 1
	for i, cy := range pos {
		for j, cx := range pos {
			if i == 0 && (j == 0 || j == last) || i == last && j == 0 { // a finder's corner
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					m.set(cx+dx, cy+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	m.format(0) // reserves; build overwrites with the real bits
	m.set(8, n-8, true)
	if v >= 7 {
		m.version(v)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// bch is the remainder of value·2^(deg poly) divided by poly, appended to
// value: the format (§7.9) and version (§7.10) information codes.
func bch(value, poly int) int {
	deg := 0
	for p := poly; p > 1; p >>= 1 {
		deg++
	}
	r := value << deg
	for i := 2*deg + 1; i >= deg; i-- { // wide enough for value's bits
		if r>>i&1 == 1 {
			r ^= poly << (i - deg)
		}
	}
	return value<<deg | r
}

// formatBits is the 15-bit format information for level M (00) and mask.
func formatBits(mask int) int {
	return bch(0b00<<3|mask, 0x537) ^ 0x5412
}

// format writes mask's format information in both of its places (§7.9.1,
// Figure 25): bit 14 is the most significant.
func (m *matrix) format(mask int) {
	n := m.size
	f := formatBits(mask)
	bit := func(i int) bool { return f>>i&1 == 1 }
	for i := 0; i < 15; i++ {
		// Around the top-left finder: bits 0–7 down column 8 skipping
		// the timing row, then bits 8–14 leftwards along row 8 skipping
		// the timing column.
		switch {
		case i < 6:
			m.set(8, i, bit(i))
		case i < 8:
			m.set(8, i+1, bit(i))
		case i == 8:
			m.set(7, 8, bit(i))
		default:
			m.set(14-i, 8, bit(i))
		}
		// The copy: bits 0–7 leftwards along row 8 from the right edge,
		// bits 8–14 down column 8 to the bottom.
		if i < 8 {
			m.set(n-1-i, 8, bit(i))
		} else {
			m.set(8, n-15+i, bit(i))
		}
	}
}

// version writes the 18-bit version information for v ≥ 7 below the
// top-right finder and right of the bottom-left one (§7.10, Annex D).
func (m *matrix) version(v int) {
	n := m.size
	bits := bch(v, 0x1F25)
	for i := 0; i < 18; i++ {
		d := bits>>i&1 == 1
		a, b := i/3, n-11+i%3
		m.set(a, b, d)
		m.set(b, a, d)
	}
}

// placeData writes codewords into the non-function modules in the two-column
// zigzag from the bottom-right corner (§7.7.3); remainder modules stay light.
func (m *matrix) placeData(codewords []byte) {
	n := m.size
	i := 0
	up := true
	for right := n - 1; right >= 1; right -= 2 {
		if right == 6 { // the vertical timing pattern
			right = 5
		}
		for k := 0; k < n; k++ {
			y := k
			if up {
				y = n - 1 - k
			}
			for x := right; x >= right-1; x-- {
				if m.isFunction(x, y) {
					continue
				}
				if i < len(codewords)*8 {
					m.dark[y*n+x] = codewords[i/8]>>(7-uint(i%8))&1 == 1
				}
				i++
			}
		}
		up = !up
	}
}

// maskFuncs are the data mask conditions of Table 10, by column x and row y.
var maskFuncs = [8]func(x, y int) bool{
	func(x, y int) bool { return (y+x)%2 == 0 },
	func(x, y int) bool { return y%2 == 0 },
	func(x, y int) bool { return x%3 == 0 },
	func(x, y int) bool { return (y+x)%3 == 0 },
	func(x, y int) bool { return (y/2+x/3)%2 == 0 },
	func(x, y int) bool { return y*x%2+y*x%3 == 0 },
	func(x, y int) bool { return (y*x%2+y*x%3)%2 == 0 },
	func(x, y int) bool { return ((y+x)%2+y*x%3)%2 == 0 },
}

// masked is a copy of m with mask applied to the data modules and mask's
// format information written.
func (m *matrix) masked(v, mask int) *matrix {
	c := &matrix{size: m.size, dark: append([]bool(nil), m.dark...), function: append([]bool(nil), m.function...)}
	for y := 0; y < c.size; y++ {
		for x := 0; x < c.size; x++ {
			if !c.isFunction(x, y) && maskFuncs[mask](x, y) {
				c.dark[y*c.size+x] = !c.dark[y*c.size+x]
			}
		}
	}
	c.format(mask)
	return c
}

// penalty scores a masked symbol by the four rules of §7.8.3.1 (Table 11);
// the lowest score is the mask to use.
func (m *matrix) penalty() int {
	n := m.size
	at := func(x, y int) bool { return m.dark[y*n+x] }
	p := 0
	// Rule 1: runs of five or more same-colour modules in a row or column,
	// N1 = 3 plus one per module beyond five.
	// Rule 3: each 1:1:3:1:1 finder-like pattern, N3 = 40. Later editions
	// also ask for four light modules on one side; without that condition
	// the score picks the mask of the standard's worked example (010).
	for line := 0; line < n; line++ {
		for _, get := range []func(i int) bool{
			func(i int) bool { return at(i, line) },
			func(i int) bool { return at(line, i) },
		} {
			run := 1
			for i := 1; i <= n; i++ {
				if i < n && get(i) == get(i-1) {
					run++
					continue
				}
				if run >= 5 {
					p += 3 + run - 5
				}
				run = 1
			}
			for i := 0; i+7 <= n; i++ {
				if get(i) && !get(i+1) && get(i+2) && get(i+3) && get(i+4) && !get(i+5) && get(i+6) {
					p += 40
				}
			}
		}
	}
	// Rule 2: each 2×2 block of one colour, N2 = 3.
	for y := 0; y+1 < n; y++ {
		for x := 0; x+1 < n; x++ {
			c := at(x, y)
			if at(x+1, y) == c && at(x, y+1) == c && at(x+1, y+1) == c {
				p += 3
			}
		}
	}
	// Rule 4: N4 = 10 per full 5% the dark proportion deviates from 50%.
	dark := 0
	for _, d := range m.dark {
		if d {
			dark++
		}
	}
	p += abs(dark*20-n*n*10) / (n * n) * 10
	return p
}
