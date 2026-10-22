package qr

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The reference matrices under testdata were generated once, outside this
// module, by two independent encoders: rsc.io/qr/coding v0.2.0 at a fixed
// mask (rsc-*.txt) and github.com/boombuler/barcode/qr, which picks the
// mask by penalty (boombuler-*.txt). Each holds `data HEX`, a `version V`
// line (with `mask M` for rsc) and the symbol, '#' dark and '.' light. The
// inputs are synthetic: a pairing-URL-like string or a byte pattern, at the
// shortest and longest length of each version 1–10.

type reference struct {
	data    []byte
	version int
	mask    int // -1 when the reference chose it
	rows    []string
}

func readReference(t *testing.T, path string) reference {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	ref := reference{mask: -1}
	for len(lines) > 0 && !strings.ContainsAny(lines[0][:1], "#.") {
		f := strings.Fields(lines[0])
		lines = lines[1:]
		for i := 0; i+1 < len(f); i += 2 {
			switch f[i] {
			case "data":
				if ref.data, err = hex.DecodeString(f[i+1]); err != nil {
					t.Fatalf("%s: %v", path, err)
				}
			case "version":
				ref.version, _ = strconv.Atoi(f[i+1])
			case "mask":
				ref.mask, _ = strconv.Atoi(f[i+1])
			}
		}
	}
	ref.rows = lines
	return ref
}

func rows(c *Code) []string {
	var out []string
	for y := 0; y < c.Size; y++ {
		var b strings.Builder
		for x := 0; x < c.Size; x++ {
			if c.Dark(x, y) {
				b.WriteByte('#')
			} else {
				b.WriteByte('.')
			}
		}
		out = append(out, b.String())
	}
	return out
}

func compare(t *testing.T, name string, got *Code, ref reference) {
	t.Helper()
	if got.Version != ref.version {
		t.Fatalf("%s: version %d, reference %d", name, got.Version, ref.version)
	}
	if ref.mask >= 0 && got.Mask != ref.mask {
		t.Fatalf("%s: mask %d, reference %d", name, got.Mask, ref.mask)
	}
	g := rows(got)
	if len(g) != len(ref.rows) {
		t.Fatalf("%s: %d rows, reference %d", name, len(g), len(ref.rows))
	}
	for y := range g {
		if g[y] != ref.rows[y] {
			t.Errorf("%s: row %d (mask %d)\n got %s\nwant %s", name, y, got.Mask, g[y], ref.rows[y])
		}
	}
}

// The worked example of ISO/IEC 18004 Annex I: "01234567" as 1-M. Its data
// codewords are in numeric mode, which Encode never makes, so the example
// checks the Reed-Solomon codewords the standard prints and then the
// symbol built from them with its mask 010.
func TestISOWorkedExample(t *testing.T) {
	data := []byte{0x10, 0x20, 0x0C, 0x56, 0x61, 0x80, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11}
	wantEC := []byte{0xA5, 0x24, 0xD4, 0xC1, 0xED, 0x36, 0xC7, 0x87, 0x2C, 0x55}
	ec := rsCheck(data, 10)
	if !bytes.Equal(ec, wantEC) {
		t.Fatalf("check codewords % X, the standard's % X", ec, wantEC)
	}
	if cw := interleave(1, data); !bytes.Equal(cw, append(data, wantEC...)) {
		t.Fatalf("interleave(1) = % X", cw)
	}
	ref := readReference(t, filepath.Join("testdata", "iso-18004-annex.txt"))
	compare(t, "fixed mask", build(1, append(data, wantEC...), 2), ref)
	compare(t, "chosen mask", build(1, append(data, wantEC...), -1), ref)
}

func TestReferenceFixedMask(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "rsc-*.txt"))
	if len(files) != 20 {
		t.Fatalf("%d rsc references, want 20", len(files))
	}
	for _, f := range files {
		ref := readReference(t, f)
		c, err := encode(ref.data, ref.mask)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		compare(t, filepath.Base(f), c, ref)
	}
}

