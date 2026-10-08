package checks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/config"
)

func doctorIn(t *testing.T, dir string) []Result {
	t.Helper()
	return Doctor(context.Background(), DoctorOptions{Dir: dir, Options: Options{Getenv: func(string) string { return "" }}})
}

func byID(rs []Result, id string) []Result { return Pick(rs, id) }

func write(t *testing.T, path, data string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), perm); err != nil {
		t.Fatal(err)
	}
}

func TestAllowsSkip(t *testing.T) {
	for rule, want := range map[string]bool{
		"Bash(igris skip:*)":  true,
		"Bash(igris skip *)":  true,
		"Bash(igris skip)":    true,
		"Bash(igris:*)":       true,
		"Bash(igris *)":       true,
		"Bash(igris*)":        true,
		"Bash(*)":             true,
		"Bash(igris done:*)":  false,
		"Bash(igris done *)":  false,
		"Bash(igris skipper)": false,
		"Read(./x)":           false,
	} {
		if got := allowsSkip(rule); got != want {
			t.Errorf("allowsSkip(%q) = %v, want %v", rule, got, want)
		}
	}
}

func TestDoctorOutsideProject(t *testing.T) {
	rs := doctorIn(t, t.TempDir())
	for _, r := range rs {
		if r.Level == Fail {
			t.Errorf("failure outside a project: %+v", r)
		}
	}
	if p := byID(rs, IDProject); len(p) != 1 || !strings.Contains(p[0].Message, "no igris.toml") {
		t.Errorf("project = %+v", p)
	}
	if p := byID(rs, IDPlanValid); len(p) != 1 || p[0].Level != Warn {
		t.Errorf("plan-valid = %+v", p)
	}
}

func TestDoctorAllowRules(t *testing.T) {
	tests := []struct {
		name, file string
		level      Level
		in         string
	}{
		{"missing file", "", Warn, "not found"},
		{"invalid json", "{", Warn, "not valid JSON"},
		{"no rules", `{}`, Warn, "lacks"},
		{"one rule", `{"permissions":{"allow":["Bash(igris done:*)"]}}`, Warn, "Bash(igris done *)"},
		{"both", `{"permissions":{"allow":["Bash(igris done:*)","Bash(igris done *)"]}}`, OK, "allowed"},
		{"skip allowed", `{"permissions":{"allow":["Bash(igris done:*)","Bash(igris done *)","Bash(igris skip:*)"]}}`, Warn, "igris skip"},
		{"wildcard allows skip", `{"permissions":{"allow":["Bash(igris done:*)","Bash(igris done *)","Bash(igris:*)"]}}`, Warn, "igris skip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.file != "" {
				write(t, filepath.Join(dir, claudeSettingsFile), tt.file, 0o600)
			}
			got := allowRules(dir)
			if got.Level != tt.level || !strings.Contains(got.Message, tt.in) {
				t.Errorf("got %+v, want %s containing %q", got, tt.level, tt.in)
			}
			if got.Level != OK && got.Next == "" {
				t.Errorf("no next command: %+v", got)
			}
		})
	}
}

func TestStatePerms(t *testing.T) {
	dir := t.TempDir()
	if r := statePerms(dir); r.Level != OK {
		t.Errorf("no .igris: %+v", r)
	}
	write(t, filepath.Join(dir, ".igris", "state.json"), "{}", 0o600)
	if err := os.Chmod(filepath.Join(dir, ".igris"), 0o700); err != nil { //nolint:gosec // test: the modes are what is under test
		t.Fatal(err)
	}
	if r := statePerms(dir); r.Level != OK {
		t.Errorf("good modes: %+v", r)
	}
	if err := os.Chmod(filepath.Join(dir, ".igris"), 0o755); err != nil { //nolint:gosec // test: the modes are what is under test
		t.Fatal(err)
	}
	if r := statePerms(dir); r.Level != Warn || r.Next != "chmod 700 .igris" {
		t.Errorf("open dir: %+v", r)
	}
	if err := os.Chmod(filepath.Join(dir, ".igris", "state.json"), 0o644); err != nil { //nolint:gosec // test: the modes are what is under test
		t.Fatal(err)
	}
	if r := statePerms(dir); r.Level != Warn || r.Next != "chmod -R go-rwx .igris" || !strings.Contains(r.Message, "1 more") {
		t.Errorf("open dir and file: %+v", r)
	}
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	if r := lock(dir); r.Level != OK {
		t.Errorf("no lock: %+v", r)
	}
	host, _ := os.Hostname()
	info, _ := json.Marshal(map[string]any{"pid": 2147483000, "host": host, "started_at": "2026-01-02T03:04:05Z"})
	write(t, filepath.Join(dir, ".igris", "igris.lock"), string(info), 0o600)
	if r := lock(dir); r.Level != Warn || r.Next != "igris arise --force-unlock" || !strings.Contains(r.Message, "stale") {
		t.Errorf("stale lock: %+v", r)
	}
	info, _ = json.Marshal(map[string]any{"pid": os.Getpid(), "host": "elsewhere.invalid", "started_at": "2026-01-02T03:04:05Z"})
	write(t, filepath.Join(dir, ".igris", "igris.lock"), string(info), 0o600)
	if r := lock(dir); r.Level != Warn || !strings.Contains(r.Message, "another host") {
		t.Errorf("remote lock: %+v", r)
	}
	info, _ = json.Marshal(map[string]any{"pid": os.Getpid(), "host": host, "started_at": "2026-01-02T03:04:05Z"})
	write(t, filepath.Join(dir, ".igris", "igris.lock"), string(info), 0o600)
	if r := lock(dir); r.Level != OK || !strings.Contains(r.Message, "in progress") {
		t.Errorf("live lock: %+v", r)
	}
	write(t, filepath.Join(dir, ".igris", "igris.lock"), "not json", 0o600)
	if r := lock(dir); r.Level != Warn || r.Next != "igris arise --force-unlock" {
		t.Errorf("unreadable lock: %+v", r)
	}
}

