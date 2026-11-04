package backend

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/textsafe"
)

// The backend names Host compares; the backends' own Name constants
// (tests check they agree).
const (
	hostTmux  = "tmux"
	hostHerdr = "herdr"
)

// Host is where a run's sessions live (SPEC §13 `session_host`, since 1.9,
// decisions.md V141-P1): the run's backend and, for tmux, the server's
// socket path and the session the windows open in. A client's own Host is
// where it runs: its multiplexer and, in tmux, its server's socket ("" for
// Backend outside herdr and tmux; Session unused).
type Host struct {
	Backend string `json:"backend"`
	Socket  string `json:"socket_path,omitempty"`
	Session string `json:"session,omitempty"`
}

// Hoster is a backend that can say where its sessions live. A backend
// without it is located by its name alone.
type Hoster interface {
	Host(ctx context.Context) (Host, error)
}

// Clean is h with its text cleaned of control characters: it comes from
// the socket and is drawn (SPEC §16).
func (h Host) Clean() Host {
	return Host{Backend: textsafe.Line(h.Backend), Socket: textsafe.Line(h.Socket), Session: textsafe.Line(h.Session)}
}

// Reaches reports whether a client at from can bring h's sessions to the
// front itself (SPEC §15.3): the same backend and, for tmux, the same
// server. herdr is reached from any herdr pane, as before 1.9.
func (h Host) Reaches(from Host) bool {
	if h.Backend == "" || h.Backend != from.Backend {
		return false
	}
	if h.Backend == hostTmux {
		return h.Socket != "" && h.Socket == from.Socket
	}
	return true
}

// Where is the card's line for a session at h that the client can't reach
// (SPEC §15.3), and the command that shows it, "" for herdr: `tmux session
// igris-p3` with `tmux attach -t igris-p3` (`tmux -S <socket> attach …`
// when the server is not the default one); for herdr the tab's name, tab.
func (h Host) Where(tab string) (place, command string) {
	switch h.Backend {
	case hostTmux:
		command = "tmux"
		if h.Socket != "" && !defaultSocket(h.Socket) {
			command += " -S " + shellWord(h.Socket)
		}
		command += " attach"
		if h.Session == "" {
			return "tmux server " + h.Socket, command
		}
		return "tmux session " + h.Session, command + " -t " + shellWord(h.Session)
	case hostHerdr:
		return "herdr tab " + tab, ""
	}
	return h.Backend, ""
}

// defaultSocket reports whether socket is a default tmux server's, the one
// a plain `tmux` reaches: tmux-<uid>/default in tmux's directory.
func defaultSocket(socket string) bool {
	return filepath.Base(socket) == "default" && strings.HasPrefix(filepath.Base(filepath.Dir(socket)), "tmux-")
}

// shellWord is s as one shell word: as it is when it is plain, else in
// single quotes.
func shellWord(s string) string {
	plain := func(c rune) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_-./:@%+=,", c)
	}
	if s != "" && strings.IndexFunc(s, func(c rune) bool { return !plain(c) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
