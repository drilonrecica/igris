package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

// --parallel N takes a whole number from 1 to 8; anything else is a usage
// error, for arise and serve alike (SPEC §14).
func TestParallelFlagUsage(t *testing.T) {
	for _, cmd := range []string{"arise", "serve"} {
		for _, v := range []string{"0", "9", "x", "-1", "2.5", ""} {
			var out, errb bytes.Buffer
			code := run([]string{cmd, "--parallel", v, "A"}, &out, &errb)
			if code != exitUsage || !strings.Contains(errb.String(), "--parallel") {
				t.Errorf("%s --parallel %q: exit %d, stderr %q; want exit 2 naming --parallel", cmd, v, code, errb.String())
			}
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"arise", "--parallel", "9", "--no-tui", "A"}, &out, &errb); code != exitUsage || !strings.Contains(errb.String(), "invalid --parallel 9: use a whole number from 1 to 8") {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
}

func TestParallelFlagValue(t *testing.T) {
	fs := flag.NewFlagSet("arise", flag.ContinueOnError)
	check := runFlags(fs)
	if err := fs.Parse([]string{"--parallel", "3", "A"}); err != nil {
		t.Fatal(err)
	}
	if err := check(fs.Args()); err != nil {
		t.Fatal(err)
	}
	f := runFlagValues(fs, fs.Args())
	if f.parallel != 3 || f.request().Parallel != 3 || f.phase != "A" {
		t.Errorf("flags = %+v, request = %+v", f, f.request())
	}
	fs = flag.NewFlagSet("arise", flag.ContinueOnError)
	runFlags(fs)
	if err := fs.Parse([]string{"A"}); err != nil {
		t.Fatal(err)
	}
	if f := runFlagValues(fs, fs.Args()); f.parallel != 0 || f.request().Parallel != 0 {
		t.Errorf("unset --parallel: flags = %+v", f)
	}
}

// The dry run walks a parallel phase as the scheduler would and numbers the
// tasks that start together as one step (SPEC §14).
func TestDryRunParallelPhase(t *testing.T) {
	withVersions(t, "2.1.304 (Claude Code)\n", "herdr 0.9.1\n")
	writeProject(t, map[string]string{
		"tasks.md": "<!-- igris:format 3 -->\n## A — First\n\n<!-- igris:parallel -->\n\n| ID | Task | Deps | Status | Model | Owner | Group |\n|---|---|---|---|---|---|---|\n" +
			"| A-1 | **One** | — | ready | sonnet | agent | db |\n" +
			"| A-2 | **Two** | — | ready | sonnet | agent | db |\n" +
			"| A-3 | **Three** | — | ready | opus | agent | — |\n" +
			"| A-4 | **Four** | A-1 | blocked | — | user | — |\n",
		"igris.toml": "[run]\nverify = \"false\"\ncommit = \"auto\"\n[parallel]\nmax = 3\n",
	})
	var out, errb bytes.Buffer
	if code := run([]string{"arise", "A", "--dry-run"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	got := out.String()
	want := []string{
		"  1  · A-1  sonnet  sonnet     auto  default  One",
		"  1  · A-3  opus    opus       auto  default  Three",
		"  2  · A-2  sonnet  sonnet     auto  default  Two",
		"  3  ! A-4  —       user task  —     —        Four (waits for you)",
		"  ✓ phase A complete",
		"dry run: 3 sessions, 1 user task",
	}
	last := -1
	for _, w := range want {
		i := strings.Index(got, w)
		if i < 0 || i < last {
			t.Errorf("output lacks %q (in order):\n%s", w, got)
			continue
		}
		last = i
	}
	// --parallel 1 walks it one at a time, each its own step.
	out.Reset()
	if code := run([]string{"arise", "A", "--dry-run", "--parallel", "1"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, errb.String())
	}
	if got := out.String(); !strings.Contains(got, "  1  · A-1 ") || !strings.Contains(got, "  2  · A-2 ") || !strings.Contains(got, "  3  · A-3 ") || !strings.Contains(got, "  4  ! A-4 ") {
		t.Errorf("--parallel 1 output:\n%s", got)
	}
}
