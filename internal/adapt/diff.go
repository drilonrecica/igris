package adapt

import "strings"

// Op is what a diff line does.
type Op int

const (
	Equal Op = iota // in both files
	Del             // only in the original
	Add             // only in the proposal
)

// Line is one line of a line diff. Old and New are its 1-based line
// numbers in the original and the proposal; 0 where it has none.
type Line struct {
	Op   Op
	Text string
	Old  int
	New  int
}

// maxCells bounds the LCS table. Plans are a few hundred lines; past this
// the diff shows the whole original as removed and the proposal as added
// instead of using a lot of memory.
const maxCells = 16 << 20

// Lines splits a file into lines without their line endings. A final
// newline doesn't start another line.
func Lines(data []byte) []string {
	s := strings.TrimSuffix(string(data), "\n")
	if s == "" {
		return nil
	}
	out := strings.Split(s, "\n")
	for i, l := range out {
		out[i] = strings.TrimSuffix(l, "\r")
	}
	return out
}

// Diff returns a line diff of a and b: a longest common subsequence of
// lines kept, the rest removed (before) or added. Removed lines come
// before the added lines that replace them.
func Diff(a, b []string) []Line {
	// Common head and tail need no table.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	out := make([]Line, 0, len(a)+len(b))
	for i := range pre {
		out = append(out, Line{Equal, a[i], i + 1, i + 1})
	}
	out = append(out, middle(a[pre:len(a)-suf], b[pre:len(b)-suf], pre)...)
	for i := range suf {
		ai, bi := len(a)-suf+i, len(b)-suf+i
		out = append(out, Line{Equal, a[ai], ai + 1, bi + 1})
	}
	return out
}

// middle diffs a and b, which start after off equal lines, with an LCS
// table: lcs[i][j] is the LCS length of a[i:] and b[j:].
func middle(a, b []string, off int) []Line {
	n, m := len(a), len(b)
	var out []Line
	if n*m > maxCells {
		for i, s := range a {
			out = append(out, Line{Del, s, off + i + 1, 0})
		}
		for j, s := range b {
			out = append(out, Line{Add, s, 0, off + j + 1})
		}
		return out
	}
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			out = append(out, Line{Equal, a[i], off + i + 1, off + j + 1})
			i++
			j++
		case j == m || (i < n && lcs[i+1][j] >= lcs[i][j+1]):
			out = append(out, Line{Del, a[i], off + i + 1, 0})
			i++
		default:
			out = append(out, Line{Add, b[j], 0, off + j + 1})
			j++
		}
	}
	return out
}

// Counts returns how many lines a diff adds and removes.
func Counts(d []Line) (added, removed int) {
	for _, l := range d {
		switch l.Op {
		case Add:
			added++
		case Del:
			removed++
		}
	}
	return added, removed
}
