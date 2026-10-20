package plan

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/drilonrecica/igris/internal/fsafe"
)

// MaxFiles is how many files a plan may have, the main plan included
// (SPEC §3.7).
const MaxFiles = 32

// includer reads the files a main plan includes (SPEC §3.7): each a .md
// file inside the root once symlinks are resolved, not the main plan, not
// under .igris/, included once, read as a regular file without blocking,
// all files together at most MaxSize.
type includer struct {
	root     string // Options.Root
	mainReal string // the main plan, absolute with symlinks resolved; "" if that failed
	total    int64  // bytes read so far, the main plan's included

	rootReal string
	rootErr  error
	rootDone bool
	seen     map[string]include // resolved path -> the include that read it
}

func newIncluder(mainPath string, mainSize int, root string) *includer {
	inc := &includer{root: root, total: int64(mainSize), seen: map[string]include{}}
	if abs, err := filepath.Abs(mainPath); err == nil {
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			inc.mainReal = real
		}
	}
	return inc
}

// read checks and reads the file inc names. On a problem it returns the
// message to report at the include line.
func (r *includer) read(p *Plan, inc *include) (*planFile, string) {
	path := inc.path
	switch {
	case path == "":
		return nil, "the include names no file; write <!-- igris:include phases/v2.md --> with a path to a .md file"
	case !strings.HasSuffix(path, ".md"):
		return nil, fmt.Sprintf("include %q is not a .md file; include only markdown plan files", path)
	case strings.HasPrefix(path, "/") || filepath.IsAbs(path):
		return nil, fmt.Sprintf("include %q is an absolute path; use a path relative to the directory of %s", path, p.Path)
	case strings.HasPrefix(path, "~"):
		return nil, fmt.Sprintf("include %q starts with ~; use a path relative to the directory of %s", path, p.Path)
	case hasDotDot(path):
		return nil, fmt.Sprintf("include %q has a .. element; included files must stay inside the project", path)
	case len(p.files) >= MaxFiles:
		return nil, fmt.Sprintf("include %q is one file too many: a plan has at most %d files, the main plan included", path, MaxFiles)
	}
	root, err := r.realRoot()
	if err != nil {
		return nil, fmt.Sprintf("include %q can't be checked: resolve the project root: %v", path, err)
	}
	real, err := filepath.Abs(inc.name)
	if err == nil {
		real, err = filepath.EvalSymlinks(real)
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Sprintf("included file %s does not exist; fix the path or remove the include", inc.name)
	case err != nil:
		return nil, fmt.Sprintf("include %q can't be checked: %v", path, err)
	}
	rel, err := filepath.Rel(root, real)
	switch {
	case err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)):
		return nil, fmt.Sprintf("include %q leads outside the project through a symlink; included files must stay inside it", path)
	case rel == ".igris" || strings.HasPrefix(rel, ".igris"+string(filepath.Separator)):
		return nil, fmt.Sprintf("include %q is under .igris/, igris's own state; include a plan file", path)
	case real == r.mainReal:
		return nil, fmt.Sprintf("include %q is the main plan %s itself; a file is part of the plan once", path, p.Path)
	}
	if first, dup := r.seen[real]; dup {
		return nil, fmt.Sprintf("include %q names %s again (first included at line %d); a file is part of the plan once", path, first.name, first.line)
	}
	r.seen[real] = *inc

	// The resolved path holds no symlink, so it is read without following
	// one: a symlink put in its place since is refused, not followed.
	data, err := fsafe.ReadFile(real, MaxSize, false)
	switch {
	case errors.Is(err, fsafe.ErrTooLarge):
		return nil, fmt.Sprintf("included file %s is over %d MiB; split it or remove what is not a plan", inc.name, MaxSize>>20)
	case errors.Is(err, fsafe.ErrNotRegular):
		return nil, fmt.Sprintf("included file %s is not a regular file; put the file back", inc.name)
	case err != nil:
		return nil, fmt.Sprintf("include %q can't be read: %v", path, err)
	case r.total+int64(len(data)) > MaxSize:
		return nil, fmt.Sprintf("include %q takes the plan's files over %d MiB together; move what is not a plan out of them", path, MaxSize>>20)
	}
	r.total += int64(len(data))
	return &planFile{name: inc.name, lines: splitLines(data), incLine: inc.line}, ""
}

func (r *includer) realRoot() (string, error) {
	if !r.rootDone {
		r.rootReal, r.rootErr = resolveRoot(r.root)
		r.rootDone = true
	}
	return r.rootReal, r.rootErr
}

// resolveRoot returns root ("" for the working directory) absolute and
// with symlinks resolved. Abs comes first: for "." it reads $PWD, which
// may name a symlink.
func resolveRoot(root string) (string, error) {
	if root == "" {
		root = "."
	}
	r, err := filepath.Abs(root)
	if err == nil {
		r, err = filepath.EvalSymlinks(r)
	}
	return r, err
}

// hasDotDot reports whether a slash- or OS-separated path has a ".."
// element.
func hasDotDot(path string) bool {
	for _, el := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == filepath.Separator }) {
		if el == ".." {
			return true
		}
	}
	return false
}

// Inline returns the plan as one file: the main plan with each include
// line replaced by the lines of the file it read, less their format
// markers, between blank lines (so no table runs across the seam), and a
// fence an included file leaves open closed after it. Its
// phases and tasks are the plan's; its lines and line endings are not. It
// is for a scratch copy only (a dry run's walk, SPEC §14), never written
// over the plan.
func (p *Plan) Inline() []byte {
	byLine := map[int]*planFile{}
	for _, f := range p.files[min(1, len(p.files)):] {
		byLine[f.incLine] = f
	}
	var b strings.Builder
	for _, l := range p.files[0].lines {
		f := byLine[l.num]
		if f == nil {
			b.WriteString(l.text + "\n")
			continue
		}
		b.WriteString("\n")
		for _, il := range f.lines {
			if !slices.Contains(f.markers, il.num) {
				b.WriteString(il.text + "\n")
			}
		}
		if f.fence != "" {
			b.WriteString(f.fence + "\n")
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}
