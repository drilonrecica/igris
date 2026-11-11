package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHubTokenPath(t *testing.T) {
	for _, tc := range []struct {
		env  HubTokenEnv
		want string
	}{
		{HubTokenEnv{StateHome: "/s", Home: "/h"}, "/s/igris/hub-token"},
		{HubTokenEnv{StateHome: "rel", Home: "/h"}, "/h/.local/state/igris/hub-token"},
		{HubTokenEnv{Home: "/h"}, "/h/.local/state/igris/hub-token"},
	} {
		if got, err := tc.env.Path(); err != nil || got != tc.want {
			t.Errorf("%+v: Path = %q, %v; want %q", tc.env, got, err, tc.want)
		}
	}
	if _, err := (HubTokenEnv{Home: "rel"}).Path(); err == nil {
		t.Error("no absolute directory: want an error")
	}
}

func TestHubToken(t *testing.T) {
	home := t.TempDir()
	env := HubTokenEnv{Home: home}
	path := filepath.Join(home, ".local", "state", "igris", HubTokenName)
	if _, err := ReadHubToken(path); !errors.Is(err, ErrNoHubToken) {
		t.Fatalf("no token yet: %v, want ErrNoHubToken", err)
	}
	tok, got, created, err := EnsureHubToken(env)
	if err != nil || !created || !ValidServeToken(tok) || got != path {
		t.Fatalf("EnsureHubToken = %q, %q, %v, %v", tok, got, created, err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v, mode %v", err, fi.Mode())
	}
	if di, err := os.Stat(filepath.Dir(path)); err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("token directory: %v, mode %v", err, di.Mode())
	}
	if again, _, created, err := EnsureHubToken(env); err != nil || created || again != tok {
		t.Errorf("second EnsureHubToken = %q, %v, %v; want the same token", again, created, err)
	}
	rotated, _, err := RotateHubToken(env)
	if err != nil || rotated == tok || !ValidServeToken(rotated) {
		t.Fatalf("RotateHubToken = %q, %v", rotated, err)
	}
	if got, err := ReadHubToken(path); err != nil || got != rotated {
		t.Errorf("after rotation ReadHubToken = %q, %v; want %q", got, err, rotated)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// A damaged, symlinked or oversized hub token, or one in a symlinked
// directory, authenticates nobody.
func TestHubTokenUntrusted(t *testing.T) {
	good := strings.Repeat("A", 43)
	for name, setup := range map[string]func(dir string) error{
		"damaged": func(d string) error { return os.WriteFile(filepath.Join(d, HubTokenName), []byte("abc\n"), 0o600) },
		"huge": func(d string) error {
			return os.WriteFile(filepath.Join(d, HubTokenName), []byte(strings.Repeat("A", 8<<10)), 0o600)
		},
		"symlink": func(d string) error {
			real := filepath.Join(d, "real")
			if err := os.WriteFile(real, []byte(good+"\n"), 0o600); err != nil {
				return err
			}
			return os.Symlink(real, filepath.Join(d, HubTokenName))
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := setup(dir); err != nil {
				t.Fatal(err)
			}
			if tok, err := ReadHubToken(filepath.Join(dir, HubTokenName)); err == nil {
				t.Errorf("ReadHubToken = %q, want an error", tok)
			}
		})
	}
	t.Run("symlinked directory", func(t *testing.T) {
		base := t.TempDir()
		real := filepath.Join(base, "real")
		if err := os.Mkdir(real, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(real, HubTokenName), []byte(good+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(base, "state"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(base, "state", "igris")); err != nil {
			t.Fatal(err)
		}
		env := HubTokenEnv{StateHome: filepath.Join(base, "state")}
		path, _ := env.Path()
		if tok, err := ReadHubToken(path); err == nil {
			t.Errorf("ReadHubToken through a symlinked directory = %q, want an error", tok)
		}
		if _, _, _, err := EnsureHubToken(env); err == nil {
			t.Error("EnsureHubToken through a symlinked directory: want an error")
		}
	})
}
