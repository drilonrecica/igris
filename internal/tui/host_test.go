package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/serve"
)

// hostHarness is a view attached to a run whose session_host is host (nil
// for an older serve), running at place, with M0-02's session in tmux
// pane %3.
func hostHarness(t *testing.T, w, h int, host *backend.Host, place backend.Host) (*harness, *bytes.Buffer) {
	t.Helper()
	hs := newHarness(t, w, h)
	hs.m.loc = time.UTC
	hs.withPlan(cardPlan)
	out := &bytes.Buffer{}
	hs.m.opts.Place, hs.m.opts.Out = place, out
	ref := &backend.SessionRef{Backend: "tmux", TabID: "@3", PaneID: "%3", ClaudeSession: "0b6c3f9e-2d4a-4c1b-9e7f-5a8d1c2b3e4f"}
	hs.m.restore(serve.Snapshot{Run: "r", Phase: "M0", Backend: "tmux", SessionHost: host,
		Active: []serve.Current{{Task: "M0-02", Title: "title of M0-02", Rank: "opus", Model: "opus", Mode: "plan", State: serve.StateWorking,
			Started: t0, Since: t0, Session: ref, ClaudeSession: ref.ClaudeSession}}})
	return hs, out
}

// copied reports whether out holds the clipboard sequence for text.
func copied(out *bytes.Buffer, text string) bool {
	return strings.Contains(out.String(), base64.StdEncoding.EncodeToString([]byte(text)))
}

// From the run's tmux server, in whichever session, o switches the client
// to the session; elsewhere the card says where it is and y copies the
// command (SPEC §15.3, decisions.md V141-P1).
func TestSessionHost(t *testing.T) {
	run := &backend.Host{Backend: "tmux", Socket: "/tmp/tmux-1000/default", Session: "igris-p3"}
	tests := []struct {
		name    string
		host    *backend.Host
		place   backend.Host
		open    bool
		line    string // the card's Session line, "" for none
		copyCmd string // what y copies
	}{
		{"same server", run, backend.Host{Backend: "tmux", Socket: "/tmp/tmux-1000/default"}, true, "", "claude --resume 0b6c3f9e-2d4a-4c1b-9e7f-5a8d1c2b3e4f"},
		{"another server", run, backend.Host{Backend: "tmux", Socket: "/tmp/tmux-1000/work"}, false,
			"Session: tmux session igris-p3 — tmux attach -t igris-p3", "tmux attach -t igris-p3"},
		{"outside tmux", run, backend.Host{}, false, "Session: tmux session igris-p3 — tmux attach -t igris-p3", "tmux attach -t igris-p3"},
		{"herdr", run, backend.Host{Backend: "herdr"}, false, "Session: tmux session igris-p3", "tmux attach -t igris-p3"},
		{"not the default server", &backend.Host{Backend: "tmux", Socket: "/tmp/tmux-1000/gate", Session: "main"}, backend.Host{}, false,
			"Session: tmux session main — tmux -S /tmp/tmux-1000/gate attach -t main", "tmux -S /tmp/tmux-1000/gate attach -t main"},
		{"an older serve", nil, backend.Host{}, true, "", "claude --resume 0b6c3f9e-2d4a-4c1b-9e7f-5a8d1c2b3e4f"},
	}
	for _, tt := range tests {
		for _, size := range append([][2]int{{100, 30}}, cardSizes...) {
			t.Run(fmt.Sprint(tt.name, size), func(t *testing.T) {
				hs, out := hostHarness(t, size[0], size[1], tt.host, tt.place)
				v := hs.m.View()
				checkFits(t, v, size[0], size[1])
				if got := strings.Contains(v, "Open session"); got != tt.open {
					t.Errorf("Open session offered = %v, want %v:\n%s", got, tt.open, v)
				}
				for _, b := range hs.m.buttons() {
					if b.act == actOpen && !tt.open {
						t.Errorf("the bar offers Open session: %v", hs.m.buttons())
					}
				}
				line, _ := hs.m.sessionWhere(hs.m.cur)
				switch {
				case tt.line == "" && line != "":
					t.Errorf("Session line %q, want none", line)
				case tt.line != "" && !strings.HasPrefix(line, tt.line):
					t.Errorf("Session line %q, want %q", line, tt.line)
				}
				if tt.line != "" && size[0] >= 100 {
					place, command, _ := strings.Cut(tt.line, " — ")
					if !strings.Contains(v, tt.line) && (!strings.Contains(v, place+" ") || !strings.Contains(v, command)) {
						t.Errorf("the card lacks %q:\n%s", tt.line, v)
					}
				}
				hs.key("o")
				if want := map[bool]int{true: 1, false: 0}[tt.open]; len(hs.focus) != want {
					t.Errorf("o focused %d sessions, want %d", len(hs.focus), want)
				}
				hs.key("y")
				if !copied(out, tt.copyCmd) {
					t.Errorf("y didn't copy %q", tt.copyCmd)
				}
			})
		}
	}
}

// A herdr run seen from outside herdr names the tab.
func TestSessionHostHerdr(t *testing.T) {
	hs, _ := hostHarness(t, 120, 40, &backend.Host{Backend: "herdr"}, backend.Host{Backend: "tmux", Socket: "/s"})
	if v := hs.m.View(); !strings.Contains(v, "Session: herdr tab M0-02 · opus") || strings.Contains(v, "Open session") {
		t.Errorf("view:\n%s", v)
	}
	hs, _ = hostHarness(t, 120, 40, &backend.Host{Backend: "herdr"}, backend.Host{Backend: "herdr"})
	if v := hs.m.View(); strings.Contains(v, "Session: ") || !strings.Contains(v, "Open session") {
		t.Errorf("from herdr:\n%s", v)
	}
}

// A pane here that igris didn't start for the task is not this run's
// session, which is not the same as a gone one (decisions.md V141-P1).
func TestFocusRefusedForeign(t *testing.T) {
	ref := backend.SessionRef{Backend: "tmux", TabID: "@3", PaneID: "%3"}
	foreign := fmt.Errorf("attach tmux pane %%3 for M0-02: %w: %w; state.json names a pane igris did not start for it", backend.ErrSessionGone, backend.ErrSessionForeign)
	if got := focusErrText(ref, foreign); got != "could not open the session: tmux pane %3 here is not this run's session (the run's sessions are on another server)" {
		t.Errorf("foreign: %q", got)
	}
	gone := fmt.Errorf("focus M0-02: %w", backend.ErrSessionGone)
	if got := focusErrText(ref, gone); got != "could not open the session: focus M0-02: session is gone" {
		t.Errorf("gone: %q", got)
	}
	hs, _ := hostHarness(t, 120, 40, nil, backend.Host{})
	hs.m.Update(focusErrMsg{ref, foreign})
	if l := hs.m.log[len(hs.m.log)-1].text; !strings.Contains(l, "not this run's session") || strings.Contains(l, "session is gone") {
		t.Errorf("log: %q", l)
	}
}