// boombuler picks its mask with its own reading of the penalty rules, so
// its symbols are compared at the mask their format information names.
func TestReferenceOtherEncoder(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "boombuler-*.txt"))
	if len(files) != 20 {
		t.Fatalf("%d boombuler references, want 20", len(files))
	}
	for _, f := range files {
		ref := readReference(t, f)
		ref.mask = refMask(t, f, ref)
		c, err := encode(ref.data, ref.mask)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		compare(t, filepath.Base(f), c, ref)
	}
}

// refMask reads a reference symbol's mask from the copy of its format
// information along row 8 at the right edge (bits 0–7).
func refMask(t *testing.T, name string, ref reference) int {
	t.Helper()
	row, low := ref.rows[8], 0
	for i := 0; i < 8; i++ {
		if row[len(row)-1-i] == '#' {
			low |= 1 << i
		}
	}
	for mask := 0; mask < 8; mask++ {
		if formatBits(mask)&0xFF == low {
			return mask
		}
	}
	t.Fatalf("%s: no level-M mask has format bits %#x", name, low)
	return 0
}

func TestCapacity(t *testing.T) {
	// ISO/IEC 18004 Table 7, byte mode, level M.
	want := []int{0, 14, 26, 42, 62, 84, 106, 122, 152, 180, 213}
	for v := 1; v <= 10; v++ {
		if got := capacity(v); got != want[v] {
			t.Errorf("capacity(%d) = %d, want %d", v, got, want[v])
		}
	}
	for _, tc := range []struct{ n, version int }{{0, 1}, {14, 1}, {15, 2}, {106, 6}, {107, 7}, {213, 10}} {
		c, err := Encode(bytes.Repeat([]byte("a"), tc.n))
		if err != nil {
			t.Fatalf("%d bytes: %v", tc.n, err)
		}
		if c.Version != tc.version || c.Size != 17+4*tc.version {
			t.Errorf("%d bytes: version %d size %d, want version %d", tc.n, c.Version, c.Size, tc.version)
		}
	}
	if _, err := Encode(make([]byte, MaxBytes+1)); !errors.Is(err, ErrTooLong) {
		t.Errorf("%d bytes: err %v, want ErrTooLong", MaxBytes+1, err)
	}
}

func TestFormatAndVersionBits(t *testing.T) {
	// Annex C, Table C.1 (level M) and Annex D, Table D.1.
	format := []int{0x5412, 0x5125, 0x5E7C, 0x5B4B, 0x45F9, 0x40CE, 0x4F97, 0x4AA0}
	for mask, want := range format {
		if got := formatBits(mask); got != want {
			t.Errorf("formatBits(%d) = %#x, want %#x", mask, got, want)
		}
	}
	version := map[int]int{7: 0x07C94, 8: 0x085BC, 9: 0x09A99, 10: 0x0A4D3}
	for v, want := range version {
		if got := bch(v, 0x1F25); got != want {
			t.Errorf("version bits %d = %#x, want %#x", v, got, want)
		}
	}
}

// HalfBlocks draws two module rows per line with a four-module quiet zone,
// and the glyphs read back to the modules.
func TestHalfBlocks(t *testing.T) {
	c, err := Encode([]byte("http://127.0.0.1:7777/pair?token=AAAA"))
	if err != nil {
		t.Fatal(err)
	}
	lines := c.HalfBlocks()
	side := c.Size + 2*quiet
	if len(lines) != (side+1)/2 {
		t.Fatalf("%d lines, want %d", len(lines), (side+1)/2)
	}
	for i, l := range lines {
		r := []rune(l)
		if len(r) != side {
			t.Fatalf("line %d: %d columns, want %d", i, len(r), side)
		}
		for j, g := range r {
			x, y := j-quiet, 2*i-quiet
			top, bot := strings.ContainsRune("█▀", g), strings.ContainsRune("█▄", g)
			if !strings.ContainsRune("█▀▄ ", g) || top != c.Dark(x, y) || bot != c.Dark(x, y+1) {
				t.Fatalf("line %d column %d: %q, modules %v %v", i, j, g, c.Dark(x, y), c.Dark(x, y+1))
			}
		}
	}
	if strings.TrimSpace(lines[0]) != "" || strings.TrimSpace(lines[len(lines)-1]) != "" {
		t.Error("the quiet zone isn't light")
	}
}
