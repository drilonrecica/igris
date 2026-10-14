package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// Migrate drops the deprecated keys' lines and keeps every other byte
// (SPEC §12); what it can't remove by dropping a line is left to the owner.
func TestMigrate(t *testing.T) {
	const handWritten = `# my project
plan = "tasks.md"   # the plan

[claude]
# how claude starts
command = "claude"  # old
extra_args = ["--verbose"]

[run]
commit = "ask"
`
	tests := []struct {
		name, in, want string
		line           int    // the removed line; 0 for no change
		err            string // part of the error; "" for none
	}{
		{name: "hand written", in: handWritten, line: 6,
			want: strings.Replace(handWritten, "command = \"claude\"  # old\n", "", 1)},
		{name: "dotted at the root", in: "plan = \"t.md\"\nclaude.command = \"x\"\n[run]\ncommit = \"ask\"\n", line: 2,
			want: "plan = \"t.md\"\n[run]\ncommit = \"ask\"\n"},
		{name: "spaced dotted", in: "claude . command = \"x\"\n", line: 1, want: ""},
		{name: "quoted key", in: "[claude]\n\"command\" = \"x\"\n", line: 2, want: "[claude]\n"},
		{name: "quoted table", in: "[ \"claude\" ]\ncommand = 'x'\n", line: 2, want: "[ \"claude\" ]\n"},
		{name: "CRLF and BOM", in: "\uFEFF[claude]\r\ncommand = \"x\"\r\nextra_args = []\r\n", line: 2,
			want: "\uFEFF[claude]\r\nextra_args = []\r\n"},
		{name: "BOM on the removed line", in: "\uFEFFclaude.command = \"x\"\nplan = \"t.md\"\n", line: 1,
			want: "\uFEFFplan = \"t.md\"\n"},
		{name: "no trailing newline", in: "[claude]\ncommand = \"x\"", line: 2, want: "[claude]\n"},
		{name: "in a multi-line string", in: "[run]\ncommit_message = \"\"\"\ncommand = \"x\"\n\"\"\"\n[claude]\ncommand = \"y\"\n", line: 6,
			want: "[run]\ncommit_message = \"\"\"\ncommand = \"x\"\n\"\"\"\n[claude]\n"},
		{name: "other table's command", in: "[hooks]\nbefore_task = \"x\"\n", want: "[hooks]\nbefore_task = \"x\"\n"},
		{name: "none", in: "plan = \"t.md\"\n", want: "plan = \"t.md\"\n"},
		{name: "inline table", in: "claude = { command = \"x\", extra_args = [] }\n", err: "can't remove claude.command automatically"},
		{name: "multi-line value", in: "[claude]\ncommand = \"\"\"\nx\"\"\"\n", err: "can't remove claude.command automatically (line 2); delete it by hand"},
		{name: "not TOML", in: "[claude\n", err: "igris.toml:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, changes, err := Migrate([]byte(tt.in))
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != tt.want {
				t.Errorf("out = %q\nwant %q", out, tt.want)
			}
			switch {
			case tt.line == 0 && len(changes) != 0:
				t.Errorf("changes = %+v, want none", changes)
			case tt.line != 0 && (len(changes) != 1 || changes[0].Line != tt.line || changes[0].Key != "claude.command" || !strings.Contains(changes[0].Why, "0.1.2")):
				t.Errorf("changes = %+v, want line %d", changes, tt.line)
			}
			var m map[string]any
			md, err := toml.Decode(string(out), &m)
			if err != nil || md.IsDefined("claude", "command") {
				t.Errorf("result: %v, claude.command defined %v", err, md.IsDefined("claude", "command"))
			}
		})
	}
}

// Every config `igris init` wrote has claude.command = "claude": migrate
// removes that line and nothing else, and the result loads to the same
// config.
func TestMigrateInitConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "igris.toml")
	if err := Write(path, Default()); err != nil {
		t.Fatal(err)
	}
	in, err := os.ReadFile(path) //nolint:gosec // the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(in), "command = \"claude\"") {
		t.Skip("config.Write no longer writes claude.command")
	}
	out, changes, err := Migrate(in)
	if err != nil || len(changes) != 1 {
		t.Fatalf("changes %+v, err %v", changes, err)
	}
	if strings.Count(string(in), "\n")-strings.Count(string(out), "\n") != 1 {
		t.Errorf("more than one line removed:\n%s", out)
	}
	c, err := Parse(out, "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Hash() != Default().Hash() {
		t.Error("the migrated config hashes differently")
	}
}
