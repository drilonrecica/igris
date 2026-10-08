package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionGoldens(t *testing.T) {
	for _, shell := range completionShells {
		t.Run(shell, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run([]string{"completion", shell}, &out, &errb); code != exitOK || errb.Len() != 0 {
				t.Fatalf("exit %d, stderr %q", code, errb.String())
			}
			golden := filepath.Join("testdata", "completion", shell+".golden")
			if *update {
				if err := os.WriteFile(golden, out.Bytes(), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden) //nolint:gosec // a golden under testdata/
			if err != nil {
				t.Fatalf("%v; run `go test ./cmd/igris -run TestCompletionGoldens -update`", err)
			}
			if out.String() != string(want) {
				t.Errorf("script changed:\n%s\nwant:\n%s", out.String(), want)
			}
		})
	}
}

// Every command and flag shows up in every script, so a new one can't be
// forgotten.
func TestCompletionCoversCommands(t *testing.T) {
	d := completionData()
	for _, shell := range completionShells {
		s, err := completionScript(shell)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range d.Cmds {
			if !strings.Contains(s, c.Name) {
				t.Errorf("%s: command %s missing", shell, c.Name)
			}
			for _, f := range c.Flags {
				if !strings.Contains(s, f.Name) {
					t.Errorf("%s: flag %s of %s missing", shell, f.Name, c.Name)
				}
			}
		}
		if strings.Contains(d.Words(), "__complete") {
			t.Error("__complete offered as a command")
		}
	}
}

func TestCompletionScriptsParse(t *testing.T) {
	for _, shell := range completionShells {
		bin, err := exec.LookPath(shell)
		if err != nil {
			t.Logf("%s not installed; skipping its syntax check", shell)
			continue
		}
		s, _ := completionScript(shell)
		cmd := exec.Command(bin, "-n") //nolint:gosec // bin is a shell found on PATH
		if shell == "fish" {
			cmd = exec.Command(bin, "--no-execute") //nolint:gosec // bin is a shell found on PATH
		}
		cmd.Stdin = strings.NewReader(s)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s rejects the script: %v\n%s", shell, err, out)
		}
	}
}

