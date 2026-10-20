package usage

import "testing"

func TestMatches(t *testing.T) {
	tests := []struct {
		want, id string
		ok       bool
	}{
		{"sonnet", "claude-sonnet-5-5", true},
		{"sonnet", "claude-sonnet-4-5-20250929", true},
		{"sonnet", "claude-opus-5-5", false},
		{"sonnet", "claude-sonnetx-5", false},
		{"opus", "claude-opus-5-5[1m]", true},
		{"opus[1m]", "claude-opus-5-5", true},
		{"Opus", "claude-opus-5-5", true},
		{"haiku", "claude-haiku-4-5-20251001", true},
		{"fable", "claude-fable-5-1", true},
		{"fable", "claude-sonnet-5-5", false},
		{"claude-opus-5-5", "claude-opus-5-5", true},
		{"claude-haiku-4-5", "claude-haiku-4-5-20251001", true},
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5", true},
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5-20250101", true},
		{"claude-opus-5-5", "claude-opus-5-1", false},
		{"claude-opus-5-5", "claude-sonnet-5-5", false},
		{"claude-opus-5-5[1m]", "claude-opus-5-5", true},
		{"claude-opus-5", "claude-opus-5-5", false},
		// Unchecked values match anything.
		{"default", "claude-haiku-5-5", true},
		{"opusplan", "claude-sonnet-5-5", true},
		{"my-model", "anything", true},
	}
	for _, tt := range tests {
		if got := Matches(tt.want, tt.id); got != tt.ok {
			t.Errorf("Matches(%q, %q) = %v, want %v", tt.want, tt.id, got, tt.ok)
		}
	}
}

func TestCheckable(t *testing.T) {
	for want, ok := range map[string]bool{
		"sonnet": true, "opus": true, "fable": true, "haiku": true, "opus[1m]": true,
		"claude-opus-5-5": true, "default": false, "opusplan": false, "custom": false, "": false,
	} {
		if got := Checkable(want); got != ok {
			t.Errorf("Checkable(%q) = %v, want %v", want, got, ok)
		}
	}
}

func TestMismatch(t *testing.T) {
	if m, bad := Mismatch("sonnet", []string{"claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"}); !bad || m != "claude-opus-5-5" {
		t.Errorf("Mismatch = %q, %v; want the first other model", m, bad)
	}
	if m, bad := Mismatch("sonnet", []string{"claude-sonnet-5-5"}); bad {
		t.Errorf("Mismatch = %q on a match", m)
	}
	if _, bad := Mismatch("opus", nil); bad {
		t.Error("Mismatch on no models")
	}
	if _, bad := Mismatch("default", []string{"claude-haiku-5-5"}); bad {
		t.Error("Mismatch on an unchecked value")
	}
}
