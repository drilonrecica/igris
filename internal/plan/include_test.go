package plan

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// writeFiles writes files (slash paths relative to a new temp dir), makes
// that dir the working directory and returns it.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // test dir
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // test file
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	return dir
}

func loadSplit(t *testing.T) *Plan {
	t.Helper()
	p, err := Load("tasks.md", Options{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// phaseTable is a phase with one task table; its rows start at line 5.
func phaseTable(id string, rows ...string) string {
	return "## " + id + " — Phase " + id + "\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n" + strings.Join(rows, "\n") + "\n"
}

const format2 = "<!-- igris:format 2 -->\n"

// A split plan reads as one: the included phases take the include line's
// place, dependencies and ranges cross files in plan order, and each phase
// knows its file.
func TestIncludeSplitPlan(t *testing.T) {
	writeFiles(t, map[string]string{
		"tasks.md": "# Plan\n" + format2 + "\n" + phaseTable("A", "| A-1 | — | done | sonnet |") +
			"\n<!-- igris:include phases/b.md -->\n\n" + phaseTable("C", "| C-1 | A-1…B-2 | blocked | opus |"),
		"phases/b.md": "<!-- igris:format 1 -->\n# Phase B, kept apart\n\nSome prose.\n\n" +
			phaseTable("B", "| B-1 | A-1 | ready | sonnet |", "| B-2 | B-1 | blocked | sonnet |"),
	})
	p := loadSplit(t)
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatalf("issues: %v", issues)
	}
	if got, want := p.Files(), []string{"tasks.md", filepath.Join("phases", "b.md")}; !slices.Equal(got, want) {
		t.Errorf("files %q, want %q", got, want)
	}
	var phases, files []string
	for _, ph := range p.Phases {
		phases, files = append(phases, ph.ID), append(files, ph.File)
	}
	if want := []string{"A", "B", "C"}; !slices.Equal(phases, want) {
		t.Errorf("phases %v, want %v", phases, want)
	}
	if want := []string{"tasks.md", "phases/b.md", "tasks.md"}; !slices.Equal(files, want) {
		t.Errorf("phase files %v, want %v", files, want)
	}
	if got, want := p.Task("C-1").Deps, []string{"A-1", "B-1", "B-2"}; !slices.Equal(got, want) {
		t.Errorf("C-1 deps %v, want %v", got, want)
	}
	if p.Format != 2 || !p.Split() {
		t.Errorf("format %d, split %v", p.Format, p.Split())
	}
	if drift := p.Readiness(); len(drift) > 0 {
		t.Errorf("drift: %v", drift)
	}
}

// Every problem with an include is an issue at its line, in the file the
// line is in, and the rest of the plan is still checked.
func TestIncludeProblems(t *testing.T) {
	main := func(lines ...string) string {
		return format2 + phaseTable("A", "| A-1 | — | ready | sonnet |") + "\n" + strings.Join(lines, "\n") + "\n"
	}
	b := phaseTable("B", "| B-1 | — | ready | sonnet |")
	tests := []struct {
		name  string
		files map[string]string
		link  string // a symlink to make, to a file outside the root
		want  string // an issue, as `file:line: message`, it must contain
	}{
		{"no format marker", map[string]string{"tasks.md": strings.TrimPrefix(main("<!-- igris:include b.md -->"), format2), "b.md": b},
			"", "tasks.md:7: an include needs plan format 2; add <!-- igris:format 2 -->"},
		{"format 1", map[string]string{"tasks.md": "<!-- igris:format 1 -->\n" + strings.TrimPrefix(main("<!-- igris:include b.md -->"), format2), "b.md": b},
			"", "tasks.md:8: an include needs plan format 2"},
		{"missing", map[string]string{"tasks.md": main("<!-- igris:include b.md -->")},
			"", "tasks.md:8: included file b.md does not exist"},
		{"empty path", map[string]string{"tasks.md": main("<!-- igris:include -->")},
			"", "tasks.md:8: the include names no file"},
		{"not markdown", map[string]string{"tasks.md": main("<!-- igris:include b.txt -->"), "b.txt": b},
			"", `tasks.md:8: include "b.txt" is not a .md file`},
		{"absolute", map[string]string{"tasks.md": main("<!-- igris:include /etc/b.md -->")},
			"", `include "/etc/b.md" is an absolute path`},
		{"home", map[string]string{"tasks.md": main("<!-- igris:include ~/b.md -->")},
			"", `include "~/b.md" starts with ~`},
		{"dot dot", map[string]string{"tasks.md": main("<!-- igris:include sub/../b.md -->"), "b.md": b},
			"", `include "sub/../b.md" has a .. element`},
		{"outside through a symlink", map[string]string{"tasks.md": main("<!-- igris:include out.md -->")},
			"out.md", ""},
		{"under .igris", map[string]string{"tasks.md": main("<!-- igris:include .igris/b.md -->"), ".igris/b.md": b},
			"", `include ".igris/b.md" is under .igris/`},
		{"the main plan", map[string]string{"tasks.md": main("<!-- igris:include ./tasks.md -->")},
			"", `include "./tasks.md" is the main plan tasks.md itself`},
		{"twice", map[string]string{"tasks.md": main("<!-- igris:include b.md -->", "<!-- igris:include ./b.md -->"), "b.md": b},
			"", `tasks.md:9: include "./b.md" names b.md again (first included at line 8)`},
		{"nested", map[string]string{"tasks.md": main("<!-- igris:include b.md -->"), "b.md": b + "<!-- igris:include c.md -->\n", "c.md": ""},
			"", "b.md:6: only the main plan may include files; move this include to tasks.md"},
		{"table after the include", map[string]string{"tasks.md": main("<!-- igris:include b.md -->", "", "| ID | Status | Model |", "|---|---|---|", "| X-1 | ready | sonnet |"), "b.md": b},
			"", "tasks.md:10: task table after the include line at line 8"},
		{"table before the first heading", map[string]string{"tasks.md": main("<!-- igris:include b.md -->"), "b.md": "| ID | Status | Model |\n|---|---|---|\n| X-1 | ready | sonnet |\n"},
			"", "b.md:1: task table outside a phase"},
		{"task ID in two files", map[string]string{"tasks.md": main("<!-- igris:include b.md -->"), "b.md": phaseTable("B", "| A-1 | — | ready | sonnet |")},
			"", "b.md:5: task A-1 is also in tasks.md:6; task IDs must be unique across the plan's files"},
		{"phase ID in two files", map[string]string{"tasks.md": main("<!-- igris:include b.md -->"), "b.md": phaseTable("a", "| B-1 | — | ready | sonnet |")},
			"", "b.md:1: phase a is also in tasks.md:2; phase IDs must be unique across the plan's files"},
		{"included marker 3", map[string]string{"tasks.md": main("<!-- igris:include b.md -->"), "b.md": "<!-- igris:format 3 -->\n" + b},
			"", "b.md:1: an included file may declare format 1 or 2, not 3"},
		{"unknown dependency in an included file", map[string]string{"tasks.md": main("<!-- igris:include b.md -->"), "b.md": phaseTable("B", "| B-1 | Z-9 | blocked | sonnet |")},
			"", "b.md:5: B-1 depends on unknown task Z-9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeFiles(t, tt.files)
			want := tt.want
			if tt.link != "" {
				// A target outside the root: a file in another temp dir.
				out := filepath.Join(t.TempDir(), "outside.md")
				if err := os.WriteFile(out, []byte(b), 0o644); err != nil { //nolint:gosec // test file
					t.Fatal(err)
				}
				if err := os.Symlink(out, filepath.Join(dir, tt.link)); err != nil {
					t.Fatal(err)
				}
				want = `tasks.md:8: include "out.md" leads outside the project through a symlink`
			}
			issues := loadSplit(t).Validate(testRules)
			for _, is := range issues {
				if strings.Contains(is.Error(), want) {
					return
				}
			}
			t.Errorf("no issue containing %q in %v", want, issues)
		})
	}
}

// A symlink to a file inside the project is followed, as for the plan.
func TestIncludeSymlinkInside(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"tasks.md":     format2 + "<!-- igris:include b.md -->\n",
		"real/b.md":    phaseTable("B", "| B-1 | — | ready | sonnet |"),
		"unrelated.md": "",
	})
	if err := os.Symlink(filepath.Join(dir, "real", "b.md"), filepath.Join(dir, "b.md")); err != nil {
		t.Fatal(err)
	}
	p := loadSplit(t)
	if issues := p.Validate(testRules); len(issues) > 0 || p.Task("B-1") == nil {
		t.Fatalf("issues %v, tasks %d", issues, len(p.Tasks))
	}
}

