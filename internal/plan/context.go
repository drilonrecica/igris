package plan

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/textsafe"
)

// MaxContext is how many paths a Context cell may list (SPEC §3.2).
const MaxContext = 20

// checkContext reports the problems of t's Context paths (SPEC §3.2): too
// many, absolute, with a ".." element, the whole project, missing, a file
// with a trailing slash, or leading outside the root once symlinks are
// resolved. igris only names the paths in the
// prompt; it never reads the files.
func (v *validator) checkContext(at int, name string, t *Task) {
	if len(t.Context) > MaxContext {
		v.add(at, "%s: Context lists %d paths, more than %d; name only the files and directories the task must read", name, len(t.Context), MaxContext)
	}
	for _, e := range t.Context {
		if textsafe.HasControl(e) {
			continue // the row is reported for it already (SPEC §3.5)
		}
		if msg := v.contextProblem(e); msg != "" {
			v.add(at, "%s: Context %q %s", name, e, msg)
		}
	}
}

// contextProblem says what is wrong with one Context entry, "" if nothing.
func (v *validator) contextProblem(e string) string {
	if strings.HasPrefix(e, "/") || filepath.IsAbs(e) {
		return "is an absolute path; use a path relative to the project root"
	}
	for _, el := range strings.Split(e, "/") {
		if el == ".." {
			return "leaves the project: paths must stay inside the project; use a repo-relative path"
		}
	}
	if path.Clean(e) == "." {
		return "names the whole project; list the files or directories to read"
	}
	root, err := v.realRoot()
	if err != nil {
		return fmt.Sprintf("can't be checked: resolve the project root: %v", err)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(e)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "does not exist; fix the path or remove it from the cell"
	case err != nil:
		return fmt.Sprintf("can't be checked: %v", err)
	}
	if rel, err := filepath.Rel(root, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "leads outside the project through a symlink; paths must stay inside the project; use a repo-relative path"
	}
	// filepath.Join drops a trailing slash, so "SPEC.md/" resolved fine.
	if strings.HasSuffix(e, "/") {
		if fi, err := os.Stat(real); err == nil && !fi.IsDir() { //nolint:gosec // real is checked to be inside the root above
			return "is a file, not a directory; drop the trailing /"
		}
	}
	return ""
}

// realRoot is the root Context paths are resolved against, absolute and
// with symlinks resolved, computed once per validation. Abs comes first:
// for "." it reads $PWD, which may name a symlink.
func (v *validator) realRoot() (string, error) {
	if v.root == "" && v.rootErr == nil {
		root := v.rules.Root
		if root == "" {
			root = "."
		}
		r, err := filepath.Abs(root)
		if err == nil {
			r, err = filepath.EvalSymlinks(r)
		}
		v.root, v.rootErr = r, err
	}
	return v.root, v.rootErr
}
