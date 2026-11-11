package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/hub"
	"github.com/drilonrecica/igris/internal/state"
)

// useHubSeams points the hub at a temporary token directory and live runs.
func useHubSeams(t *testing.T, live []state.RunEntry) string {
	t.Helper()
	dir := t.TempDir()
	savedEnv, savedLive := hubTokenEnv, hubLive
	t.Cleanup(func() { hubTokenEnv, hubLive = savedEnv, savedLive })
	hubTokenEnv = func() state.HubTokenEnv { return state.HubTokenEnv{StateHome: dir} }
	hubLive = func(context.Context) []state.RunEntry { return live }
	return filepath.Join(dir, "igris", state.HubTokenName)
}

func TestHubUsage(t *testing.T) {
	useHubSeams(t, nil)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"hub"}, "the hub needs --http ADDR:PORT"},
		{[]string{"hub", "--http", "nope"}, "invalid --http"},
		{[]string{"hub", "--all", "--http", "127.0.0.1:7777", "x"}, "--all takes every live run"},
		{[]string{"hub", "--http", "127.0.0.1:7777", "--tls-cert", "c.pem"}, "--tls-cert and --tls-key go together"},
		{[]string{"hub", "--rotate", "--http", "127.0.0.1:7777"}, "--rotate is for `igris hub token`"},
		{[]string{"hub", "token", "--all"}, "hub token takes only --rotate, not --all"},
		{[]string{"hub", "token", "x"}, "unexpected argument"},
		// Off a terminal there is no checklist.
		{[]string{"hub", "--http", "127.0.0.1:7777"}, "the checklist needs a terminal; pass --all"},
	} {
		var out, errb bytes.Buffer
		if code := run(tc.args, &out, &errb); code != exitUsage || !strings.Contains(errb.String(), tc.want) {
			t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.args, code, errb.String(), tc.want)
		}
	}
}

