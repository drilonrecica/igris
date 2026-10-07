package project

import (
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/tui"
)

// ErrNoEditor says neither $VISUAL nor $EDITOR is set. igris then asks;
// it never falls back to vi on its own (SPEC §15.6).
var ErrNoEditor = tui.ErrNoEditor

// EditorArgv is the owner's editor command for path (SPEC §16): $VISUAL,
// else $EDITOR, split with strings.Fields (no shell, so quoted arguments
// don't work), with path made absolute as the last argument.
func EditorArgv(getenv func(string) string, path string) ([]string, error) {
	argv := strings.Fields(getenv("VISUAL"))
	if len(argv) == 0 {
		argv = strings.Fields(getenv("EDITOR"))
	}
	if len(argv) == 0 {
		return nil, ErrNoEditor
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return append(argv, abs), nil
}

// viArgv is vi on path, the editor home offers when neither $VISUAL nor
// $EDITOR is set; nil when lookPath doesn't find vi.
func viArgv(lookPath func(string) (string, error), path string) []string {
	vi, err := lookPath("vi")
	if err != nil {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	return []string{vi, abs}
}

// editorCmd runs the editor as a tea.ExecCommand. It is the one process
// igris starts without a timeout: it is interactive and the owner's own
// (SPEC §16).
type editorCmd struct{ cmd *exec.Cmd }

func newEditorCmd(argv []string) *editorCmd {
	return &editorCmd{cmd: exec.Command(argv[0], argv[1:]...)} //nolint:gosec,noctx // the owner's own $VISUAL/$EDITOR, argv only; no timeout by design
}

func (e *editorCmd) Run() error            { return e.cmd.Run() }
func (e *editorCmd) SetStdin(r io.Reader)  { e.cmd.Stdin = r }
func (e *editorCmd) SetStdout(w io.Writer) { e.cmd.Stdout = w }
func (e *editorCmd) SetStderr(w io.Writer) { e.cmd.Stderr = w }
