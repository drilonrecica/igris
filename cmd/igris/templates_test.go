package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/examples"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
)

const suggestionParagraph = "**Ranks are suggestions.**"

// templateProject is an empty project that ran `igris init --template name`.
func templateProject(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	savedGetenv, savedRunner := ariseGetenv, ariseRunner
	t.Cleanup(func() { ariseGetenv, ariseRunner = savedGetenv, savedRunner })
	ariseGetenv = func(string) string { return "" }
	ariseRunner = &runner.Fake{}
	t.Chdir(dir)
	if code, out, errb := runCmd("init", "--template", name); code != exitOK {
		t.Fatalf("init --template %s: exit %d\n%s%s", name, code, out, errb)
	}
	return dir
}

func TestTemplatesAreEmbedded(t *testing.T) {
	names := examples.TemplateNames()
	if got := strings.Join(names, ","); got != "go-cli,library,web-app" {
		t.Fatalf("templates = %s", got)
	}
	// The gallery directory is the embedded set.
	dirs, err := os.ReadDir(filepath.Join("..", "..", "examples", "templates"))
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, d := range dirs {
		if d.IsDir() {
			n++
		}
	}
	if n != len(names) {
		t.Errorf("examples/templates has %d directories, embedded %d", n, len(names))
	}
	readme, err := os.ReadFile(filepath.Join("..", "..", "examples", "templates", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !strings.Contains(string(readme), "`"+name+"`") {
			t.Errorf("examples/templates/README.md doesn't list %s", name)
		}
	}
}

// Each template, as written by init, is a valid plan under --strict, has
// no readiness drift, starts with the format marker and the suggestion
// paragraph, ends every phase in a gate, and walks a dry run of M0.
func TestTemplates(t *testing.T) {
	for _, name := range examples.TemplateNames() {
		t.Run(name, func(t *testing.T) {
			dir := templateProject(t, name)
			data, _ := examples.Template(name)
			if got := readFile(t, filepath.Join(dir, "tasks.md")); got != string(data) {
				t.Fatal("tasks.md differs from the embedded template")
			}
			if !strings.HasPrefix(string(data), "<!-- igris:format 1 -->\n# ") {
				t.Error("template doesn't start with the format marker and a title")
			}
			if !strings.Contains(string(data), suggestionParagraph) {
				t.Errorf("template lacks the %q paragraph", suggestionParagraph)
			}
			if strings.Contains(string(data), "haiku") || strings.Contains(string(data), "fable") {
				t.Error("template uses a rank it shouldn't")
			}

			cfg, err := config.Load(filepath.Join(dir, "igris.toml"))
			if err != nil {
				t.Fatal(err)
			}
			p, err := plan.Load(filepath.Join(dir, cfg.Plan), plan.Options{Columns: cfg.Columns})
			if err != nil {
				t.Fatal(err)
			}
			if got := p.Readiness(); len(got) != 0 {
				t.Errorf("Status cells out of sync with Deps: %v", got)
			}
			if first := p.Tasks[0]; first.ID != "M0-01" || first.Owner != "user" {
				t.Errorf("first task = %s (%s), want M0-01 owned by user", first.ID, first.Owner)
			}
			byID := map[string]*plan.Task{}
			for i := range p.Tasks {
				byID[p.Tasks[i].ID] = p.Tasks[i]
			}
			if d := byID["M0-02"]; d == nil || d.Mode != "plan" || d.Rank != "opus" {
				t.Errorf("M0-02 is not an opus plan-mode decision: %+v", d)
			}
			for _, ph := range p.Phases {
				gate := byID[ph.ID+"-G"]
				if gate == nil || gate.Owner != "agent + user" {
					t.Errorf("phase %s has no agent + user gate", ph.ID)
				}
			}

			code, out, errb := runCmd("check", "--strict")
			if code != exitOK || strings.Contains(out, "warning:") {
				t.Errorf("check --strict: exit %d\n%s%s", code, out, errb)
			}
			code, out, errb = runCmd("arise", "M0", "--dry-run")
			if code != exitOK {
				t.Errorf("arise M0 --dry-run: exit %d\n%s%s", code, out, errb)
			}
		})
	}
}

// Every task of a template waits, directly or through others, for M0-01:
// a run starts only once the owner has said what to build.
func TestTemplatesWaitForTheSpec(t *testing.T) {
	for _, name := range examples.TemplateNames() {
		t.Run(name, func(t *testing.T) {
			dir := templateProject(t, name)
			p, err := plan.Load(filepath.Join(dir, "tasks.md"), plan.Options{})
			if err != nil {
				t.Fatal(err)
			}
			deps := map[string][]string{}
			for _, task := range p.Tasks {
				deps[task.ID] = task.Deps
			}
			var waits func(id string, seen map[string]bool) bool
			waits = func(id string, seen map[string]bool) bool {
				if id == "M0-01" {
					return true
				}
				if seen[id] {
					return false
				}
				seen[id] = true
				for _, d := range deps[id] {
					if waits(d, seen) {
						return true
					}
				}
				return false
			}
			for id := range deps {
				if id != "M0-01" && !waits(id, map[string]bool{}) {
					t.Errorf("%s doesn't wait for M0-01", id)
				}
			}
		})
	}
}

func TestInitTemplateOutput(t *testing.T) {
	golden := mustAbs(filepath.Join("testdata", "golden", "init_template_go_cli.golden"))
	dir := t.TempDir()
	savedGetenv, savedRunner := ariseGetenv, ariseRunner
	t.Cleanup(func() { ariseGetenv, ariseRunner = savedGetenv, savedRunner })
	ariseGetenv = func(string) string { return "" }
	t.Chdir(dir)
	code, out, errb := runCmd("init", "--template", "go-cli")
	if code != exitOK || errb != "" {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if *update {
		if err := os.WriteFile(golden, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden) //nolint:gosec // a golden under testdata/
	if err != nil {
		t.Fatalf("%v; run `go test ./cmd/igris -run TestInitTemplateOutput -update`", err)
	}
	if out != string(want) {
		t.Errorf("output changed:\n%s\nwant:\n%s", out, want)
	}
}

func TestInitTemplateNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	mine := "# my plan\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	savedGetenv := ariseGetenv
	t.Cleanup(func() { ariseGetenv = savedGetenv })
	ariseGetenv = func(string) string { return "" }
	t.Chdir(dir)
	code, out, errb := runCmd("init", "--template", "library")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if got := readFile(t, filepath.Join(dir, "tasks.md")); got != mine {
		t.Errorf("tasks.md overwritten: %q", got)
	}
	if !strings.Contains(out, "kept tasks.md (already exists)") {
		t.Errorf("output lacks the kept line:\n%s", out)
	}
}

func TestInitTemplateUsageErrors(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"init", "--template", "rust"}, `unknown template "rust"; templates: go-cli, library, web-app`},
		{[]string{"init", "--template", "Go-CLI"}, `unknown template "Go-CLI"`},
		{[]string{"init", "--template", "example"}, `unknown template "example"`},
		{[]string{"init", "--template", ""}, "empty template name; templates: go-cli, library, web-app"},
		{[]string{"init", "--template", "../x"}, `unknown template "../x"`},
		{[]string{"init", "--template", "go-cli", "--example"}, "--template and --example can't be used together"},
	} {
		dir := inDir(t)
		code, out, errb := runCmd(c.args...)
		if code != exitUsage || !strings.Contains(errb, c.want) {
			t.Errorf("%v: exit %d, stderr %q; want usage error with %q", c.args, code, errb, c.want)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 || out != "" {
			t.Errorf("%v: touched the project: %v %q", c.args, entries, out)
		}
	}
}
