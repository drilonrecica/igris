package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
)

// server answers the latest-release request with status and body, and
// records the request's headers.
func server(t *testing.T, status int, body string) (*httptest.Server, *http.Header) {
	t.Helper()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/drilonrecica/igris/releases/latest" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		got = r.Header.Clone()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestLatest(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr string
	}{
		{"release", 200, `{"tag_name":"v1.2.0","name":"\u001b[31mevil","body":"long notes"}`, "1.2.0", ""},
		{"big numbers", 200, `{"tag_name":"v10.20.300"}`, "10.20.300", ""},
		{"pre-release tag", 200, `{"tag_name":"v1.3.0-rc.1"}`, "", "not vX.Y.Z"},
		{"no v", 200, `{"tag_name":"1.2.0"}`, "", "not vX.Y.Z"},
		{"injected", 200, `{"tag_name":"v1.2.0; rm -rf ~"}`, "", "not vX.Y.Z"},
		{"no tag", 200, `{}`, "", "not vX.Y.Z"},
		{"not JSON", 200, `<html>`, "", "isn't the JSON"},
		{"oversized", 200, `{"tag_name":"v1.2.0","body":"` + strings.Repeat("x", maxBody) + `"}`, "", "over 1 MiB"},
		{"server error", 500, `oops`, "", "GitHub answered 500 Internal Server Error"},
		{"rate limited", 403, `{"message":"API rate limit exceeded"}`, "", "rate limit"},
		{"no release", 404, `{}`, "", "no release found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, hdr := server(t, tc.status, tc.body)
			got, err := Latest(context.Background(), srv.Client(), srv.URL, "igris/1.1.0")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				if strings.Contains(err.Error(), "evil") || strings.Contains(err.Error(), "rm -rf") || strings.Contains(err.Error(), "API rate limit exceeded") {
					t.Errorf("the error shows GitHub's text: %v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Latest = %q, %v; want %q", got, err, tc.want)
			}
			if hdr.Get("Accept") != "application/vnd.github+json" || hdr.Get("User-Agent") != "igris/1.1.0" {
				t.Errorf("headers: Accept %q, User-Agent %q", hdr.Get("Accept"), hdr.Get("User-Agent"))
			}
		})
	}
}

// A server that doesn't answer in time, and one that refuses, are network
// errors that say to check the connection.
func TestLatestNetworkErrors(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Latest(ctx, slow.Client(), slow.URL, "igris/1.1.0")
	var ne *NetworkError
	if !errors.As(err, &ne) || !strings.Contains(err.Error(), "no answer within") || !strings.Contains(err.Error(), "check your connection") {
		t.Errorf("timeout: %v", err)
	}

	client := &http.Client{Timeout: 50 * time.Millisecond}
	if _, err := Latest(context.Background(), client, slow.URL, "igris/1.1.0"); !errors.As(err, &ne) || !strings.Contains(err.Error(), "no answer within") {
		t.Errorf("client timeout: %v", err)
	}

	if _, err := Latest(context.Background(), nil, "http://127.0.0.1:1", "igris/1.1.0"); !errors.As(err, &ne) || !strings.Contains(err.Error(), "check your connection") {
		t.Errorf("refused: %v", err)
	}

	cctx, ccancel := context.WithCancel(context.Background())
	ccancel()
	if _, err := Latest(cctx, nil, slow.URL, "igris/1.1.0"); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("cancelled: %v", err)
	}
}

