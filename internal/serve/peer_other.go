//go:build !linux

package serve

import "net"

// peerUID is not available here: the socket's mode and its directory are
// the guard (SPEC §16).
func peerUID(*net.UnixConn) (int, error) { return 0, errNoPeerCred }
