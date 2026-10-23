package update

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// CommandTimeout bounds the install command: a `go install` builds igris
// from source.
const CommandTimeout = 10 * time.Minute

// tailLines is how much of a failed command's output is shown.
const tailLines = 10

// Result is how the install command went.
type Result struct {
	// Err is nil when the command exited 0.
	Err error
	// Tail is the end of the command's output, cleaned (textsafe), one
	// line each; shown when it failed.
	Tail []string
	// Hint says what may fix a failure; "" when igris knows nothing.
	Hint string
	// Message is what igris says after a success (Updated).
	Message string
}

// Run runs in's command through r as argv, with env (igris's environment
// without the notification secrets' variables, SPEC §16) plus in.Env, and
// CommandTimeout.
func Run(ctx context.Context, r runner.Runner, in Install, env []string) Result {
	if len(in.Argv) == 0 {
		return Result{Err: errors.New("igris can't tell how it was installed, so it runs nothing; follow the steps")}
	}
	var names []string
	for _, kv := range in.Env {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	cmd := runner.Cmd{
		Name:     in.Argv[0],
		Args:     in.Argv[1:],
		Env:      append(runner.Without(env, names), in.Env...),
		Timeout:  CommandTimeout,
		Combined: true,
	}
	res, err := r.Run(ctx, cmd)
	out := Result{Tail: tail(string(res.Stdout))}
	switch {
	case err != nil:
		out.Err = err
	case res.ExitCode != 0:
		out.Err = fmt.Errorf("%s exited with status %d", in.Command(), res.ExitCode)
	default:
		return Result{}
	}
	if in.Method == GoInstall && strings.Contains(string(res.Stdout), "unknown revision") {
		out.Hint = "The Go checksum database can lag a new tag by about 30 minutes. Try again later, or now with: GOPROXY=direct GONOSUMDB=" + Module + " " + in.Command()
	}
	return out
}

// tail is the last tailLines non-empty lines of out, cleaned.
func tail(out string) []string {
	var lines []string
	for _, l := range strings.Split(textsafe.Clean(out), "\n") {
		if l = strings.TrimRight(l, " \t"); strings.TrimSpace(l) != "" {
			lines = append(lines, strings.ReplaceAll(l, "\t", "    "))
		}
	}
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	return lines
}

// Updated is what igris says after a successful update: what `igris
// version` of the igris in PATH printed (inPath "" when none is in PATH,
// pathVersion "" when it couldn't be read), against the release installed.
func Updated(latest, inPath, pathVersion string) string {
	switch {
	case inPath == "":
		return "Updated to " + latest + ", but igris is not in PATH: put it in PATH so sessions can run `igris done`."
	case pathVersion == "":
		return "Updated to " + latest + ", but " + inPath + " didn't print its version: run `igris version`."
	case Compare(pathVersion, latest) != 0:
		return "Updated to " + latest + ", but igris in PATH (" + inPath + ") is " + pathVersion + ": put the updated igris first in PATH."
	}
	return "Updated: igris in PATH is now " + pathVersion + "."
}
