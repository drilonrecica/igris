package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// contextRoot is a project with a file, a directory, a symlink inside it and
// symlinks leading out of it.
func contextRoot(t *testing.T) string {
	t.Helper()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, d := range []string{"docs", "internal/plan"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"SPEC.md", "docs/a b.md", "internal/plan/plan.go"} {
		if err := os.WriteFile(filepath.Join(root, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	up, err := filepath.Rel(filepath.Join(root, "docs"), outside)
	if err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"spec-link":   "SPEC.md",                        // stays inside
		"docs/up":     "..",                             // the root itself
		"out-file":    filepath.Join(outside, "secret"), // absolute, outside
		"out-dir":     outside,                          // a directory outside
		"docs/escape": up,                               // relative, outside
		"dangling":    "nowhere",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestContextValidation(t *testing.T) {
	root := contextRoot(t)
	const head = "## M1\n\n| ID | Status | Model | Owner | Context |\n|---|---|---|---|---|\n"
	escape := func(cell string) string {
		return fmt.Sprintf(`tasks.md:5: a: Context %q leads outside the project through a symlink; paths must stay inside the project; use a repo-relative path`, cell)
	}
	tests := []struct {
		name string
		row  string
		want []string
	}{
		{"file, directory, backticks", "| a | ready | sonnet | agent | `SPEC.md`, docs/, internal/plan/plan.go, docs/a b.md |", nil},
		{"the root itself", "| a | ready | sonnet | agent | . |", []string{`tasks.md:5: a: Context "." names the whole project; list the files or directories to read`}},
		{"the root with a slash", "| a | ready | sonnet | agent | ./ |", []string{`tasks.md:5: a: Context "./" names the whole project; list the files or directories to read`}},
		{"a file with a trailing slash", "| a | ready | sonnet | agent | SPEC.md/ |", []string{`tasks.md:5: a: Context "SPEC.md/" is a file, not a directory; drop the trailing /`}},
		{"a directory with or without a slash", "| a | ready | sonnet | agent | docs, ./docs/, internal/plan |", nil},
		{"symlink inside", "| a | ready | sonnet | agent | spec-link, docs/up |", nil},
		{"not set", "| a | ready | sonnet | agent | — |", nil},
		{"absolute", "| a | ready | sonnet | agent | /etc/passwd |", []string{`tasks.md:5: a: Context "/etc/passwd" is an absolute path; use a path relative to the project root`}},
		{"dot dot", "| a | ready | sonnet | agent | ../secrets |", []string{`tasks.md:5: a: Context "../secrets" leaves the project: paths must stay inside the project; use a repo-relative path`}},
		{"dot dot inside", "| a | ready | sonnet | agent | docs/../SPEC.md |", []string{`tasks.md:5: a: Context "docs/../SPEC.md" leaves the project: paths must stay inside the project; use a repo-relative path`}},
		{"missing", "| a | ready | sonnet | agent | SPEC.md, nope.md |", []string{`tasks.md:5: a: Context "nope.md" does not exist; fix the path or remove it from the cell`}},
		{"dangling symlink", "| a | ready | sonnet | agent | dangling |", []string{`tasks.md:5: a: Context "dangling" does not exist; fix the path or remove it from the cell`}},
		{"symlink to a file outside", "| a | ready | sonnet | agent | out-file |", []string{escape("out-file")}},
		{"through a symlinked directory outside", "| a | ready | sonnet | agent | out-dir/secret |", []string{escape("out-dir/secret")}},
		{"relative symlink outside", "| a | ready | sonnet | agent | docs/escape |", []string{escape("docs/escape")}},
		{"one problem each", "| a | ready | sonnet | agent | /x, ../y, z |", []string{
			`tasks.md:5: a: Context "/x" is an absolute path; use a path relative to the project root`,
			`tasks.md:5: a: Context "../y" leaves the project: paths must stay inside the project; use a repo-relative path`,
			`tasks.md:5: a: Context "z" does not exist; fix the path or remove it from the cell`,
		}},
		{"control character", "| a | ready | sonnet | agent | SPEC\x1b[31m.md |", []string{`tasks.md:5: row contains a control character (an escape sequence?); remove it`}},
		{"bad on a done task", "| a | done | sonnet | agent | ../x, /y, nope |", nil},
		{"bad on a skipped task", "| a | skipped | sonnet | agent | ../x |", nil},
		{"bad on a user task", "| a | ready | — | user | ../x |", nil},
		{"duplicates count once", "| a | ready | sonnet | agent | " + strings.Repeat("SPEC.md, ", MaxContext+5) + " |", nil},
		{"duplicates count once however written", "| a | ready | sonnet | agent | " + strings.Repeat("SPEC.md, ./SPEC.md, docs/, docs, ", MaxContext) + " |", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Parse("tasks.md", []byte(head+tt.row+"\n"), Options{})
			got := issueMsgs(p.Validate(Rules{Models: testModels, Root: root}))
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("issues:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestContextLimit(t *testing.T) {
	root := t.TempDir()
	var paths []string
	for i := range MaxContext + 1 {
		name := fmt.Sprintf("f%02d", i)
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, name)
	}
	const head = "## M1\n\n| ID | Status | Model | Context |\n|---|---|---|---|\n"
	rules := Rules{Models: testModels, Root: root}
	ok := Parse("tasks.md", []byte(head+"| a | ready | sonnet | "+strings.Join(paths[:MaxContext], ", ")+" |\n"), Options{})
	if got := issueMsgs(ok.Validate(rules)); len(got) != 0 {
		t.Errorf("%d paths: issues %q", MaxContext, got)
	}
	over := Parse("tasks.md", []byte(head+"| a | ready | sonnet | "+strings.Join(paths, ", ")+" |\n"), Options{})
	want := `tasks.md:5: a: Context lists 21 paths, more than 20; name only the files and directories the task must read`
	if got := issueMsgs(over.Validate(rules)); len(got) != 1 || got[0] != want {
		t.Errorf("%d paths: issues %q, want %q", MaxContext+1, got, want)
	}
}

// Without a root the paths are checked against the working directory, as
// check, phases and status do.
func TestContextRootDefaultsToTheWorkingDirectory(t *testing.T) {
	root := contextRoot(t)
	t.Chdir(root)
	p := Parse("tasks.md", []byte("## M1\n\n| ID | Status | Model | Context |\n|---|---|---|---|\n| a | ready | sonnet | SPEC.md, out-file |\n"), Options{})
	got := issueMsgs(p.Validate(Rules{Models: testModels}))
	if len(got) != 1 || !strings.Contains(got[0], `"out-file" leads outside the project`) {
		t.Errorf("issues = %q", got)
	}
}

// A root reached through a symlink is resolved too, so paths inside it are
// not mistaken for escapes.
func TestContextRootThroughASymlink(t *testing.T) {
	root := contextRoot(t)
	link := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	p := Parse("tasks.md", []byte("## M1\n\n| ID | Status | Model | Context |\n|---|---|---|---|\n| a | ready | sonnet | SPEC.md, spec-link, docs |\n"), Options{})
	if got := issueMsgs(p.Validate(Rules{Models: testModels, Root: link})); len(got) != 0 {
		t.Errorf("issues = %q", got)
	}
}

// A working directory reached through a symlink ($PWD names the link) is
// resolved like the files are, so paths inside it are not escapes.
func TestContextWorkingDirectoryThroughASymlink(t *testing.T) {
	root := contextRoot(t)
	link := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(link)
	t.Setenv("PWD", link)
	p := Parse("tasks.md", []byte("## M1\n\n| ID | Status | Model | Context |\n|---|---|---|---|\n| a | ready | sonnet | SPEC.md, docs/, spec-link |\n"), Options{})
	for _, root := range []string{"", "."} {
		if got := issueMsgs(p.Validate(Rules{Models: testModels, Root: root})); len(got) != 0 {
			t.Errorf("root %q: issues = %q", root, got)
		}
	}
}
