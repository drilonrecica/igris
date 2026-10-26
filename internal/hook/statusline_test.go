package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

func openRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := state.Open(root, state.Options{}); err != nil {
		t.Fatal(err)
	}
	return root
}

var statusNow = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }

// The status line prints one line and records the rate limits, only when
// they changed (SPEC §6.3).
func TestStatusLine(t *testing.T) {
	root := openRoot(t)
	payload, err := os.ReadFile("testdata/statusline.json")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := StatusLine(context.Background(), root, "V11-06", bytes.NewReader(payload), &out, statusNow); err != nil {
		t.Fatal(err)
	}
	if want := "V11-06 · claude-sonnet-5-5 · ctx 5% · 5h 4% · 7d 52%\n"; out.String() != want {
		t.Errorf("line = %q, want %q", out.String(), want)
	}
	q, ok, err := state.ReadQuota(root)
	if err != nil || !ok || q.FiveHour.Used != 4 || q.FiveHour.ResetsAt != 1792518000 || q.SevenDay.Used != 52 || !q.At.Equal(statusNow()) {
		t.Fatalf("quota %+v ok=%v err=%v", q, ok, err)
	}
	// Later, unchanged: the record keeps its time.
	later := func() time.Time { return statusNow().Add(time.Hour) }
	if err := StatusLine(context.Background(), root, "V11-06", bytes.NewReader(payload), &out, later); err != nil {
		t.Fatal(err)
	}
	if q, _, _ := state.ReadQuota(root); !q.At.Equal(statusNow()) {
		t.Errorf("rewritten: %v", q.At)
	}
}

// Parts Claude Code didn't send are left out, and nothing is recorded
// without a rate limit (the first render, an API-key login).
func TestStatusLineWithoutQuota(t *testing.T) {
	root := openRoot(t)
	var out bytes.Buffer
	in := `{"model":{"id":"claude-opus-5-5"},"cost":{"total_cost_usd":3}}`
	if err := StatusLine(context.Background(), root, "A-1", strings.NewReader(in), &out, statusNow); err != nil {
		t.Fatal(err)
	}
	if want := "A-1 · claude-opus-5-5\n"; out.String() != want {
		t.Errorf("line = %q, want %q", out.String(), want)
	}
	if _, ok, _ := state.ReadQuota(root); ok {
		t.Error("recorded a quota that was not sent")
	}
}

// What ends up in a terminal is shape-checked, and bad input is an error
// that prints nothing.
func TestStatusLineHostileInput(t *testing.T) {
	root := openRoot(t)
	var out bytes.Buffer
	in := `{"model":{"id":"\u001b]0;pwn\u0007"},"context_window":{"used_percentage":500},"rate_limits":{"five_hour":{"used_percentage":900,"resets_at":1}}}`
	if err := StatusLine(context.Background(), root, "x\x1b[2J", strings.NewReader(in), &out, statusNow); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out.String(), 0x1b) || strings.ContainsRune(out.String(), 7) {
		t.Errorf("escape sequence in %q", out.String())
	}
	if _, ok, _ := state.ReadQuota(root); ok {
		t.Error("recorded a percentage over 100")
	}
	out.Reset()
	for _, bad := range []string{"{", strings.Repeat(" ", maxInput+1)} {
		if err := StatusLine(context.Background(), root, "A-1", strings.NewReader(bad), &out, statusNow); err == nil || out.Len() != 0 {
			t.Errorf("bad input: err %v, printed %q", err, out.String())
		}
	}
}

// The settings file gets the status line only when asked, and its command
// quotes the task ID (SPEC §6.3).
func TestSettingsStatusLine(t *testing.T) {
	var s struct {
		StatusLine *struct{ Type, Command string } `json:"statusLine"`
	}
	data, err := Settings("/opt/my igris/igris", "/p", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &s); err != nil || s.StatusLine != nil {
		t.Errorf("status line without being asked: %+v %v", s.StatusLine, err)
	}
	data, err = Settings("/opt/my igris/igris", "/p", nil, nil, "V11-06")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &s); err != nil || s.StatusLine == nil {
		t.Fatalf("no status line: %s", data)
	}
	if want := "'/opt/my igris/igris' hook --root '/p' statusline 'V11-06'"; s.StatusLine.Type != "command" || s.StatusLine.Command != want {
		t.Errorf("status line = %+v, want command %q", s.StatusLine, want)
	}
	if _, err := Settings("/x", "/p", nil, nil, "a b'; rm"); err == nil {
		t.Error("accepted a task ID that is not one")
	}
}

// Behind a Claude gateway the status line also sends spend_limit, which is
// recorded with the two windows since 1.8 (SPEC §6.3, §13); the printed
// line is unchanged.
func TestStatusLineSpendLimit(t *testing.T) {
	root := openRoot(t)
	in := `{"model":{"id":"claude-opus-5-5"},"rate_limits":{"five_hour":{"used_percentage":4,"resets_at":1792518000},` +
		`"spend_limit":{"used_percentage":103.5,"resets_at":1792652400,"used_usd":41.4,"limit_usd":40,"period":"monthly"}}}`
	var out bytes.Buffer
	if err := StatusLine(context.Background(), root, "V18-03", strings.NewReader(in), &out, statusNow); err != nil {
		t.Fatal(err)
	}
	if want := "V18-03 · claude-opus-5-5 · 5h 4%\n"; out.String() != want {
		t.Errorf("line = %q, want %q", out.String(), want)
	}
	q, ok, err := state.ReadQuota(root)
	if err != nil || !ok || q.SpendLimit == nil || q.SpendLimit.Used != 103.5 || q.SpendLimit.ResetsAt != 1792652400 ||
		q.SpendLimit.UsedUSD == nil || *q.SpendLimit.UsedUSD != 41.4 || *q.SpendLimit.LimitUSD != 40 || q.SpendLimit.Period != "monthly" {
		t.Fatalf("quota %+v ok=%v err=%v", q, ok, err)
	}
}
