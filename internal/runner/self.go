package runner

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Self finds the running igris and the one in PATH, which sessions run
// `igris done` with (SPEC §6.2): self is the running binary ("" if
// unknown), inPath the absolute path PATH resolves igris to ("" if none),
// and same reports that both are the same file (os.SameFile), as with a
// package manager's link to the binary.
func Self() (self, inPath string, same bool) {
	self, err := os.Executable()
	if err != nil {
		self = ""
	}
	if p, err := exec.LookPath("igris"); err == nil && filepath.IsAbs(p) {
		inPath = p
	}
	return self, inPath, self != "" && inPath != "" && sameFile(inPath, self)
}

func sameFile(a, b string) bool {
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ia, ib)
}
