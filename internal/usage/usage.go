// Package usage counts the tokens a Claude Code session's transcript
// records for one attempt of a task (SPEC §6.9, V11-P2). The transcript is
// untrusted input: it is read through fsafe, streaming, only what is new
// since the last read, and of each line only the record type, the
// timestamp, the message id, the model and the four token counts are
// decoded. Message content is never kept.
package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/fsafe"
)

const (
	// maxLine is the longest line decoded; longer ones (request blobs,
	// attachments) are skipped unparsed.
	maxLine = 1 << 20
	// readBudget bounds what one Read takes in, over all files, so a large
	// transcript never holds up the run loop; the rest waits for the next
	// read.
	readBudget = 32 << 20
	// MaxSubagentFiles bounds the subagent transcripts read.
	MaxSubagentFiles = 256
	// maxModels bounds the distinct model ids kept per list.
	maxModels = 16
	// maxModelLen and maxIDLen bound a model id and a message id.
	maxModelLen = 80
	maxIDLen    = 128
)

// synthetic is the model of the records Claude Code writes for an API
// error, with zero usage.
const synthetic = "<synthetic>"

// Counts are the tokens of a thread: the four counts of the responses'
// usage, and how many responses there were.
type Counts struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Messages   int64 `json:"messages"`
}

// IsZero reports that nothing was counted.
func (c Counts) IsZero() bool { return c == Counts{} }

func (c *Counts) add(u tokens) {
	c.Input = sat(c.Input, u.Input)
	c.Output = sat(c.Output, u.Output)
	c.CacheRead = sat(c.CacheRead, u.CacheRead)
	c.CacheWrite = sat(c.CacheWrite, u.CacheWrite)
	c.Messages = sat(c.Messages, 1)
}

// sat adds two non-negative counts without overflowing: a forged
// transcript can hold any number.
func sat(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// Usage is what an attempt's transcripts record: the main thread's tokens
// and model ids, and the same for its subagents.
type Usage struct {
	Main      Counts
	Sub       Counts
	Models    []string
	SubModels []string
}

// tokens are the counts of message.usage.
type tokens struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

// record holds the only fields of a transcript line igris decodes; the
// content of a message is skipped by the decoder, never kept.
type record struct {
	Type           string    `json:"type"`
	IsSidechain    bool      `json:"isSidechain"`
	Timestamp      time.Time `json:"timestamp"`
	RequestedModel string    `json:"requestedModel"`
	Message        struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage tokens `json:"usage"`
	} `json:"message"`
}

// file is how far a transcript has been read.
type file struct {
	offset   int64
	skipping bool // inside a line over maxLine
}

// Reader counts the usage of one attempt: the records of the transcript at
// path, and of its subagents' files, whose timestamp is at or after from
// (and, on Finish, not after the attempt's end). It is not safe for
// concurrent use.
type Reader struct {
	path  string
	from  time.Time
	until time.Time // zero while the attempt runs
	files map[string]*file
	seen  map[string]bool // message ids counted
	u     Usage
	ok    bool // the transcript was read at least once
}

// NewReader returns a reader of the transcript at path for an attempt that
// started at from. The caller checks path (state.ValidTranscript).
func NewReader(path string, from time.Time) *Reader {
	return &Reader{path: path, from: from, files: map[string]*file{}, seen: map[string]bool{}}
}

// Read takes in what the transcript and its subagents' files gained since
// the last read, up to a budget. An error means the transcript itself
// could not be read; subagent files that can't be read are left out.
func (r *Reader) Read() error { return r.read(readBudget) }

// Finish reads everything left, counting only records up to until, the
// attempt's end.
func (r *Reader) Finish(until time.Time) error {
	r.until = until
	return r.read(math.MaxInt64)
}

// Available reports whether the transcript was read at least once.
func (r *Reader) Available() bool { return r.ok }

// Usage returns what was counted so far. The model lists are copies.
func (r *Reader) Usage() Usage {
	u := r.u
	u.Models = append([]string(nil), u.Models...)
	u.SubModels = append([]string(nil), u.SubModels...)
	return u
}

