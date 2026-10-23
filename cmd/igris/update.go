package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/textsafe"
	upd "github.com/drilonrecica/igris/internal/update" // "update" is the tests' -update flag
)

// Seams for tests: the GitHub API `igris update` asks ("" is GitHub's),
// how this igris was built, where it reads its answer, and igris's
// environment.
var (
	updateAPI               = ""
	updateBuild             = igrisBuild
	updateStdin   io.Reader = os.Stdin
	updateEnviron           = os.Environ
)

// igrisBuild is how this igris was built: its version, the ldflags
// version, the build info's module version and the binary's resolved path
// (SPEC §14, igris update).
func igrisBuild() upd.Build {
	info, ok := debug.ReadBuildInfo()
	b := upd.Build{Version: resolveVersion(version, info, ok), Ldflags: version}
	if ok && info != nil {
		b.Module = info.Main.Version
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		b.Executable = exe
	}
	return b
}

// updateArgs registers the flags of `igris update`.
func updateArgs(fs *flag.FlagSet) func([]string) error {
	fs.Bool("check", false, "print the current and latest versions and the install method; run nothing")
	fs.Bool("yes", false, "update without asking")
	return maxArgs(fs, 0)
}

// execUpdate is `igris update` (SPEC §14, decisions.md V12-P4): it asks
// GitHub for the latest release and, when it is newer, runs the command of
// the package manager that installed igris, after asking. igris never
// replaces its own binary; an archive install gets the steps instead.
func execUpdate(fs *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris update: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	ctx := context.Background()
	env := projectEnv()
	c, err := project.CheckUpdate(ctx, env)
	if err != nil {
		return fail("%v", err)
	}
	current := textsafe.Line(c.Current)
	o := newOut(stdout)
	switch c.Status {
	case upd.Dev:
		fmt.Fprintf(stdout, "igris %s is a development build, which igris doesn't update; the latest release is %s\n%s\n", current, c.Latest, c.URL())
		o.next("igris doctor")
		return exitOK
	case upd.UpToDate:
		fmt.Fprintf(stdout, "%s igris %s is up to date (the latest release is %s)\n", o.paint(look.Accent, look.GlyphDone), current, c.Latest)
		if fs.Lookup("check").Value.String() == "true" {
			fmt.Fprintf(stdout, "installed with %s\n", upd.Detect(env.Build))
		}
		o.next("igris doctor")
		return exitOK
	}
	in := c.Install
	fmt.Fprintf(stdout, "igris %s → %s\n%s\ninstalled with %s\n", current, c.Latest, c.URL(), in.Method)
	if in.Method == upd.Archive {
		fmt.Fprintln(stdout, "Download, verify and install it by hand:")
		for _, s := range in.Steps {
			fmt.Fprintf(stdout, "  %s\n", s)
		}
		fmt.Fprintln(stdout, `The ssh-keygen line must print Good "file" signature for igris.`)
		o.next("igris version")
		return exitOK
	}
	fmt.Fprintf(stdout, "update runs: %s\n", in.Command())
	if fs.Lookup("check").Value.String() == "true" {
		o.next("igris update")
		return exitOK
	}
	if fs.Lookup("yes").Value.String() != "true" {
		if !interactiveTerm() {
			return fail("not on a terminal, so igris can't ask; pass --yes to update without asking")
		}
		fmt.Fprint(stdout, "Update? [y/N] ")
		answer, ok := readAnswer(updateStdin)
		fmt.Fprintln(stdout)
		switch {
		case !ok:
			return fail("no answer; nothing changed (pass --yes to update without asking)")
		case answer != "y" && answer != "yes":
			fmt.Fprintln(stdout, "nothing changed")
			o.next("igris update")
			return exitOK
		}
	}
	fmt.Fprintln(stdout, "updating… (this can take a few minutes)")
	cwd, err := os.Getwd()
	if err != nil {
		return fail("%v", err)
	}
	res := project.RunUpdate(ctx, cwd, env, c)
	if res.Err != nil {
		for _, l := range res.Tail {
			fmt.Fprintf(stderr, "  %s\n", l)
		}
		if res.Hint != "" {
			fmt.Fprintln(stderr, res.Hint)
		}
		return fail("%v", res.Err)
	}
	fmt.Fprintln(stdout, textsafe.Line(res.Message))
	fmt.Fprintln(stdout, "Runs going on keep going; their sessions now run the new igris for `igris done`.")
	o.next("igris doctor")
	return exitOK
}