func TestCompletionUsage(t *testing.T) {
	for _, args := range [][]string{{"completion"}, {"completion", "powershell"}, {"completion", "bash", "zsh"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitUsage || out.Len() != 0 {
			t.Errorf("%v: exit %d, stdout %q", args, code, out.String())
		}
	}
}

func TestCompleteHiddenFromHelp(t *testing.T) {
	var out, errb bytes.Buffer
	run([]string{"help"}, &out, &errb)
	if strings.Contains(out.String(), "__complete") {
		t.Error("help lists __complete")
	}
}

const completePlan = `# Plan

## M0 — Foundation

| ID | Task | Deps | Status | Model | Owner |
|----|------|------|--------|-------|-------|
| M0-01 | **One** | | ready | sonnet | agent |
| M0-02 | **Two** | M0-01 | blocked | sonnet | agent |

## M1 — More

| ID | Task | Deps | Status | Model | Owner |
|----|------|------|--------|-------|-------|
| M1-01 | **Three** | M0-02 | blocked | opus | agent |
`

func TestComplete(t *testing.T) {
	cases := []struct {
		name, plan string
		args       []string
		want       string
	}{
		{"phases", completePlan, []string{"__complete", "phases"}, "M0\nM1\n"},
		{"tasks", completePlan, []string{"__complete", "tasks"}, "M0-01\nM0-02\nM1-01\n"},
		{"unknown kind", completePlan, []string{"__complete", "bogus"}, ""},
		{"no kind", completePlan, []string{"__complete"}, ""},
		{"invalid plan", completePlan + "| M1-01 | dup | | ready | opus | agent |\n", []string{"__complete", "tasks"}, ""},
		{"not a plan", "hello\n", []string{"__complete", "phases"}, ""},
		{"no plan", "", []string{"__complete", "phases"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if c.plan != "" {
				if err := os.WriteFile("tasks.md", []byte(c.plan), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out, errb bytes.Buffer
			if code := run(c.args, &out, &errb); code != exitOK {
				t.Errorf("exit %d", code)
			}
			if out.String() != c.want || errb.Len() != 0 {
				t.Errorf("stdout %q (want %q), stderr %q", out.String(), c.want, errb.String())
			}
			if _, err := os.Stat(".igris"); err == nil {
				t.Error("__complete created .igris/")
			}
		})
	}
}

func TestCompleteBrokenConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("igris.toml", []byte("plan = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("tasks.md", []byte(completePlan), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"__complete", "phases"}, &out, &errb); code != exitOK || out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

// --only takes a comma-separated list: bash and fish complete the ID after
// the last comma and keep what comes before it (zsh does it with compset).
func TestCompletionOnlyList(t *testing.T) {
	fake := t.TempDir()
	stub := "#!/bin/sh\n[ \"$1 $2\" = \"__complete tasks\" ] && printf 'M1-01\\nM1-02\\nM2-01\\n'\n"
	if err := os.WriteFile(filepath.Join(fake, "igris"), []byte(stub), 0o700); err != nil { //nolint:gosec // an executable test stub
		t.Fatal(err)
	}
	path := fake + string(os.PathListSeparator) + os.Getenv("PATH")
	tests := []struct {
		shell, script string
	}{
		{"bash", `eval "$SCRIPT"; COMP_WORDS=(igris arise --only M1-01,M1-0); COMP_CWORD=3; _igris; printf '%s\n' "${COMPREPLY[@]}"`},
		{"fish", `echo $SCRIPT | source; complete -C 'igris arise --only M1-01,M1-0'`},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			bin, err := exec.LookPath(tt.shell)
			if err != nil {
				t.Skipf("%s not installed", tt.shell)
			}
			s, err := completionScript(tt.shell)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "-c", tt.script) //nolint:gosec // bin is a shell found on PATH
			cmd.Env = append(os.Environ(), "PATH="+path, "SCRIPT="+s)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			var got []string
			for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				got = append(got, strings.Fields(l)[0]) // fish adds a description column
			}
			if strings.Join(got, " ") != "M1-01,M1-01 M1-01,M1-02" {
				t.Errorf("completions = %q", out)
			}
		})
	}
}

// __complete runs reads the run log only: it lists run IDs newest first
// (v0 runs have none) even when the plan is invalid, and nothing without a
// log.
func TestCompleteRuns(t *testing.T) {
	log, err := os.ReadFile(filepath.Join("testdata", "report", "runs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		log  []byte
		plan string
		want string
	}{
		{"log", log, completePlan, "20261002-100000-00ff\n20261001-090000-3fa2\n"},
		{"invalid plan", log, completePlan + "| M1-01 | dup | | ready | opus | agent |\n", "20261002-100000-00ff\n20261001-090000-3fa2\n"},
		{"no plan", log, "", "20261002-100000-00ff\n20261001-090000-3fa2\n"},
		{"empty log", []byte{}, completePlan, ""},
		{"no log", nil, completePlan, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			if c.log != nil {
				writeLog(t, root, c.log)
			}
			if c.plan != "" {
				if err := os.WriteFile("tasks.md", []byte(c.plan), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out, errb bytes.Buffer
			if code := run([]string{"__complete", "runs"}, &out, &errb); code != exitOK {
				t.Errorf("exit %d", code)
			}
			if out.String() != c.want || errb.Len() != 0 {
				t.Errorf("stdout %q (want %q), stderr %q", out.String(), c.want, errb.String())
			}
			if c.log == nil {
				if _, err := os.Stat(".igris"); err == nil {
					t.Error("__complete created .igris/")
				}
				return
			}
			if got, err := os.ReadFile(filepath.Join(".igris", "runs.jsonl")); err != nil || !bytes.Equal(got, c.log) {
				t.Errorf("the run log changed: %v", err)
			}
		})
	}
}

// After `report`, every shell completes run IDs from __complete runs.
func TestCompletionReportRuns(t *testing.T) {
	fake := t.TempDir()
	stub := "#!/bin/sh\n[ \"$1 $2\" = \"__complete runs\" ] && printf '20261002-100000-00ff\\n20261001-090000-3fa2\\n20250101-000000-0000\\n'\n"
	if err := os.WriteFile(filepath.Join(fake, "igris"), []byte(stub), 0o700); err != nil { //nolint:gosec // an executable test stub
		t.Fatal(err)
	}
	path := fake + string(os.PathListSeparator) + os.Getenv("PATH")
	tests := []struct {
		shell, script string
	}{
		{"bash", `eval "$SCRIPT"; COMP_WORDS=(igris report 2026); COMP_CWORD=2; _igris; printf '%s\n' "${COMPREPLY[@]}"`},
		{"fish", `echo $SCRIPT | source; complete -C 'igris report 2026'`},
		// zsh without compinit: compadd stubbed to print the candidates
		// matching the prefix.
		{"zsh", `compdef() { :; }; compadd() { while [[ $1 != -- ]]; do shift; done; shift; print -l -- ${(M)@:#$PREFIX*}; }
eval "$SCRIPT"; words=(igris report 2026); CURRENT=3; PREFIX=2026; _igris`},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			bin, err := exec.LookPath(tt.shell)
			if err != nil {
				t.Skipf("%s not installed", tt.shell)
			}
			s, err := completionScript(tt.shell)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "-c", tt.script) //nolint:gosec // bin is a shell found on PATH
			cmd.Env = append(os.Environ(), "PATH="+path, "SCRIPT="+s)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			var got []string
			for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				got = append(got, strings.Fields(l)[0])
			}
			if strings.Join(got, " ") != "20261002-100000-00ff 20261001-090000-3fa2" {
				t.Errorf("completions = %q", out)
			}
		})
	}
}
