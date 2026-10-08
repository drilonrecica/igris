package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/config"
)

// notifyProject makes a project whose config sends to the given servers, and
// a fake backend in place of herdr.
func notifyProject(t *testing.T, toml string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "igris.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	old := ariseBackend
	t.Cleanup(func() { ariseBackend = old })
	ariseBackend = func(*config.Config) (backend.Backend, error) { return fake.New(), nil }
}

func TestNotifyTest(t *testing.T) {
	var mu sync.Mutex
	var titles, bodies []string
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		titles, bodies = append(titles, r.Header.Get("Priority")), append(bodies, string(b))
		mu.Unlock()
	}))
	defer ntfy.Close()
	notifyProject(t, "[notify.backend]\nenabled = false\n[notify.ntfy]\ntopic = \"igris\"\nserver = \""+ntfy.URL+"\"\ntoken = \"env:NTFY_TOKEN\"\n")
	oldEnv := ariseGetenv
	t.Cleanup(func() { ariseGetenv = oldEnv })
	ariseGetenv = func(k string) string {
		if k == "NTFY_TOKEN" {
			return "tk_secret"
		}
		return ""
	}

	var out, errb bytes.Buffer
	if code := run([]string{"notify", "test"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	for _, want := range []string{"needs_input", "session_lost", "task_overdue", "verify_failed_limit", "phase_done", "phase_stuck", "run_error"} {
		if !strings.Contains(got, want+strings.Repeat(" ", 21-len(want))+"ntfy") {
			t.Errorf("no ntfy line for %s in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "task_done") {
		t.Errorf("task_done is not a default event:\n%s", got)
	}
	if strings.Contains(got, "tk_secret") {
		t.Errorf("output leaks the token:\n%s", got)
	}
	if len(titles) != 7 {
		t.Errorf("ntfy got %d messages, want 7", len(titles))
	}
	// Each sample names its event and says what a real one would.
	for i, want := range []string{
		"phase TEST · TEST-1 Notification check: test of needs_input: needs you",
		"phase TEST · TEST-1 Notification check: test of session_lost: session lost",
		"phase TEST · TEST-1 Notification check: test of task_overdue: needs you (running longer than its Timeout 45m)",
		"phase TEST · TEST-1 Notification check: test of verify_failed_limit: verification keeps failing; needs you",
		"phase TEST: test of phase_done: complete",
		"phase TEST: test of phase_stuck: stuck: 2 unfinished task(s), none can start",
		"phase TEST: test of run_error: the run stopped with an error",
	} {
		if i >= len(bodies) || bodies[i] != want {
			t.Errorf("ntfy body %d = %q, want %q", i, bodies[i:min(i+1, len(bodies))], want)
		}
	}
}

func TestNotifyTestReportsFailuresAndMissingChannels(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	notifyProject(t, "[notify.backend]\nenabled = false\n[notify.ntfy]\ntopic = \"igris\"\nserver = \""+bad.URL+"\"\n")
	var out, errb bytes.Buffer
	if code := run([]string{"notify", "test", "--event", "needs_input"}, &out, &errb); code != exitFail {
		t.Errorf("exit %d, want %d for a failing channel", code, exitFail)
	}
	if !strings.Contains(out.String(), "FAILED") || !strings.Contains(out.String(), "500") || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("output = %q, want one FAILED line with the status", out.String())
	}

	notifyProject(t, "[notify.backend]\nenabled = false\n")
	out.Reset()
	if code := run([]string{"notify", "test"}, &out, &errb); code != exitFail || !strings.Contains(out.String(), "no channel is set up") {
		t.Errorf("exit %d, output %q, want a hint about setting a channel up", code, out.String())
	}

	if code := run([]string{"notify"}, &out, &errb); code != exitUsage {
		t.Errorf("`igris notify` exit %d, want usage error", code)
	}
	if code := run([]string{"notify", "test", "--event", "bogus"}, &out, &errb); code != exitFail {
		t.Errorf("unknown event exit %d", code)
	}
}

// --event for an event no channel sends says so, instead of claiming no
// channel is set up; the hint names the backend toast, not herdr's.
func TestNotifyTestEventNoChannelSends(t *testing.T) {
	notifyProject(t, "[notify.backend]\nenabled = false\n[notify.ntfy]\ntopic = \"igris\"\nserver = \"https://ntfy.invalid\"\n")
	var out, errb bytes.Buffer
	if code := run([]string{"notify", "test", "--event", "task_done"}, &out, &errb); code != exitFail {
		t.Errorf("exit %d, want %d", code, exitFail)
	}
	if want := "no channel sends task_done; add it to a channel's events in igris.toml\n"; out.String() != want {
		t.Errorf("output %q, want %q", out.String(), want)
	}
	notifyProject(t, "[notify.backend]\nenabled = false\n")
	out.Reset()
	run([]string{"notify", "test", "--event", "task_done"}, &out, &errb)
	if !strings.Contains(out.String(), "no channel is set up") || !strings.Contains(out.String(), "enable the backend toast") || strings.Contains(out.String(), "herdr toast") {
		t.Errorf("output %q", out.String())
	}
}