func (r *Reader) read(budget int64) error {
	used, err := r.readFile(r.path, false, budget)
	if err != nil {
		return err
	}
	r.ok = true
	budget -= used
	for _, p := range r.subagentFiles() {
		if budget <= 0 {
			break
		}
		n, _ := r.readFile(p, true, budget) // one subagent file is not the attempt's usage
		budget -= n
	}
	return nil
}

// subagentFiles lists the subagent transcripts next to the transcript:
// <path without .jsonl>/subagents/agent-*.jsonl, at most MaxSubagentFiles,
// in name order. igris looks nowhere else.
func (r *Reader) subagentFiles() []string {
	dir := filepath.Join(strings.TrimSuffix(r.path, ".jsonl"), "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, en := range entries {
		name := en.Name()
		if !en.Type().IsRegular() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		if paths = append(paths, filepath.Join(dir, name)); len(paths) == MaxSubagentFiles {
			break
		}
	}
	return paths
}

// readFile reads path from where the last read stopped, at most budget
// bytes, and counts each complete line; a line cut off at the end is read
// again next time. It returns the bytes taken in.
func (r *Reader) readFile(path string, sub bool, budget int64) (int64, error) {
	f, err := fsafe.Open(path, false)
	if err != nil {
		return 0, fmt.Errorf("read transcript: %w", err)
	}
	defer func() { _ = f.Close() }()
	st := r.files[path]
	if st == nil {
		st = &file{}
		r.files[path] = st
	}
	info, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("read transcript: %w", err)
	}
	if info.Size() < st.offset {
		// Rewritten: read it again. Message ids already counted stay
		// counted once.
		*st = file{}
	}
	if _, err := f.Seek(st.offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("read transcript: %w", err)
	}
	br := bufio.NewReaderSize(io.LimitReader(f, budget), 64<<10)
	var line []byte
	var used int64
	for {
		chunk, err := br.ReadSlice('\n')
		used += int64(len(chunk))
		switch {
		case st.skipping:
			st.offset += int64(len(chunk))
			st.skipping = err != nil
		case len(line)+len(chunk) > maxLine:
			st.offset += int64(len(line) + len(chunk))
			st.skipping, line = err != nil, line[:0]
		default:
			line = append(line, chunk...)
			if err == nil {
				r.record(line, sub)
				st.offset += int64(len(line))
				line = line[:0]
			}
		}
		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF):
			return used, nil // a line without its newline yet stays for the next read
		default:
			return used, fmt.Errorf("read transcript %s: %w", path, err)
		}
	}
}

var assistantKey = []byte(`"assistant"`)

// record counts one transcript line if it is a response of the attempt not
// counted yet.
func (r *Reader) record(line []byte, sub bool) {
	if !bytes.Contains(line, assistantKey) {
		return // most lines; spares decoding them
	}
	var rec record
	if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" {
		return
	}
	m := rec.Message
	model := m.Model
	if model == "" {
		model = rec.RequestedModel
	}
	u := m.Usage
	switch {
	case m.ID == "", len(m.ID) > maxIDLen, model == synthetic, r.seen[m.ID]:
		return
	case rec.Timestamp.Before(r.from), !r.until.IsZero() && rec.Timestamp.After(r.until):
		return
	case u.Input < 0, u.Output < 0, u.CacheRead < 0, u.CacheWrite < 0:
		return
	}
	r.seen[m.ID] = true
	if sub || rec.IsSidechain {
		r.u.Sub.add(u)
		r.u.SubModels = addModel(r.u.SubModels, model)
		return
	}
	r.u.Main.add(u)
	r.u.Models = addModel(r.u.Models, model)
}

// addModel adds the model id to list once, if it looks like one: it goes
// into the run log and the TUI.
func addModel(list []string, model string) []string {
	if !ValidModel(model) || len(list) >= maxModels {
		return list
	}
	for _, m := range list {
		if m == model {
			return list
		}
	}
	return append(list, model)
}

// ValidModel reports whether s has the shape of a model id as the
// transcript records it (claude-sonnet-5-5, claude-opus-5-5[1m]): letters,
// digits and . _ - : / @ [ ], at most 80 bytes.
func ValidModel(s string) bool {
	if s == "" || len(s) > maxModelLen {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("._-:/@[]", c):
		default:
			return false
		}
	}
	return true
}
