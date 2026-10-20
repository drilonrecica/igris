package config

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateReference = flag.Bool("update-reference", false, "rewrite docs/config.md")

const referencePath = "../../docs/config.md"

// TestReferenceDocsEveryKey fails when a key was added to or removed from
// Config without its prose in keyDocs.
func TestReferenceDocsEveryKey(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range referenceKeys() {
		seen[k.path] = true
		if keyDocs[k.path].desc == "" {
			t.Errorf("config key %q has no entry in keyDocs (internal/config/reference.go)", k.path)
		}
	}
	for path := range keyDocs {
		if !seen[path] {
			t.Errorf("keyDocs documents %q, which Config no longer has", path)
		}
	}
}

// TestReferenceUpToDate is the drift test: docs/config.md is what Reference
// renders from Config and Default().
func TestReferenceUpToDate(t *testing.T) {
	want := Reference()
	if *updateReference {
		if err := os.WriteFile(filepath.Clean(referencePath), []byte(want), 0o644); err != nil { //nolint:gosec // a docs file
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatalf("read %s: %v; run `make config-ref`", referencePath, err)
	}
	if string(got) != want {
		t.Errorf("docs/config.md is out of date with internal/config; run `make config-ref` and review the diff")
	}
}
