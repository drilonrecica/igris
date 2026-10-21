package serve

import (
	"fmt"
	"net"
	"syscall"
)

// peerUID returns the uid of the process at the other end of c, from the
// kernel (SO_PEERCRED): a peer can't forge it.
func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("peer credentials: %w", err)
	}
	var cred *syscall.Ucred
	var cerr error
	err = raw.Control(func(fd uintptr) {
		cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED) //nolint:gosec // a file descriptor fits an int
	})
	if err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("peer credentials: %w", err)
	}
	return int(cred.Uid), nil
}
