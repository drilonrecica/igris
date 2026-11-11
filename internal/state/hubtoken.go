package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/fsafe"
)

// The hub token (SPEC §14, since 1.11): `igris hub` serves several projects'
// runs, so its owner token can't live in any one project's .igris/. It is
// the one file igris keeps in the home directory (decisions.md V111-P1):
// $XDG_STATE_HOME/igris/hub-token, else ~/.local/state/igris/hub-token. It
// is written as the serve token is (0600, atomically) and read as one
// (bounded, a regular file, never through a symlink).

// HubTokenName is the hub token's file name.
const HubTokenName = "hub-token"

// ErrNoHubToken is wrapped when there is no hub token yet.
var ErrNoHubToken = errors.New("no hub token")

// HubTokenEnv is what the hub token's location reads from the environment;
// tests replace it.
type HubTokenEnv struct {
	StateHome string // $XDG_STATE_HOME
	Home      string // the home directory
}

// SystemHubTokenEnv reads the real environment.
func SystemHubTokenEnv() HubTokenEnv {
	home, _ := os.UserHomeDir()
	return HubTokenEnv{StateHome: os.Getenv("XDG_STATE_HOME"), Home: home}
}

// Path is the hub token's path: under $XDG_STATE_HOME when that is absolute
// (the XDG spec ignores a relative one), else under ~/.local/state.
func (e HubTokenEnv) Path() (string, error) {
	if e.StateHome != "" && filepath.IsAbs(e.StateHome) {
		return filepath.Join(e.StateHome, "igris", HubTokenName), nil
	}
	if e.Home != "" && filepath.IsAbs(e.Home) {
		return filepath.Join(e.Home, ".local", "state", "igris", HubTokenName), nil
	}
	return "", errors.New("no directory for the hub token: set XDG_STATE_HOME or HOME to an absolute path")
}

// ReadHubToken returns the hub token at path. The file is read as a regular
// file of at most 4 KiB, never through a symlink (nor its directory), and
// must hold one token: anything else is an error, so a damaged file
// authenticates nobody. A missing file wraps ErrNoHubToken.
func ReadHubToken(path string) (string, error) {
	if err := realDir(filepath.Dir(path)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s: %w", path, ErrNoHubToken)
		}
		return "", err
	}
	data, err := fsafe.ReadFile(path, serveTokenMax, false)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s: %w", path, ErrNoHubToken)
	}
	if err != nil {
		return "", err
	}
	tok := strings.TrimSuffix(string(data), "\n")
	if !ValidServeToken(tok) {
		return "", fmt.Errorf("%s is damaged; replace it with igris hub token --rotate", path)
	}
	return tok, nil
}

// realDir checks that dir is a directory and not a symlink to one.
func realDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory (a symlink is refused); remove it", dir)
	}
	return nil
}

// hubTokenDir creates the hub token's directory: its parents as needed and
// the igris directory 0700, checked as the run registry's is (this user's,
// not a symlink).
func hubTokenDir(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dir), err)
	}
	return PrivateDir(dir, "hub token directory")
}

// EnsureHubToken returns the hub token, creating it when there is none;
// created reports that it did. As for the serve token, creation never
// replaces a token another igris wrote meanwhile: the one there wins.
func EnsureHubToken(e HubTokenEnv) (tok, path string, created bool, err error) {
	path, err = e.Path()
	if err != nil {
		return "", "", false, err
	}
	tok, err = ReadHubToken(path)
	if !errors.Is(err, ErrNoHubToken) {
		return tok, path, false, err
	}
	if err := hubTokenDir(path); err != nil {
		return "", path, false, err
	}
	tok, err = newServeToken()
	if err != nil {
		return "", path, false, err
	}
	tmp, err := writeTemp(path, []byte(tok+"\n"))
	if err != nil {
		return "", path, false, err
	}
	defer func() { _ = os.Remove(tmp) }()
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			tok, err = ReadHubToken(path)
			return tok, path, false, err
		}
		return "", path, false, fmt.Errorf("write %s: %w", path, err)
	}
	syncDir(filepath.Dir(path))
	return tok, path, true, nil
}

// RotateHubToken replaces the hub token with a new one, which unpairs every
// device paired with the hub, and returns it.
func RotateHubToken(e HubTokenEnv) (tok, path string, err error) {
	path, err = e.Path()
	if err != nil {
		return "", "", err
	}
	if err := hubTokenDir(path); err != nil {
		return "", path, err
	}
	tok, err = newServeToken()
	if err != nil {
		return "", path, err
	}
	if err := writeFileAtomic(path, []byte(tok+"\n")); err != nil {
		return "", path, err
	}
	return tok, path, nil
}
