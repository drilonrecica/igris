package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// The compatibility suite (SPEC §19): what every release wrote keeps
// working. testdata/compat/<tag>/ is captured from the release itself by
// tools/compat.sh (its init config and plan, its examples, the signal files
// its `done` and `skip` wrote); state/ and runs/ hold the state files and
// run logs, shaped after each release's structs.
var compatDir = mustAbs("../../testdata/compat")

// compatProject copies src (a directory of the suite) into a new project
// and makes it the working directory.
func compatProject(t *testing.T, src string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "p")
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	saved := ariseRunner
	t.Cleanup(func() { ariseRunner = saved })
	ariseRunner = &runner.Fake{} // no git, herdr or tmux
	return root
}

// planOnly is a project with just the latest release's example plan: the
// state and run-log tests need a plan, not an old igris.toml.
func planOnly(t *testing.T) string {
	t.Helper()
	root := compatProject(t, filepath.Join(compatDir, "v0.6.0", "examples"))
	if err := os.Remove(filepath.Join(root, "igris.toml")); err != nil {
		t.Fatal(err)
	}
	return root
}

// runJSON runs igris with args, wants exit 0, and checks the output against
// docs/json/<schema>.schema.json, failing on fields it doesn't list.
func runJSON(t *testing.T, schema string, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitOK {
		t.Fatalf("%v: exit %d\n%s%s", args, code, out.String(), errb.String())
	}
	for _, e := range validate(loadSchema(t, schema), out.Bytes(), true) {
		t.Errorf("%v against %s.schema.json: %s", args, schema, e)
	}
	return out.String()
}

func compatReleases(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(compatDir, "v*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no releases in %s: %v", compatDir, err)
	}
	return dirs
}

// Every release's configs and plans load, after `igris config migrate`
// where a key was removed since, and igris reads and runs them.
func TestCompatConfigsAndPlans(t *testing.T) {
	for _, rel := range compatReleases(t) {
		for _, proj := range []string{"init", "examples", "advanced"} {
			src := filepath.Join(rel, proj)
			if _, err := os.Stat(src); err != nil {
				continue // advanced/ is from v0.4 on
			}
			t.Run(filepath.Base(rel)+"/"+proj, func(t *testing.T) {
				root := compatProject(t, src)
				cfgPath := filepath.Join(root, "igris.toml")
				if _, err := config.Load(cfgPath); errors.Is(err, config.ErrRemovedKey) {
					var out, errb bytes.Buffer
					if code := run([]string{"config", "migrate", "--yes"}, &out, &errb); code != exitOK {
						t.Fatalf("config migrate: exit %d\n%s%s", code, out.String(), errb.String())
					}
				}
				if _, err := config.Load(cfgPath); err != nil {
					t.Fatal(err)
				}
				runJSON(t, "check", "check", "--json")
				runJSON(t, "phases", "phases", "--json")
				runJSON(t, "status", "status", "--json")
				var out, errb bytes.Buffer
				if code := run([]string{"arise", "P1", "--dry-run"}, &out, &errb); code != exitOK {
					t.Errorf("arise --dry-run: exit %d\n%s%s", code, out.String(), errb.String())
				}
			})
		}
	}
}

// The plan templates a release shipped (1.2 on) still pass check and a
// dry run of their first phase, each in a project of its own.
func TestCompatTemplates(t *testing.T) {
	for _, rel := range compatReleases(t) {
		plans, _ := filepath.Glob(filepath.Join(rel, "templates", "*", "tasks.md"))
		for _, src := range plans {
			name := filepath.Base(filepath.Dir(src))
			t.Run(filepath.Base(rel)+"/"+name, func(t *testing.T) {
				root := compatProject(t, filepath.Dir(src))
				runJSON(t, "check", "check", "--strict", "--json")
				var out, errb bytes.Buffer
				if code := run([]string{"arise", "M0", "--dry-run"}, &out, &errb); code != exitOK {
					t.Errorf("%s: arise M0 --dry-run: exit %d\n%s%s", root, code, out.String(), errb.String())
				}
			})
		}
	}
}

