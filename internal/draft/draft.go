// Package draft implements `igris draft` (SPEC §9.1): a Claude Code
// session writes a plan proposal under .igris/draft/, igris validates it
// with the normal parser for the owner's review (the adapt review, §9), and
// only the owner's Accept writes it.
package draft

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/fsafe"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// ID is the session and signal ID of the draft session.
const ID = prompt.DraftID

// Source is what a plan is drafted from: a draft subcommand (SPEC §9.1).
type Source string

// The sources, in the order the help lists them.
const (
	Goal   Source = "goal"
	Spec   Source = "spec"
	Import Source = "import"
	Extend Source = "extend"
)

// Sources are the draft subcommands.
var Sources = []Source{Goal, Spec, Import, Extend}

// Formats are the export formats `draft import --format` names.
var Formats = []string{"github", "linear", "jira"}

// NewPlan reports whether the source drafts a new plan, which is written
// only where no plan file is; extend adds to the current one.
func (s Source) NewPlan() bool { return s != Extend }

// HasFile reports whether the source takes a FILE argument.
func (s Source) HasFile() bool { return s == Spec || s == Import }

// Options configure Run.
type Options struct {
	adapt.SessionOptions

	Source   Source
	PlanPath string // absolute path of the plan the proposal becomes
	Model    string // "sonnet" or "opus"; resolved through [models]
	// Text is the goal (goal) or the request (extend); "" makes the
	// session ask for it.
	Text string
	// File is the spec or the export (spec, import), as the owner named it.
	File string
	// Format is import's --format; "" detects it.
	Format string
}

// Result is a finished draft session: the proposal and its validation
// problems (none if it passes `igris check`).
type Result struct {
	Source       Source
	PlanPath     string
	ProposalPath string
	Proposed     []byte
	Issues       []plan.Issue
	Ranks        Ranks
	Note         string // the session's `igris done DRAFT --note`
}

// Ranks counts the Model cells of the proposal's agent tasks: every one is
// a suggestion, and Open of them are left at `?` (SPEC §9.1).
type Ranks struct {
	Set, Open int
}

// String is the review header's line, e.g.
// "ranks are suggestions: 12 set, 2 left at ?".
func (r Ranks) String() string {
	return fmt.Sprintf("ranks are suggestions: %d set, %d left at ?", r.Set, r.Open)
}

// CountRanks counts the ranks of p's agent tasks.
func CountRanks(p *plan.Plan) Ranks {
	var r Ranks
	for _, t := range p.Tasks {
		switch {
		case !t.Owner.IsAgent() || t.Rank == "":
		case t.Rank == "?":
			r.Open++
		default:
			r.Set++
		}
	}
	return r
}

// ProposalPath returns .igris/draft/<plan-name>.proposed.md for planPath.
func ProposalPath(d *state.Dir, planPath string) string {
	return filepath.Join(d.DraftDir(), planName(planPath)+".proposed.md")
}

// planName is the plan's file name without .md: "tasks" for tasks.md.
func planName(planPath string) string {
	base := filepath.Base(planPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// CheckSlot checks that a new plan may be written at planPath (SPEC
// §9.1): inside the project root, in a directory that exists, and where no
// file is (not even a dangling symlink).
func CheckSlot(root, planPath string) error {
	name := relTo(root, planPath)
	if !inside(root, planPath) {
		return fmt.Errorf("%s is outside the project %s; pass a --plan PATH inside it", planPath, root)
	}
	dir := filepath.Dir(planPath)
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("the directory of %s doesn't exist; create %s first, or pass another --plan", name, relTo(root, dir))
	case err != nil:
		return fmt.Errorf("check %s: %w", name, err)
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory; pass another --plan", relTo(root, dir))
	}
	// A symlinked directory must not lead out of the project either.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", root, err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", dir, err)
	}
	if !inside(realRoot, realDir) {
		return fmt.Errorf("%s leads outside the project %s; pass a --plan PATH inside it", relTo(root, dir), root)
	}
	switch _, err := os.Lstat(planPath); {
	case err == nil:
		return fmt.Errorf("%s already exists; add to it with igris draft extend, or draft into a new file with --plan PATH (a format-2 plan can include it)", name)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("check %s: %w", name, err)
	}
	return nil
}

// errNotYet is what a source this build doesn't have yet answers.
func errNotYet(s Source) error {
	return fmt.Errorf("`igris draft %s` is not available in this build yet", s)
}

// Check is what Run refuses before any session opens or the lock is taken
// (SPEC §9.1): a plan in the way of a new one, and a source this build
// doesn't have yet. Callers may run it first, before they create anything.
func Check(root string, src Source, planPath string) error {
	if src.NewPlan() {
		if err := CheckSlot(root, planPath); err != nil {
			return err
		}
	}
	if src != Goal {
		return errNotYet(src)
	}
	return nil
}

