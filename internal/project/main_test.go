package project

import (
	"fmt"
	"os"
	"testing"
)

// TestMain refuses to run the suite when this binary was started as `igris
// serve` (SpawnedButNotIgris): a test that reached the real SpawnServe
// would start this binary, which would run the suite, which would start
// it again. After the suite it fails on a child process left running.
func TestMain(m *testing.M) {
	if err := SpawnedButNotIgris(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	code := m.Run()
	if left := leftoverChildren(); len(left) > 0 {
		fmt.Fprintf(os.Stderr, "igris processes left running by the tests: %v\n", left)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
