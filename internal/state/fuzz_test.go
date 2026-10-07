package state

import (
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/textsafe"
)

// FuzzParseSignal: a session can write anything into .igris/signals/, so
// arbitrary bytes must give an error or a done/skip for the right task with
// a note that is safe to draw, never a panic.
func FuzzParseSignal(f *testing.F) {
	for _, s := range []string{
		`{"id":"M0-01","action":"done","note":"did it","at":"2026-10-07T10:00:00Z"}`,
		`{"id":"M0-01","action":"skip","note":"not needed"}`,
		`{"id":"M0-01","action":"done","note":"\u001b[31mred\u001b[0m\nline\ttab"}`,
		`{"id":"M0-02","action":"done"}`,
		`{"id":"M0-01","action":"delete"}`,
		`{"id":"M0-01","action":"done","at":"yesterday"}`,
		`[1,2,3]`, `null`, ``, `{"id":1}`,
	} {
		f.Add([]byte(s), "M0-01")
	}
	f.Fuzz(func(t *testing.T, data []byte, id string) {
		s, err := parseSignal(data, id)
		if err != nil {
			if s != nil {
				t.Fatalf("error %v with a signal %+v", err, s)
			}
			return
		}
		if s.ID != id || (s.Action != ActionDone && s.Action != ActionSkip) {
			t.Fatalf("accepted %+v for id %q", s, id)
		}
		if textsafe.HasControl(s.Note) || strings.ContainsAny(s.Note, "\n\t") {
			t.Fatalf("note not cleaned: %q", s.Note)
		}
	})
}
