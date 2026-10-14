package signers

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// namespaces finds the namespaces option among a line's options, which may
// also hold valid-before="YYYYMMDD" on a rotated key.
var namespaces = regexp.MustCompile(`(?:^|,)namespaces="([^"]*)"`)

// signerLines returns the non-comment, non-blank lines of allowed_signers.
func signerLines(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("../../allowed_signers")
	if err != nil {
		t.Fatalf("read allowed_signers: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	return lines
}

// The release key's line: principal igris, valid for files (checksums.txt)
// and git (the tags), an Ed25519 key (decisions.md V06-P1). A rotated key
// adds a line, with valid-before on the old one.
func TestAllowedSigners(t *testing.T) {
	lines := signerLines(t)
	if len(lines) == 0 {
		t.Fatal("allowed_signers has no key")
	}
	for _, l := range lines {
		f := strings.Fields(l)
		var m []string
		if len(f) >= 4 {
			m = namespaces.FindStringSubmatch(f[1])
		}
		if m == nil || f[0] != "igris" || f[2] != "ssh-ed25519" {
			t.Errorf("line %q: want igris namespaces=\"file,git\" ssh-ed25519 <key> [comment]", l)
			continue
		}
		if ns := strings.Split(m[1], ","); !contains(ns, "file") || !contains(ns, "git") {
			t.Errorf("line %q: namespaces %v, want file and git", l, ns)
		}
	}
}

// The README prints the current key (the last line) for users to compare
// with the allowed_signers they download.
func TestReadmeQuotesTheKey(t *testing.T) {
	lines := signerLines(t)
	if len(lines) == 0 {
		t.Fatal("allowed_signers has no key")
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		t.Fatalf("short line %q", lines[len(lines)-1])
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(f[:4], " ")
	if !strings.Contains(string(readme), want) {
		t.Errorf("README.md doesn't quote the release key %q", want)
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