// The root is Options.Root: an include outside it is refused even when it
// is next to the plan.
func TestIncludeOutsideRoot(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"plans/tasks.md": format2 + "<!-- igris:include b.md -->\n",
		"plans/b.md":     phaseTable("B", "| B-1 | — | ready | sonnet |"),
	})
	p, err := Load(filepath.Join("plans", "tasks.md"), Options{Root: filepath.Join(dir, "plans", "b.md", "..", "..", "elsewhere")})
	if err != nil {
		t.Fatal(err)
	}
	if issues := p.Validate(testRules); len(issues) == 0 || !strings.Contains(issues[0].Error(), "can't be checked: resolve the project root") {
		t.Errorf("issues %v", issues)
	}
	if err := os.Mkdir(filepath.Join(dir, "elsewhere"), 0o755); err != nil { //nolint:gosec // test dir
		t.Fatal(err)
	}
	p, err = Load(filepath.Join("plans", "tasks.md"), Options{Root: filepath.Join(dir, "elsewhere")})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("plans", "tasks.md") + `:2: include "b.md" leads outside the project`
	if issues := p.Validate(testRules); len(issues) == 0 || !strings.Contains(issues[0].Error(), want) {
		t.Errorf("issues %v, want %q", issues, want)
	}
}

// A FIFO in an included file's place is an issue, never a hang.
func TestIncludeNotRegular(t *testing.T) {
	dir := writeFiles(t, map[string]string{"tasks.md": format2 + "<!-- igris:include b.md -->\n"})
	if err := syscall.Mkfifo(filepath.Join(dir, "b.md"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	issues := loadSplit(t).Validate(testRules)
	if len(issues) == 0 || !strings.Contains(issues[0].Error(), "tasks.md:2: included file b.md is not a regular file") {
		t.Errorf("issues %v", issues)
	}
}

// At most MaxFiles files, the main plan included; the include that crosses
// the limit is the one reported.
func TestIncludeTooManyFiles(t *testing.T) {
	files := map[string]string{}
	main := format2
	for i := range MaxFiles {
		name := "p" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + ".md"
		files[name] = phaseTable("P"+strings.TrimSuffix(name, ".md"), "| "+name+" | — | ready | sonnet |")
		main += "<!-- igris:include " + name + " -->\n"
	}
	files["tasks.md"] = main
	writeFiles(t, files)
	p := loadSplit(t)
	issues := p.Validate(testRules)
	if len(issues) != 1 || !strings.Contains(issues[0].Error(), "tasks.md:33: include \"pbf.md\" is one file too many: a plan has at most 32 files") {
		t.Errorf("issues %v", issues)
	}
	if len(p.Files()) != MaxFiles {
		t.Errorf("%d files read, want %d", len(p.Files()), MaxFiles)
	}
}

// Each file and all files together at most MaxSize.
func TestIncludeSizeLimits(t *testing.T) {
	big := func(n int) string {
		return phaseTable("X", "| X-1 | — | done | sonnet |") + strings.Repeat("x", n) + "\n"
	}
	half := MaxSize/2 + 1
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"one file over", map[string]string{"tasks.md": format2 + "<!-- igris:include a.md -->\n", "a.md": big(MaxSize)},
			"tasks.md:2: included file a.md is over 16 MiB"},
		{"together over", map[string]string{"tasks.md": format2 + "<!-- igris:include a.md -->\n<!-- igris:include b.md -->\n", "a.md": big(half), "b.md": strings.Replace(big(half), "X", "Y", 2)},
			`tasks.md:3: include "b.md" takes the plan's files over 16 MiB together`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeFiles(t, tt.files)
			issues := loadSplit(t).Validate(testRules)
			if len(issues) != 1 || !strings.Contains(issues[0].Error(), tt.want) {
				t.Errorf("issues %v, want %q", issues, tt.want)
			}
		})
	}
}

