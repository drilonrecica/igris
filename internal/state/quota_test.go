package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQuotaRoundTrip(t *testing.T) {
	root := t.TempDir()
	d, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = d
	if _, ok, err := ReadQuota(root); ok || err != nil {
		t.Fatalf("no file: ok=%v err=%v", ok, err)
	}
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	q := Quota{At: at, FiveHour: &QuotaWindow{Used: 4, ResetsAt: 1792518000}, SevenDay: &QuotaWindow{Used: 52.5, ResetsAt: 1792652400}}
	if err := WriteQuota(root, q); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadQuota(root)
	if err != nil || !ok || !got.SameWindows(q) || !got.At.Equal(at) {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	// Same windows at another time: the file stays as it was.
	if err := WriteQuota(root, Quota{At: at.Add(time.Hour), FiveHour: q.FiveHour, SevenDay: q.SevenDay}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := ReadQuota(root); !got.At.Equal(at) {
		t.Errorf("rewritten without a change: %v", got.At)
	}
	// A change is written.
	q.FiveHour = &QuotaWindow{Used: 5, ResetsAt: 1792518000}
	q.At = at.Add(time.Minute)
	if err := WriteQuota(root, q); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := ReadQuota(root); got.FiveHour.Used != 5 {
		t.Errorf("change not written: %+v", got)
	}
	if info, err := os.Stat(QuotaPath(root)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v err %v", info.Mode(), err)
	}
}

func TestQuotaNeverCreatesStateAndDropsBadWindows(t *testing.T) {
	root := t.TempDir()
	q := Quota{At: time.Now(), FiveHour: &QuotaWindow{Used: 1, ResetsAt: 5}}
	if err := WriteQuota(root, q); err == nil {
		t.Error("wrote without .igris/")
	}
	if _, err := os.Stat(filepath.Join(root, DirName)); !os.IsNotExist(err) {
		t.Errorf(".igris/ created: %v", err)
	}
	if _, err := Open(root, Options{}); err != nil {
		t.Fatal(err)
	}
	// Windows out of shape are not recorded; with none left nothing is.
	bad := Quota{At: time.Now(), FiveHour: &QuotaWindow{Used: 101, ResetsAt: 5}, SevenDay: &QuotaWindow{Used: -1, ResetsAt: 5}}
	if err := WriteQuota(root, bad); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ReadQuota(root); ok {
		t.Error("recorded a quota without a valid window")
	}
	// A forged file is shape-checked on read.
	forged := `{"at":"2026-10-20T12:00:00Z","five_hour":{"used":400,"resets_at":5},"seven_day":{"used":9,"resets_at":7}}`
	if err := os.WriteFile(QuotaPath(root), []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadQuota(root)
	if err != nil || !ok || got.FiveHour != nil || got.SevenDay == nil {
		t.Errorf("forged: %+v ok=%v err=%v", got, ok, err)
	}
	for _, content := range []string{"{", `{"five_hour":{"used":1,"resets_at":5}}`, strings.Repeat(" ", maxQuotaSize+1)} {
		if err := os.WriteFile(QuotaPath(root), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := ReadQuota(root); ok {
			t.Errorf("accepted %.30q", content)
		}
	}
}

// Since 1.8 quota.json also holds spend_limit (SPEC §13): a window whose
// use may pass 100% once exceeded, with Claude Code's optional amounts.
func TestQuotaSpendLimit(t *testing.T) {
	root := t.TempDir()
	if _, err := Open(root, Options{}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	usd, limit := 41.2, 40.0
	q := Quota{At: at, SpendLimit: &QuotaWindow{Used: 103, ResetsAt: 1792652400, UsedUSD: &usd, LimitUSD: &limit, Period: "monthly"}}
	if err := WriteQuota(root, q); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadQuota(root)
	if err != nil || !ok || got.SpendLimit == nil || got.SpendLimit.Used != 103 || *got.SpendLimit.UsedUSD != 41.2 || got.SpendLimit.Period != "monthly" {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	if !got.SameWindows(q) || got.SameWindows(Quota{SpendLimit: &QuotaWindow{Used: 50, ResetsAt: 1792652400}}) {
		t.Error("SameWindows ignores spend_limit")
	}
	// Windows is every window by name, for the hold (SPEC §6.9).
	ws := got.Windows()
	if len(ws) != 1 || ws[0].Name != "spend_limit" || ws[0].Used != 103 {
		t.Errorf("Windows = %+v", ws)
	}
	// A spend window out of shape is dropped like the others.
	data := `{"at":"2026-10-20T12:00:00Z","five_hour":{"used":4,"resets_at":1792518000},"spend_limit":{"used":-1,"resets_at":1}}`
	if err := os.WriteFile(QuotaPath(root), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := ReadQuota(root); !ok || got.SpendLimit != nil || got.FiveHour == nil {
		t.Errorf("bad spend window kept: %+v", got)
	}
}