// Run checks where the plan goes, runs the draft session until it signals
// `igris done DRAFT`, and validates the proposal it wrote. It never writes
// the plan itself. If ctx ends first, the session is left open.
func Run(ctx context.Context, o Options) (*Result, error) {
	if o.Clock == nil {
		o.Clock = engine.SystemClock()
	}
	d := o.State
	if err := Check(d.Root(), o.Source, o.PlanPath); err != nil {
		return nil, err
	}
	proposal := ProposalPath(d, o.PlanPath)
	text, err := o.prompt(proposal)
	if err != nil {
		return nil, err
	}

	lock, err := d.Lock(false)
	if err != nil {
		return nil, lockError(err)
	}
	defer func() { _ = lock.Release() }()

	if err := os.Remove(proposal); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("remove the earlier proposal %s: %w", proposal, err)
	}
	if err := d.RemoveSignal(ID); err != nil {
		return nil, err
	}
	sig, err := adapt.RunSession(ctx, o.SessionOptions, adapt.SessionSpec{
		ID:      ID,
		Command: "draft",
		Rerun:   "igris draft " + string(o.Source),
		Model:   o.Model,
		// manual: the owner sees every permission prompt, writing the
		// proposal included, whatever Claude Code's own default is (V13-P1).
		Mode:   engine.ModeManual,
		Rules:  prompt.DraftRules(),
		Prompt: text,
	})
	if err != nil {
		return nil, err
	}

	// The session wrote the proposal: read it as a regular file of
	// bounded size, never through a symlink.
	proposed, err := fsafe.ReadFile(proposal, plan.MaxSize, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the draft session finished without writing %s; run `igris draft %s` again", relTo(d.Root(), proposal), o.Source)
	}
	if err != nil {
		return nil, fmt.Errorf("read proposal: %w", err)
	}
	// Parsed without the project root, so an include line is a problem:
	// accepting it would bring in files the owner never reviewed.
	p := plan.Parse(proposal, proposed, plan.Options{Columns: o.Config.Columns})
	return &Result{
		Source:       o.Source,
		PlanPath:     o.PlanPath,
		ProposalPath: proposal,
		Proposed:     proposed,
		Issues:       p.Validate(o.Config.Rules(d.Root())),
		Ranks:        CountRanks(p),
		Note:         sig.Note,
	}, nil
}

// prompt renders the draft prompt for o's source.
func (o Options) prompt(proposal string) (string, error) {
	cfg, root := o.Config, o.State.Root()
	vars := prompt.DraftVars{
		Source:       string(o.Source),
		PlanFile:     relTo(root, o.PlanPath),
		ProposalFile: relTo(root, proposal),
		Models:       prompt.Aliases(cfg.Models),
		Verify:       cfg.Rules("").Verify,
	}
	switch o.Source {
	case Goal:
		// Typed into the pane: no escape sequences.
		vars.Goal = strings.TrimSpace(textsafe.Clean(o.Text))
	default:
		return "", errNotYet(o.Source)
	}
	return prompt.RenderDraft(vars)
}

// Review is what the owner reviews: the proposal against no plan, as a
// line diff.
func Review(res *Result) adapt.Review {
	return adapt.LineReview(nil, res.Proposed)
}

// Accept writes the reviewed proposal of res as the new plan. The file is
// created exclusively, so a file that appeared at the plan path since the
// review is never overwritten: nothing is written then and the proposal
// stays (SPEC §9.1).
func Accept(res *Result) error {
	name := res.PlanPath
	//nolint:gosec // a plan is the owner's project file, readable like the rest of the tree (SPEC §9.1: mode 0644)
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("not writing %s: a file appeared there after the review; the proposal stays in %s", name, res.ProposalPath)
	}
	if err != nil {
		return fmt.Errorf("write plan %s: %w; the proposal stays in %s", name, err, res.ProposalPath)
	}
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(name) // created above: nothing else is lost
		return fmt.Errorf("write plan %s: %w; the proposal stays in %s", name, err, res.ProposalPath)
	}
	if err := f.Chmod(0o644); err != nil { // whatever the umask
		return fail(err)
	}
	if _, err := f.Write(res.Proposed); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write plan %s: %w; the proposal stays in %s", name, err, res.ProposalPath)
	}
	return nil
}

// lockError rewords the run-lock errors for draft, which has no
// --force-unlock of its own.
func lockError(err error) error {
	var stale *state.StaleLockError
	if errors.As(err, &stale) {
		return fmt.Errorf("%v (or run `igris arise --force-unlock` once)", err)
	}
	return fmt.Errorf("%w; draft can't run while igris arise runs in this project", err)
}

// inside reports whether path is root or below it.
func inside(root, path string) bool {
	r, err := filepath.Rel(root, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}

// relTo returns p relative to root when it is inside root, else p.
func relTo(root, p string) string {
	if rp, err := filepath.Rel(root, p); err == nil && inside(root, p) {
		return rp
	}
	return p
}
