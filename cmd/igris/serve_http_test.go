package main

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
)

// freePort returns a loopback address no one listens on (config and
// --http refuse port 0).
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// serve with [serve] http hosts the run on HTTP too: the event stream
// starts with the snapshot, a bearer's stop stops the run, the token is
// made without being printed (SPEC §14).
func TestServeHTTPListener(t *testing.T) {
	addr := freePort(t)
	type result struct {
		snapshot string
		status   int
		err      error
	}
	got := make(chan result, 1)
	code, out, root := noTUIRun{
		cmd: "serve", plan: servePlan, args: []string{"A"}, noSignal: true,
		toml: "[serve]\nhttp = \"" + addr + "\"\n",
		actions: map[string]func(){"A-1 session open": func() {
			var r result
			defer func() { got <- r }()
			wd, _ := os.Getwd()
			tok, err := state.ReadServeToken(wd)
			if r.err = err; err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+serve.PathEvents, nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := http.DefaultClient.Do(req)
			if r.err = err; err != nil {
				return
			}
			br := bufio.NewReader(resp.Body)
			for !strings.HasPrefix(r.snapshot, "data: ") {
				if r.snapshot, r.err = br.ReadString('\n'); r.err != nil {
					return
				}
			}
			_ = resp.Body.Close()
			req, _ = http.NewRequestWithContext(ctx, "POST", "http://"+addr+serve.PathCommand, strings.NewReader(`{"v":1,"type":"stop","id":"web-1"}`))
			req.Header.Set("Authorization", "Bearer "+tok)
			req.Header.Set("Content-Type", "application/json")
			if resp, r.err = http.DefaultClient.Do(req); r.err == nil {
				r.status = resp.StatusCode
				_ = resp.Body.Close()
			}
		}},
	}.run(t)
	r := <-got
	if r.err != nil || r.status != http.StatusOK || !strings.Contains(r.snapshot, `"type":"snapshot"`) {
		t.Fatalf("over HTTP: %+v", r)
	}
	if code != exitOK {
		t.Fatalf("exit %d, want 0 after a stop over HTTP; output:\n%s", code, out)
	}
	for _, w := range []string{"web UI on http://" + addr + "/ for paired devices", "no device is paired yet; run igris serve token", "run stopped: stopped"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	tok, err := state.ReadServeToken(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, tok) {
		t.Error("serve printed the token")
	}
	if fi, err := os.Stat(state.ServeTokenPath(root)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("token file: %v %v", err, fi.Mode())
	}
	noSocket(t, root)
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		_ = c.Close()
		t.Error("the HTTP listener outlived serve")
	}
}

// An address beyond loopback and Tailscale needs --listen-public and TLS;
// refused, serve starts nothing and leaves no socket or lock (SPEC §14).
func TestServeHTTPBindRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		toml string
		want string
	}{
		{"public address", []string{"A", "--http", "0.0.0.0:7777"}, "", "pass --listen-public"},
		{"public from the config", []string{"A"}, "[serve]\nhttp = \"192.168.1.10:7777\"\n", "pass --listen-public"},
		{"public without TLS", []string{"A", "--http", "0.0.0.0:7777", "--listen-public"}, "", "--listen-public needs TLS"},
		{"listen-public alone", []string{"A", "--listen-public"}, "", "--listen-public needs an address"},
		{"a missing certificate", []string{"A", "--http", "127.0.0.1:7777"}, "[serve]\ntls_cert = \"c.pem\"\ntls_key = \"k.pem\"\n", "doesn't load"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, root := noTUIRun{cmd: "serve", plan: servePlan, args: tc.args, toml: tc.toml}.run(t)
			if code != exitFail || !strings.Contains(out, "the HTTP listener can't start") || !strings.Contains(out, tc.want) {
				t.Errorf("exit %d, want 1 saying %q; output:\n%s", code, tc.want, out)
			}
			if strings.Contains(out, "run started") || planStatuses(t, root) != "A-1=ready" {
				t.Errorf("the run started; output:\n%s", out)
			}
			noSocket(t, root)
		})
	}
}

func TestServeFlagUsage(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": servePlan, "igris.toml": ""})
	for _, args := range [][]string{
		{"serve", "token", "--mode", "auto"},
		{"serve", "token", "--http", "127.0.0.1:7777"},
		{"serve", "token", "extra"},
		{"serve", "A", "--rotate"},
		{"serve", "--rotate"},
		{"serve", "A", "--http", "pi.local:7777"},
		{"serve", "A", "--http", "7777"},
		{"serve", "A", "--http", "127.0.0.1:0"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%q: exit %d, want 2; stderr %s", args, code, errb.String())
		}
	}
}

