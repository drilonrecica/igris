package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no args prints usage", nil, exitUsage, "", "Usage:"},
		{"help", []string{"--help"}, exitOK, "Commands:", ""},
		{"help word", []string{"help"}, exitOK, "Commands:", ""},
		{"version", []string{"version"}, exitOK, "igris dev", ""},
		{"unknown command", []string{"bogus"}, exitUsage, "", "unknown command"},
		{"init stub", []string{"init"}, exitFail, "", "not implemented"},
		{"check unknown flag", []string{"check", "--nope"}, exitUsage, "", "flag provided but not defined"},
		{"check missing plan", []string{"check", "--plan", "/nonexistent/p.md"}, exitFail, "", "read plan"},
		{"check extra arg", []string{"check", "x"}, exitUsage, "", "unexpected argument"},
		{"phases extra arg", []string{"phases", "x"}, exitUsage, "", "unexpected argument"},
		{"status two phases", []string{"status", "M0", "M1"}, exitUsage, "", "unexpected argument"},
		{"arise stub", []string{"arise", "M0", "--through", "M1", "--mode", "plan", "--no-tui", "--dry-run"}, exitFail, "", "not implemented"},
		{"arise bad mode", []string{"arise", "--mode", "wild"}, exitUsage, "", "invalid --mode"},
		{"done stub", []string{"done", "M0-01", "--note", "ok"}, exitFail, "", "not implemented"},
		{"done flag before id", []string{"done", "--note", "ok", "M0-01"}, exitFail, "", "not implemented"},
		{"done missing id", []string{"done"}, exitUsage, "", "missing ID"},
		{"skip stub", []string{"skip", "M0-01", "--reason", "n/a"}, exitFail, "", "not implemented"},
		{"skip missing reason", []string{"skip", "M0-01"}, exitUsage, "", "--reason is required"},
		{"skip missing id", []string{"skip", "--reason", "x"}, exitUsage, "", "missing ID"},
		{"adapt stub", []string{"adapt", "--model", "opus"}, exitFail, "", "not implemented"},
		{"adapt bad model", []string{"adapt", "--model", "gpt"}, exitUsage, "", "invalid --model"},
		{"subcommand help", []string{"check", "-h"}, exitOK, "", "Usage of igris check"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if got := run(tt.args, &out, &errb); got != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", got, tt.wantCode, errb.String())
			}
			if !strings.Contains(out.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want substring %q", out.String(), tt.wantStdout)
			}
			if !strings.Contains(errb.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", errb.String(), tt.wantStderr)
			}
		})
	}
}

func TestVersionOverride(t *testing.T) {
	old := version
	version = "1.2.3"
	t.Cleanup(func() { version = old })
	var out bytes.Buffer
	run([]string{"version"}, &out, &bytes.Buffer{})
	if out.String() != "igris 1.2.3\n" {
		t.Errorf("got %q", out.String())
	}
}
