package state

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/fsafe"
)

// ServeTokenName is the file in .igris/ that holds the owner token of
// serve's HTTP listener (SPEC §13, §14, since 1.6).
const ServeTokenName = "serve-token"

// serveTokenMax bounds what is read of the token file: a session can
// replace it.
const serveTokenMax = 4 << 10

// serveTokenBytes is the token's entropy; its text is the unpadded
// base64url of these bytes.
const serveTokenBytes = 32

// ErrNoServeToken is wrapped when the project has no token yet.
var ErrNoServeToken = errors.New("no serve token")

// ServeTokenPath returns .igris/serve-token.
func ServeTokenPath(root string) string { return filepath.Join(root, DirName, ServeTokenName) }

// ValidServeToken reports whether s has the token's shape: 43 characters
// of unpadded base64url.
func ValidServeToken(s string) bool {
	if len(s) != base64.RawURLEncoding.EncodedLen(serveTokenBytes) {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(b) == serveTokenBytes
}

// ReadServeToken returns the project's owner token. The file is read as
// a regular file of at most 4 KiB, never through a symlink, and must hold
// one token: anything else is an error, so a damaged file authenticates
// nobody. A missing file wraps ErrNoServeToken.
func ReadServeToken(root string) (string, error) {
	path := ServeTokenPath(root)
	data, err := fsafe.ReadFile(path, serveTokenMax, false)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s: %w", path, ErrNoServeToken)
	}
	if err != nil {
		return "", err
	}
	tok := strings.TrimSuffix(string(data), "\n")
	if !ValidServeToken(tok) {
		return "", fmt.Errorf("%s is damaged; replace it with igris serve token --rotate", path)
	}
	return tok, nil
}

// EnsureServeToken returns the project's token, creating it when there is
// none; created reports that it did. Creation never replaces a token
// written meanwhile by another igris: the file is linked into place, and
// the one that is there wins.
func (d *Dir) EnsureServeToken() (tok string, created bool, err error) {
	tok, err = ReadServeToken(d.root)
	if !errors.Is(err, ErrNoServeToken) {
		return tok, false, err
	}
	tok, err = newServeToken()
	if err != nil {
		return "", false, err
	}
	path := ServeTokenPath(d.root)
	tmp, err := writeTemp(path, []byte(tok+"\n"))
	if err != nil {
		return "", false, err
	}
	defer func() { _ = os.Remove(tmp) }()
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			tok, err = ReadServeToken(d.root)
			return tok, false, err
		}
		return "", false, fmt.Errorf("write %s: %w", path, err)
	}
	syncDir(d.path)
	return tok, true, nil
}

// RotateServeToken replaces the project's token with a new one, which
// unpairs every device (SPEC §14), and returns it.
func (d *Dir) RotateServeToken() (string, error) {
	tok, err := newServeToken()
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(ServeTokenPath(d.root), []byte(tok+"\n")); err != nil {
		return "", err
	}
	return tok, nil
}

func newServeToken() (string, error) {
	b := make([]byte, serveTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("make a serve token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