func TestDevelopment(t *testing.T) {
	tests := []struct {
		v    string
		want bool
	}{
		{"1.2.0", false},
		{"v1.2.0", false},
		{"1.2.0-rc.1", false},
		{"dev", true},
		{"(devel)", true},
		{"", true},
		{"1.1.1-0.20261020232616-74dcdb86e12b", true},
		{"v1.1.1-0.20261020232616-74dcdb86e12b+dirty", true},
		{"v0.0.0-20261020232616-74dcdb86e12b", true},
		{"v1.2.0-rc.1.0.20261020232616-74dcdb86e12b", true},
		{"v1.0.0-12-g12d9801", true},
		{"v1.0.0-12-g12d9801-dirty", true},
		{"v1.2.0-dirty", true},
		{"74dcdb8", true},
	}
	for _, tc := range tests {
		if got := Development(tc.v); got != tc.want {
			t.Errorf("Development(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.1.0", "1.2.0", -1},
		{"1.2.0", "1.2.0", 0},
		{"v1.2.0", "1.2.0", 0},
		{"1.3.0", "1.2.0", 1},
		{"1.10.0", "1.9.0", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.2.0-rc.1", "1.2.0", -1},
		{"1.2.0-rc.2", "1.2.0-rc.10", -1},
		{"1.2.0-rc.1", "1.1.9", 1},
		{"1.2.0-alpha", "1.2.0-beta", -1},
		{"1.2.0-1", "1.2.0-alpha", -1},
		{"1.2.0-rc", "1.2.0-rc.1", -1},
		{"junk", "1.0.0", -1},
	}
	for _, tc := range tests {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestDetectAndFor(t *testing.T) {
	tests := []struct {
		name    string
		b       Build
		method  Method
		argv    []string
		env     []string
		command string
	}{
		{"linuxbrew", Build{Version: "1.1.0", Ldflags: "1.1.0", Executable: "/home/linuxbrew/.linuxbrew/Cellar/igris/1.1.0/bin/igris"}, Brew, []string{"brew", "upgrade", "igris"}, nil, "brew upgrade igris"},
		{"macOS brew", Build{Version: "1.1.0", Ldflags: "1.1.0", Executable: "/opt/homebrew/Cellar/igris/1.1.0/bin/igris"}, Brew, []string{"brew", "upgrade", "igris"}, nil, "brew upgrade igris"},
		{"go install", Build{Version: "1.1.0", Ldflags: "dev", Module: "v1.1.0", Executable: "/home/u/go/bin/igris"}, GoInstall,
			[]string{"go", "install", "github.com/drilonrecica/igris/cmd/igris@v1.2.0"}, []string{"GOBIN=/home/u/go/bin"},
			"GOBIN=/home/u/go/bin go install github.com/drilonrecica/igris/cmd/igris@v1.2.0"},
		{"go install, path with a space", Build{Version: "1.1.0", Module: "v1.1.0", Executable: "/home/u/my go/bin/igris"}, GoInstall,
			[]string{"go", "install", "github.com/drilonrecica/igris/cmd/igris@v1.2.0"}, []string{"GOBIN=/home/u/my go/bin"},
			"GOBIN='/home/u/my go/bin' go install github.com/drilonrecica/igris/cmd/igris@v1.2.0"},
		{"release archive", Build{Version: "1.1.0", Ldflags: "1.1.0", Module: "(devel)", Executable: "/home/u/.local/bin/igris"}, Archive, nil, nil, ""},
		{"make install with ldflags", Build{Version: "v1.1.0", Ldflags: "v1.1.0", Module: "(devel)", Executable: "/home/u/go/bin/igris"}, Archive, nil, nil, ""},
		{"go build from a checkout", Build{Version: "dev", Ldflags: "dev", Module: "v1.1.1-0.20261020232616-74dcdb86e12b", Executable: "/src/igris/igris"}, Archive, nil, nil, ""},
		{"unknown executable", Build{Version: "1.1.0", Ldflags: "1.1.0"}, Archive, nil, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Detect(tc.b); got != tc.method {
				t.Fatalf("Detect = %v, want %v", got, tc.method)
			}
			in := For(tc.b, "1.2.0")
			if in.Method != tc.method || !slices.Equal(in.Argv, tc.argv) || !slices.Equal(in.Env, tc.env) || in.Command() != tc.command {
				t.Errorf("For = %+v (command %q)", in, in.Command())
			}
			if tc.method == Archive && len(in.Steps) == 0 {
				t.Error("an archive install has no steps")
			}
		})
	}
}

// The steps are the release notes' (docs/release-notes), for the platform.
func TestArchiveSteps(t *testing.T) {
	got := ArchiveSteps("1.2.0", "darwin", "arm64", "/usr/local/bin/igris")
	want := []string{
		"curl -fsSLO https://github.com/drilonrecica/igris/releases/download/v1.2.0/igris_1.2.0_darwin_arm64.tar.gz",
		"curl -fsSLO https://github.com/drilonrecica/igris/releases/download/v1.2.0/checksums.txt",
		"curl -fsSLO https://github.com/drilonrecica/igris/releases/download/v1.2.0/checksums.txt.sig",
		"curl -fsSLO https://raw.githubusercontent.com/drilonrecica/igris/v1.2.0/allowed_signers",
		"ssh-keygen -Y verify -f allowed_signers -I igris -n file -s checksums.txt.sig < checksums.txt",
		"shasum -a 256 --ignore-missing -c checksums.txt",
		"tar xzf igris_1.2.0_darwin_arm64.tar.gz",
		"install -m 755 igris /usr/local/bin/igris",
		"igris version",
	}
	if !slices.Equal(got, want) {
		t.Errorf("ArchiveSteps:\n%s", strings.Join(got, "\n"))
	}
	if linux := ArchiveSteps("1.2.0", "linux", "amd64", ""); linux[5] != "sha256sum --ignore-missing -c checksums.txt" || linux[7] != "install -m 755 igris ~/.local/bin/igris" {
		t.Errorf("linux steps: %q", linux)
	}
}

func TestBuildCompare(t *testing.T) {
	brew := Build{Version: "1.1.0", Ldflags: "1.1.0", Executable: "/opt/homebrew/Cellar/igris/1.1.0/bin/igris"}
	if c := brew.Compare("1.2.0"); c.Status != Newer || c.Install.Method != Brew || c.URL() != "https://github.com/drilonrecica/igris/releases/tag/v1.2.0" {
		t.Errorf("newer: %+v", c)
	}
	for _, latest := range []string{"1.1.0", "1.0.9"} {
		if c := brew.Compare(latest); c.Status != UpToDate || len(c.Install.Argv) != 0 {
			t.Errorf("latest %s: %+v", latest, c)
		}
	}
	dev := Build{Version: "dev", Ldflags: "dev", Module: "(devel)"}
	if c := dev.Compare("1.2.0"); c.Status != Dev || len(c.Install.Argv) != 0 {
		t.Errorf("dev: %+v", c)
	}
}

func TestRun(t *testing.T) {
	brew := Install{Method: Brew, Argv: []string{"brew", "upgrade", "igris"}}
	goIn := Install{Method: GoInstall, Argv: []string{"go", "install", Module + "/cmd/igris@v1.2.0"}, Env: []string{"GOBIN=/home/u/go/bin"}}
	env := []string{"PATH=/usr/bin", "GOBIN=/elsewhere", "HOME=/home/u"}

	t.Run("brew success", func(t *testing.T) {
		r := &runner.Fake{}
		r.On([]string{"brew", "upgrade", "igris"}, runner.Result{Stdout: []byte("==> Upgrading igris\n")}, nil)
		res := Run(context.Background(), r, brew, env)
		if res.Err != nil || len(res.Tail) != 0 {
			t.Fatalf("Run = %+v", res)
		}
		c := r.Calls()[0]
		if c.Timeout != CommandTimeout || !c.Combined || !slices.Equal(c.Env, env) {
			t.Errorf("cmd = %+v", c)
		}
	})
	t.Run("brew failure", func(t *testing.T) {
		r := &runner.Fake{}
		var out strings.Builder
		for i := range 15 {
			out.WriteString("line " + string(rune('a'+i)) + "\n")
		}
		out.WriteString("Error: \x1b]0;title\x07igris: no bottle\r\n")
		r.On([]string{"brew"}, runner.Result{Stdout: []byte(out.String()), ExitCode: 1}, nil)
		res := Run(context.Background(), r, brew, env)
		if res.Err == nil || !strings.Contains(res.Err.Error(), "brew upgrade igris exited with status 1") || res.Hint != "" {
			t.Fatalf("Run = %+v", res)
		}
		if len(res.Tail) != tailLines || res.Tail[len(res.Tail)-1] != "Error: igris: no bottle" || res.Tail[0] != "line g" {
			t.Errorf("tail = %q", res.Tail)
		}
	})
	t.Run("go install with GOBIN", func(t *testing.T) {
		r := &runner.Fake{}
		r.On([]string{"go", "install"}, runner.Result{}, nil)
		if res := Run(context.Background(), r, goIn, env); res.Err != nil {
			t.Fatalf("Run = %+v", res)
		}
		if c := r.Calls()[0]; !slices.Equal(c.Env, []string{"PATH=/usr/bin", "HOME=/home/u", "GOBIN=/home/u/go/bin"}) {
			t.Errorf("env = %q", c.Env)
		}
	})
	t.Run("unknown revision", func(t *testing.T) {
		r := &runner.Fake{}
		r.On([]string{"go", "install"}, runner.Result{Stdout: []byte("go: github.com/drilonrecica/igris/cmd/igris@v1.2.0: unknown revision v1.2.0\n"), ExitCode: 1}, nil)
		res := Run(context.Background(), r, goIn, env)
		want := "GOPROXY=direct GONOSUMDB=github.com/drilonrecica/igris GOBIN=/home/u/go/bin go install github.com/drilonrecica/igris/cmd/igris@v1.2.0"
		if res.Err == nil || !strings.Contains(res.Hint, want) || !strings.Contains(res.Hint, "30 minutes") {
			t.Errorf("Run = %+v", res)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		r := &runner.Fake{}
		r.On([]string{"go"}, runner.Result{ExitCode: -1}, runner.ErrTimeout)
		if res := Run(context.Background(), r, goIn, env); !errors.Is(res.Err, runner.ErrTimeout) {
			t.Errorf("Run = %+v", res)
		}
	})
	t.Run("archive", func(t *testing.T) {
		r := &runner.Fake{}
		if res := Run(context.Background(), r, Install{Method: Archive, Steps: []string{"x"}}, env); res.Err == nil || len(r.Calls()) != 0 {
			t.Errorf("an archive install ran something: %+v", res)
		}
	})
}

func TestUpdated(t *testing.T) {
	tests := []struct {
		inPath, version, want string
	}{
		{"/usr/local/bin/igris", "1.2.0", "Updated: igris in PATH is now 1.2.0."},
		{"/usr/local/bin/igris", "v1.2.0", "Updated: igris in PATH is now v1.2.0."},
		{"/usr/local/bin/igris", "1.1.0", "Updated to 1.2.0, but igris in PATH (/usr/local/bin/igris) is 1.1.0: put the updated igris first in PATH."},
		{"/usr/local/bin/igris", "", "Updated to 1.2.0, but /usr/local/bin/igris didn't print its version: run igris version."},
		{"", "", "Updated to 1.2.0, but igris is not in PATH: put it in PATH so sessions can run igris done."},
	}
	for _, tc := range tests {
		if got := Updated("1.2.0", tc.inPath, tc.version); got != tc.want {
			t.Errorf("Updated(%q, %q) = %q", tc.inPath, tc.version, got)
		}
	}
}