// serve token prints the pairing URL, the same one again, and a new one
// after --rotate (SPEC §14).
func TestServeToken(t *testing.T) {
	root := writeProject(t, map[string]string{"tasks.md": servePlan, "igris.toml": "[serve]\nhttp = \"100.101.102.103:7777\"\n"})
	token := func(args ...string) (string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		if code := run(append([]string{"serve", "token"}, args...), &out, &errb); code != exitOK {
			t.Fatalf("serve token %q: exit %d: %s", args, code, errb.String())
		}
		tok, err := state.ReadServeToken(root)
		if err != nil {
			t.Fatal(err)
		}
		return out.String(), tok
	}
	out, tok := token()
	if !strings.Contains(out, "  http://100.101.102.103:7777/pair?token="+tok+"\n") {
		t.Errorf("output lacks the pairing URL:\n%s", out)
	}
	if !strings.Contains(out, "or scan it:\n\x1b[30;107m") || !strings.Contains(out, "█") {
		t.Errorf("output lacks the QR code, black on white:\n%s", out)
	}
	again, tok2 := token()
	if tok2 != tok || again != out {
		t.Errorf("a second serve token changed the token or the URL:\n%s", again)
	}
	rotated, tok3 := token("--rotate")
	if tok3 == tok || !strings.Contains(rotated, "every paired device must pair again") || !strings.Contains(rotated, "token="+tok3) {
		t.Errorf("--rotate kept the token or didn't say so:\n%s", rotated)
	}
}

// With no address set the URL needs editing, so no QR code of it is drawn.
func TestServeTokenNoAddress(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": servePlan})
	var out, errb bytes.Buffer
	if code := run([]string{"serve", "token"}, &out, &errb); code != exitOK {
		t.Fatalf("serve token: exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "ADDR:PORT") || strings.Contains(out.String(), "scan") {
		t.Errorf("want the placeholder URL and no QR code:\n%s", out.String())
	}
}

func TestPairingBase(t *testing.T) {
	for _, tc := range []struct {
		cfg        string
		base, note string
	}{
		{"", "http://ADDR:PORT", "set [serve] http"},
		{"[serve]\nhttp = \"localhost:7777\"\n", "http://127.0.0.1:7777", ""},
		{"[serve]\nhttp = \"[::1]:7777\"\ntls_cert = \"c\"\ntls_key = \"k\"\n", "https://[::1]:7777", ""},
		{"[serve]\nhttp = \"0.0.0.0:7777\"\n", "http://0.0.0.0:7777", "this machine's own address"},
	} {
		writeProject(t, map[string]string{"tasks.md": servePlan, "igris.toml": tc.cfg})
		proj, err := loadProject("")
		if err != nil {
			t.Fatal(err)
		}
		base, note := pairingBase(proj.Cfg.Serve)
		if base != tc.base || (tc.note == "") != (note == "") || !strings.Contains(note, tc.note) {
			t.Errorf("%q: %q, %q; want %q, %q", tc.cfg, base, note, tc.base, tc.note)
		}
	}
	// Outside a project there is no token to make.
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"serve", "token"}, &out, &errb); code != exitFail {
		t.Errorf("outside a project: exit %d, want 1", code)
	}
}

// addrListener is a listener that says it is on addr.
type addrListener struct {
	net.Listener
	addr net.Addr
}

func (l addrListener) Addr() net.Addr { return l.addr }

// ntfy's answer buttons are made only with [notify.ntfy] actions on, and
// not for a listener on every address, whose URL no phone reaches.
func TestServeButtons(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	srv := serve.New(serve.Options{})
	on := config.Ntfy{Topic: "t", Actions: true}
	tests := []struct {
		name    string
		ntfy    config.Ntfy
		addr    string
		want    bool
		printed string
	}{
		{"on", on, "100.64.0.1:7777", true, "ntfy's answer buttons go to http://100.64.0.1:7777\n"},
		{"off", config.Ntfy{Topic: "t"}, "100.64.0.1:7777", false, ""},
		{"no topic", config.Ntfy{Actions: true}, "100.64.0.1:7777", false, ""},
		{"every address", on, "0.0.0.0:7777", false, "warning: ntfy's answer buttons are left out: the HTTP listener takes every address (0.0.0.0), so no URL in a button would reach it; set [serve] http to one address\n"},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		web := &serve.HTTPListener{Listener: addrListener{ln, net.TCPAddrFromAddrPort(netip.MustParseAddrPort(tt.addr))}}
		if got := serveButtons(srv, web, tt.ntfy, &out); (got != nil) != tt.want {
			t.Errorf("%s: buttons %v, want %v", tt.name, got != nil, tt.want)
		}
		if out.String() != tt.printed {
			t.Errorf("%s: printed %q, want %q", tt.name, out.String(), tt.printed)
		}
	}
}
