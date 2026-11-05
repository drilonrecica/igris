package vsixpublish

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The stand-ins: vsix-check records the environment it ran in and keeps a
// .vsix in the directory it is given, as the real one keeps the file it
// verified; vsce and ovsx record what they were given and which token they
// saw.
const (
	checkStub = `#!/bin/sh
env | grep -c '_PAT=' > "$OUT/check-tokens" || true
[ -n "${2:-}" ] && printf 'checked bytes' > "$2/igris-${1#v}.vsix"
exit 0
`
	vsceStub = `#!/bin/sh
echo "args $*" > "$OUT/vsce"
echo "vsce=${VSCE_PAT:-} ovsx=${OVSX_PAT:-}" >> "$OUT/vsce"
cat "$3" >> "$OUT/vsce"
`
	ovsxStub = `#!/bin/sh
echo "args $*" > "$OUT/ovsx"
echo "vsce=${VSCE_PAT:-} ovsx=${OVSX_PAT:-}" >> "$OUT/ovsx"
cat "$2" >> "$OUT/ovsx"
`
)

// repo is a scratch git repository with this tree's vsix-publish.sh, the
// stand-ins, and a stale bin/igris-1.2.3.vsix that must never be published.
func repo(t *testing.T) (root, out string) {
	t.Helper()
	root, out = t.TempDir(), t.TempDir()
	script, err := os.ReadFile(filepath.Join("..", "vsix-publish.sh"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"tools/vsix-publish.sh":                 string(script),
		"tools/vsix-check.sh":                   checkStub,
		"editors/vscode/node_modules/.bin/vsce": vsceStub,
		"editors/vscode/node_modules/.bin/ovsx": ovsxStub,
		"bin/igris-1.2.3.vsix":                  "stale bytes",
	}
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o700); err != nil { //nolint:gosec // the stand-ins are scripts the test runs
			t.Fatal(err)
		}
	}
	if b, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil { //nolint:gosec // the test's own temp dir
		t.Fatalf("git init: %v: %s", err, b)
	}
	return root, out
}

func publish(t *testing.T, root, out string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", append([]string{"tools/vsix-publish.sh"}, args...)...) //nolint:gosec // fixed script, the test's own arguments
	cmd.Dir = root
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "OUT=" + out}, env...)
	b, err := cmd.CombinedOutput()
	return string(b), err
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // the test's own temp file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestPublishesTheCheckedVsixWithoutTokensInTheCheck: the check runs with
// neither token in its environment (it runs npm ci and the build), each
// store command gets only its own token, and what is published is the
// .vsix the check kept, never bin/'s (docs/threat-model.md R80).
func TestPublishesTheCheckedVsixWithoutTokensInTheCheck(t *testing.T) {
	root, out := repo(t)
	got, err := publish(t, root, out, []string{"VSCE_PAT=tok-marketplace", "OVSX_PAT=tok-openvsx"}, "v1.2.3")
	if err != nil {
		t.Fatalf("publish: %v\n%s", err, got)
	}
	if strings.Contains(got, "tok-") {
		t.Errorf("a token in the output:\n%s", got)
	}
	if n := strings.TrimSpace(read(t, filepath.Join(out, "check-tokens"))); n != "0" {
		t.Errorf("vsix-check ran with %s token(s) in its environment", n)
	}
	vsce := read(t, filepath.Join(out, "vsce"))
	if !strings.Contains(vsce, "vsce=tok-marketplace ovsx=\n") || !strings.HasSuffix(vsce, "checked bytes") || !strings.Contains(vsce, "args publish --packagePath ") {
		t.Errorf("vsce got:\n%s", vsce)
	}
	ovsx := read(t, filepath.Join(out, "ovsx"))
	if !strings.Contains(ovsx, "vsce= ovsx=tok-openvsx\n") || !strings.HasSuffix(ovsx, "checked bytes") {
		t.Errorf("ovsx got:\n%s", ovsx)
	}
}

// TestRefusesWithoutATokenBeforeTheCheck: a missing token is said at once,
// and nothing is checked or published.
func TestRefusesWithoutATokenBeforeTheCheck(t *testing.T) {
	root, out := repo(t)
	got, err := publish(t, root, out, []string{"VSCE_PAT=tok-marketplace"}, "v1.2.3")
	if err == nil || !strings.Contains(got, "OVSX_PAT is not set") {
		t.Fatalf("got %v:\n%s", err, got)
	}
	for _, f := range []string{"check-tokens", "vsce", "ovsx"} {
		if _, err := os.Stat(filepath.Join(out, f)); err == nil {
			t.Errorf("%s ran", f)
		}
	}
}

// TestDryRunChecksAndPublishesNothing: DRY needs no token, still checks,
// and prints the two commands masked.
func TestDryRunChecksAndPublishesNothing(t *testing.T) {
	root, out := repo(t)
	got, err := publish(t, root, out, nil, "v1.2.3", "dry")
	if err != nil {
		t.Fatalf("dry: %v\n%s", err, got)
	}
	if !strings.Contains(got, "VSCE_PAT=*** vsce publish --packagePath igris-1.2.3.vsix") || !strings.Contains(got, "OVSX_PAT=*** ovsx publish igris-1.2.3.vsix") {
		t.Errorf("dry output:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(out, "check-tokens")); err != nil {
		t.Error("the dry run did not check")
	}
	for _, f := range []string{"vsce", "ovsx"} {
		if _, err := os.Stat(filepath.Join(out, f)); err == nil {
			t.Errorf("%s ran in a dry run", f)
		}
	}
}
