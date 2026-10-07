package checks

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
)

// Tool is an external program igris relies on and the versions it was
// verified with (SPEC §11.4).
type Tool struct {
	Name    string // shown to the owner
	Program string // run as `Program --version`; also the check's ID
	// VersionArgs replaces --version (tmux prints its version with -V).
	VersionArgs []string
	// Min is the oldest version verified to work; older ones get a warning.
	Min string
	// Tested is the newest version re-verified with docs/reverify.md; a
	// newer major version gets a warning.
	Tested string
}

// Tools is the one table of verified versions. Update it (and SPEC §11.4)
// after running docs/reverify.md.
var Tools = []Tool{
	{Name: "Claude Code", Program: IDClaude, Min: "2.1.291", Tested: "2.1.292"},             // P0-02; re-verified for v0.1.2
	{Name: "herdr", Program: IDHerdr, Min: "0.9.1", Tested: "0.9.1"},                        // P0-03; re-verified for v0.1.2
	{Name: "tmux", Program: IDTmux, VersionArgs: []string{"-V"}, Min: "3.2", Tested: "3.7"}, // V03-P2 (3.7c)
}

// versionCommand is how t prints its version, e.g. "tmux -V".
func (t Tool) versionCommand() (args []string, shown string) {
	args = t.VersionArgs
	if args == nil {
		args = []string{"--version"}
	}
	return args, t.Program + " " + strings.Join(args, " ")
}

// versionTimeout bounds each `--version` call of ToolVersions.
const versionTimeout = 5 * time.Second

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// parseVersion finds the first dotted version in out, leniently: prefixes
// ("herdr 0.9.1"), suffixes ("2.1.292 (Claude Code)", "-beta") and a
// missing patch number are fine.
func parseVersion(out string) ([3]int, bool) {
	var v [3]int
	m := versionRE.FindStringSubmatch(out)
	if m == nil {
		return v, false
	}
	for i, s := range m[1:] {
		if s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func compareVersions(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func formatVersion(v [3]int) string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

// ToolVersions runs `--version` of every tool in Tools, one result each. It
// warns when a tool is missing, its version can't be read, it is older than
// the oldest verified version, or it is a newer major version than the
// newest one (SPEC §11.4). It is a warning only: igris never refuses to run
// on a version.
func ToolVersions(ctx context.Context, r runner.Runner) []Result {
	out := make([]Result, 0, len(Tools))
	for _, t := range Tools {
		out = append(out, checkTool(ctx, r, t))
	}
	return out
}

func checkTool(ctx context.Context, r runner.Runner, t Tool) Result {
	warn := func(msg string) Result { return Result{ID: t.Program, Level: Warn, Message: msg} }
	args, shown := t.versionCommand()
	res, err := r.Run(ctx, runner.Cmd{Name: t.Program, Args: args, Timeout: versionTimeout})
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return warn(fmt.Sprintf("%s not found in PATH; igris arise needs %s %s or later", t.Program, t.Name, t.Min))
	case errors.Is(err, runner.ErrTimeout):
		return warn(unknownVersion(t, fmt.Sprintf("`%s` took longer than %s", shown, versionTimeout)))
	case err != nil:
		return warn(unknownVersion(t, err.Error()))
	case res.ExitCode != 0:
		return warn(unknownVersion(t, fmt.Sprintf("`%s` exited with %d", shown, res.ExitCode)))
	}
	v, ok := parseVersion(string(res.Stdout))
	if !ok {
		return warn(unknownVersion(t, fmt.Sprintf("no version in the output of `%s`", shown)))
	}
	minV, _ := parseVersion(t.Min)
	tested, _ := parseVersion(t.Tested)
	switch {
	case compareVersions(v, minV) < 0:
		return warn(fmt.Sprintf("%s %s is older than %s, the oldest version igris is verified with; update %s", t.Name, formatVersion(v), t.Min, t.Program))
	case v[0] > tested[0]:
		return warn(fmt.Sprintf("%s %s is a newer major version than igris was verified with (%s); if something breaks, report it with the output of `igris version` and `%s`", t.Name, formatVersion(v), t.Tested, shown))
	}
	return Result{ID: t.Program, Level: OK, Message: fmt.Sprintf("%s %s", t.Name, formatVersion(v))}
}

func unknownVersion(t Tool, why string) string {
	return fmt.Sprintf("couldn't determine the %s version (%s); igris is verified with %s %s to %s", t.Name, why, t.Name, t.Min, t.Tested)
}
