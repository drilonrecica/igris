package plan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// writeAttempts is how often Update retries when the file changes under it
// (SPEC §4 rule 4).
const writeAttempts = 3

// ErrConcurrentEdit is returned (wrapped) when the plan kept changing on disk
// during every write attempt.
var ErrConcurrentEdit = errors.New("plan changed on disk while igris was writing it")

// UpdateFunc computes the status changes to write from a freshly read and
// valid plan, e.g. func(p *Plan) ([]Change, error) { return p.Sync(id, Done) }.
// It may run more than once, each time on a new parse.
type UpdateFunc func(p *Plan) ([]Change, error)

// Writer changes Status cells of the plan file and nothing else (SPEC §4).
type Writer struct {
	path  string
	opts  Options
	rules Rules

	beforeRename func() // test hook: runs after the temp file is written
}

// NewWriter returns a writer for the plan at path. opts and rules are the
// same as for parsing and validation.
func NewWriter(path string, opts Options, rules Rules) *Writer {
	return &Writer{path: path, opts: opts, rules: rules}
}

// Update re-reads and validates the plan, asks fn for the changes and writes
// them atomically. If the file changes between the read and the rename, the
// write is discarded and retried from the read, up to 3 times. It returns the
// changes written; none means the file was left untouched.
func (w *Writer) Update(ctx context.Context, fn UpdateFunc) ([]Change, error) {
	for range writeAttempts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		changes, retry, err := w.attempt(fn)
		if !retry {
			return changes, err
		}
	}
	return nil, fmt.Errorf("write %s: %w %d times; stop editing it while igris runs, then retry", w.path, ErrConcurrentEdit, writeAttempts)
}

// attempt makes one read-validate-write pass. retry reports that the file
// changed before the rename and nothing was written.
func (w *Writer) attempt(fn UpdateFunc) (changes []Change, retry bool, err error) {
	data, err := os.ReadFile(w.path)
	if err != nil {
		return nil, false, fmt.Errorf("read plan %s: %w", w.path, err)
	}
	sum := sha256.Sum256(data)

	p := Parse(w.path, data, w.opts)
	if err := p.Check(w.rules); err != nil {
		return nil, false, fmt.Errorf("not writing %s: %w", w.path, err)
	}
	changes, err = fn(p)
	if err != nil {
		return nil, false, err
	}
	if len(changes) == 0 {
		return nil, false, nil
	}
	out, err := splice(data, p, changes)
	if err != nil {
		return nil, false, err
	}

	// The file the plan path resolves to is the one replaced, so a plan that
	// is a symlink stays one.
	dst, err := resolve(w.path)
	if err != nil {
		return nil, false, err
	}
	info, err := os.Stat(dst)
	if err != nil {
		return nil, false, fmt.Errorf("stat plan %s: %w", w.path, err)
	}
	tmp, err := writeTemp(dst, out, info.Mode().Perm())
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if tmp != "" {
			_ = os.Remove(tmp) // best effort; the write already failed or was discarded
		}
	}()

	if w.beforeRename != nil {
		w.beforeRename()
	}
	now, err := os.ReadFile(w.path)
	if err != nil {
		return nil, false, fmt.Errorf("re-read plan %s: %w", w.path, err)
	}
	if sha256.Sum256(now) != sum {
		return nil, true, nil
	}
	if err := os.Rename(tmp, dst); err != nil {
		return nil, false, fmt.Errorf("replace plan %s: %w", w.path, err)
	}
	tmp = ""
	syncDir(filepath.Dir(dst))
	return changes, false, nil
}

// resolve follows symlinks in path. Renaming a temp file over path itself
// would replace a symlink with a regular file and leave the file it pointed
// to untouched.
func resolve(path string) (string, error) {
	dst, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve plan %s: %w", path, err)
	}
	return dst, nil
}

// splice returns data with the Status cell of every changed task replaced.
// Cell padding and the backtick style are kept; any suffix after the old
// keyword is dropped. Every other byte is copied unchanged.
func splice(data []byte, p *Plan, changes []Change) ([]byte, error) {
	final := map[string]Status{} // last change per task wins
	for _, c := range changes {
		if p.Task(c.ID) == nil {
			return nil, fmt.Errorf("task %s is no longer in %s; not writing (check whether its row was removed or renamed)", c.ID, p.Path)
		}
		final[c.ID] = c.To
	}
	tasks := make([]*Task, 0, len(final))
	for id := range final {
		tasks = append(tasks, p.Task(id))
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].statusStart < tasks[j].statusStart })

	var b bytes.Buffer
	b.Grow(len(data) + 16*len(tasks))
	pos := 0
	for _, t := range tasks {
		b.Write(data[pos:t.statusStart])
		b.WriteString(statusCell(string(data[t.statusStart:t.statusEnd]), final[t.ID]))
		pos = t.statusEnd
	}
	b.Write(data[pos:])
	return b.Bytes(), nil
}

// statusCell renders s in the style of the old cell content: backticked if
// the old keyword was.
func statusCell(old string, s Status) string {
	if strings.HasPrefix(old, "`") {
		return "`" + s.String() + "`"
	}
	return s.String()
}

// writeTemp writes data to a new temp file next to path and fsyncs it.
func writeTemp(path string, data []byte, perm os.FileMode) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".igris-*")
	if err != nil {
		return "", fmt.Errorf("create temp file for %s: %w", path, err)
	}
	name := f.Name()
	fail := func(err error) (string, error) {
		_ = f.Close()
		_ = os.Remove(name)
		return "", fmt.Errorf("write temp file for %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Chmod(perm); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("write temp file for %s: %w", path, err)
	}
	return name, nil
}

// syncDir fsyncs a directory so a rename in it is durable. It is best
// effort: some file systems don't support syncing directories.
func syncDir(dir string) {
	d, err := os.Open(dir) //nolint:gosec // the plan's own directory
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// Replace swaps the whole plan file at path for data, keeping its file
// mode. It is the one write outside Status cells, for an `igris adapt`
// proposal the owner accepted (SPEC §9). was is the content the owner
// reviewed: if the file no longer holds it, nothing is written and the
// error wraps ErrConcurrentEdit.
func Replace(path string, was, data []byte) error {
	same := func() error {
		now, err := os.ReadFile(path) //nolint:gosec // the owner's plan
		if err != nil {
			return fmt.Errorf("read plan %s: %w", path, err)
		}
		if !bytes.Equal(now, was) {
			return fmt.Errorf("not replacing %s: %w after the review; run `igris adapt` again", path, ErrConcurrentEdit)
		}
		return nil
	}
	if err := same(); err != nil {
		return err
	}
	dst, err := resolve(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(dst)
	if err != nil {
		return fmt.Errorf("stat plan %s: %w", path, err)
	}
	tmp, err := writeTemp(dst, data, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()
	if err := same(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("replace plan %s: %w", path, err)
	}
	tmp = ""
	syncDir(filepath.Dir(dst))
	return nil
}
