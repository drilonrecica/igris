package project

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/update"
)

// Home's services check against the configured API and run the install
// command without the notification secrets' variables, then ask the igris
// in PATH for its version.
func TestServicesUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.0"}`))
	}))
	defer srv.Close()
	root := t.TempDir()
	cfg := "[notify.discord]\nwebhook_url = \"env:DISCORD_HOOK\"\n"
	if err := os.WriteFile(filepath.Join(root, "igris.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &runner.Fake{}
	r.On([]string{"brew", "upgrade", "igris"}, runner.Result{}, nil)
	r.On([]string{"/opt/homebrew/bin/igris", "version"}, runner.Result{Stdout: []byte("igris 1.2.0\n")}, nil)
	env := Env{
		Runner:    r,
		UpdateAPI: srv.URL,
		Build:     update.Build{Version: "1.1.0", Ldflags: "1.1.0", Executable: "/opt/homebrew/Cellar/igris/1.1.0/bin/igris"},
		Environ:   func() []string { return []string{"PATH=/usr/bin", "DISCORD_HOOK=https://secret"} },
		Self: func() (string, string, bool) {
			return "/opt/homebrew/Cellar/igris/1.1.0/bin/igris", "/opt/homebrew/bin/igris", true
		},
	}
	s := NewServices(root, env)
	c, err := s.CheckUpdate(context.Background())
	if err != nil || c.Status != update.Newer || c.Install.Method != update.Brew {
		t.Fatalf("CheckUpdate = %+v, %v", c, err)
	}
	res := s.Update(context.Background(), c)
	if res.Err != nil || res.Message != "Updated: igris in PATH is now 1.2.0." {
		t.Fatalf("Update = %+v", res)
	}
	if got := r.Calls()[0].Env; !slices.Equal(got, []string{"PATH=/usr/bin"}) {
		t.Errorf("the command's env = %q, want the secret's variable left out", got)
	}
}
