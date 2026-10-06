package textsafe

import "testing"

func TestClean(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "M0-01 Go module — done ✓", "M0-01 Go module — done ✓"},
		{"newline and tab stay", "a\n\tb", "a\n\tb"},
		{"color", "\x1b[31mFAIL\x1b[0m ok", "FAIL ok"},
		{"clear screen", "\x1b[2J\x1b[Hspoof", "spoof"},
		{"paste end then shift-tab", "out\x1b[201~\x1b[Z", "out"},
		{"osc title with bel", "a\x1b]0;evil\x07b", "ab"},
		{"osc 52 with st", "a\x1b]52;c;ZXZpbA==\x1b\\b", "ab"},
		{"unclosed osc ends at the line", "a\x1b]0;evil\nnext", "a\nnext"},
		{"dcs", "a\x1bPq#0\x1b\\b", "ab"},
		{"two-char escape", "a\x1bcb", "ab"},
		{"escape with intermediate", "a\x1b(Bb", "ab"},
		{"lone escape at the end", "a\x1b", "a"},
		{"unfinished csi", "a\x1b[12;", "a"},
		{"csi cut by a control char", "a\x1b[1\x07b", "ab"},
		{"c1 csi rune", "a\u009b31mb", "ab"},
		{"other c1", "a\u0085b\u009cc", "abc"},
		{"c0 and del", "a\x00\x07\x08\r\x7fb", "ab"},
		{"crlf", "a\r\nb", "a\nb"},
		{"invalid utf-8", "a\xffb", "a�b"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Clean(tt.in)
			if got != tt.want {
				t.Errorf("Clean(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if again := Clean(got); again != got {
				t.Errorf("Clean is not stable: %q then %q", got, again)
			}
		})
	}
}

func TestLine(t *testing.T) {
	tests := []struct{ in, want string }{
		{"one line", "one line"},
		{"two\nlines\tand a tab", "two lines and a tab"},
		{"note\x1b[2J\nspoofed prompt", "note spoofed prompt"},
	}
	for _, tt := range tests {
		if got := Line(tt.in); got != tt.want {
			t.Errorf("Line(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHasControl(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"| M0-01 | **Go module** |\tready |", false},
		{"| M0-01 | \x1b[31mred | ready |", true},
		{"bell\x07", true},
		{"c1 \u009b", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := HasControl(tt.in); got != tt.want {
			t.Errorf("HasControl(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
