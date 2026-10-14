package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const oldConfig = `# my project
plan = "tasks.md"

[claude]
command = "claude"  # from igris init
extra_args = []
`

// configMigrate runs `igris config migrate args…` in a project whose
// igris.toml is cfg ("" for none), answering stdin.
func configMigrate(t *testing.T, cfg, stdin string, args ...string) (code int, out, errs, root string) {
	t.Helper()
	root = t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".igris"), 0o700); err != nil {
		t.Fatal(err)
	}
	if cfg != "" {
		//nolint:gosec // 0640 on purpose: migrate must keep the file's mode
		if err := os.WriteFile(filepath.Join(root, "igris.toml"), []byte(cfg), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	savedIn, savedNow := configStdin, configNow
	t.Cleanup(func() { configStdin, configNow = savedIn, savedNow })
	configStdin = strings.NewReader(stdin)
	configNow = func() time.Time { return time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC) }
	var o, e bytes.Buffer
	code = run(append([]string{"config", "migrate"}, args...), &o, &e)
	return code, o.String(), e.String(), root
}

func readConfig(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "igris.toml")) //nolint:gosec // the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// `igris config migrate` shows the deprecated lines, asks, and removes only
// them, keeping a backup and the file's mode (SPEC §14).
func TestConfigMigrate(t *testing.T) {
	migrated := strings.Replace(oldConfig, "command = \"claude\"  # from igris init\n", "", 1)
	backup := filepath.Join(".igris", "config", "igris.20261014-093000.bak.toml")
	for _, tc := range []struct {
		name, stdin string
		args        []string
	}{
		{"yes flag", "", []string{"--yes"}},
		{"answer y", "y\n", nil},
		{"answer YES", "YES\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errs, root := configMigrate(t, oldConfig, tc.stdin, tc.args...)
			if code != exitOK {
				t.Fatalf("exit %d: %s%s", code, out, errs)
			}
			for _, want := range []string{`igris.toml:5: - command = "claude"  # from igris init`, "claude.command: deprecated in 0.1.2", "migrated igris.toml (backup: " + backup + ")"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if got := readConfig(t, root); got != migrated {
				t.Errorf("igris.toml = %q\nwant %q", got, migrated)
			}
			if data, err := os.ReadFile(filepath.Join(root, backup)); err != nil || string(data) != oldConfig { //nolint:gosec // test temp dir
				t.Errorf("backup = %q, %v", data, err)
			}
			if fi, err := os.Stat(filepath.Join(root, "igris.toml")); err != nil || fi.Mode().Perm() != 0o640 {
				t.Errorf("mode = %v, %v; want 0640 kept", fi.Mode(), err)
			}
		})
	}
}

// Nothing to do, a declined or missing answer, and what migrate can't
// handle leave igris.toml as it is.
func TestConfigMigrateLeavesTheFile(t *testing.T) {
	for _, tc := range []struct {
		name, cfg, stdin string
		code             int
		want             string
	}{
		{"declined", oldConfig, "n\n", exitOK, "nothing changed"},
		{"empty answer", oldConfig, "\n", exitOK, "nothing changed"},
		{"no answer", oldConfig, "", exitFail, "no answer; nothing changed (pass --yes to apply without asking)"},
		{"nothing to migrate", "plan = \"tasks.md\"\n", "", exitOK, "igris.toml: nothing to migrate"},
		{"inline table", "claude = { command = \"x\" }\n", "y\n", exitFail, "can't remove claude.command automatically"},
		{"no igris.toml", "", "", exitOK, "no igris.toml here; nothing to migrate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errs, root := configMigrate(t, tc.cfg, tc.stdin)
			if code != tc.code || !strings.Contains(out+errs, tc.want) {
				t.Errorf("exit %d, want %d; output:\n%s%s", code, tc.code, out, errs)
			}
			if tc.cfg != "" && readConfig(t, root) != tc.cfg {
				t.Error("igris.toml changed")
			}
			if _, err := os.Stat(filepath.Join(root, ".igris", "config")); err == nil {
				t.Error("a backup was written")
			}
		})
	}
}

func TestConfigMigrateOutsideAProject(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"config", "migrate", "--yes"}, &out, &errb); code != exitFail || !strings.Contains(errb.String(), "igris init") {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
}

func TestConfigUsage(t *testing.T) {
	for _, args := range [][]string{{"config"}, {"config", "nope"}, {"config", "migrate", "x"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
