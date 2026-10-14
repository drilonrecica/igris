package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Seams for tests: where `igris config migrate` reads its answer, and the
// time its backup is named after.
var (
	configStdin io.Reader = os.Stdin
	configNow             = time.Now
)

// configArgs checks `igris config migrate`.
func configArgs(fs *flag.FlagSet) func([]string) error {
	fs.Bool("yes", false, "apply the changes without asking")
	return func(args []string) error {
		if len(args) == 0 || args[0] != "migrate" {
			return fmt.Errorf("missing subcommand; use `igris config migrate`")
		}
		return atMost(args, 1)
	}
}

// execConfig is `igris config migrate` (SPEC §14): it removes deprecated
// keys from igris.toml after showing them and asking, keeping a backup.
func execConfig(flags *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris config migrate: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail("%v", err)
	}
	root, err := state.FindRoot(cwd)
	if err != nil {
		return fail("%v", err)
	}
	path := filepath.Join(root, state.ConfigFile)
	data, err := os.ReadFile(path) //nolint:gosec // the project's own config
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stdout, "no %s here; nothing to migrate\n", state.ConfigFile)
		return exitOK
	}
	if err != nil {
		return fail("%v", err)
	}
	out, changes, err := config.Migrate(data)
	if err != nil {
		return fail("%v", err)
	}
	if len(changes) == 0 {
		fmt.Fprintf(stdout, "%s: nothing to migrate\n", state.ConfigFile)
		return exitOK
	}
	for _, c := range changes {
		fmt.Fprintf(stdout, "%s:%d: - %s\n    %s: %s\n", state.ConfigFile, c.Line, textsafe.Line(c.Old), c.Key, c.Why)
	}
	if flags.Lookup("yes").Value.String() != "true" {
		fmt.Fprint(stdout, "Apply these changes? [y/N] ")
		answer, ok := readAnswer(configStdin)
		fmt.Fprintln(stdout)
		switch {
		case !ok:
			return fail("no answer; nothing changed (pass --yes to apply without asking)")
		case answer != "y" && answer != "yes":
			fmt.Fprintln(stdout, "nothing changed")
			return exitOK
		}
	}
	backup, err := applyMigration(root, path, data, out)
	if err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(stdout, "migrated %s (backup: %s)\n", state.ConfigFile, backup)
	return exitOK
}

// readAnswer reads one line, lower-cased; ok is false when the input ended
// before one.
func readAnswer(r io.Reader) (answer string, ok bool) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(line)), true
}

// applyMigration writes out over path, which must still hold was, after
// backing was up under .igris/config/. It returns the backup's path,
// relative to root.
func applyMigration(root, path string, was, out []byte) (string, error) {
	now, err := os.ReadFile(path) //nolint:gosec // the project's own config
	if err != nil {
		return "", err
	}
	if !bytes.Equal(now, was) {
		return "", fmt.Errorf("%s changed meanwhile; nothing changed, run it again", state.ConfigFile)
	}
	if _, err := config.Parse(out, state.ConfigFile); err != nil {
		return "", fmt.Errorf("the migrated %s doesn't load (%w); nothing changed", state.ConfigFile, err)
	}
	dir := filepath.Join(root, state.DirName, "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	backup := filepath.Join(dir, "igris."+configNow().UTC().Format("20060102-150405")+".bak.toml")
	if err := os.WriteFile(backup, was, 0o600); err != nil { //nolint:gosec // the project's .igris/config/, a name igris makes
		return "", fmt.Errorf("back up %s: %w", state.ConfigFile, err)
	}
	if err := writeReplacing(path, out); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, backup)
	if err != nil {
		rel = backup
	}
	return rel, nil
}

// writeReplacing replaces path with data atomically, keeping its mode: a
// temporary file in the same directory, synced, then renamed over it.
func writeReplacing(path string, data []byte) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".igris.toml.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(fi.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
