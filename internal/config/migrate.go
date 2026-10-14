package config

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// deprecation is a key igris still reads but that is going away: `igris
// config migrate` removes it from igris.toml (SPEC §12).
type deprecation struct {
	table, key string // the key's table ("" for the root) and name
	since      string // the release that deprecated it
	why        string
}

// name is the key as igris.toml spells it: table.key.
func (d deprecation) name() string {
	if d.table == "" {
		return d.key
	}
	return d.table + "." + d.key
}

// deprecations are the keys `igris config migrate` removes.
var deprecations = []deprecation{
	{table: "claude", key: "command", since: "0.1.2", why: "ignored: igris always starts claude from PATH"},
}

// Change is one line `igris config migrate` removes.
type Change struct {
	Line int    // 1-based line in igris.toml
	Old  string // the line, without its line ending
	Key  string // the deprecated key, e.g. claude.command
	Why  string // since when, and why it goes
}

// Migrate returns data without the deprecated keys and the lines it removed,
// in file order. Every other byte is kept: comments, layout, line endings, a
// byte order mark. A key it can't remove by dropping one line (an inline
// table, a multi-line value) is an error that says to remove it by hand;
// each removal is checked by decoding the result. Without deprecated keys,
// out is data and there are no changes.
func Migrate(data []byte) (out []byte, changes []Change, err error) {
	var orig map[string]any
	md, err := toml.Decode(string(data), &orig)
	if err != nil {
		return nil, nil, fmt.Errorf("igris.toml: %w", err)
	}
	lines := splitKeepEnds(data)
	drop := map[int]bool{}
	for _, d := range deprecations {
		if !md.IsDefined(d.tablePath()...) {
			continue
		}
		i := findKeyLine(lines, d)
		if i < 0 || !removes(lines, drop, i, d, orig) {
			n := 0
			if i >= 0 {
				n = i + 1
			}
			return nil, nil, fmt.Errorf("igris.toml: can't remove %s automatically%s; delete it by hand", d.name(), lineHint(n))
		}
		drop[i] = true
		changes = append(changes, Change{
			Line: i + 1, Old: strings.TrimRight(string(lines[i]), "\r\n"), Key: d.name(),
			Why: fmt.Sprintf("deprecated in %s, %s", d.since, d.why),
		})
	}
	if len(changes) == 0 {
		return data, nil, nil
	}
	slices.SortFunc(changes, func(a, b Change) int { return a.Line - b.Line })
	return join(lines, drop), changes, nil
}

func (d deprecation) tablePath() []string {
	if d.table == "" {
		return []string{d.key}
	}
	return []string{d.table, d.key}
}

func lineHint(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" (line %d)", n)
}

// removes says dropping line i (with the lines already in drop) removes
// exactly d's key: the result decodes, and is the original without it.
func removes(lines [][]byte, drop map[int]bool, i int, d deprecation, orig map[string]any) bool {
	try := map[int]bool{i: true}
	for k := range drop {
		try[k] = true
	}
	var got map[string]any
	md, err := toml.Decode(string(join(lines, try)), &got)
	if err != nil || md.IsDefined(d.tablePath()...) {
		return false
	}
	// The original without every key whose line is dropped.
	want := deepCopy(orig)
	for _, e := range deprecations {
		if try[findKeyLine(lines, e)] {
			deleteKey(want, e)
		}
	}
	// An emptied table and no table are the same config.
	return reflect.DeepEqual(pruned(got), pruned(want))
}

// pruned is m without its empty tables, at any depth.
func pruned(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if t, ok := v.(map[string]any); ok {
			if t = pruned(t); len(t) == 0 {
				continue
			}
			v = t
		}
		out[k] = v
	}
	return out
}

func deleteKey(m map[string]any, d deprecation) {
	if d.table == "" {
		delete(m, d.key)
		return
	}
	if t, ok := m[d.table].(map[string]any); ok {
		delete(t, d.key)
	}
}

func deepCopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if t, ok := v.(map[string]any); ok {
			v = deepCopy(t)
		}
		out[k] = v
	}
	return out
}

// findKeyLine returns the index of the line that sets d's key, or -1: a
// `key = …` line under its table header, or `table.key = …` at the root.
// Lines inside multi-line strings are skipped.
func findKeyLine(lines [][]byte, d deprecation) int {
	table, inString := "", ""
	for i, raw := range lines {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimRight(string(raw), "\r\n"), "\uFEFF"))
		if inString != "" {
			if strings.Count(line, inString)%2 == 1 {
				inString = ""
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "[["):
			table = "\x00" // an array of tables: never the deprecated key's table
			continue
		case strings.HasPrefix(line, "["):
			if end := strings.Index(line, "]"); end > 0 {
				table = unquote(strings.TrimSpace(line[1:end]))
			}
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if ok {
			key = strings.TrimSpace(key)
			if (table == d.table && unquote(key) == d.key) ||
				(table == "" && d.table != "" && dotted(key) == d.name()) {
				return i
			}
		}
		for _, q := range []string{`"""`, "'''"} {
			if strings.Count(line, q)%2 == 1 {
				inString = q
				break
			}
		}
	}
	return -1
}

// dotted normalizes a dotted key: spaces around the dots and quotes removed.
func dotted(key string) string {
	parts := strings.Split(key, ".")
	for i, p := range parts {
		parts[i] = unquote(strings.TrimSpace(p))
	}
	return strings.Join(parts, ".")
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// splitKeepEnds splits data into lines that keep their line endings.
func splitKeepEnds(data []byte) [][]byte {
	var out [][]byte
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			out = append(out, data)
			break
		}
		out = append(out, data[:i+1])
		data = data[i+1:]
	}
	return out
}

func join(lines [][]byte, drop map[int]bool) []byte {
	var b bytes.Buffer
	for i, l := range lines {
		if drop[i] {
			if i == 0 && bytes.HasPrefix(l, []byte("\uFEFF")) {
				b.WriteString("\uFEFF") // the byte order mark stays
			}
			continue
		}
		b.Write(l)
	}
	return b.Bytes()
}
