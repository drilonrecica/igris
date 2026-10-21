package update

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/drilonrecica/igris/internal/runner"
)

// Build is how the running igris was built, as main knows it.
type Build struct {
	// Version is igris's version as `igris version` prints it (the
	// ldflags version, else the module version without its "v").
	Version string
	// Ldflags is the version set with -ldflags "-X main.version=…"; "" or
	// "dev" when none was.
	Ldflags string
	// Module is the main module's version from the build info: "vX.Y.Z"
	// for `go install …@vX.Y.Z`, "(devel)" or a pseudo-version otherwise;
	// "" when there is no build info.
	Module string
	// Executable is the running binary's path with symlinks resolved; ""
	// when it can't be found.
	Executable string
}

// Method is how igris was installed, and so what updates it.
type Method int

const (
	// Archive is a release archive, or an install igris can't tell: it
	// runs nothing and shows the steps instead.
	Archive Method = iota
	// Brew is Homebrew: `brew upgrade igris`.
	Brew
	// GoInstall is `go install …@vX.Y.Z`: the same command for the new
	// version, into the running binary's directory.
	GoInstall
)

// String names the method for the owner.
func (m Method) String() string {
	switch m {
	case Brew:
		return "Homebrew"
	case GoInstall:
		return "go install"
	}
	return "a release archive (or a way igris can't tell)"
}

// Detect tells how b was installed: Homebrew when the binary is in its
// Cellar, go install when no ldflags version was set and the build info
// has a release's module version, else Archive.
func Detect(b Build) Method {
	exe := filepath.ToSlash(b.Executable)
	switch {
	case strings.Contains(exe, "/Cellar/igris/"):
		return Brew
	case (b.Ldflags == "" || b.Ldflags == "dev") && strings.HasPrefix(b.Module, "v") && !Development(b.Module):
		return GoInstall
	}
	return Archive
}

// Install is what updates igris to a version.
type Install struct {
	Method Method
	// Argv is the command, program first; empty for Archive.
	Argv []string
	// Env is what the command gets on top of igris's environment
	// (GOBIN for go install, so the running binary's file is replaced).
	Env []string
	// Steps are the download, verify and install steps for Archive, one
	// shell line each, for the owner to run.
	Steps []string
}

// Command is the exact command as the owner would type it, environment
// first; "" for Archive. It is for display only, never run through a
// shell.
func (in Install) Command() string {
	if len(in.Argv) == 0 {
		return ""
	}
	c := runner.Cmd{Name: in.Argv[0], Args: in.Argv[1:]}.String()
	if len(in.Env) > 0 {
		var env []string
		for _, kv := range in.Env {
			name, val, _ := strings.Cut(kv, "=")
			env = append(env, name+"="+quoteValue(val))
		}
		c = strings.Join(env, " ") + " " + c
	}
	return c
}

// quoteValue quotes an environment value as runner.Cmd.String quotes an
// argument.
func quoteValue(v string) string { return runner.Cmd{Name: v}.String() }

// For builds the install for latest (X.Y.Z, shape-checked by Latest) on
// b's method. Only latest goes into the command, as argv.
func For(b Build, latest string) Install {
	in := Install{Method: Detect(b)}
	switch in.Method {
	case Brew:
		in.Argv = []string{"brew", "upgrade", "igris"}
	case GoInstall:
		in.Argv = []string{"go", "install", Module + "/cmd/igris@v" + latest}
		if b.Executable != "" {
			in.Env = []string{"GOBIN=" + filepath.Dir(b.Executable)}
		}
	default:
		in.Steps = ArchiveSteps(latest, runtime.GOOS, runtime.GOARCH, b.Executable)
	}
	return in
}

// ArchiveSteps are the release notes' download, verify and install steps
// for version v on goos/goarch, installing over exe (or into ~/.local/bin
// when it isn't known).
func ArchiveSteps(v, goos, goarch, exe string) []string {
	archive := fmt.Sprintf("igris_%s_%s_%s.tar.gz", v, goos, goarch)
	dl := "https://github.com/" + repo + "/releases/download/v" + v + "/"
	sum := "sha256sum --ignore-missing -c checksums.txt"
	if goos == "darwin" {
		sum = "shasum -a 256 --ignore-missing -c checksums.txt"
	}
	dest := "~/.local/bin/igris"
	if exe != "" {
		dest = runner.Cmd{Name: exe}.String()
	}
	return []string{
		"curl -fsSLO " + dl + archive,
		"curl -fsSLO " + dl + "checksums.txt",
		"curl -fsSLO " + dl + "checksums.txt.sig",
		"curl -fsSLO https://raw.githubusercontent.com/" + repo + "/v" + v + "/allowed_signers",
		"ssh-keygen -Y verify -f allowed_signers -I igris -n file -s checksums.txt.sig < checksums.txt",
		sum,
		"tar xzf " + archive,
		"install -m 755 igris " + dest,
		"igris version",
	}
}

// Status is what a check found.
type Status int

const (
	// UpToDate: the running igris is the latest release or newer.
	UpToDate Status = iota
	// Newer: a newer release is out.
	Newer
	// Dev: the running igris is a development build, which isn't updated.
	Dev
)

// Check is the result of asking for the latest release.
type Check struct {
	Current string // the running igris's version
	Latest  string // the latest release, X.Y.Z
	Status  Status
	// Install updates igris to Latest; set when Status is Newer.
	Install Install
}

// URL is the latest release's page.
func (c Check) URL() string { return ReleaseURL(c.Latest) }

// Compare compares b with latest (X.Y.Z, from Latest).
func (b Build) Compare(latest string) Check {
	c := Check{Current: b.Version, Latest: latest}
	switch {
	case Development(b.Version):
		c.Status = Dev
	case Compare(b.Version, latest) < 0:
		c.Status, c.Install = Newer, For(b, latest)
	}
	return c
}
