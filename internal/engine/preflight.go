package engine

import (
	"context"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
)

// APIKeyVar is the variable that makes Claude Code bill the API instead of
// the owner's subscription login (SPEC §7.4).
const APIKeyVar = "ANTHROPIC_API_KEY" //nolint:gosec // G101: a variable name, not a credential

// APIKeyWarning is the warning when APIKeyVar is set, for every command that
// mentions it (never the value: it is a secret).
const APIKeyWarning = APIKeyVar + " is set: Claude Code would bill the API instead of your subscription login. Unset it unless that is what you want."

// preflightTimeout bounds each git call of Preflight.
const preflightTimeout = 30 * time.Second

// StartWarning is something the owner should know before a run starts
// (SPEC §7.3, §14).
type StartWarning struct {
	Text string
	// Confirm says the run starts only after the owner confirms.
	Confirm bool
}

// Preflight checks the environment of a run in root: ANTHROPIC_API_KEY set
// (needs confirmation), not a git repository, uncommitted changes. It only
// reads; git problems are warnings, never errors.
func Preflight(ctx context.Context, r runner.Runner, root string, getenv func(string) string) []StartWarning {
	var out []StartWarning
	if getenv(APIKeyVar) != "" {
		out = append(out, StartWarning{Text: APIKeyWarning, Confirm: true})
	}
	git := func(args ...string) (string, bool) {
		res, err := r.Run(ctx, runner.Cmd{Name: "git", Args: args, Dir: root, Timeout: preflightTimeout})
		if err != nil || res.ExitCode != 0 {
			return "", false
		}
		return string(res.Stdout), true
	}
	if inside, ok := git("rev-parse", "--is-inside-work-tree"); !ok || strings.TrimSpace(inside) != "true" {
		return append(out, StartWarning{Text: "this is not a git repository: sessions' changes can't be reviewed or undone with git, and commits fail unless commit = \"never\". Run `git init` first."})
	}
	if status, ok := git("status", "--porcelain"); ok && strings.TrimSpace(status) != "" {
		out = append(out, StartWarning{Text: "the working tree has uncommitted changes: the first task's session sees them as its own, and igris's first commit includes them."})
	}
	return out
}
