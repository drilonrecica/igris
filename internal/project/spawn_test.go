package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpawnGuard(t *testing.T) {
	tests := []struct {
		name    string
		self    string
		inTest  bool
		depth   string
		wantErr string // "" is allowed
	}{
		{"igris from a terminal", "/usr/local/bin/igris", false, "", ""},
		{"depth 0", "/usr/local/bin/igris", false, "0", ""},
		{"in a test", "/usr/local/bin/igris", true, "", "test binary"},
		{"test binary by name", "/tmp/go-build123/b001/igris.test", false, "", "test binary"},
		{"project test binary", "/home/me/project.test", false, "", "test binary"},
		{"go-build dir", filepath.Join(os.TempDir(), "go-build42", "b001", "renamed"), false, "", "test binary"},
		{"spawned already", "/usr/local/bin/igris", false, "1", "does not start another igris serve"},
		{"spawned deep", "/usr/local/bin/igris", false, "7", "does not start another igris serve"},
		{"depth not a number", "/usr/local/bin/igris", false, "x", "not a depth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, err := spawnGuard(tt.self, tt.inTest, tt.depth)
			if tt.wantErr == "" {
				if err != nil || next != 1 {
					t.Fatalf("spawnGuard() = %d, %v; want 1, nil", next, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("spawnGuard() error = %v; want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// The real SpawnServe, called from this test binary, starts nothing: the
// 2026-10-22 process storm was this binary started as `igris serve`
// running its suite, which spawned it again.
func TestSpawnServeRefusesATestBinary(t *testing.T) {
	log, err := os.CreateTemp(t.TempDir(), "serve.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	before := childProcesses(t)
	exited, err := SpawnServe(t.TempDir(), []string{"--", "A"}, nil, log)
	if err == nil || !strings.Contains(err.Error(), "test binary") {
		t.Fatalf("SpawnServe() error = %v; want a refusal naming the test binary", err)
	}
	if exited != nil {
		t.Error("SpawnServe() returned an exited channel: something was started")
	}
	if after := childProcesses(t); len(after) != len(before) {
		t.Errorf("child processes before %v, after %v", before, after)
	}
}

func TestSpawnedButNotIgris(t *testing.T) {
	t.Setenv(SpawnDepthVar, "")
	_ = os.Unsetenv(SpawnDepthVar)
	if err := SpawnedButNotIgris(); err != nil {
		t.Errorf("SpawnedButNotIgris() = %v in a plain test run", err)
	}
	t.Setenv(SpawnDepthVar, "1")
	if err := SpawnedButNotIgris(); err == nil || !strings.Contains(err.Error(), SpawnDepthVar) {
		t.Errorf("SpawnedButNotIgris() = %v with %s set", err, SpawnDepthVar)
	}
}

func TestSpawnRefused(t *testing.T) {
	exited, err := SpawnRefused("no")(t.TempDir(), nil, nil, nil)
	if err == nil || err.Error() != "no" || exited != nil {
		t.Errorf("SpawnRefused() = %v, %v", exited, err)
	}
}

func TestWithoutVars(t *testing.T) {
	got := withoutVars([]string{"A=1", "B=2", "AB=3", "C=4"}, "A", "C")
	if strings.Join(got, " ") != "B=2 AB=3" {
		t.Errorf("withoutVars() = %v", got)
	}
}
