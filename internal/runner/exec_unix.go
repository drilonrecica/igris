//go:build unix

package runner

import (
	"errors"
	"fmt"
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

// KilledBy reports the signal that killed the process of a failed Run, as
// "signal 9 (killed)"; ok is false when no signal did.
func KilledBy(err error) (sig string, ok bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return "", false
	}
	ws, isWait := exitErr.Sys().(syscall.WaitStatus)
	if !isWait || !ws.Signaled() {
		return "", false
	}
	return fmt.Sprintf("signal %d (%s)", int(ws.Signal()), ws.Signal()), true
}
