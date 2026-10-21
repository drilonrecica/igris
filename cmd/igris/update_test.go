package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/runner"
	upd "github.com/drilonrecica/igris/internal/update"
)

// Builds as main sees them.
var (
	brewBuild    = upd.Build{Version: "1.1.0", Ldflags: "1.1.0", Executable: "/home/linuxbrew/.linuxbrew/Cellar/igris/1.1.0/bin/igris"}
	goBuild      = upd.Build{Version: "1.1.0", Ldflags: "dev", Module: "v1.1.0", Executable: "/home/u/go/bin/igris"}
	archiveBuild = upd.Build{Version: "1.1.0", Ldflags: "1.1.0", Executable: "/home/u/.local/bin/igris"}
	devBuild     = upd.Build{Version: "dev", Ldflags: "dev", Module: "(devel)", Executable: "/src/igris/bin/igris"}
)

// withUpdate points igris update at a local server answering tag (status
// 200) and at build, with r as the command runner and igris in PATH at
// inPath; env is igris's environment.
func withUpdate(t *testing.T, tag string, build upd.Build, r runner.Runner, inPath string, env []string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/repos/drilonrecica/igris/releases/latest" || req.Header.Get("User-Agent") != "igris/"+build.Version {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `","body":"\u001b[31mnot shown"}`))
	}))
	t.Cleanup(srv.Close)
	savedAPI, savedBuild, savedRunner, savedSelf := updateAPI, updateBuild, ariseRunner, igrisSelf
	savedEnviron, savedTerm, savedStdin := updateEnviron, interactiveTerm, updateStdin
	t.Cleanup(func() {
		updateAPI, updateBuild, ariseRunner, igrisSelf = savedAPI, savedBuild, savedRunner, savedSelf
		updateEnviron, interactiveTerm, updateStdin = savedEnviron, savedTerm, savedStdin
	})
	updateAPI = srv.URL
	updateBuild = func() upd.Build { return build }
	ariseRunner = r
	igrisSelf = func() (string, string, bool) { return build.Executable, inPath, inPath == build.Executable }
	updateEnviron = func() []string { return env }
	updateStdin = strings.NewReader("")
}

func TestUpdateCheck(t *testing.T) {
	tests := []struct {
		name  string
		tag   string
		build upd.Build
		want  []string
	}{
		{"newer brew", "v1.2.0", brewBuild, []string{"igris 1.1.0 → 1.2.0", "https://github.com/drilonrecica/igris/releases/tag/v1.2.0", "installed with Homebrew", "update runs: brew upgrade igris"}},
		{"newer go install", "v1.2.0", goBuild, []string{"installed with go install", "update runs: GOBIN=/home/u/go/bin go install github.com/drilonrecica/igris/cmd/igris@v1.2.0"}},
		{"newer archive", "v1.2.0", archiveBuild, []string{"installed with a release archive", "curl -fsSLO https://github.com/drilonrecica/igris/releases/download/v1.2.0/checksums.txt.sig", "ssh-keygen -Y verify -f allowed_signers -I igris -n file -s checksums.txt.sig < checksums.txt", "install -m 755 igris /home/u/.local/bin/igris"}},
		{"equal", "v1.1.0", brewBuild, []string{"igris 1.1.0 is up to date (the latest release is 1.1.0)", "installed with Homebrew"}},
		{"older", "v1.0.0", brewBuild, []string{"igris 1.1.0 is up to date (the latest release is 1.0.0)"}},
		{"dev", "v1.2.0", devBuild, []string{"igris dev is a development build, which igris doesn't update; the latest release is 1.2.0"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inDir(t)
			r := &runner.Fake{}
			withUpdate(t, tc.tag, tc.build, r, "/usr/local/bin/igris", nil)
			code, out, errb := runCmd("update", "--check")
			if code != exitOK || errb != "" {
				t.Fatalf("exit %d, stderr %q", code, errb)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			if strings.Contains(out, "not shown") {
				t.Error("text from GitHub's answer other than the tag was shown")
			}
			if n := len(r.Calls()); n != 0 {
				t.Errorf("--check ran %d commands", n)
			}
		})
	}
}

// Without a terminal and without --yes, nothing runs and it exits 1; on a
// terminal it asks.
func TestUpdateAsks(t *testing.T) {
	inDir(t)
	r := &runner.Fake{}
	withUpdate(t, "v1.2.0", brewBuild, r, "/usr/local/bin/igris", nil)
	code, _, errb := runCmd("update")
	if code != exitFail || !strings.Contains(errb, "pass --yes") || len(r.Calls()) != 0 {
		t.Fatalf("no terminal: exit %d, stderr %q, %d commands", code, errb, len(r.Calls()))
	}

	interactiveTerm = func() bool { return true }
	updateStdin = strings.NewReader("n\n")
	code, out, _ := runCmd("update")
	if code != exitOK || !strings.Contains(out, "Update? [y/N]") || !strings.Contains(out, "nothing changed") || len(r.Calls()) != 0 {
		t.Fatalf("declined: exit %d, %d commands:\n%s", code, len(r.Calls()), out)
	}

	updateStdin = strings.NewReader("")
	if code, _, errb := runCmd("update"); code != exitFail || !strings.Contains(errb, "no answer") {
		t.Errorf("no answer: exit %d, stderr %q", code, errb)
	}

	r.On([]string{"brew", "upgrade", "igris"}, runner.Result{}, nil)
	r.On([]string{"/usr/local/bin/igris", "version"}, runner.Result{Stdout: []byte("igris 1.2.0\n")}, nil)
	updateStdin = strings.NewReader("y\n")
	code, out, errb = runCmd("update")
	if code != exitOK || !strings.Contains(out, "Updated: igris in PATH is now 1.2.0.") {
		t.Errorf("accepted: exit %d, stderr %q:\n%s", code, errb, out)
	}
}

// --yes runs the command as argv with igris's environment minus the
// notification secrets' variables, then asks the igris in PATH for its
// version.
func TestUpdateYes(t *testing.T) {
	writeProject(t, map[string]string{"igris.toml": "[notify.ntfy]\ntopic = \"t\"\ntoken = \"env:NTFY_TOKEN\"\n"})
	r := &runner.Fake{}
	r.On([]string{"brew", "upgrade", "igris"}, runner.Result{Stdout: []byte("==> Upgrading igris\n")}, nil)
	r.On([]string{"/usr/local/bin/igris", "version"}, runner.Result{Stdout: []byte("igris 1.2.0\n")}, nil)
	withUpdate(t, "v1.2.0", brewBuild, r, "/usr/local/bin/igris", []string{"PATH=/usr/bin", "NTFY_TOKEN=secret", "HOME=/home/u"})
	code, out, errb := runCmd("update", "--yes")
	if code != exitOK || !strings.Contains(out, "Updated: igris in PATH is now 1.2.0.") {
		t.Fatalf("exit %d, stderr %q:\n%s", code, errb, out)
	}
	calls := r.Calls()
	if len(calls) != 2 {
		t.Fatalf("%d commands, want brew and igris version: %v", len(calls), calls)
	}
	brew := calls[0]
	if brew.Timeout != upd.CommandTimeout || !slices.Equal(brew.Env, []string{"PATH=/usr/bin", "HOME=/home/u"}) {
		t.Errorf("brew ran with timeout %s, env %q", brew.Timeout, brew.Env)
	}
}

// A failed command exits 1 with the end of its output, cleaned, and for
// go install's unknown revision the hint; go install gets GOBIN.
func TestUpdateFails(t *testing.T) {
	inDir(t)
	r := &runner.Fake{}
	out := "go: downloading github.com/drilonrecica/igris v1.2.0\ngo: github.com/drilonrecica/igris/cmd/igris@v1.2.0: unknown revision v1.2.0 \x1b[31mred\x1b[0m\n"
	r.On([]string{"go", "install"}, runner.Result{Stdout: []byte(out), ExitCode: 1}, nil)
	withUpdate(t, "v1.2.0", goBuild, r, "/home/u/go/bin/igris", []string{"PATH=/usr/bin", "GOBIN=/elsewhere"})
	code, _, errb := runCmd("update", "--yes")
	if code != exitFail {
		t.Fatalf("exit %d", code)
	}
	for _, w := range []string{"unknown revision v1.2.0 red", "GOPROXY=direct GONOSUMDB=github.com/drilonrecica/igris GOBIN=/home/u/go/bin go install github.com/drilonrecica/igris/cmd/igris@v1.2.0", "exited with status 1"} {
		if !strings.Contains(errb, w) {
			t.Errorf("stderr lacks %q:\n%s", w, errb)
		}
	}
	if strings.Contains(errb, "\x1b") {
		t.Error("the output isn't cleaned")
	}
	c := r.Calls()[0]
	if !slices.Equal(c.Args, []string{"install", "github.com/drilonrecica/igris/cmd/igris@v1.2.0"}) || !slices.Equal(c.Env, []string{"PATH=/usr/bin", "GOBIN=/home/u/go/bin"}) {
		t.Errorf("go install ran as %v with env %q", c.Args, c.Env)
	}
}

// A request that fails exits 1, an unexpected argument or flag 2.
func TestUpdateExitCodes(t *testing.T) {
	inDir(t)
	withUpdate(t, "v1.2.0", brewBuild, &runner.Fake{}, "/usr/local/bin/igris", nil)
	updateAPI = "http://127.0.0.1:1" // refused
	if code, _, errb := runCmd("update", "--check"); code != exitFail || !strings.Contains(errb, "check your connection") {
		t.Errorf("refused: exit %d, stderr %q", code, errb)
	}
	withUpdate(t, "v1.2", brewBuild, &runner.Fake{}, "/usr/local/bin/igris", nil)
	if code, _, errb := runCmd("update", "--check"); code != exitFail || !strings.Contains(errb, "not vX.Y.Z") {
		t.Errorf("malformed tag: exit %d, stderr %q", code, errb)
	}
	for _, args := range [][]string{{"update", "now"}, {"update", "--force"}} {
		if code, _, _ := runCmd(args...); code != exitUsage {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
