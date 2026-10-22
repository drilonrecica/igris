package draft

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/fsafe"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// The bounds of `draft import` (SPEC §9.1).
const (
	MaxImportSize  = 16 << 20 // the export file
	MaxImportItems = 1000     // items in the export
	MaxBodySize    = 16 << 10 // one item's body, cut here

	maxTitleSize = 512
	maxKeySize   = 128
	maxLabels    = 50
	maxLinks     = 100
)

// ImportFile is the name, under .igris/draft/, of the parsed export the
// session reads instead of the raw one.
const ImportFile = "import.json"

// bodyCutNote ends a body that was cut at MaxBodySize.
const bodyCutNote = "\n[body cut at 16 KiB by igris]"

// Item states: what an export's status words are reduced to.
const (
	StateOpen     = "open"
	StateDone     = "done"
	StateCanceled = "canceled"
)

// Item is one issue of an export, cleaned and bounded.
type Item struct {
	Key    string   `json:"key"`    // "#12" (github), "ENG-12" (linear), "PROJ-12" (jira)
	Title  string   `json:"title"`  //
	Body   string   `json:"body"`   // cut at MaxBodySize
	State  string   `json:"state"`  // open, done or canceled
	Status string   `json:"status"` // the export's own status word
	Labels []string `json:"labels"`
	// Links are the keys of the items in this export that block this one,
	// from the export's blocking links; empty where it has none.
	Links   []string `json:"links"`
	BodyCut bool     `json:"body_cut,omitempty"`
}

// Export is a parsed issue export: what igris writes to .igris/draft/
// import.json.
type Export struct {
	V      int    `json:"v"`
	Source string `json:"source"` // github, linear or jira
	Items  []Item `json:"items"`
}

// JSON returns the export as import.json holds it.
func (e *Export) JSON() ([]byte, error) {
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", ImportFile, err)
	}
	return append(data, '\n'), nil
}

// Done counts the items that are already done.
func (e *Export) Done() int {
	n := 0
	for _, it := range e.Items {
		if it.State == StateDone {
			n++
		}
	}
	return n
}

// LoadImport reads and parses the export file named by the owner: a
// regular file of at most MaxImportSize bytes, anywhere (it is only read,
// never by a shell). A relative file is taken from the working directory.
// format "" detects the format.
func LoadImport(file, format string) (*Export, error) {
	if file == "" {
		return nil, errors.New("no export file; use `igris draft import FILE`")
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, fmt.Errorf("export file %s: %w", file, err)
	}
	data, err := fsafe.ReadFile(abs, MaxImportSize, true)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("export file %s doesn't exist; pass the path of a GitHub JSON, Linear CSV or Jira CSV export", file)
	case errors.Is(err, fsafe.ErrNotRegular):
		return nil, fmt.Errorf("export file %s is not a regular file; pass the export as a plain file", file)
	case errors.Is(err, fsafe.ErrTooLarge):
		return nil, fmt.Errorf("export file %s is over %d MiB; export a smaller set (one milestone, one project)", file, MaxImportSize>>20)
	case err != nil:
		return nil, fmt.Errorf("read export file %s: %w", file, err)
	}
	e, err := ParseImport(data, format)
	if err != nil {
		return nil, fmt.Errorf("export file %s: %w", file, err)
	}
	return e, nil
}

// ParseImport parses an issue export. format is "github", "linear" or
// "jira", or "" to detect it. It never panics on any input and does work
// bounded by len(data).
func ParseImport(data []byte, format string) (*Export, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // a spreadsheet's BOM
	if format == "" {
		f, err := DetectFormat(data)
		if err != nil {
			return nil, err
		}
		format = f
	}
	var items []Item
	var err error
	switch format {
	case "github":
		items, err = parseGitHub(data)
	case "linear", "jira":
		items, err = parseCSV(data, format)
	default:
		return nil, fmt.Errorf("unknown format %q (want github|linear|jira)", format)
	}
	if err != nil {
		// A named format that doesn't parse may be another tool's export:
		// say which, rather than the parser's view of a file it can't read.
		if f, derr := DetectFormat(data); derr == nil && f != format {
			return nil, fmt.Errorf("this looks like a %s export, not %s; pass --format %s, or leave --format out to detect it", f, format, f)
		}
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("the %s export holds no items; export the milestone or project again", format)
	}
	return &Export{V: 1, Source: format, Items: items}, nil
}

