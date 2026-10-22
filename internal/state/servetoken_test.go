package state

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestServeToken(t *testing.T) {
	root := t.TempDir()
	d, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadServeToken(root); !errors.Is(err, ErrNoServeToken) {
		t.Fatalf("no token yet: %v, want ErrNoServeToken", err)
	}
	tok, created, err := d.EnsureServeToken()
	if err != nil || !created || !ValidServeToken(tok) {
		t.Fatalf("EnsureServeToken = %q, %v, %v", tok, created, err)
	}
	fi, err := os.Stat(ServeTokenPath(root))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v, mode %v", err, fi.Mode())
	}
	if again, created, err := d.EnsureServeToken(); err != nil || created || again != tok {
		t.Errorf("second EnsureServeToken = %q, %v, %v; want the same token", again, created, err)
	}
	if got, err := ReadServeToken(root); err != nil || got != tok {
		t.Errorf("ReadServeToken = %q, %v", got, err)
	}
	rotated, err := d.RotateServeToken()
	if err != nil || rotated == tok || !ValidServeToken(rotated) {
		t.Fatalf("RotateServeToken = %q, %v", rotated, err)
	}
	if got, _ := ReadServeToken(root); got != rotated {
		t.Errorf("after rotation the file holds %q, want %q", got, rotated)
	}
	entries, _ := os.ReadDir(d.Path())
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// A damaged, oversized, symlinked or FIFO token file authenticates
// nobody, and reading it never blocks (SPEC §13, §16).
func TestServeTokenUntrusted(t *testing.T) {
	good := strings.Repeat("A", 43)
	for name, setup := range map[string]func(path string) error{
		"short":    func(p string) error { return os.WriteFile(p, []byte("abc\n"), 0o600) },
		"padded":   func(p string) error { return os.WriteFile(p, []byte(strings.Repeat("A", 42)+"=\n"), 0o600) },
		"two":      func(p string) error { return os.WriteFile(p, []byte(good+"\n"+good+"\n"), 0o600) },
		"huge":     func(p string) error { return os.WriteFile(p, []byte(strings.Repeat("A", 8<<10)), 0o600) },
		"bad char": func(p string) error { return os.WriteFile(p, []byte(strings.Repeat("A", 42)+"+\n"), 0o600) },
		"symlink": func(p string) error {
			target := p + ".real"
			if err := os.WriteFile(target, []byte(good+"\n"), 0o600); err != nil {
				return err
			}
			return os.Symlink(target, p)
		},
		"fifo": func(p string) error { return syscall.Mkfifo(p, 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := Open(root, Options{}); err != nil {
				t.Fatal(err)
			}
			if err := setup(ServeTokenPath(root)); err != nil {
				t.Fatal(err)
			}
			if tok, err := ReadServeToken(root); err == nil {
				t.Errorf("read %q from a %s token file", tok, name)
			}
		})
	}
	if !ValidServeToken(good) || ValidServeToken(good+"A") || ValidServeToken("") {
		t.Error("ValidServeToken shape check")
	}
}
