//go:build unix

package project

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// SpawnServe is the real Spawn: the running igris binary, `serve` and
// args as argv (never a shell), in a session of its own (setsid), so a
// closed terminal or a dropped ssh connection doesn't reach it; stdin is
// /dev/null. It is not waited for beyond reaping it: serve outlives the
// igris that started it. It refuses from a test binary and from a process
// it started itself (spawnGuard, SpawnDepthVar): a spawned test binary
// would run its suite, which spawns, without end.
func SpawnServe(root string, args, env []string, log *os.File) (<-chan struct{}, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find the running igris: %w", err)
	}
	depth, err := spawnGuard(self, testing.Testing(), os.Getenv(SpawnDepthVar))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(self, append([]string{"serve"}, args...)...) //nolint:gosec // igris itself; argv, no shell
	cmd.Dir = root
	// serve gets the starter's answers and its depth fresh, never
	// inherited: a stale answer would answer a question never asked.
	cmd.Env = append(withoutVars(os.Environ(), ServeAnswersVar, SpawnDepthVar), env...)
	cmd.Env = append(cmd.Env, SpawnDepthVar+"="+strconv.Itoa(depth))
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	return exited, nil
}

// withoutVars is environ without the named variables' entries.
func withoutVars(environ []string, names ...string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		keep := true
		for _, name := range names {
			if strings.HasPrefix(kv, name+"=") {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}