func TestHubToken(t *testing.T) {
	path := useHubSeams(t, nil)
	var out, errb bytes.Buffer
	if code := run([]string{"hub", "token"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	tok, err := state.ReadHubToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "  http://ADDR:PORT/pair?token="+tok+"\n") || !strings.Contains(out.String(), "next: igris hub --http ADDR:PORT") {
		t.Errorf("output:\n%s", out.String())
	}
	out.Reset()
	if code := run([]string{"hub", "token", "--rotate"}, &out, &errb); code != exitOK {
		t.Fatalf("rotate: exit %d: %s", code, errb.String())
	}
	rotated, _ := state.ReadHubToken(path)
	if rotated == tok || !strings.HasPrefix(out.String(), "the token was replaced") || !strings.Contains(out.String(), rotated) {
		t.Errorf("rotate output:\n%s", out.String())
	}
}

// A pinned project must have a live run; the hub says which hasn't and how
// to start one, and doesn't start.
func TestHubPinnedWithoutRun(t *testing.T) {
	live := t.TempDir()
	idle := t.TempDir()
	for _, d := range []string{live, idle} {
		if err := os.Mkdir(filepath.Join(d, state.DirName), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	useHubSeams(t, []state.RunEntry{{V: 1, Root: live, Project: filepath.Base(live), PID: 1, Started: time.Now(), Socket: "/nowhere"}})
	var out, errb bytes.Buffer
	sub := filepath.Join(idle, state.DirName)
	code := run([]string{"hub", "--http", "127.0.0.1:7777", live, sub}, &out, &errb)
	want := "igris hub: no run is live in " + idle + "; start one there with `igris arise` (or `igris serve`)\n"
	if code != exitFail || errb.String() != want {
		t.Errorf("exit %d, stderr %q; want 1, %q", code, errb.String(), want)
	}
}

func TestParseChoice(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		err  bool
	}{
		{"", "[]", false},
		{"0", "[]", false},
		{"all", "[]", false},
		{"2", "[1]", false},
		{"3, 1 3", "[2 0]", false},
		{"1 0", "[]", false},
		{"4", "", true},
		{"x", "", true},
	} {
		got, err := parseChoice(tc.in, 3)
		if (err != nil) != tc.err {
			t.Errorf("%q: error %v", tc.in, err)
			continue
		}
		if !tc.err && fmt.Sprint(got) != tc.want {
			t.Errorf("%q: %v, want %s", tc.in, got, tc.want)
		}
	}
}

// The checklist shows the runs with All checked; Enter keeps All, numbers
// choose, a wrong answer asks again, and an ended stdin chooses nothing.
func TestChooseRuns(t *testing.T) {
	live := []state.RunEntry{
		{Root: "/p/sinjal", Project: "sinjal", Started: time.Date(2026, 10, 23, 9, 41, 0, 0, time.UTC)},
		{Root: "/p/billing", Project: "billing", Started: time.Date(2026, 10, 23, 10, 2, 0, 0, time.UTC)},
	}
	var out bytes.Buffer
	all, roots, ok := chooseRuns(strings.NewReader("\n"), &out, live)
	if !ok || !all || roots != nil {
		t.Errorf("Enter: %v %v %v", all, roots, ok)
	}
	for _, want := range []string{"2 runs live", "  [x] 0  All (and new runs)\n", "  [ ] 1  sinjal  /p/sinjal · since ", "  [ ] 2  billing  /p/billing"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("checklist lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	all, roots, ok = chooseRuns(strings.NewReader("7\n2\n"), &out, live)
	if !ok || all || len(roots) != 1 || roots[0] != "/p/billing" || !strings.Contains(out.String(), `"7" is not on the list`) {
		t.Errorf("7 then 2: %v %v %v\n%s", all, roots, ok, out.String())
	}
	if _, _, ok := chooseRuns(strings.NewReader(""), io.Discard, live); ok {
		t.Error("an ended stdin chose something")
	}
}

// lockedBuf is stdout written by the hub's goroutines while the test reads
// it.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// The hub listens, prints its pairing URL with the QR code, pairs a browser
// with the hub token and the cookie igris_hub, and ends on its context
// (the signal) saying the runs go on.
func TestRunHub(t *testing.T) {
	path := useHubSeams(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, errb := &lockedBuf{}, &lockedBuf{}
	done := make(chan int)
	go func() { done <- runHub(ctx, hubConfig{all: true, addr: "127.0.0.1:0"}, out, errb) }()
	re := regexp.MustCompile(`  (http://127\.0\.0\.1:\d+)/pair\?token=(\S+)\n`)
	deadline := time.Now().Add(10 * time.Second)
	var m []string
	for m == nil {
		if time.Now().After(deadline) {
			t.Fatalf("no pairing URL:\n%s\n%s", out.String(), errb.String())
		}
		if strings.Contains(out.String(), "\x1b[0m\n") { // the QR code's last line
			m = re.FindStringSubmatch(out.String())
		}
		if m == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	base, tok := m[1], m[2]
	if stored, err := state.ReadHubToken(path); err != nil || stored != tok {
		t.Errorf("the URL's token %q, the file's %q (%v)", tok, stored, err)
	}
	for _, want := range []string{"hub on " + base + "/ for paired devices", "or scan it:\n\x1b[30;107m"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(base + "/pair?token=" + tok)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == hub.CookieName {
			cookie = c
		}
	}
	if resp.StatusCode != http.StatusSeeOther || cookie == nil {
		t.Fatalf("pair: %d %v", resp.StatusCode, resp.Cookies())
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/", nil)
	req.AddCookie(cookie)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "no live run") {
		t.Errorf("paired: %d %q", resp.StatusCode, body)
	}
	cancel()
	select {
	case code := <-done:
		if code != exitOK || !strings.HasSuffix(out.String(), " the hub ended; the runs go on\n") {
			t.Errorf("exit %d, output:\n%s\n%s", code, out.String(), errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the hub didn't end with its context")
	}
}

// A public address needs --listen-public and TLS, named as the hub's flags.
func TestRunHubPublic(t *testing.T) {
	useHubSeams(t, nil)
	for _, tc := range []struct {
		c    hubConfig
		want string
	}{
		{hubConfig{all: true, addr: "0.0.0.0:0"}, "or pass --listen-public with --tls-cert and --tls-key"},
		{hubConfig{all: true, addr: "0.0.0.0:0", public: true}, "--listen-public needs TLS: set --tls-cert and --tls-key"},
	} {
		var out, errb bytes.Buffer
		if code := runHub(context.Background(), tc.c, &out, &errb); code != exitFail || !strings.Contains(errb.String(), tc.want) {
			t.Errorf("%+v: exit %d, %q", tc.c, code, errb.String())
		}
	}
}

func TestHubOpenBase(t *testing.T) {
	dir := t.TempDir()
	e := state.RegistryEnv{RuntimeDir: dir, TempDir: dir}
	root := "/work/Demo App"
	if got := hubOpenBase(e, root, "Demo App"); got != "" {
		t.Errorf("without a hub: %q", got)
	}
	remove, err := state.RegisterHub(e, state.HubEntry{URL: "http://127.0.0.1:7777/", Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hubOpenBase(e, root, "Demo App"), "http://127.0.0.1:7777/p/"+hub.RunID("Demo App", root); got != want {
		t.Errorf("hub has the project: %q, want %q", got, want)
	}
	if got := hubOpenBase(e, "/work/other", "other"); got != "" {
		t.Errorf("project not in the hub: %q", got)
	}
	_ = remove()
	if got := hubOpenBase(e, root, "Demo App"); got != "" {
		t.Errorf("after the hub ended: %q", got)
	}
}