// DetectFormat tells the format of an export: a JSON array is github; a
// CSV whose header has "Issue key" and "Summary" is jira, and one with "ID"
// and "Title" is linear.
func DetectFormat(data []byte) (string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	switch trimmed := bytes.TrimLeft(data, " \t\r\n"); {
	case len(trimmed) == 0:
		return "", errors.New("the export is empty")
	case trimmed[0] == '[':
		return "github", nil
	case trimmed[0] == '{':
		return "", errors.New("a JSON object, not the array `gh issue list --json number,title,body,state,labels` prints; pass --format to say what it is")
	}
	r := newCSV(data)
	header, err := firstRecord(r)
	if err != nil {
		return "", fmt.Errorf("can't tell the export's format: %w; pass --format github|linear|jira", err)
	}
	cols := columns(header)
	switch {
	case cols.has("issue key") && cols.has("summary"):
		return "jira", nil
	case cols.has("id") && cols.has("title"):
		return "linear", nil
	}
	return "", errors.New("can't tell the export's format (no Issue key + Summary header for jira, no ID + Title header for linear, no JSON array for github); pass --format github|linear|jira")
}

// ---- github

// ghIssue is one element of what `gh issue list --json …` prints; other
// fields (assignees, milestone, url, …) are ignored.
type ghIssue struct {
	Number int64             `json:"number"`
	Title  string            `json:"title"`
	Body   string            `json:"body"`
	State  string            `json:"state"`
	Labels []json.RawMessage `json:"labels"`
}

func parseGitHub(data []byte) ([]Item, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("not the JSON array `gh issue list --json number,title,body,state,labels` prints: %w", err)
	}
	if len(raw) > MaxImportItems {
		return nil, tooMany(len(raw))
	}
	items := make([]Item, 0, len(raw))
	seen := map[string]bool{}
	for i, r := range raw {
		var is ghIssue
		if err := json.Unmarshal(r, &is); err != nil {
			return nil, fmt.Errorf("item %d: %w", i+1, err)
		}
		if is.Number <= 0 {
			return nil, fmt.Errorf("item %d has no issue number; export with --json number,title,body,state,labels", i+1)
		}
		key := fmt.Sprintf("#%d", is.Number)
		if seen[key] {
			return nil, fmt.Errorf("item %d: %s appears twice", i+1, key)
		}
		seen[key] = true
		items = append(items, newItem(key, is.Title, is.Body, is.State, ghLabels(is.Labels)))
	}
	return items, nil
}

// ghLabels reads gh's labels, objects with a name, or plain strings.
func ghLabels(raw []json.RawMessage) []string {
	var out []string
	for _, r := range raw {
		var obj struct {
			Name string `json:"name"`
		}
		var s string
		switch {
		case json.Unmarshal(r, &obj) == nil && obj.Name != "":
			out = append(out, obj.Name)
		case json.Unmarshal(r, &s) == nil && s != "":
			out = append(out, s)
		}
	}
	return out
}

// ---- linear and jira CSV

// columnIndex maps a lower-cased header name to the positions it has (Jira
// repeats Labels and the link columns).
type columnIndex map[string][]int

func columns(header []string) columnIndex {
	c := columnIndex{}
	for i, h := range header {
		name := strings.ToLower(strings.TrimSpace(textsafe.Line(h)))
		c[name] = append(c[name], i)
	}
	return c
}

func (c columnIndex) has(name string) bool { return len(c[name]) > 0 }

// cells returns the non-empty cells of rec under the named columns.
func (c columnIndex) cells(rec []string, names ...string) []string {
	var out []string
	for _, n := range names {
		for _, i := range c[n] {
			if i < len(rec) && strings.TrimSpace(rec[i]) != "" {
				out = append(out, rec[i])
			}
		}
	}
	return out
}

func (c columnIndex) first(rec []string, name string) string {
	if cells := c.cells(rec, name); len(cells) > 0 {
		return cells[0]
	}
	return ""
}

// csvLayout names a CSV format's columns. blockedBy columns hold the keys
// that block the row's issue; blocks columns the keys it blocks.
type csvLayout struct {
	key, title, body, status string
	blockedBy, blocks        []string
}

var csvLayouts = map[string]csvLayout{
	"linear": {
		key: "id", title: "title", body: "description", status: "status",
		blockedBy: []string{"blocked by"}, blocks: []string{"blocks"},
	},
	"jira": {
		key: "issue key", title: "summary", body: "description", status: "status",
		blockedBy: []string{"inward issue link (blocks)", "blocked by"},
		blocks:    []string{"outward issue link (blocks)", "blocks"},
	},
}

func newCSV(data []byte) *csv.Reader {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1 // rows of any width
	r.LazyQuotes = true
	return r
}

// firstRecord is the first row that holds anything.
func firstRecord(r *csv.Reader) ([]string, error) {
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no header row")
		}
		if err != nil {
			return nil, err
		}
		if !blank(rec) {
			return rec, nil
		}
	}
}

