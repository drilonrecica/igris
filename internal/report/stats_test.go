package report

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/drilonrecica/igris/internal/state"
)

func readLog(t *testing.T, name string) []state.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // a fixture under testdata/
	if err != nil {
		t.Fatal(err)
	}
	var out []state.Event
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var e state.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func TestStats(t *testing.T) {
	s := NewStats(HistoryInput{Events: readLog(t, "stats-two-runs.jsonl")})
	if s.V != 1 || s.Runs != 2 {
		t.Fatalf("stats = %+v", s)
	}
	if len(s.Ranks) != 2 || s.Ranks[0].Rank != "sonnet" || s.Ranks[1].Rank != "opus" {
		t.Fatalf("ranks = %+v", s.Ranks)
	}
	so := s.Ranks[0]
	// sonnet: A-1 (2 attempts, 1 failed of 2), A-1 again (1, 1 of 2), A-3 (1, 0 of 1)
	if so.Tasks != 3 || so.Attempts != 4.0/3 || so.VerifyRuns != 5 || so.VerifyFailed != 2 {
		t.Errorf("sonnet = %+v", so)
	}
	if so.VerifyFailRate == nil || *so.VerifyFailRate != 0.4 {
		t.Errorf("fail rate = %v", so.VerifyFailRate)
	}
	if so.AgentS == nil || so.NeedsYouS == nil || *so.NeedsYouS != 0 {
		t.Errorf("times = %v %v", so.AgentS, so.NeedsYouS)
	}
	op := s.Ranks[1]
	if op.Tasks != 2 || op.Attempts != 1 || op.VerifyFailed != 0 || op.VerifyFailRate == nil || *op.VerifyFailRate != 0 {
		t.Errorf("opus = %+v", op)
	}
	if len(s.Rewrite) != 1 {
		t.Fatalf("rewrite = %+v", s.Rewrite)
	}
	if c := s.Rewrite[0]; c.Task != "A-1" || c.Phase != "P1" || c.Rank != "sonnet" || c.Runs != 2 || c.Attempts != 3 || c.VerifyFailed != 2 {
		t.Errorf("candidate = %+v", c)
	}
}

func TestStatsLastRunOnly(t *testing.T) {
	s := NewStats(HistoryInput{Events: readLog(t, "stats-two-runs.jsonl"), N: 1})
	if s.Runs != 1 || len(s.Rewrite) != 1 || s.Rewrite[0].Runs != 1 || s.Rewrite[0].Attempts != 1 {
		t.Errorf("stats = %+v", s)
	}
}

func TestStatsEmpty(t *testing.T) {
	s := NewStats(HistoryInput{})
	if s.Runs != 0 || s.Ranks == nil || s.Rewrite == nil {
		t.Errorf("stats = %+v", s)
	}
}

// A retry is one more attempt in history too, as in report.
func TestHistoryCountsRetries(t *testing.T) {
	h := NewHistory(HistoryInput{Events: readLog(t, "stats-two-runs.jsonl")})
	for _, r := range h.Runs {
		if r.Run == "20261007-090000-aaaa" && r.Tasks[0].Attempts != 2 {
			t.Errorf("A-1 attempts = %d", r.Tasks[0].Attempts)
		}
	}
}
