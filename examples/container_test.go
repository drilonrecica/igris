package examples

import (
	"os"
	"regexp"
	"testing"
)

// The Containerfile installs a release tarball, so its pin must be the newest
// released version in the CHANGELOG; a release bumps both.
func TestContainerfilePinsCurrentVersion(t *testing.T) {
	cl, err := os.ReadFile("../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`).FindSubmatch(cl)
	if m == nil {
		t.Fatal("no released version in CHANGELOG.md")
	}
	cf, err := os.ReadFile("container/Containerfile")
	if err != nil {
		t.Fatal(err)
	}
	p := regexp.MustCompile(`(?m)^ARG IGRIS_VERSION=(\S+)$`).FindSubmatch(cf)
	if p == nil {
		t.Fatal("Containerfile has no ARG IGRIS_VERSION=")
	}
	if string(p[1]) != string(m[1]) {
		t.Errorf("Containerfile pins igris %s, newest release is %s", p[1], m[1])
	}
}
