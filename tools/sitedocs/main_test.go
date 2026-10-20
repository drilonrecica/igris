package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	r := newRenderer(func(h string) string { return "L(" + h + ")" })
	tests := []struct {
		name, in string
		want     []string
	}{
		{"heading id", "## A `x` — b\n", []string{`<h2 id="a-x--b">A <code>x</code> — b</h2>`}},
		{"paragraph inline", "one **bold** and *em* with `a<b` and [t](u.md#f)\n", []string{"<strong>bold</strong>", "<em>em</em>", "<code>a&lt;b</code>", `<a href="L(u.md#f)">t</a>`}},
		{"code span keeps markup", "`**x** [a](b)`\n", []string{"<code>**x** [a](b)</code>"}},
		{"double backtick span", "`` `ready` `` is a status\n", []string{"<code>`ready`</code>"}},
		{"fence", "```toml\n[run]\nverify = \"<x>\"\n```\n", []string{`<code class="language-toml">[run]`, "&lt;x&gt;"}},
		{"nested list", "- a\n  - b\n- c\n", []string{"<ul>\n<li>a<ul>\n<li>b</li></ul>\n</li>\n<li>c</li></ul>"}},
		{"ordered list", "1. a\n2. b\n", []string{"<ol>\n<li>a</li>\n<li>b</li></ol>"}},
		{"table with escaped pipe", "| A | B |\n|---|---|\n| `x\\|y` | \\| z |\n", []string{"<th>A</th>", "<td><code>x|y</code></td>", "<td>| z</td>"}},
		{"comment dropped", "<!-- hidden -->\n\ntext\n", []string{"<p>text</p>"}},
		{"quote", "> note\n> more\n", []string{"<blockquote>", "<p>note\nmore</p>"}},
		{"html escaped", "a <script> b\n", []string{"a &lt;script&gt; b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.render(tt.in)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("render(%q) = %q, missing %q", tt.in, got, w)
				}
			}
		})
	}
	if strings.Contains(r.render("<!-- hidden -->\n"), "hidden") {
		t.Error("comment leaked")
	}
}

func TestDuplicateHeadingIDs(t *testing.T) {
	r := newRenderer(func(h string) string { return h })
	r.render("## Same\n\n## Same\n")
	if len(r.headers) != 2 || r.headers[0].ID != "same" || r.headers[1].ID != "same-1" {
		t.Errorf("headers = %+v", r.headers)
	}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"SPEC.md", "docs/json/x.json", "docs/plan-format.md"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b := &builder{root: root, bySrc: map[string]page{"docs/plan-format.md": {Slug: "plan-format"}}}
	tests := []struct{ src, href, want string }{
		{"site/docs/a.md", "https://x.dev/y", "https://x.dev/y"},
		{"site/docs/a.md", "#top", "#top"},
		{"site/docs/a.md", "../../docs/plan-format.md#status", "plan-format.html#status"},
		{"docs/config.md", "../SPEC.md#12-config", repoURL + "/blob/master/SPEC.md#12-config"},
		{"docs/compatibility.md", "json/", repoURL + "/tree/master/docs/json"},
	}
	for _, tt := range tests {
		got := b.resolve(tt.src, tt.href)
		if got != tt.want {
			t.Errorf("resolve(%q, %q) = %q, want %q", tt.src, tt.href, got, tt.want)
		}
	}
	if len(b.problems) != 0 {
		t.Errorf("unexpected problems: %v", b.problems)
	}
	b.resolve("docs/config.md", "../missing.md")
	if len(b.problems) != 1 || !strings.Contains(b.problems[0], "no such file") {
		t.Errorf("problems = %v", b.problems)
	}
}

// TestBuildRepo builds the real site docs: every page's source exists, every
// link to a file or a heading resolves, and every page is written.
func TestBuildRepo(t *testing.T) {
	out := t.TempDir()
	if err := build("../..", out); err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		raw, err := os.ReadFile(filepath.Join(out, p.Slug+".html")) //nolint:gosec // test output directory
		if err != nil {
			t.Fatalf("page %s: %v", p.Slug, err)
		}
		if !strings.Contains(string(raw), "<h1") {
			t.Errorf("page %s has no title", p.Slug)
		}
	}
	index, err := os.ReadFile(filepath.Join(out, "index.html")) //nolint:gosec // test output directory
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		if !strings.Contains(string(index), p.Slug+".html") {
			t.Errorf("index does not list %s", p.Slug)
		}
	}
}

func TestBuildReportsBrokenHeadingLink(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range pages {
		write(p.Src, "# "+p.Nav+"\n")
	}
	write("site/docs/install.md", "# Install\n\n[x](quick-start.md#nope)\n")
	err := build(root, filepath.Join(root, "out"))
	if err == nil || !strings.Contains(err.Error(), "quick-start.html#nope: no such heading") {
		t.Errorf("err = %v", err)
	}
}