// Issues are sorted in plan order: an included file's issues take the
// place of its include line, after that line's own issues.
func TestIncludeIssueOrder(t *testing.T) {
	writeFiles(t, map[string]string{
		"tasks.md": phaseTable("A", "| A-1 | — | ready | nope |") + "<!-- igris:include b.md -->\n" + phaseTable("C", "| C-1 | — | ready | nope |"),
		"b.md":     phaseTable("B", "| B-1 | — | ready | nope |"),
	})
	var got []string
	for _, is := range loadSplit(t).Validate(testRules) {
		got = append(got, is.File+":"+strings.SplitN(is.Error(), ":", 3)[1])
	}
	if want := []string{"tasks.md:5", "tasks.md:6", "b.md:5", "tasks.md:11"}; !slices.Equal(got, want) {
		t.Errorf("issue order %v, want %v", got, want)
	}
}

// Parse reads no other file, so an include is an issue there; a fenced
// include is not an include.
func TestParseInclude(t *testing.T) {
	p := Parse("tasks.md", []byte(format2+"<!-- igris:include b.md -->\n"+phaseTable("A", "| A-1 | — | ready | sonnet |")), Options{})
	if issues := p.Validate(testRules); len(issues) != 1 || !strings.Contains(issues[0].Error(), "tasks.md:2: includes are not read here") {
		t.Errorf("issues %v", issues)
	}
	p = Parse("tasks.md", []byte("```\n<!-- igris:include b.md -->\n```\n"+phaseTable("A", "| A-1 | — | ready | sonnet |")), Options{})
	if issues := p.Validate(testRules); len(issues) > 0 || p.Split() {
		t.Errorf("fenced include: issues %v, split %v", issues, p.Split())
	}
}

