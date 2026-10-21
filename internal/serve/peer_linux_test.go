package serve

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// SO_PEERCRED reports the uid of the process that connected: this one.
func TestPeerUID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	srv, err := l.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	uid, err := peerUID(srv)
	if err != nil || uid != os.Getuid() {
		t.Errorf("peerUID = %d, %v; want %d", uid, err, os.Getuid())
	}
}
