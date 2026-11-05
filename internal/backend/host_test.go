package backend

import "testing"

// Open session is offered only from the run's backend and, for tmux, its
// server (SPEC §15.3, decisions.md V141-P1).
func TestHostReaches(t *testing.T) {
	run := Host{Backend: "tmux", Socket: "/tmp/tmux-1000/default", Session: "igris-p3"}
	tests := []struct {
		name string
		h    Host
		from Host
		want bool
	}{
		{"the same server, another session", run, Host{Backend: "tmux", Socket: "/tmp/tmux-1000/default"}, true},
		{"another tmux server", run, Host{Backend: "tmux", Socket: "/tmp/tmux-1000/work"}, false},
		{"herdr", run, Host{Backend: "herdr"}, false},
		{"outside any multiplexer", run, Host{}, false},
		{"a tmux run whose server is unknown", Host{Backend: "tmux"}, Host{Backend: "tmux"}, false},
		{"herdr from herdr", Host{Backend: "herdr"}, Host{Backend: "herdr"}, true},
		{"herdr from tmux", Host{Backend: "herdr"}, Host{Backend: "tmux", Socket: "/s"}, false},
		{"no backend", Host{}, Host{}, false},
	}
	for _, tt := range tests {
		if got := tt.h.Reaches(tt.from); got != tt.want {
			t.Errorf("%s: Reaches = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// The card's line says where the session is and how to get there.
func TestHostWhere(t *testing.T) {
	tests := []struct {
		h              Host
		place, command string
	}{
		{Host{Backend: "tmux", Socket: "/tmp/tmux-1000/default", Session: "igris-p3"},
			"tmux session igris-p3", "tmux attach -t igris-p3"},
		{Host{Backend: "tmux", Socket: "/private/tmp/tmux-501/default", Session: "igris-p3"},
			"tmux session igris-p3", "tmux attach -t igris-p3"},
		{Host{Backend: "tmux", Socket: "/tmp/tmux-1000/work", Session: "main"},
			"tmux session main", "tmux -S /tmp/tmux-1000/work attach -t main"},
		{Host{Backend: "tmux", Socket: "/home/o/my socks/s", Session: "my work"},
			"tmux session my work", "tmux -S '/home/o/my socks/s' attach -t 'my work'"},
		{Host{Backend: "tmux", Socket: "/tmp/tmux-1000/default", Session: "it's"},
			"tmux session it's", `tmux attach -t 'it'\''s'`},
		{Host{Backend: "tmux", Socket: "/tmp/tmux-1000/x"},
			"tmux server /tmp/tmux-1000/x", "tmux -S /tmp/tmux-1000/x attach"},
		{Host{Backend: "herdr"}, "herdr tab V19-16 · opus", ""},
	}
	for _, tt := range tests {
		place, command := tt.h.Where("V19-16 · opus")
		if place != tt.place || command != tt.command {
			t.Errorf("%+v: Where = %q, %q; want %q, %q", tt.h, place, command, tt.place, tt.command)
		}
	}
}

// Read inside another tmux server the command is for a shell outside tmux
// (SPEC §15.3, decisions.md V19-G); everywhere else there is no note.
func TestHostWhereFrom(t *testing.T) {
	run := Host{Backend: "tmux", Socket: "/tmp/tmux-1000/gate", Session: "igris-p3"}
	dflt := Host{Backend: "tmux", Socket: "/tmp/tmux-1000/default", Session: "igris-p3"}
	tests := []struct {
		name string
		h    Host
		from Host
		note string
	}{
		{"another tmux server", run, Host{Backend: "tmux", Socket: "/tmp/tmux-1000/work"}, OutsideTmux},
		{"the default server, from another", dflt, Host{Backend: "tmux", Socket: "/tmp/tmux-1000/work"}, OutsideTmux},
		{"outside tmux", run, Host{}, ""},
		{"herdr", run, Host{Backend: "herdr"}, ""},
		{"unknown socket", run, Host{Backend: "tmux"}, ""},
		{"the same server", run, run, ""},
		{"a herdr run", Host{Backend: "herdr"}, Host{Backend: "tmux", Socket: "/s"}, ""},
	}
	for _, tt := range tests {
		place, command, note := tt.h.WhereFrom("T", tt.from)
		wp, wc := tt.h.Where("T")
		if place != wp || command != wc || note != tt.note {
			t.Errorf("%s: WhereFrom = %q, %q, %q; want %q, %q, %q", tt.name, place, command, note, wp, wc, tt.note)
		}
	}
}

// What comes from the socket is cleaned before it is drawn.
func TestHostClean(t *testing.T) {
	h := Host{Backend: "tmux\x1b[31m", Socket: "/s\x07", Session: "a\nb"}.Clean()
	if h.Backend != "tmux" || h.Socket != "/s" || h.Session != "ab" && h.Session != "a b" {
		t.Errorf("Clean = %+v", h)
	}
}