// Inline gives one file with the split plan's phases and tasks, for a dry
// run's scratch copy; an included file's marker is dropped and a fence it
// leaves open is closed.
func TestInline(t *testing.T) {
	writeFiles(t, map[string]string{
		"tasks.md": "\xef\xbb\xbf" + format2 + phaseTable("A", "| A-1 | — | done | sonnet |") + "<!-- igris:include b.md -->\n" +
			phaseTable("C", "| C-1 | A-1…B-1 | blocked | opus |"),
		"b.md": "<!-- igris:format 2 -->\r\n" + strings.ReplaceAll(phaseTable("B", "| B-1 | A-1 | ready | sonnet |"), "\n", "\r\n") + "```\n## Z — fenced\n",
	})
	p := loadSplit(t)
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatalf("issues: %v", issues)
	}
	one := Parse("tasks.md", p.Inline(), Options{})
	if issues := one.Validate(testRules); len(issues) > 0 {
		t.Fatalf("inlined plan: %v\n%s", issues, p.Inline())
	}
	summary := func(p *Plan) string {
		var b strings.Builder
		for _, ph := range p.Phases {
			b.WriteString(ph.ID + ":")
			for _, t := range ph.Tasks {
				b.WriteString(" " + t.ID + "/" + t.Status.String() + "/" + t.Rank + "/" + strings.Join(t.Deps, ","))
			}
			b.WriteString("\n")
		}
		return b.String()
	}
	if a, b := summary(p), summary(one); a != b {
		t.Errorf("inlined plan differs:\n%s---\n%s", a, b)
	}
}

// Until the writer learns split plans, it refuses one and writes nothing.
func TestWriterRefusesSplitPlan(t *testing.T) {
	in := format2 + phaseTable("A", "| A-1 | — | ready | sonnet |") + "<!-- igris:include b.md -->\n"
	writeFiles(t, map[string]string{"tasks.md": in, "b.md": phaseTable("B", "| B-1 | — | ready | sonnet |")})
	_, err := NewWriter("tasks.md", Options{}, testRules).Update(context.Background(), set("A-1", Done))
	if err == nil || !strings.Contains(err.Error(), "split plan") {
		t.Errorf("err = %v", err)
	}
	if out := readPlan(t, "tasks.md"); out != in {
		t.Errorf("plan changed: %q", out)
	}
}
