package project

import (
	"context"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/update"
)

// CheckUpdate is `igris update --check` and home's Check for updates (SPEC
// §14, §15.6): it asks GitHub for the latest release and compares it with
// this igris. It is igris's only request to GitHub, and only the owner
// starts it.
func CheckUpdate(ctx context.Context, env Env) (update.Check, error) {
	latest, err := update.Latest(ctx, nil, env.UpdateAPI, "igris/"+env.Build.Version)
	if err != nil {
		return update.Check{}, err
	}
	return env.Build.Compare(latest), nil
}

// RunUpdate runs the install command c found, with igris's environment
// minus the notification secrets' variables of the project cwd belongs to
// (as git gets it, SPEC §16; outside a project, or with an igris.toml that
// doesn't load, nothing more is left out). After a success it asks the
// igris in PATH for its version, which is the one sessions run igris done
// with.
func RunUpdate(ctx context.Context, cwd string, env Env, c update.Check) update.Result {
	var strip []string
	if p, err := Open(cwd, env); err == nil && p.CfgErr == nil {
		strip = p.Cfg.SecretEnvNames()
	}
	r := env.runner()
	res := update.Run(ctx, r, c.Install, runner.Without(env.environ(), strip))
	if res.Err != nil {
		return res
	}
	_, inPath, _ := env.self()()
	v := ""
	if inPath != "" {
		v = checks.IgrisVersion(ctx, r, inPath)
	}
	res.Message = update.Updated(c.Latest, inPath, v)
	return res
}
