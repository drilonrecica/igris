// Command sitedocs builds the documentation pages of the project site
// (decisions.md P0-09) from markdown: the pages written for the site in
// site/docs/ and the reference pages that live in docs/ (the plan format, the
// generated config reference, the compatibility promise), so those are
// published from the one copy and cannot drift. Standard library only. It
// fails on a link to a file that doesn't exist or to a heading that isn't
// there, so a renamed section can't leave a dead link on the site.
package main

import (
	"embed"
	"flag"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed page.html.tmpl
var pageFS embed.FS

const repoURL = "https://github.com/drilonrecica/igris"

// page is one docs page: where its markdown lives and where it goes.
type page struct {
	Slug    string // output name: docs/<slug>.html
	Nav     string // sidebar label
	Summary string // one line on the docs index
	Src     string // markdown path, relative to the repository root
}

// pages is the docs in reading order.
var pages = []page{
	{"install", "Install", "Go, Homebrew or a signed binary; what you need first.", "site/docs/install.md"},
	{"quick-start", "Quick start", "From a new project to a finished phase.", "site/docs/quick-start.md"},
	{"plan-format", "Plan format", "Phases, task tables, every column and what igris writes.", "docs/plan-format.md"},
	{"config", "Configuration", "Every igris.toml key with its type and default.", "docs/config.md"},
	{"modes", "Modes", "The permission modes, and which one a task gets.", "site/docs/modes.md"},
	{"notifications", "Notifications", "Toasts, ntfy, Discord, Slack, Gotify and a signed webhook.", "site/docs/notifications.md"},
	{"usage", "Usage and plans", "Approve plans in igris, token usage, the rank audit and quota.", "site/docs/usage.md"},
	{"split-plans", "Split plans", "Spread a long plan over several files with includes.", "site/docs/split-plans.md"},
	{"templates", "Plan templates", "Start a project from go-cli, web-app or library.", "site/docs/templates.md"},
	{"draft", "Draft a plan", "Have Claude write a plan from a goal, a spec or an issue export.", "site/docs/draft.md"},
	{"detach-attach", "Detach and attach", "Leave a run going, and open it again from any terminal.", "site/docs/detach-attach.md"},
	{"parallel-phases", "Parallel phases", "Run independent tasks at once, each in its own worktree, and what it costs.", "site/docs/parallel-phases.md"},
	{"side-sessions", "Side sessions", "A Claude Code session of your own next to a run.", "site/docs/side-sessions.md"},
	{"adapt", "Adapt a plan", "Convert a plan in another format, and review it first.", "site/docs/adapt.md"},
	{"several-projects", "Several projects", "Running igris in more than one project at once.", "site/docs/several-projects.md"},
	{"faq", "FAQ", "Subscription or API, models, skip-permissions, Windows.", "site/docs/faq.md"},
	{"troubleshooting", "Troubleshooting", "What the symptoms mean and what to do.", "site/docs/troubleshooting.md"},
	{"upgrade", "Upgrade", "From any 0.x release to 1.0, then 1.1, 1.2, 1.4 and 1.5.", "site/docs/upgrade.md"},
	{"compatibility", "Compatibility", "What 1.x promises not to break, and the tests behind it.", "docs/compatibility.md"},
}

type rendered struct {
	page
	Title   string
	Body    template.HTML
	Outline []heading
	ids     map[string]bool
}

type linkRef struct {
	from, to, frag string
}

type builder struct {
	root     string
	bySrc    map[string]page
	problems []string
	refs     []linkRef
}

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "_site/docs", "output directory for the docs pages")
	flag.Parse()
	if err := build(*root, *out); err != nil {
		fmt.Fprintln(os.Stderr, "sitedocs:", err)
		os.Exit(1)
	}
}

