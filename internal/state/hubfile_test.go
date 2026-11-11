package state

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRegisterHubWritesReadsAndRemoves(t *testing.T) {
	e := regEnv(t)
	remove, err := RegisterHub(e, HubEntry{URL: "https://hub.example:7777/", PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	path, _ := e.HubFile()
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("hub.json: %v, %v", fi, err)
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Errorf("directory is %v, want 0700", fi.Mode().Perm())
	}
	data, _ := os.ReadFile(path) //nolint:gosec // the test's own file
	if want := `{"v":1,"url":"https://hub.example:7777/","pid":` + strconv.Itoa(os.Getpid()) + "}\n"; string(data) != want {
		t.Errorf("hub.json = %q, want %q", data, want)
	}
	h, ok := LiveHub(e)
	if !ok || h.URL != "https://hub.example:7777/" || !h.Has("/any/root") {
		t.Fatalf("LiveHub = %+v, %v", h, ok)
	}
	if err := remove(); err != nil {
		t.Fatal(err)
	}
	if err := remove(); err != nil {
		t.Errorf("removing twice: %v", err)
	}
	if _, ok := LiveHub(e); ok {
		t.Error("a removed hub.json still counts")
	}
}

func TestLiveHubRefusals(t *testing.T) {
	write := func(e RegistryEnv, body string) {
		t.Helper()
		path, _ := e.HubFile()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name  string
		body  string
		alive bool
	}{
		{"dead pid", `{"v":1,"url":"http://127.0.0.1:7777/","pid":99999}`, false},
		{"other version", `{"v":2,"url":"http://127.0.0.1:7777/","pid":1}`, true},
		{"no pid", `{"v":1,"url":"http://127.0.0.1:7777/","pid":0}`, true},
		{"not json", `nope`, true},
		{"file url", `{"v":1,"url":"file:///etc/passwd","pid":1}`, true},
		{"javascript url", `{"v":1,"url":"javascript:alert(1)","pid":1}`, true},
		{"user info", `{"v":1,"url":"http://u:p@127.0.0.1:7777/","pid":1}`, true},
		{"no host", `{"v":1,"url":"http:///x","pid":1}`, true},
		{"empty", ``, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := regEnv(t)
			write(e, tt.body)
			if h, ok := liveHub(e, func(int) bool { return tt.alive }); ok {
				t.Errorf("accepted %+v", h)
			}
		})
	}
	t.Run("no file", func(t *testing.T) {
		if _, ok := LiveHub(regEnv(t)); ok {
			t.Error("a missing file counts")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		e := regEnv(t)
		path, _ := e.HubFile()
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		target := filepath.Join(t.TempDir(), "t.json")
		_ = os.WriteFile(target, []byte(`{"v":1,"url":"http://127.0.0.1:7777/","pid":1}`), 0o600)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, ok := liveHub(e, func(int) bool { return true }); ok {
			t.Error("a symlink counts")
		}
	})
}

func TestHubEntryHasRoots(t *testing.T) {
	h := HubEntry{Roots: []string{"/a", "/b"}}
	if !h.Has("/a") || h.Has("/c") {
		t.Errorf("Has: %v %v", h.Has("/a"), h.Has("/c"))
	}
	if !(HubEntry{}).Has("/c") {
		t.Error("a hub with All takes every project")
	}
}
