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

	"github.com/drilonrecica/igris/internal/fsafe"
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

// Writer changes Status cells of the plan's files and nothing else (SPEC
// §4).
type Writer struct {
	path  string
	opts  Options
	rules Rules

	beforeRename func() // test hook: runs after the temp files are written
}

// NewWriter returns a writer for the plan at path. opts and rules are the
// same as for parsing and validation.
func NewWriter(path string, opts Options, rules Rules) *Writer {
	return &Writer{path: path, opts: opts, rules: rules}
}

// Update re-reads and validates the plan with the files it includes, asks
// fn for the changes and writes each Status cell in the file its row is in
// (SPEC §4). If any file of the plan changes between the read and the
// renames, the write is discarded and retried from the read, up to 3 times.
// It returns the changes written; none means every file was left untouched.
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

// pending is a file of the plan with its new content in a temp file.
type pending struct {
	f   *planFile
	dst string // the file the temp file replaces
	tmp string
}

// attempt makes one read-validate-write pass. retry reports that a file of
// the plan changed before the renames and nothing was written.
func (w *Writer) attempt(fn UpdateFunc) (changes []Change, retry bool, err error) {
	data, err := readPlanFile(w.path)
	if err != nil {
		return nil, false, err
	}
	p := parse(w.path, data, w.opts, newIncluder(w.path, len(data), w.opts.Root))
	if err := p.Check(w.rules); err != nil {
		return nil, false, fmt.Errorf("not writing %s: %w", w.path, err)
	}
	sums := make(map[*planFile][sha256.Size]byte, len(p.files))
	for _, f := range p.files {
		sums[f] = sha256.Sum256(f.data)
	}
	changes, err = fn(p)
	if err != nil {
		return nil, false, err
	}
	if len(changes) == 0 {
		return nil, false, nil
	}
	edits, err := splice(p, changes)
	if err != nil {
		return nil, false, err
	}

	var writes []*pending
	defer func() {
		for _, pw := range writes {
			if pw.tmp != "" {
				_ = os.Remove(pw.tmp) // best effort; the write already failed or was discarded
			}
		}
	}()
	for _, e := range edits {
		if bytes.Equal(e.out, e.f.data) {
			continue
		}
		pw := &pending{f: e.f, dst: e.f.real}
		if e.f == p.files[0] {
			// The file the plan path resolves to is the one replaced, so
			// a plan that is a symlink stays one. An included file is
			// replaced at the path it was read from, resolved the same way.
			if pw.dst, err = resolve(w.path); err != nil {
				return nil, false, err
			}
		}
		info, err := os.Stat(pw.dst)
		if err != nil {
			return nil, false, fmt.Errorf("stat plan %s: %w", e.f.name, err)
		}
		if pw.tmp, err = writeTemp(pw.dst, e.out, info.Mode().Perm()); err != nil {
			return nil, false, err
		}
		writes = append(writes, pw)
	}
	if len(writes) == 0 {
		return changes, false, nil
	}

	if w.beforeRename != nil {
		w.beforeRename()
	}
	switch same, err := w.unchanged(p, sums); {
	case err != nil:
		return nil, false, err
	case !same:
		return nil, true, nil
	}
	for _, pw := range writes {
		if err := os.Rename(pw.tmp, pw.dst); err != nil {
			return nil, false, fmt.Errorf("replace plan %s: %w", pw.f.name, err)
		}
		pw.tmp = ""
		syncDir(filepath.Dir(pw.dst))
	}
	return changes, false, nil
}

// unchanged reports whether every file of p still holds what was read: the
// main plan, and each included file at the path its include resolves to.
// An included file that can no longer be read counts as changed, so the
// retry's validation reports it.
func (w *Writer) unchanged(p *Plan, sums map[*planFile][sha256.Size]byte) (bool, error) {
	now, err := readPlanFile(w.path)
	if err != nil {
		return false, fmt.Errorf("re-read: %w", err)
	}
	if sha256.Sum256(now) != sums[p.files[0]] {
		return false, nil
	}
	for _, f := range p.files[1:] {
		real, err := filepath.Abs(f.name)
		if err == nil {
			real, err = filepath.EvalSymlinks(real)
		}
		if err != nil || real != f.real {
			return false, nil
		}
		data, err := fsafe.ReadFile(real, MaxSize, false)
		if err != nil || sha256.Sum256(data) != sums[f] {
			return false, nil
		}
	}
	return true, nil
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

// edit is the new content of one file of the plan.
type edit struct {
	f   *planFile
	out []byte
}

// splice returns, in plan order, each file of p holding a changed task with
// the Status cell of every changed task in it replaced (SPEC §4 rule 7).
// Cell padding and the backtick style are kept; any suffix after the old
// keyword is dropped. Every other byte is copied unchanged.
func splice(p *Plan, changes []Change) ([]edit, error) {
	final := map[string]Status{} // last change per task wins
	byFile := map[*planFile][]*Task{}
	for _, c := range changes {
		t := p.Task(c.ID)
		if t == nil {
			return nil, fmt.Errorf("task %s is no longer in %s; not writing (check whether its row was removed or renamed)", c.ID, p.Path)
		}
		if _, seen := final[c.ID]; !seen {
			byFile[t.Phase.file] = append(byFile[t.Phase.file], t)
		}
		final[c.ID] = c.To
	}
	var edits []edit
	for _, f := range p.files {
		if tasks := byFile[f]; len(tasks) > 0 {
			edits = append(edits, edit{f: f, out: spliceFile(f.data, tasks, final)})
		}
	}
	return edits, nil
}

// spliceFile returns data, one file of the plan, with the Status cell of
// each of tasks (all in that file) set to its final status.
func spliceFile(data []byte, tasks []*Task, final map[string]Status) []byte {
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
	return b.Bytes()
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
