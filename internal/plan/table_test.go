package plan

import (
	"reflect"
	"testing"
)

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []line
	}{
		{"empty", "", nil},
		{"lf", "a\nbc\n", []line{{"a", 0, 1}, {"bc", 2, 2}}},
		{"crlf", "a\r\nbc\r\n", []line{{"a", 0, 1}, {"bc", 3, 2}}},
		{"no trailing newline", "a\nbc", []line{{"a", 0, 1}, {"bc", 2, 2}}},
		{"blank lines", "a\n\n\nb\n", []line{{"a", 0, 1}, {"", 2, 2}, {"", 3, 3}, {"b", 4, 4}}},
		{"mixed endings", "a\r\nb\nc", []line{{"a", 0, 1}, {"b", 3, 2}, {"c", 5, 3}}},
		{"utf-8 bom", "\ufeffa\r\nb\n", []line{{"a", 3, 1}, {"b", 6, 2}}},
		{"bom only", "\ufeff", nil},
		{"bom not at start", "a\n\ufeffb\n", []line{{"a", 0, 1}, {"\ufeffb", 2, 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitLines([]byte(tt.in))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for _, l := range got {
				if tt.in[l.start:l.start+len(l.text)] != l.text {
					t.Errorf("line %d: offset %d does not point at %q", l.num, l.start, l.text)
				}
			}
		})
	}
}

func values(cells []cell) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = c.value
	}
	return out
}

func TestSplitRow(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"basic", "| a | b | c |", []string{"a", "b", "c"}},
		{"no padding", "|a|b|", []string{"a", "b"}},
		{"wide padding", "|   a   |\tb\t|", []string{"a", "b"}},
		{"no outer pipes", "a | b", []string{"a", "b"}},
		{"leading pipe only", "| a | b", []string{"a", "b"}},
		{"empty cells", "| a |  | c |", []string{"a", "", "c"}},
		{"empty last cell", "| a | |", []string{"a", ""}},
		{"escaped pipe", `| v1 \| nonce | b |`, []string{"v1 | nonce", "b"}},
		{"escaped pipe in backticks", "| `a \\| b` | c |", []string{"`a | b`", "c"}},
		{"escaped backslash before pipe", `| a\\| b |`, []string{`a\\`, "b"}},
		{"other backslashes kept", `| a\*b | c |`, []string{`a\*b`, "c"}},
		{"backticked status", "| `ready` |", []string{"`ready`"}},
		{"unicode", "| M0-02…M0-07 | — |", []string{"M0-02…M0-07", "—"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitRow(tt.in)
			if !reflect.DeepEqual(values(got), tt.want) {
				t.Fatalf("got %q, want %q", values(got), tt.want)
			}
		})
	}
}

func TestSplitRowOffsets(t *testing.T) {
	rows := []string{
		"| a | b | c |",
		"|  `ready`   | x |",
		`| v1 \| nonce |   done  |`,
		"a|b",
		"| a |  | c |",
	}
	for _, row := range rows {
		for i, c := range splitRow(row) {
			raw := row[c.start:c.end]
			if c.value == "" {
				if raw != "" {
					t.Errorf("%q cell %d: empty cell has raw %q", row, i, raw)
				}
				continue
			}
			if raw != c.value && raw != `v1 \| nonce` {
				t.Errorf("%q cell %d: raw %q != value %q", row, i, raw, c.value)
			}
		}
	}
	// Splicing at the offsets changes only the cell content.
	row := "|  `ready`   | x |"
	c := splitRow(row)[0]
	if got := row[:c.start] + "`done`" + row[c.end:]; got != "|  `done`   | x |" {
		t.Fatalf("splice = %q", got)
	}
}

func TestIsSeparator(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"|---|---|", true},
		{"| --- | :-: | --: | :-- |", true},
		{"|-|", true},
		{"| a | --- |", false},
		{"| | --- |", false},
		{"| ::: |", false},
		{"| -:- |", false},
	}
	for _, tt := range tests {
		if got := isSeparator(splitRow(tt.in)); got != tt.want {
			t.Errorf("isSeparator(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestIsTableStart(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"table", "| ID | Status |\n|---|---|\n| a | ready |", true},
		{"crlf table", "| ID | Status |\r\n|---|---|\r\n", true},
		{"no separator", "| ID | Status |\n| a | ready |", false},
		{"cell count mismatch", "| ID | Status |\n|---|---|---|", false},
		{"last line", "| ID | Status |", false},
		{"prose", "some text\n|---|", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTableStart(splitLines([]byte(tt.in)), 0); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
