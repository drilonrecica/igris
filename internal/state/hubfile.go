package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/drilonrecica/igris/internal/fsafe"
)

// The hub's file (SPEC §13, since 1.11): while `igris hub` runs it writes
// $XDG_RUNTIME_DIR/igris/hub.json, so a serve finds the hub its ntfy Open
// button can go through.

const (
	// HubFileVersion is the "v" of hub.json.
	HubFileVersion = 1
	// hubFileName is the file's name next to runs/.
	hubFileName = "hub.json"
	// maxHubFile bounds hub.json when it is read back.
	maxHubFile = 64 << 10
)

// HubEntry is hub.json: where the hub answers and which process it is.
// Roots names the projects of a hub that was given PATHs; it is absent when
// the hub takes every run.
type HubEntry struct {
	V     int      `json:"v"`
	URL   string   `json:"url"`
	PID   int      `json:"pid"`
	Roots []string `json:"roots,omitempty"`
}

// Has says whether the hub takes the run of the project at root.
func (h HubEntry) Has(root string) bool {
	if len(h.Roots) == 0 {
		return true
	}
	for _, r := range h.Roots {
		if r == root {
			return true
		}
	}
	return false
}

// HubFile returns the path of hub.json (not created).
func (e RegistryEnv) HubFile() (string, error) {
	b, err := e.base()
	if err != nil {
		return "", err
	}
	return filepath.Join(b, hubFileName), nil
}

// RegisterHub writes hub.json atomically, in a 0700 directory checked as the
// run registry's, and returns the function that removes it (a missing file
// is not an error). pid is the hub's own process.
func RegisterHub(e RegistryEnv, entry HubEntry) (remove func() error, err error) {
	path, err := e.HubFile()
	if err != nil {
		return nil, err
	}
	if err := PrivateDir(filepath.Dir(path), "run registry directory"); err != nil {
		return nil, err
	}
	entry.V = HubFileVersion
	if entry.PID <= 0 {
		entry.PID = os.Getpid()
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("encode hub.json: %w", err)
	}
	if err := writeFileAtomic(path, append(data, '\n')); err != nil {
		return nil, err
	}
	return func() error {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}, nil
}

// LiveHub reads hub.json: ok only when it is a regular file of the right
// shape (version 1, an http or https URL with a host, no user info, a
// positive pid) and its process exists. Anything else is no hub, never an
// error: a crashed hub's file is simply ignored.
func LiveHub(e RegistryEnv) (HubEntry, bool) {
	return liveHub(e, processAlive)
}

func liveHub(e RegistryEnv, alive func(int) bool) (HubEntry, bool) {
	path, err := e.HubFile()
	if err != nil {
		return HubEntry{}, false
	}
	if !ownedPrivate(filepath.Dir(path)) {
		return HubEntry{}, false
	}
	data, err := fsafe.ReadFile(path, maxHubFile, false)
	if err != nil || !ownedFile(path) {
		return HubEntry{}, false
	}
	var h HubEntry
	if err := json.Unmarshal(data, &h); err != nil || h.V != HubFileVersion || h.PID <= 0 || !alive(h.PID) {
		return HubEntry{}, false
	}
	u, err := url.Parse(h.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return HubEntry{}, false
	}
	return h, true
}
