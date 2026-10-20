package plan

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fileCaption is the line before a ```markdown block that names the file
// of a split example, e.g. **`phases/v2.md`**.
var fileCaption = regexp.MustCompile("^\\*\\*`([^`]+\\.md)`\\*\\*$")

// docBlock is a ```markdown block of docs/plan-format.md.
type docBlock struct {
	src     string
	end     int    // line of the closing fence
	caption string // the file it is in a split example, "" for none
}

// docBlocks returns the ```markdown blocks of a document.
func docBlocks(data string) []docBlock {
	var out []docBlock
	var block []string
	in, prev := false, ""
	for i, line := range strings.Split(data, "\n") {
		switch {
		case !in && line == "```markdown":
			in, block = true, nil
		case in && line == "```":
			in = false
			b := docBlock{src: strings.Join(block, "\n") + "\n", end: i + 1}
			if m := fileCaption.FindStringSubmatch(prev); m != nil {
				b.caption = m[1]
			}
			out = append(out, b)
			prev = ""
		case in:
			block = append(block, line)
		case line != "":
			prev = line
		}
	}
	return out
}

// docs/plan-format.md describes formats 1 and 2 for people and tools
// writing plans (SPEC §19). Every ```markdown block in it with a phase
// heading is a plan igris must read as valid, with Status cells in step
// with their dependencies; examples of mistakes are written as ```text.
// Blocks captioned with a file name are the files of one split plan: a
// **`tasks.md`** block and the captioned blocks right after it.
func TestPlanFormatDocExamples(t *testing.T) {
	data, err := os.ReadFile("../../docs/plan-format.md")
	if err != nil {
		t.Fatal(err)
	}
	// The default ranks (config.Default; config imports plan).
	models := map[string]string{"sonnet": "sonnet", "opus": "opus", "fable": "fable", "haiku": "haiku"}
	check := func(p *Plan, at int, r Rules) {
		t.Helper()
		if issues := p.Validate(r); len(issues) > 0 {
			t.Errorf("example ending on line %d is not valid: %v", at, issues)
		}
		if drift := p.Readiness(); len(drift) > 0 {
			t.Errorf("example ending on line %d has Status out of step with Deps: %v", at, drift)
		}
	}
	blocks := docBlocks(string(data))
	n := 0
	for i := 0; i < len(blocks); i++ {
		b := blocks[i]
		switch {
		case b.caption == "":
			if strings.Contains(b.src, "\n## ") {
				n++
				check(Parse("plan-format.md", []byte(b.src), Options{}), b.end, Rules{Models: models})
			}
			continue
		case b.caption != "tasks.md":
			t.Fatalf("line %d: a **`%s`** block without a **`tasks.md`** block before it", b.end, b.caption)
		}
		dir := t.TempDir()
		for ; i < len(blocks) && blocks[i].caption != "" && (blocks[i].caption != "tasks.md" || blocks[i] == b); i++ {
			path := filepath.Join(dir, filepath.FromSlash(blocks[i].caption))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // test dir
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(blocks[i].src), 0o644); err != nil { //nolint:gosec // test file
				t.Fatal(err)
			}
		}
		i--
		n++
		p, err := Load(filepath.Join(dir, "tasks.md"), Options{Root: dir})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Files()) < 2 {
			t.Errorf("split example ending on line %d includes no file", blocks[i].end)
		}
		check(p, blocks[i].end, Rules{Models: models, Root: dir})
	}
	if n == 0 {
		t.Fatal("docs/plan-format.md has no example plan")
	}
}