func blank(rec []string) bool {
	for _, c := range rec {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

func parseCSV(data []byte, format string) ([]Item, error) {
	lay := csvLayouts[format]
	r := newCSV(data)
	header, err := firstRecord(r)
	if err != nil {
		return nil, fmt.Errorf("read the %s CSV header: %w", format, err)
	}
	cols := columns(header)
	for _, need := range []string{lay.key, lay.title} {
		if !cols.has(need) {
			return nil, fmt.Errorf("the %s CSV has no %q column (has %s); export again with all columns", format, need, headerNames(header))
		}
	}

	var items []Item
	index := map[string]int{}
	blockedBy := map[string][]string{} // key → keys that block it, from either direction
	for row := 2; ; row++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read the %s CSV: %w", format, err)
		}
		if blank(rec) {
			continue
		}
		if len(items) == MaxImportItems {
			return nil, tooMany(len(items) + 1)
		}
		key := field(cols.first(rec, lay.key), maxKeySize)
		if key == "" {
			return nil, fmt.Errorf("record %d has no %s; export again with all rows complete", row, header[cols[lay.key][0]])
		}
		if _, dup := index[key]; dup {
			return nil, fmt.Errorf("record %d: %s appears twice", row, key)
		}
		var labels []string
		for _, cell := range cols.cells(rec, "labels") {
			labels = append(labels, strings.Split(cell, ",")...)
		}
		it := newItem(key, cols.first(rec, lay.title), cols.first(rec, lay.body), cols.first(rec, lay.status), labels)
		index[it.Key] = len(items)
		items = append(items, it)
		blockedBy[it.Key] = append(blockedBy[it.Key], splitKeys(cols.cells(rec, lay.blockedBy...))...)
		for _, k := range splitKeys(cols.cells(rec, lay.blocks...)) {
			blockedBy[k] = append(blockedBy[k], it.Key)
		}
	}

	// Links name only items of this export, one way: the blockers.
	for k, blockers := range blockedBy {
		i, ok := index[k]
		if !ok {
			continue
		}
		for _, b := range blockers {
			if _, ok := index[b]; ok && b != k {
				items[i].Links = append(items[i].Links, b)
			}
		}
	}
	for i := range items {
		slices.Sort(items[i].Links)
		items[i].Links = slices.Compact(items[i].Links)
		if len(items[i].Links) > maxLinks {
			items[i].Links = items[i].Links[:maxLinks]
		}
	}
	return items, nil
}

// splitKeys splits link cells into issue keys: they come comma, semicolon
// or space separated.
func splitKeys(cells []string) []string {
	var out []string
	for _, c := range cells {
		for _, k := range strings.FieldsFunc(c, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' }) {
			if k = clip(textsafe.Line(k), maxKeySize); k != "" {
				out = append(out, k)
			}
		}
	}
	return out
}

func headerNames(header []string) string {
	names := make([]string, 0, len(header))
	for _, h := range header {
		if h = field(h, 40); h != "" {
			names = append(names, h)
		}
		if len(names) == 12 {
			names = append(names, "…")
			break
		}
	}
	return strings.Join(names, ", ")
}

func tooMany(n int) error {
	return fmt.Errorf("the export holds more than %d items (%d or more); export one milestone or project at a time", MaxImportItems, n)
}

// ---- shared

// newItem builds an item from raw export text: cleaned of escape sequences
// and control characters, bounded, with the status reduced to a state.
func newItem(key, title, body, status string, labels []string) Item {
	it := Item{
		Key:    field(key, maxKeySize),
		Title:  field(title, maxTitleSize),
		Status: field(status, maxKeySize),
		Labels: []string{},
		Links:  []string{},
	}
	if it.Title == "" {
		it.Title = it.Key
	}
	it.State = stateOf(it.Status)
	body = strings.ReplaceAll(strings.ReplaceAll(textsafe.Clean(body), "\r\n", "\n"), "\r", "\n")
	body = strings.TrimSpace(body)
	if len(body) > MaxBodySize {
		it.Body = clip(body, MaxBodySize) + bodyCutNote
		it.BodyCut = true
	} else {
		it.Body = body
	}
	for _, l := range labels {
		if l = field(l, maxKeySize); l != "" && len(it.Labels) < maxLabels {
			it.Labels = append(it.Labels, l)
		}
	}
	return it
}

// stateOf reduces an export's status word to open, done or canceled.
func stateOf(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "closed", "done", "complete", "completed", "resolved", "fixed", "released", "shipped", "merged", "finished":
		return StateDone
	case "canceled", "cancelled", "duplicate", "won't do", "wont do", "won’t do", "rejected", "obsolete", "declined", "abandoned":
		return StateCanceled
	}
	return StateOpen
}

// clip cuts s to at most n bytes at a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// field is one line of export text cleaned of escape sequences and control
// characters, trimmed after cleaning (a value of spaces and control
// characters is empty), cut to at most n bytes.
func field(s string, n int) string {
	return clip(strings.TrimSpace(textsafe.Line(s)), n)
}
