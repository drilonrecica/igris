package usage

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/fsafe"
)

const uuid = "2b7f3c1e-8d4a-4f6b-9c2e-5a1d0e9f8b7c"

// Counts of the fixtures (V11-P2): the main transcript has one sonnet
// response written as three records, an API error and one opus response;
// the subagent file one haiku response.
var (
	wantMain = Counts{Input: 5, Output: 214, CacheRead: 77233, CacheWrite: 25167, Messages: 2}
	wantSub  = Counts{Input: 10, Output: 300, CacheWrite: 4000, Messages: 1}
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// layout writes the transcript and the subagent file where Claude Code
// keeps them and returns the transcript's path.
func layout(t *testing.T, main, sub []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, uuid+".jsonl")
	if err := os.WriteFile(path, main, 0o600); err != nil {
		t.Fatal(err)
	}
	if sub != nil {
		sd := filepath.Join(dir, uuid, "subagents")
		if err := os.MkdirAll(sd, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sd, "agent-a3872e398082733b4.jsonl"), sub, 0o600); err != nil {
			t.Fatal(err)
		}
		// Not a subagent transcript: ignored.
		if err := os.WriteFile(filepath.Join(sd, "agent-a3872e398082733b4.meta.json"), sub, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func at(s string) time.Time {
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return ts
}

func TestReadFixtures(t *testing.T) {
	path := layout(t, fixture(t, "transcript.jsonl"), fixture(t, "subagent.jsonl"))
	tests := []struct {
		name         string
		from, until  time.Time
		main, sub    Counts
		models, subs []string
	}{
		{"whole attempt", at("2026-10-20T14:00:00Z"), time.Time{}, wantMain, wantSub,
			[]string{"claude-sonnet-5-5", "claude-opus-5-5"}, []string{"claude-haiku-5-5"}},
		{"a continued attempt counts from its start", at("2026-10-20T14:30:00Z"), time.Time{},
			Counts{Input: 3, Output: 4, CacheRead: 50600, CacheWrite: 1200, Messages: 1}, Counts{},
			[]string{"claude-opus-5-5"}, nil},
		{"nothing after the attempt's end", at("2026-10-20T14:00:00Z"), at("2026-10-20T14:30:00Z"),
			Counts{Input: 2, Output: 210, CacheRead: 26633, CacheWrite: 23967, Messages: 1}, wantSub,
			[]string{"claude-sonnet-5-5"}, []string{"claude-haiku-5-5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(path, tt.from)
			var err error
			if tt.until.IsZero() {
				err = r.Read()
			} else {
				err = r.Finish(tt.until)
			}
			if err != nil || !r.Available() {
				t.Fatalf("read: %v, available %v", err, r.Available())
			}
			u := r.Usage()
			if u.Main != tt.main || u.Sub != tt.sub {
				t.Errorf("main %+v sub %+v; want %+v %+v", u.Main, u.Sub, tt.main, tt.sub)
			}
			if !reflect.DeepEqual(u.Models, tt.models) && (len(u.Models) != 0 || len(tt.models) != 0) {
				t.Errorf("models %q, want %q", u.Models, tt.models)
			}
			if !reflect.DeepEqual(u.SubModels, tt.subs) && (len(u.SubModels) != 0 || len(tt.subs) != 0) {
				t.Errorf("sub models %q, want %q", u.SubModels, tt.subs)
			}
		})
	}
}

// Reading in pieces, with a line cut off mid-way, counts what one read
// does, and reading again adds nothing.
func TestReadIncremental(t *testing.T) {
	data := fixture(t, "transcript.jsonl")
	path := layout(t, nil, nil)
	r := NewReader(path, at("2026-10-20T14:00:00Z"))
	for _, cut := range []int{0, 1, len(data) / 3, len(data)/3 + 7, 2 * len(data) / 3, len(data) - 1, len(data)} {
		if err := os.WriteFile(path, data[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		if err := r.Read(); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Read(); err != nil {
		t.Fatal(err)
	}
	if got := r.Usage().Main; got != wantMain {
		t.Errorf("main %+v, want %+v", got, wantMain)
	}
}

// A file shorter than what was read was rewritten: it is read again, and a
// response is still counted once.
func TestReadRewritten(t *testing.T) {
	data := fixture(t, "transcript.jsonl")
	path := layout(t, data, nil)
	r := NewReader(path, time.Time{})
	if err := r.Read(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data[:len(data)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Read(); err != nil {
		t.Fatal(err)
	}
	if got := r.Usage().Main; got != wantMain {
		t.Errorf("main %+v, want %+v", got, wantMain)
	}
}

func line(id, model string, in int64, ts string) string {
	return `{"type":"assistant","isSidechain":false,"timestamp":"` + ts + `","message":{"id":"` + id +
		`","model":"` + model + `","content":[{"type":"text","text":"…"}],"usage":{"input_tokens":` +
		strconv.FormatInt(in, 10) + `,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}` + "\n"
}

// Lines over 1 MiB are skipped unparsed, also when one spans reads; lines
// igris can't use are skipped; nothing of them is kept.
func TestReadSkips(t *testing.T) {
	const ts = "2026-10-20T14:00:00Z"
	big := `{"type":"assistant","message":{"id":"msg_big","model":"claude-opus-5-5","content":"` +
		strings.Repeat("x", maxLine) + `","usage":{"input_tokens":1000}}}` + "\n"
	lines := []string{
		big,
		"not json\n",
		`{"type":"assistant","message":{"id":"msg_neg","model":"claude-opus-5-5","usage":{"input_tokens":-5}}}` + "\n",
		line("msg_ok", "claude-opus-5-5", 7, ts),
		line("msg_badmodel", "claude opus; rm -rf", 1, ts),
		line("msg_ok", "claude-opus-5-5", 7, ts), // a second block of the same response
		`{"type":"assistant","timestamp":"` + ts + `","message":{"model":"claude-opus-5-5","usage":{"input_tokens":9}}}` + "\n",
	}
	data := strings.Join(lines, "")
	path := layout(t, nil, nil)
	r := NewReader(path, time.Time{})
	// The big line arrives in two writes.
	for _, cut := range []int{maxLine / 2, len(data)} {
		if err := os.WriteFile(path, []byte(data[:cut]), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := r.Read(); err != nil {
			t.Fatal(err)
		}
	}
	u := r.Usage()
	want := Counts{Input: 8, Output: 2, Messages: 2}
	if u.Main != want {
		t.Errorf("main %+v, want %+v", u.Main, want)
	}
	if !reflect.DeepEqual(u.Models, []string{"claude-opus-5-5"}) {
		t.Errorf("models %q", u.Models)
	}
}

// A transcript igris can't read is an error, never a hang, and leaves the
// usage unavailable.
func TestReadUnavailable(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, uuid+".jsonl")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewReader(fifo, time.Time{})
	if err := r.Read(); !errors.Is(err, fsafe.ErrNotRegular) || r.Available() {
		t.Errorf("fifo: %v, available %v", err, r.Available())
	}
	r = NewReader(filepath.Join(dir, "missing.jsonl"), time.Time{})
	if err := r.Finish(time.Now()); !errors.Is(err, os.ErrNotExist) || r.Available() {
		t.Errorf("missing: %v, available %v", err, r.Available())
	}
	// A symlink in the transcript's place is not followed.
	target := filepath.Join(dir, "elsewhere.jsonl")
	if err := os.WriteFile(target, fixture(t, "transcript.jsonl"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := NewReader(link, time.Time{}).Read(); !errors.Is(err, fsafe.ErrNotRegular) {
		t.Errorf("symlink: %v", err)
	}
}

func TestValidModel(t *testing.T) {
	for s, want := range map[string]bool{
		"claude-sonnet-5-5": true, "claude-opus-5-5[1m]": true, "claude-3-5-sonnet-20241022": true,
		"": false, "<synthetic>": false, "a b": false, "x\x1b[31m": false, strings.Repeat("a", 81): false,
	} {
		if got := ValidModel(s); got != want {
			t.Errorf("ValidModel(%q) = %v", s, got)
		}
	}
}
