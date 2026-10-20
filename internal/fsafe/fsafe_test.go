package fsafe

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	small := write("small", []byte("hello"))
	big := write("big", make([]byte, 11))
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(small, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		path   string
		follow bool
		want   string
		err    error
	}{
		{"regular", small, false, "hello", nil},
		{"over the limit", big, false, "", ErrTooLarge},
		{"fifo never blocks", filepath.Join(dir, "fifo"), false, "", ErrNotRegular},
		{"directory", dir, false, "", ErrNotRegular},
		{"symlink followed", filepath.Join(dir, "link"), true, "hello", nil},
		{"symlink refused", filepath.Join(dir, "link"), false, "", ErrNotRegular},
		{"missing", filepath.Join(dir, "none"), true, "", os.ErrNotExist},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadFile(tt.path, 10, tt.follow)
			if !errors.Is(err, tt.err) || string(got) != tt.want {
				t.Errorf("ReadFile = %q, %v; want %q, %v", got, err, tt.want, tt.err)
			}
		})
	}
}
