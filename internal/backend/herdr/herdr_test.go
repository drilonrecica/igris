package herdr

import (
	"context"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/runner"
)

func TestAvailable(t *testing.T) {
	notRunning := runner.Result{Stdout: []byte("status: stopped\nversion: 0.9.1\n")}
	tests := []struct {
		name      string
		workspace string
		setup     func(f *runner.Fake)
		want      string // substring of the error; "" = available
		calls     int
	}{
		{"running", "w2B", func(f *runner.Fake) { f.On(cmd("status", "server"), ok(t, "status_server.txt"), nil) }, "", 1},
		{"outside a herdr pane", "", func(*runner.Fake) {}, "inside a herdr pane", 0},
		{"server unreachable", "w2B", func(f *runner.Fake) {
			f.On(cmd("status", "server"), runner.Result{Stderr: []byte("no socket"), ExitCode: 1}, nil)
		}, "not reachable", 1},
		{"server not running", "w2B", func(f *runner.Fake) { f.On(cmd("status", "server"), notRunning, nil) }, `"stopped"`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &runner.Fake{}
			tt.setup(f)
			err := New(f, tt.workspace).Available(context.Background())
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("Available: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			case err != nil && !strings.Contains(err.Error(), "tmux support is planned"):
				t.Errorf("err = %v, want the v1-requires-herdr message", err)
			}
			if got := len(f.Calls()); got != tt.calls {
				t.Errorf("%d herdr calls, want %d", got, tt.calls)
			}
		})
	}
}

func TestNewFromEnv(t *testing.T) {
	b := NewFromEnv(&runner.Fake{}, func(k string) string {
		if k == WorkspaceEnv {
			return "w9"
		}
		return ""
	})
	if b.workspace != "w9" {
		t.Errorf("workspace = %q, want w9", b.workspace)
	}
}

func TestIntegrationHint(t *testing.T) {
	f := &runner.Fake{}
	f.On(cmd("integration", "status"), ok(t, "integration_status.txt"), nil)
	if h := New(f, "w").IntegrationHint(context.Background()); !strings.Contains(h, "herdr integration install claude") {
		t.Errorf("hint = %q", h)
	}

	f = &runner.Fake{}
	f.On(cmd("integration", "status"), runner.Result{Stdout: []byte("claude: installed (/x)\n")}, nil)
	if h := New(f, "w").IntegrationHint(context.Background()); h != "" {
		t.Errorf("hint with the integration installed = %q, want none", h)
	}

	f = &runner.Fake{}
	f.On(cmd("integration", "status"), runner.Result{Stderr: []byte("x"), ExitCode: 1}, nil)
	if h := New(f, "w").IntegrationHint(context.Background()); h != "" {
		t.Errorf("hint when herdr fails = %q, want none", h)
	}
}