// The signal files every release's `igris done` and `igris skip` wrote are
// read as done and skip signals.
func TestCompatSignals(t *testing.T) {
	for _, rel := range compatReleases(t) {
		t.Run(filepath.Base(rel), func(t *testing.T) {
			root := compatProject(t, filepath.Join(rel, "init"))
			if err := os.CopyFS(filepath.Join(root, ".igris", "signals"), os.DirFS(filepath.Join(rel, "signals"))); err != nil {
				t.Fatal(err)
			}
			sigs, bad, err := state.PeekSignals(root)
			if err != nil || len(bad) != 0 || len(sigs) != 2 {
				t.Fatalf("signals %+v, unreadable %v, %v", sigs, bad, err)
			}
			got := map[string]string{}
			for _, s := range sigs {
				got[s.ID] = s.Action
				if s.Note != "compat capture" {
					t.Errorf("%s: note %q", s.ID, s.Note)
				}
			}
			if got["P1-02"] != state.ActionDone || got["P1-03"] != state.ActionSkip {
				t.Errorf("signals = %v", got)
			}
		})
	}
}

// Every release's state.json loads (migrated to version 4) and status
// shows its run.
func TestCompatState(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(compatDir, "state", "*.json"))
	if len(files) == 0 {
		t.Fatal("no state files")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			root := planOnly(t)
			data, err := os.ReadFile(f) //nolint:gosec // a fixture in the repo
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, ".igris"), 0o700); err != nil {
				t.Fatal(err)
			}
			//nolint:gosec // a fixture copied into the test's own project
			if err := os.WriteFile(filepath.Join(root, ".igris", "state.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			r, err := state.PeekRun(root)
			if err != nil || r.Version != 4 || (r.Current != nil && (len(r.Active) == 0 || r.Active[0] != r.Current)) {
				t.Fatalf("run %+v, %v", r, err)
			}
			if out := runJSON(t, "status", "status", "--json"); !bytes.Contains([]byte(out), []byte(`"run": {`)) || bytes.Contains([]byte(out), []byte(`"unreadable"`)) {
				t.Errorf("status shows no readable run:\n%s", out)
			}
		})
	}
}

// Every release's run log is read whole by history and report.
func TestCompatRunLogs(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(compatDir, "runs", "*.jsonl"))
	if len(files) == 0 {
		t.Fatal("no run logs")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			root := planOnly(t)
			data, err := os.ReadFile(f) //nolint:gosec // a fixture in the repo
			if err != nil {
				t.Fatal(err)
			}
			writeLog(t, root, data)
			for _, args := range [][]string{{"history", "--json"}, {"report", "--json"}} {
				var doc struct{ Note string }
				if err := json.Unmarshal([]byte(runJSON(t, args[0], args...)), &doc); err != nil || doc.Note != "" {
					t.Errorf("%v: lines were skipped (%q), %v", args, doc.Note, err)
				}
			}
		})
	}
}

// Every release in the CHANGELOG has its directory in the suite: `make
// compat TAG=vX.Y.Z` after tagging (docs/homebrew-tap.md). The newest one is
// let off: its release commit comes before its tag, and the capture needs
// the tag; it is caught by the next release's commit at the latest.
func TestCompatEveryRelease(t *testing.T) {
	data, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	var have []string
	for _, d := range compatReleases(t) {
		have = append(have, filepath.Base(d))
	}
	releases := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`).FindAllStringSubmatch(string(data), -1)
	if len(releases) < 2 {
		t.Fatal("CHANGELOG.md lists no releases")
	}
	for _, m := range releases[1:] {
		if tag := "v" + m[1]; !slices.Contains(have, tag) {
			t.Errorf("no testdata/compat/%s: run `sh tools/compat.sh %s` and commit it", tag, tag)
		}
	}
}
