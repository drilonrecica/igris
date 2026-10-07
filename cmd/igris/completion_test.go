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
