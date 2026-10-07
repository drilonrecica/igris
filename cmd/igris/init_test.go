package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/examples"
	"github.com/drilonrecica/igris/internal/config"
)

// initIn runs `igris init` in a fresh or given directory with no herdr
// around and returns the exit code and output.
func initIn(t *testing.T, dir string) (int, string, string) {
	t.Helper()
	savedGetenv, savedRunner := ariseGetenv, ariseRunner
	t.Cleanup(func() { ariseGetenv, ariseRunner = savedGetenv, savedRunner })
	ariseGetenv = func(string) string { return "" }
	t.Chdir(dir)
	var out, errb bytes.Buffer
	code := run([]string{"init"}, &out, &errb)
	return code, out.String(), errb.String()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test temp file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func allowList(t *testing.T, dir string) []any {
	t.Helper()
	var s struct {
		Permissions struct {
			Allow []any `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, claudeSettingsPath))), &s); err != nil {
		t.Fatal(err)
	}
	return s.Permissions.Allow
}

func TestInitFresh(t *testing.T) {
	dir := t.TempDir()
	code, out, errb := initIn(t, dir)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if _, err := config.Load(filepath.Join(dir, "igris.toml")); err != nil {
		t.Errorf("igris.toml does not load: %v", err)
	}
	for _, sub := range []string{".igris", ".igris/signals", ".igris/prompts"} {
		if fi, err := os.Stat(filepath.Join(dir, sub)); err != nil || !fi.IsDir() {
			t.Errorf("%s missing: %v", sub, err)
		}
	}
	if got := readFile(t, filepath.Join(dir, ".gitignore")); got != ".igris/\n" {
		t.Errorf(".gitignore = %q", got)
	}
	allow := allowList(t, dir)
	if len(allow) != 2 || allow[0] != "Bash(igris done:*)" || allow[1] != "Bash(igris done *)" {
		t.Errorf("allow = %v", allow)
	}
	if strings.Contains(readFile(t, filepath.Join(dir, claudeSettingsPath)), "skip") {
		t.Error("igris skip must not be allow-listed")
	}
	if !strings.Contains(out, "herdr integration install claude") {
		t.Errorf("output lacks the herdr hint:\n%s", out)
	}
}

func TestInitIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	initIn(t, dir)
	files := []string{"igris.toml", ".gitignore", claudeSettingsPath}
	before := map[string]string{}
	for _, f := range files {
		before[f] = readFile(t, filepath.Join(dir, f))
	}
	code, out, _ := initIn(t, dir)
	if code != exitOK {
		t.Fatalf("second run exit %d", code)
	}
	for _, f := range files {
		if got := readFile(t, filepath.Join(dir, f)); got != before[f] {
			t.Errorf("%s changed on the second run:\n%s", f, got)
		}
	}
	if strings.Count(out, "kept") != 3 {
		t.Errorf("want three kept lines:\n%s", out)
	}
}

func TestInitKeepsOwnerFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("igris.toml", "plan = \"mine.md\"\n")
	write(".gitignore", "bin/\n*.log") // no trailing newline
	write(claudeSettingsPath, `{"model":"opus","permissions":{"allow":["Bash(make:*)","Bash(igris done:*)"],"deny":["Bash(rm:*)"]}}`)

	if code, _, errb := initIn(t, dir); code != exitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got := readFile(t, filepath.Join(dir, "igris.toml")); got != "plan = \"mine.md\"\n" {
		t.Errorf("igris.toml overwritten: %q", got)
	}
	if got := readFile(t, filepath.Join(dir, ".gitignore")); got != "bin/\n*.log\n.igris/\n" {
		t.Errorf(".gitignore = %q", got)
	}
	var s map[string]any
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, claudeSettingsPath))), &s); err != nil {
		t.Fatal(err)
	}
	perms := s["permissions"].(map[string]any)
	if s["model"] != "opus" || perms["deny"] == nil {
		t.Errorf("unrelated settings lost: %v", s)
	}
	allow := perms["allow"].([]any)
	if len(allow) != 3 || allow[0] != "Bash(make:*)" || allow[1] != "Bash(igris done:*)" || allow[2] != "Bash(igris done *)" {
		t.Errorf("allow = %v", allow)
	}
}

func TestInitGitignoreVariants(t *testing.T) {
	for _, existing := range []string{".igris", "/.igris/", "bin/\n.igris/\n", "  .igris/  \r\n"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o600); err != nil {
			t.Fatal(err)
		}
		initIn(t, dir)
		if got := readFile(t, filepath.Join(dir, ".gitignore")); got != existing {
			t.Errorf(".gitignore %q was changed to %q", existing, got)
		}
	}
}

func TestInitRejectsBrokenSettings(t *testing.T) {
	for _, bad := range []string{`{not json`, `[]`, `{"permissions":[]}`, `{"permissions":{"allow":"x"}}`} {
		dir := t.TempDir()
		p := filepath.Join(dir, claudeSettingsPath)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, errb := initIn(t, dir)
		if code != exitFail || !strings.Contains(errb, "settings.local.json") {
			t.Errorf("%s: exit %d, stderr %q", bad, code, errb)
		}
		if got := readFile(t, p); got != bad {
			t.Errorf("%s: settings were modified to %q", bad, got)
		}
	}
}

func initExample(t *testing.T, dir string) (int, string, string) {
	t.Helper()
	savedGetenv, savedRunner := ariseGetenv, ariseRunner
	t.Cleanup(func() { ariseGetenv, ariseRunner = savedGetenv, savedRunner })
	ariseGetenv = func(string) string { return "" }
	t.Chdir(dir)
	var out, errb bytes.Buffer
	code := run([]string{"init", "--example"}, &out, &errb)
	return code, out.String(), errb.String()
}

func TestInitExampleWritesPlan(t *testing.T) {
	dir := t.TempDir()
	code, out, errb := initExample(t, dir)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got := readFile(t, filepath.Join(dir, "tasks.md")); got != string(examples.Plan) {
		t.Error("tasks.md differs from the embedded examples/tasks.md")
	}
	if !strings.Contains(out, "created tasks.md") {
		t.Errorf("output lacks the created line:\n%s", out)
	}
	var cout bytes.Buffer
	if code := run([]string{"check"}, &cout, &bytes.Buffer{}); code != exitOK {
		t.Errorf("check on the written plan: exit %d\n%s", code, cout.String())
	}
}

func TestInitExampleNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	mine := "# my plan\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errb := initExample(t, dir)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got := readFile(t, filepath.Join(dir, "tasks.md")); got != mine {
		t.Errorf("tasks.md overwritten: %q", got)
	}
	if !strings.Contains(out, "kept tasks.md") {
		t.Errorf("output lacks the kept line:\n%s", out)
	}
}

func TestInitExampleUsesConfiguredPlanPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "igris.toml"), []byte("plan = \"docs/plan.md\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errb := initExample(t, dir); code != exitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got := readFile(t, filepath.Join(dir, "docs", "plan.md")); !strings.Contains(got, "P1-01") {
		t.Errorf("docs/plan.md is not the example plan:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks.md")); err == nil {
		t.Error("tasks.md written although the plan path is docs/plan.md")
	}
}

func TestInitWithoutExampleWritesNoPlan(t *testing.T) {
	dir := t.TempDir()
	initIn(t, dir)
	if _, err := os.Stat(filepath.Join(dir, "tasks.md")); err == nil {
		t.Error("init without --example wrote a plan")
	}
}
