package engine

import (
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/runner"
)

func TestChildEnv(t *testing.T) {
	base := []string{"PATH=/bin", "HOME=/h", "GOPATH=/go", "LC_ALL=C", "LCX=1", "NTFY_T=s3cret", "AWS_SECRET=x", "BROKEN", "=nameless", "EMPTY="}
	cases := []struct {
		name         string
		allow, strip []string
		want         string
	}{
		{"inherit", nil, nil, "PATH=/bin HOME=/h GOPATH=/go LC_ALL=C LCX=1 NTFY_T=s3cret AWS_SECRET=x EMPTY="},
		{"inherit without the secrets", nil, []string{"NTFY_T"}, "PATH=/bin HOME=/h GOPATH=/go LC_ALL=C LCX=1 AWS_SECRET=x EMPTY="},
		{"allowlist: PATH and HOME always", []string{"GOPATH"}, nil, "PATH=/bin HOME=/h GOPATH=/go"},
		{"prefix", []string{"LC_*"}, nil, "PATH=/bin HOME=/h LC_ALL=C"},
		{"a secret is stripped even when listed", []string{"NTFY_T", "EMPTY"}, []string{"NTFY_T"}, "PATH=/bin HOME=/h EMPTY="},
		{"PATH can be stripped too", nil, []string{"PATH"}, "HOME=/h GOPATH=/go LC_ALL=C LCX=1 NTFY_T=s3cret AWS_SECRET=x EMPTY="},
	}
	for _, c := range cases {
		if got := strings.Join(childEnv(base, c.allow, c.strip), " "); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// envOf returns the variable names of a command's environment.
func envOf(c runner.Cmd) []string {
	var names []string
	for _, kv := range c.Env {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	return names
}

const envBase = "PATH=/bin HOME=/h GOPATH=/go NTFY_T=s3cret AWS_SECRET=x"

func withEnviron(o *Options) { o.Environ = func() []string { return strings.Fields(envBase) } }

// Verify, task hooks and git never get the variables the notification
// secrets refer to; run.env limits verify and hooks, never git (SPEC §6.4,
// §16).
func TestCommandEnv(t *testing.T) {
	const ntfy = "[notify.ntfy]\ntopic = \"t\"\ntoken = \"env:NTFY_T\"\n"
	for _, tt := range []struct {
		name, toml        string
		verify, hook, git string
	}{
		{"no allowlist", "", "PATH HOME GOPATH AWS_SECRET", "PATH HOME GOPATH AWS_SECRET IGRIS_TASK_ID IGRIS_PHASE IGRIS_RANK IGRIS_MODEL", "PATH HOME GOPATH AWS_SECRET"},
		{"allowlist", "env = [\"GOPATH\"]\n", "PATH HOME GOPATH", "PATH HOME GOPATH IGRIS_TASK_ID IGRIS_PHASE IGRIS_RANK IGRIS_MODEL", "PATH HOME GOPATH AWS_SECRET"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, chainPlan, "[run]\ncommit = \"auto\"\nverify = \"make check\"\n"+tt.toml+"[hooks]\nbefore_task = [\"./prep\"]\n"+ntfy)
			h.dirtyTree()
			var verify, hook []runner.Cmd
			h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
				if c.Name == "sh" {
					verify = append(verify, c)
				} else {
					hook = append(hook, c)
				}
				return runner.Result{}, nil
			}
			if _, err := h.run(withEnviron); err != nil {
				t.Fatalf("Run: %v", err)
			}
			git := h.calls("git")
			if len(verify) == 0 || len(hook) == 0 || len(git) == 0 {
				t.Fatalf("%d verify, %d hook, %d git commands", len(verify), len(hook), len(git))
			}
			for what, cmds := range map[string][]runner.Cmd{"verify": verify, "hook": hook, "git": git} {
				want := map[string]string{"verify": tt.verify, "hook": tt.hook, "git": tt.git}[what]
				for _, c := range cmds {
					if got := strings.Join(envOf(c), " "); got != want {
						t.Errorf("%s env = %s, want %s", what, got, want)
					}
					if slices.ContainsFunc(c.Env, func(kv string) bool { return strings.Contains(kv, "s3cret") }) {
						t.Errorf("%s got the secret", what)
					}
				}
			}
		})
	}
}