func TestWSL(t *testing.T) {
	for root, want := range map[string]Level{"/mnt/c/Users/me/proj": Warn, "/mnt": Warn, "/home/me/proj": OK, "/mntx/proj": OK} {
		if r := wsl(root); r.Level != want {
			t.Errorf("wsl(%q) = %s, want %s", root, r.Level, want)
		}
	}
	if r := wsl("/mnt/c/x"); !strings.Contains(r.Next, "docs/check-wsl.md") {
		t.Errorf("next = %q", r.Next)
	}
}

func TestNotifyChannels(t *testing.T) {
	env := func(string) string { return "" }
	cfg := config.Default()
	if r := notifyChannels(cfg, env); r.Level != OK || !strings.Contains(r.Message, "backend toast") {
		t.Errorf("default: %+v", r)
	}
	cfg.Notify.Backend.Enabled = false
	if r := notifyChannels(cfg, env); r.Level != Warn {
		t.Errorf("none: %+v", r)
	}
	cfg.Notify.Ntfy = config.Ntfy{Topic: "t", Events: []string{"phase_done"}}
	cfg.Notify.Discord = config.Discord{WebhookURL: "https://discord.invalid/secret-webhook", Events: []string{"phase_done"}}
	r := notifyChannels(cfg, env)
	if r.Level != OK || !strings.Contains(r.Message, "ntfy, Discord") || strings.Contains(r.Message, "secret-webhook") {
		t.Errorf("ntfy+discord: %+v", r)
	}
	cfg.Notify.Webhook = config.Webhook{URL: "https://hooks.invalid/secret-url", Secret: "secret-sig", Events: []string{"needs_input"}}
	r = notifyChannels(cfg, env)
	if r.Level != OK || !strings.Contains(r.Message, "ntfy, Discord, webhook") || strings.Contains(r.Message, "secret") {
		t.Errorf("webhook: %+v", r)
	}
	cfg.Notify.Webhook = config.Webhook{}
	cfg.Notify.Discord.WebhookURL = "env:DISCORD_HOOK"
	if r := notifyChannels(cfg, env); r.Level != Warn || !strings.Contains(r.Message, "DISCORD_HOOK") {
		t.Errorf("unset env ref: %+v", r)
	}
}

func TestDoctorProject(t *testing.T) {
	dir := writePlan(t, driftPlan)
	write(t, filepath.Join(dir, "igris.toml"), "default_mode = \"nope\"\nneeds_input_after = \"0s\"\n", 0o600)
	rs := doctorIn(t, dir)
	cv := byID(rs, IDConfigValid)
	if len(cv) < 2 || cv[0].Level != Fail || cv[0].Next == "" {
		t.Errorf("config problems should each be a fail with a next step: %+v", cv)
	}

	write(t, filepath.Join(dir, "igris.toml"), "", 0o600)
	rs = doctorIn(t, dir)
	if cv := byID(rs, IDConfigValid); len(cv) != 1 || cv[0].Level != OK {
		t.Errorf("valid config: %+v", cv)
	}
	if d := byID(rs, IDDrift); len(d) != 1 || d[0].Level != Warn {
		t.Errorf("drift: %+v", d)
	}
	if h := byID(rs, IDPlanHints); len(h) != 1 || h[0].Level != Warn {
		t.Errorf("hints: %+v", h)
	}

	write(t, filepath.Join(dir, "tasks.md"), "# nothing\n", 0o600)
	rs = doctorIn(t, dir)
	pv := byID(rs, IDPlanValid)
	if len(pv) == 0 || pv[0].Level != Fail || pv[0].Next != "igris adapt" {
		t.Errorf("invalid plan: %+v", pv)
	}
	if len(byID(rs, IDDrift)) != 0 {
		t.Errorf("drift on an invalid plan: %+v", byID(rs, IDDrift))
	}

	if err := os.Remove(filepath.Join(dir, "tasks.md")); err != nil {
		t.Fatal(err)
	}
	if pv := byID(doctorIn(t, dir), IDPlanValid); len(pv) != 1 || pv[0].Level != Fail {
		t.Errorf("missing plan in a project: %+v", pv)
	}
}

func TestDoctorOrder(t *testing.T) {
	dir := writePlan(t, driftPlan)
	write(t, filepath.Join(dir, "igris.toml"), "", 0o600)
	var got []string
	for _, r := range doctorIn(t, dir) {
		if len(got) == 0 || got[len(got)-1] != r.ID {
			got = append(got, r.ID)
		}
	}
	want := []string{IDAPIKey, IDConfigValid, IDConfig, IDPlanValid, IDPlanHints, IDDrift, IDAllowRules, IDStatePerms, IDLock, IDWSL, IDNotify}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}
