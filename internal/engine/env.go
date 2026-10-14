package engine

import (
	"os"
	"slices"
	"strings"
)

// alwaysPassed are passed to verify and task hooks whatever run.env lists:
// sh and nearly every tool need them (SPEC §6.4).
var alwaysPassed = []string{"PATH", "HOME"}

// childEnv is the environment of a command igris runs (SPEC §6.4, §16):
// base (igris's own, "KEY=value" entries) without the variables in strip,
// the ones the notification secrets refer to. With allow not nil, only
// the variables it names are kept, besides PATH and HOME: a name, or a
// prefix ending in "*". Entries without "=" are dropped; the order of base
// is kept.
func childEnv(base, allow, strip []string) []string {
	out := make([]string, 0, len(base))
	for _, kv := range base {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" || slices.Contains(strip, name) {
			continue
		}
		if allow != nil && !allowed(name, allow) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// allowed reports whether run.env (allow) lets name through.
func allowed(name string, allow []string) bool {
	if slices.Contains(alwaysPassed, name) {
		return true
	}
	for _, a := range allow {
		if prefix, ok := strings.CutSuffix(a, "*"); ok {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		} else if a == name {
			return true
		}
	}
	return false
}

// environ is igris's environment (Options.Environ).
func (e *Engine) environ() []string {
	if e.opts.Environ != nil {
		return e.opts.Environ()
	}
	return os.Environ()
}

// commandEnv is the environment of verify and task hooks: igris's own,
// limited by run.env, without the secrets' variables.
func (e *Engine) commandEnv() []string {
	return childEnv(e.environ(), e.cfg.Run.Env, e.secretEnv)
}

// gitEnv is the environment of git: igris's own without the secrets'
// variables. run.env doesn't apply: a commit's hooks and signing need
// variables (SSH_AUTH_SOCK, GPG_TTY, …) no list would think of.
func (e *Engine) gitEnv() []string {
	return childEnv(e.environ(), nil, e.secretEnv)
}
