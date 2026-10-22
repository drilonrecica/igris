package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// [parallel] (SPEC §12, since 1.5): how many tasks a parallel phase runs
// at once, who runs alone, serial verify, the conflict policy and the
// per-rank caps.
func TestParallel(t *testing.T) {
	c, err := Parse([]byte("[parallel]\nmax = 3\nexclusive_owner_tasks = false\nserial_verify = true\non_conflict = \"ask\"\n\n[parallel.ranks]\nopus = 2\nfable = 8\n"), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	p := c.Parallel
	if p.MaxOrDefault() != 3 || p.ExclusiveOwnerTasksOn() || !p.SerialVerifyOn() || p.OnConflictOrDefault() != OnConflictAsk {
		t.Errorf("parallel = %+v", p)
	}
	if got := p.RankCaps(); !reflect.DeepEqual(got, map[string]int{"opus": 2, "fable": 8}) {
		t.Errorf("rank caps = %v", got)
	}
	d := Default().Parallel
	if d.MaxOrDefault() != 2 || !d.ExclusiveOwnerTasksOn() || d.SerialVerifyOn() || d.OnConflictOrDefault() != OnConflictRerun {
		t.Errorf("defaults = %+v", d)
	}
	// The default caps hold fable to one task; a listed rank replaces its
	// default, the others are added.
	if got := d.RankCaps(); !reflect.DeepEqual(got, map[string]int{"fable": 1}) {
		t.Errorf("default rank caps = %v", got)
	}
	c, err = Parse([]byte("[parallel.ranks]\nopus = 1\n"), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Parallel.RankCaps(); !reflect.DeepEqual(got, map[string]int{"fable": 1, "opus": 1}) {
		t.Errorf("rank caps = %v", got)
	}
}

func TestParallelValidation(t *testing.T) {
	tests := []struct{ src, want string }{
		{"[parallel]\nmax = 0\n", "parallel.max = 0 is out of range; use 1 to 8"},
		{"[parallel]\nmax = 9\n", "parallel.max = 9 is out of range; use 1 to 8"},
		{"[parallel]\non_conflict = \"retry\"\n", "parallel.on_conflict = \"retry\" is invalid; use one of: rerun, ask"},
		{"[parallel.ranks]\nturbo = 1\n", "parallel.ranks.turbo is not a [models] rank; use one of: fable, haiku, opus, sonnet"},
		{"[parallel.ranks]\nopus = 0\n", "parallel.ranks.opus = 0 is out of range; use 1 to 8"},
		{"[parallel.ranks]\nopus = 9\n", "parallel.ranks.opus = 9 is out of range; use 1 to 8"},
	}
	for _, tt := range tests {
		_, err := Parse([]byte(tt.src), "igris.toml")
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: err = %v, want %q", tt.src, err, tt.want)
		}
	}
}

// The [parallel] keys stay out of the hash while at their defaults, set or
// not (SPEC §12), so a run interrupted before 1.5 resumes without the
// config-changed notice, and so does one whose owner wrote the defaults out.
func TestHashWithoutParallelIsUnchanged(t *testing.T) {
	b, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"Parallel"`) {
		t.Errorf("config JSON has Parallel: %s", b)
	}
	path := filepath.Join(t.TempDir(), "igris.toml")
	if err := Write(path, Default()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || strings.Contains(string(data), "parallel") { //nolint:gosec // the test's own temp file
		t.Errorf("written default config has parallel (%v):\n%s", err, data)
	}
	for _, src := range []string{
		"[parallel]\n",
		"[parallel]\nmax = 2\nexclusive_owner_tasks = true\nserial_verify = false\non_conflict = \"rerun\"\n",
		"[parallel.ranks]\nfable = 1\n",
	} {
		c, err := Parse([]byte(src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		if c.Hash() != Default().Hash() {
			t.Errorf("%q changed the hash", src)
		}
	}
	for _, src := range []string{
		"[parallel]\nmax = 3\n", "[parallel]\nexclusive_owner_tasks = false\n", "[parallel]\nserial_verify = true\n",
		"[parallel]\non_conflict = \"ask\"\n", "[parallel.ranks]\nfable = 2\n", "[parallel.ranks]\nopus = 1\n",
	} {
		c, err := Parse([]byte(src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		if c.Hash() == Default().Hash() {
			t.Errorf("%q left the hash unchanged", src)
		}
	}
}
