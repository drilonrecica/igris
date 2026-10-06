//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

// killGroupOnCancel starts the command in its own process group and makes
// cancellation kill the whole group, not just the direct child.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
