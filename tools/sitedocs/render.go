package main

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// renderer turns the small markdown subset the docs pages use into HTML:
// headings, paragraphs, bullet and numbered lists (nested by indent), fenced
// code, GFM tables, block quotes, rules and bold, italic, code and links.
// Raw <details>/<summary> lines pass through; HTML comments are dropped.
// It is not a general markdown engine: anything outside the subset is shown
// as plain paragraph text, so a mistake is visible rather than lost.
type renderer struct {
	// link rewrites a link target found in the markdown into the one the
	// built page should use.
	link func(href string) string

	out     strings.Builder
	ids     map[string]int
	headers []heading
}

type heading struct {
	Level int
	ID    string
	Text  string
}

var (
	// reDetails is a bare <details>, </details>, <summary>…</summary> or
	// <summary> line, the only raw HTML the docs use.
	reDetails  = regexp.MustCompile(`^(<details>|</details>|<summary>[^<>]*</summary>|<summary>|</summary>)$`)
	reHeading  = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)
	reFence    = regexp.MustCompile("^(`{3,}|~{3,})\\s*([A-Za-z0-9_-]*)")
	reListItem = regexp.MustCompile(`^(\s*)([-*]|\d+\.)\s+(.*)$`)
	reTableSep = regexp.MustCompile(`^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$`)
	reRule     = regexp.MustCompile(`^\s{0,3}(-{3,}|\*{3,})\s*$`)
	reLink     = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	reBold     = regexp.MustCompile(`\*\*(\S(?:.*?\S)?)\*\*`)
	reItalic   = regexp.MustCompile(`(^|[^*\w])\*([^*\s](?:[^*]*[^*\s])?)\*`)
	reCodeMark = regexp.MustCompile("\x00(\\d+)\x00")
)

func newRenderer(link func(string) string) *renderer {
	return &renderer{link: link, ids: map[string]int{}}
}

// render returns the HTML for src and records its headings.
func (r *renderer) render(src string) string {
	r.out.Reset()
	r.headers = nil
	r.blocks(strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n"))
	return r.out.String()
}

func (r *renderer) blocks(lines []string) {
	for i := 0; i < len(lines); {
		line := lines[i]
		trim := strings.TrimSpace(line)
		switch {
		case trim == "":
			i++
		case strings.HasPrefix(trim, "<!--"):
			for i < len(lines) && !strings.Contains(lines[i], "-->") {
				i++
			}
			i++
		case reDetails.MatchString(trim):
			// Only the bare tags: anything with attributes is text.
			r.out.WriteString(trim + "\n")
			i++
		case reFence.MatchString(trim):
			i = r.fence(lines, i)
		case reHeading.MatchString(line):
			m := reHeading.FindStringSubmatch(line)
			r.heading(len(m[1]), m[2])
			i++
		case reRule.MatchString(line):
			r.out.WriteString("<hr>\n")
			i++
		case strings.HasPrefix(trim, "|") && i+1 < len(lines) && reTableSep.MatchString(lines[i+1]):
			i = r.table(lines, i)
		case strings.HasPrefix(trim, ">"):
			i = r.quote(lines, i)
		case reListItem.MatchString(line):
			i = r.list(lines, i)
		default:
			i = r.paragraph(lines, i)
		}
	}
}

func (r *renderer) heading(level int, text string) {
	plain := stripInline(text)
	id := slug(plain)
	if n := r.ids[id]; n > 0 {
		r.ids[id]++
		id = fmt.Sprintf("%s-%d", id, n)
	} else {
		r.ids[id] = 1
	}
	r.headers = append(r.headers, heading{Level: level, ID: id, Text: plain})
	fmt.Fprintf(&r.out, "<h%d id=%q>%s</h%d>\n", level, id, r.inline(text), level)
}

func (r *renderer) fence(lines []string, i int) int {
	m := reFence.FindStringSubmatch(strings.TrimSpace(lines[i]))
	mark, lang := m[1], m[2]
	i++
	var body []string
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, mark[:1]) && strings.Trim(t, mark[:1]) == "" && len(t) >= len(mark) {
			i++
			break
		}
		body = append(body, lines[i])
	}
	class := ""
	if lang != "" {
		class = fmt.Sprintf(" class=%q", "language-"+lang)
	}
	fmt.Fprintf(&r.out, "<pre tabindex=\"0\"><code%s>%s</code></pre>\n", class, html.EscapeString(strings.Join(body, "\n")))
	return i
}

func (r *renderer) table(lines []string, i int) int {
	head := splitRow(lines[i])
	i += 2
	r.out.WriteString("<div class=\"table\"><table>\n<thead><tr>")
	for _, c := range head {
		fmt.Fprintf(&r.out, "<th>%s</th>", r.inline(c))
	}
	r.out.WriteString("</tr></thead>\n<tbody>\n")
	for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
		r.out.WriteString("<tr>")
		for _, c := range splitRow(lines[i]) {
			fmt.Fprintf(&r.out, "<td>%s</td>", r.inline(c))
		}
		r.out.WriteString("</tr>\n")
	}
	r.out.WriteString("</tbody></table></div>\n")
	return i
}

// splitRow splits a table row on pipes that are not escaped or inside a
// code span, and turns the escaped ones into literal pipes.
func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	var cells []string
	var cur strings.Builder
	inCode := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && i+1 < len(line) && line[i+1] == '|':
			cur.WriteByte('|')
			i++
		case c == '`':
			inCode = !inCode
			cur.WriteByte(c)
		case c == '|' && !inCode:
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if rest := strings.TrimSpace(cur.String()); rest != "" {
		cells = append(cells, rest)
	}
	return cells
}