func build(root, out string) error {
	b := &builder{root: root, bySrc: map[string]page{}}
	for _, p := range pages {
		b.bySrc[p.Src] = p
	}

	var done []*rendered
	for _, p := range pages {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p.Src))) //nolint:gosec // paths come from the page table above
		if err != nil {
			return fmt.Errorf("read %s: %w", p.Src, err)
		}
		src := p.Src
		r := newRenderer(func(href string) string { return b.resolve(src, href) })
		body := r.render(string(raw))
		rp := &rendered{page: p, Body: template.HTML(body), ids: map[string]bool{}} //nolint:gosec // rendered from our own markdown, text escaped
		for _, h := range r.headers {
			rp.ids[h.ID] = true
			switch h.Level {
			case 1:
				if rp.Title == "" {
					rp.Title = h.Text
				}
			case 2:
				rp.Outline = append(rp.Outline, h)
			}
		}
		if rp.Title == "" {
			b.problems = append(b.problems, p.Src+": no # title")
		}
		done = append(done, rp)
	}

	byslug := map[string]*rendered{}
	for _, d := range done {
		byslug[d.Slug] = d
	}
	for _, ref := range b.refs {
		if ref.frag != "" && !byslug[ref.to].ids[ref.frag] {
			b.problems = append(b.problems, fmt.Sprintf("%s: link to %s.html#%s: no such heading", ref.from, ref.to, ref.frag))
		}
	}
	if len(b.problems) > 0 {
		sort.Strings(b.problems)
		return fmt.Errorf("%d problems:\n  %s", len(b.problems), strings.Join(b.problems, "\n  "))
	}

	tmpl, err := template.ParseFS(pageFS, "page.html.tmpl")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		return err
	}
	for i, d := range done {
		data := struct {
			Pages      []page
			Current    *rendered
			Prev, Next *page
			Index      bool
		}{Pages: pages, Current: d}
		if i > 0 {
			data.Prev = &pages[i-1]
		}
		if i+1 < len(pages) {
			data.Next = &pages[i+1]
		}
		if err := write(tmpl, filepath.Join(out, d.Slug+".html"), data); err != nil {
			return err
		}
	}
	index := struct {
		Pages      []page
		Current    *rendered
		Prev, Next *page
		Index      bool
	}{Pages: pages, Index: true}
	return write(tmpl, filepath.Join(out, "index.html"), index)
}

func write(tmpl *template.Template, file string, data any) error {
	f, err := os.Create(file) //nolint:gosec // the output directory is a build-tool flag
	if err != nil {
		return err
	}
	if err := tmpl.Execute(f, data); err != nil {
		_ = f.Close()
		return fmt.Errorf("render %s: %w", file, err)
	}
	return f.Close()
}

// resolve maps a link found in the markdown file src to the one the site
// should use: another docs page becomes <slug>.html, anything else in the
// repository a GitHub URL, a web address stays. A target that is missing is
// recorded as a problem.
func (b *builder) resolve(src, href string) string {
	if strings.HasPrefix(href, "#") {
		return href
	}
	if u, err := url.Parse(href); err != nil || u.Scheme != "" {
		// A web address or a mail link stays; any other scheme (javascript:,
		// data:, file:) would run or load something from the page.
		if err == nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto") {
			return href
		}
		b.problems = append(b.problems, fmt.Sprintf("%s: link to %s: scheme not allowed (http, https, mailto or a repository path)", src, href))
		return "#"
	}
	target, frag, _ := strings.Cut(href, "#")
	rel := path.Clean(path.Join(path.Dir(src), target))
	suffix := ""
	if frag != "" {
		suffix = "#" + frag
	}
	if p, ok := b.bySrc[rel]; ok {
		b.refs = append(b.refs, linkRef{from: src, to: p.Slug, frag: frag})
		return p.Slug + ".html" + suffix
	}
	info, err := os.Stat(filepath.Join(b.root, filepath.FromSlash(rel)))
	if err != nil {
		b.problems = append(b.problems, fmt.Sprintf("%s: link to %s: no such file", src, href))
		return href
	}
	if info.IsDir() {
		return repoURL + "/tree/master/" + strings.TrimSuffix(rel, "/") + suffix
	}
	return repoURL + "/blob/master/" + rel + suffix
}
