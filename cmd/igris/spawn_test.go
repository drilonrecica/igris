package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/project"
)

// inProcessServe is the tests' project.Spawn: `igris serve` runs in this
// process (run, on a goroutine) the way the real SpawnServe runs it in a
// child, with the starter's answers from env and its output appended to
// log's file. A serve still running when the test ends is stopped over
// its socket and waited for, so it can't run on into the next test. The
// real SpawnServe would start this test binary, which runs the suite,
// which starts it again (TestMain).
func inProcessServe(t *testing.T) project.Spawn {
	t.Helper()
	return func(_ string, args, env []string, log *os.File) (<-chan struct{}, error) {
		var answers project.ServeAnswers
		started := false
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, project.ServeAnswersVar+"="); ok {
				answers, started = project.ParseServeAnswers(v), true
			}
		}
		// Serve closes log once serve started; serve has its own.
		out, err := os.OpenFile(log.Name(), os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
		savedStarter := serveStarter
		serveStarter = func() (project.ServeAnswers, bool) { return answers, started }
		exited := make(chan struct{})
		go func() {
			defer close(exited)
			defer func() { _ = out.Close() }()
			run(append([]string{"serve"}, args...), out, out)
		}()
		t.Cleanup(func() {
			select {
			case <-exited:
			default:
				var stopOut, stopErr bytes.Buffer
				run([]string{"stop"}, &stopOut, &stopErr)
				select {
				case <-exited:
				case <-time.After(15 * time.Second):
					t.Errorf("the in-process igris serve did not end; stop said: %s%s", stopOut.String(), stopErr.String())
				}
			}
			serveStarter = savedStarter
		})
		return exited, nil
	}
}

// The real project.SpawnServe refuses from this test binary; what the
// arise tests would otherwise reach.
func TestRealSpawnRefusesATestBinary(t *testing.T) {
	log, err := os.CreateTemp(t.TempDir(), "serve.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	exited, err := project.SpawnServe(t.TempDir(), []string{"--", "A"}, nil, log)
	if err == nil || !strings.Contains(err.Error(), "test binary") || exited != nil {
		t.Fatalf("SpawnServe() = %v, %v; want a refusal naming the test binary", exited, err)
	}
}