func (r *renderer) quote(lines []string, i int) int {
	var inner []string
	for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
		t := strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")
		inner = append(inner, strings.TrimPrefix(t, " "))
	}
	r.out.WriteString("<blockquote>\n")
	r.blocks(inner)
	r.out.WriteString("</blockquote>\n")
	return i
}

type listLine struct {
	indent  int
	ordered bool
	text    string
}

// list renders one list, nesting by indent. A line indented under an item
// continues it; a blank line ends the list unless another item follows.
func (r *renderer) list(lines []string, i int) int {
	var items []listLine
	for i < len(lines) {
		line := lines[i]
		if m := reListItem.FindStringSubmatch(line); m != nil {
			items = append(items, listLine{indent: len(strings.ReplaceAll(m[1], "\t", "    ")), ordered: m[2][0] >= '0' && m[2][0] <= '9', text: m[3]})
			i++
			continue
		}
		if strings.TrimSpace(line) == "" {
			j := i
			for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
				j++
			}
			if j < len(lines) && reListItem.MatchString(lines[j]) {
				i = j
				continue
			}
			break
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			items[len(items)-1].text += " " + strings.TrimSpace(line)
			i++
			continue
		}
		break
	}
	r.listItems(items)
	return i
}

func (r *renderer) listItems(items []listLine) {
	type open struct {
		indent int
		tag    string
	}
	var stack []open
	closeTo := func(indent int) {
		for len(stack) > 0 && stack[len(stack)-1].indent > indent {
			fmt.Fprintf(&r.out, "</li></%s>\n", stack[len(stack)-1].tag)
			stack = stack[:len(stack)-1]
		}
	}
	for _, it := range items {
		tag := "ul"
		if it.ordered {
			tag = "ol"
		}
		closeTo(it.indent)
		switch {
		case len(stack) == 0 || stack[len(stack)-1].indent < it.indent:
			fmt.Fprintf(&r.out, "<%s>\n<li>", tag)
			stack = append(stack, open{it.indent, tag})
		default:
			r.out.WriteString("</li>\n<li>")
		}
		r.out.WriteString(r.inline(it.text))
	}
	closeTo(-1)
}

func (r *renderer) paragraph(lines []string, i int) int {
	var para []string
	for ; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || (len(para) > 0 && (reFence.MatchString(t) || reHeading.MatchString(lines[i]) || reListItem.MatchString(lines[i]) || strings.HasPrefix(t, "|") || strings.HasPrefix(t, ">") || reDetails.MatchString(t) || strings.HasPrefix(t, "<!--"))) {
			break
		}
		para = append(para, t)
	}
	fmt.Fprintf(&r.out, "<p>%s</p>\n", r.inline(strings.Join(para, "\n")))
	return i
}

// inline renders code spans, links, bold and italic. Code spans are lifted
// out first so their contents are never touched by the other rules.
func (r *renderer) inline(s string) string {
	var codes []string
	s = extractCode(s, &codes)
	s = html.EscapeString(s)
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		sub := reLink.FindStringSubmatch(m)
		return fmt.Sprintf("<a href=\"%s\">%s</a>", html.EscapeString(r.link(html.UnescapeString(sub[2]))), sub[1])
	})
	s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
	s = reItalic.ReplaceAllString(s, "$1<em>$2</em>")
	return reCodeMark.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.Atoi(strings.Trim(m, "\x00")) // the marker was made by extractCode
		return "<code>" + html.EscapeString(codes[n]) + "</code>"
	})
}

// extractCode replaces each code span with a numbered marker. A span opens
// with a run of backticks and closes with a run of the same length, so
// “ `x` ” keeps its inner backticks; one space either side is trimmed
// when the content itself starts or ends with a backtick.
func extractCode(s string, codes *[]string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '`' {
			b.WriteByte(s[i])
			i++
			continue
		}
		n := 0
		for i+n < len(s) && s[i+n] == '`' {
			n++
		}
		closeAt := findRun(s, i+n, n)
		if closeAt < 0 {
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		body := s[i+n : closeAt]
		if len(body) > 1 && strings.HasPrefix(body, " ") && strings.HasSuffix(body, " ") && strings.Trim(body, " ") != "" {
			body = body[1 : len(body)-1]
		}
		*codes = append(*codes, body)
		fmt.Fprintf(&b, "\x00%d\x00", len(*codes)-1)
		i = closeAt + n
	}
	return b.String()
}

// findRun returns the index of the first run of exactly n backticks at or
// after from, or -1.
func findRun(s string, from, n int) int {
	for i := from; i < len(s); i++ {
		if s[i] != '`' {
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		if j-i == n {
			return i
		}
		i = j - 1
	}
	return -1
}

// stripInline removes inline markup, for heading ids and the page outline.
func stripInline(s string) string {
	s = reLink.ReplaceAllString(s, "$1")
	s = strings.NewReplacer("**", "", "`", "").Replace(s)
	return strings.TrimSpace(s)
}

// slug makes a heading id the way GitHub does: lower case, letters, digits,
// hyphens and underscores kept, spaces turned into hyphens, the rest dropped.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r > 127 && isLetter(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isLetter(r rune) bool { return strings.ToLower(string(r)) != strings.ToUpper(string(r)) }
