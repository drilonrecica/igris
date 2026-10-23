//go:build linux || darwin

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// termWidth is the number of columns of the terminal f, 0 when it can't
// tell.
func termWidth(f *os.File) int {
	var ws struct{ Row, Col, X, Y uint16 }
	//nolint:gosec // TIOCGWINSZ fills ws; the standard library has no wrapper
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); errno != 0 {
		return 0
	}
	return int(ws.Col)
}
