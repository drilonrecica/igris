package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SpawnDepthVar marks a process SpawnServe started: its value is how many
// SpawnServes deep it is (1 for the serve arise or home start). A process
// that finds it set never spawns again, so a serve can't start a serve
// and, should the spawned binary not be igris (a test binary: see
// spawnGuard), the chain ends at once instead of growing. igris serve
// removes it from its environment (cmd/igris), so sessions don't inherit
// it: a nested igris in a session starts its own serve.
const SpawnDepthVar = "IGRIS_SPAWN_DEPTH"

// SpawnedButNotIgris reports, as an error that says what happened, that
// this process was started by SpawnServe but is not igris serve: a test
// binary, which never runs main and would run its whole suite instead,
// each test that starts a serve spawning this binary again (the 2026-10-22
// process storm). A TestMain calls it first and exits on it. SpawnServe
// itself refuses to start a test binary (spawnGuard); this is the second
// line, for a spawner that doesn't.
func SpawnedButNotIgris() error {
	if v := os.Getenv(SpawnDepthVar); v != "" {
		return fmt.Errorf("this test binary was started by igris (%s=%s); it runs no tests", SpawnDepthVar, v)
	}
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		return errors.New("this test binary was started as `igris serve`; it runs no tests")
	}
	return nil
}

// SpawnRefused is a Spawn that starts nothing and fails with why: tests
// (so no test reaches the real SpawnServe) and igris serve (which never
// starts a serve of its own).
func SpawnRefused(why string) Spawn {
	return func(string, []string, []string, *os.File) (<-chan struct{}, error) {
		return nil, errors.New(why)
	}
}

// spawnGuard says whether self, the running executable, may be started as
// `igris serve`: not a test binary (inTest is testing.Testing(); the name
// is a second check, for a test binary built by hand) and not from a
// process SpawnServe started itself (depth is SpawnDepthVar's value).
// next is the depth the child gets. Both refusals say what to do.
func spawnGuard(self string, inTest bool, depth string) (next int, err error) {
	base := filepath.Base(self)
	switch {
	case inTest:
		return 0, fmt.Errorf("igris serve can't be started from a test binary (%s); tests must set Env.Spawn to a fake", base)
	case strings.HasSuffix(base, ".test"), strings.HasPrefix(self, os.TempDir()+string(filepath.Separator)+"go-build"):
		return 0, fmt.Errorf("igris serve can't be started from a test binary (%s); tests must set Env.Spawn to a fake", self)
	}
	if depth != "" {
		n, perr := strconv.Atoi(depth)
		if perr != nil || n < 0 {
			return 0, fmt.Errorf("%s=%q is not a depth; unset it to start igris serve from here", SpawnDepthVar, depth)
		}
		if n >= 1 {
			return 0, fmt.Errorf("this igris was started by igris (%s=%d) and does not start another igris serve; run it from a terminal instead", SpawnDepthVar, n)
		}
	}
	return 1, nil
}
