package checks

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

// gitTimeout bounds each git call.
const gitTimeout = 30 * time.Second

// apiKey warns when APIKeyVar is set; a run starts only after the owner
// confirms (SPEC §7.4).
func apiKey(getenv func(string) string) Result {
	if getenv(APIKeyVar) != "" {
		return Result{ID: IDAPIKey, Level: Warn, Message: APIKeyWarning, Next: "unset " + APIKeyVar, Confirm: true}
	}
	return Result{ID: IDAPIKey, Level: OK, Message: APIKeyVar + " is not set"}
}

// git checks that root is a git repository with a clean working tree. It
// only reads; git problems are warnings, never failures.
func git(ctx context.Context, r runner.Runner, root string) Result {
	run := func(args ...string) (string, bool) {
		res, err := r.Run(ctx, runner.Cmd{Name: "git", Args: args, Dir: root, Timeout: gitTimeout})
		if err != nil || res.ExitCode != 0 {
			return "", false
		}
		return string(res.Stdout), true
	}
	if inside, ok := run("rev-parse", "--is-inside-work-tree"); !ok || strings.TrimSpace(inside) != "true" {
		return Result{ID: IDGit, Level: Warn, Next: "git init",
			Message: "this is not a git repository: sessions' changes can't be reviewed or undone with git, and commits fail unless commit = \"never\". Run `git init` first."}
	}
	status, ok := run("status", "--porcelain")
	switch {
	case !ok:
		return Result{ID: IDGit, Level: OK, Message: "git repository"}
	case strings.TrimSpace(status) != "":
		return Result{ID: IDGit, Level: Warn, Next: "git status",
			Message: "the working tree has uncommitted changes: the first task's session sees them as its own, and igris's first commit includes them."}
	}
	return Result{ID: IDGit, Level: OK, Message: "git repository, clean working tree"}
}

// backendAvailable reports whether the backend can host sessions
// (SPEC §11.3).
func backendAvailable(ctx context.Context, b Availability) Result {
	if err := b.Available(ctx); err != nil {
		return Result{ID: IDBackend, Level: Fail, Message: err.Error()}
	}
	if b.Name() == "tmux" {
		return Result{ID: IDBackend, Level: OK, Message: "tmux is running and igris is inside it"}
	}
	return Result{ID: IDBackend, Level: OK, Message: "herdr is running and igris is inside a herdr pane"}
}

// herdrIntegration says whether herdr's Claude Code integration is
// installed (SPEC §11.2). Without it igris reads the agent state from
// Claude Code's hooks (SPEC §6.3), so it is information, not a problem.
func herdrIntegration(ctx context.Context, i Integration) Result {
	if hint := i.IntegrationHint(ctx); hint != "" {
		return Result{ID: IDHerdrIntegration, Level: OK, Message: hint}
	}
	return Result{ID: IDHerdrIntegration, Level: OK, Message: "no problem found with herdr's Claude Code integration"}
}
