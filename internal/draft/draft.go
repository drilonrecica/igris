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
	"time"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/config"
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

// MaxSpecSize is the largest spec file `draft spec` takes (SPEC §9.1).
const MaxSpecSize = 1 << 20

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
// problems (none if it passes `igris check`). For extend, the proposal is
// the new part and the problems are the extended plan's.
type Result struct {
	Source       Source
	PlanPath     string
	ProposalPath string
	Proposed     []byte
	Issues       []plan.Issue
	Ranks        Ranks
	Note         string // the session's `igris done DRAFT --note`
	// Extension is extend's proposal spliced into the plan in memory; nil
	// for the other sources.
	Extension *plan.Extension

	root string // the project root, for the file names the review shows
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
func CountRanks(p *plan.Plan) Ranks { return countRanks(p.Tasks) }

// countRanks counts the ranks of the agent tasks among tasks.
func countRanks(tasks []*plan.Task) Ranks {
	var r Ranks
	for _, t := range tasks {
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

// ExtendPath returns .igris/draft/<plan-name>.extend.md for planPath:
// where the extend session writes the new part (SPEC §9.1).
func ExtendPath(d *state.Dir, planPath string) string {
	return filepath.Join(d.DraftDir(), planName(planPath)+".extend.md")
}

// proposalPath is where src's session writes its proposal.
func proposalPath(d *state.Dir, src Source, planPath string) string {
	if src == Extend {
		return ExtendPath(d, planPath)
	}
	return ProposalPath(d, planPath)
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

// LoadValid reads the plan extend adds to, with the files it includes,
// and checks it passes `igris check`: an invalid plan is for `igris
// adapt` (SPEC §9.1).
func LoadValid(cfg *config.Config, root, planPath string) (*plan.Plan, error) {
	name := relTo(root, planPath)
	if _, err := os.Stat(planPath); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s doesn't exist; draft a new plan with `igris draft goal`, `spec` or `import`, or pass the plan to extend with --plan", name)
	}
	p, err := plan.Load(planPath, plan.Options{Columns: cfg.Columns, Root: root})
	if err != nil {
		return nil, err
	}
	var invalid *plan.Invalid
	switch err := p.Check(cfg.Rules(root)); {
	case errors.As(err, &invalid) && invalid.Newer:
		return nil, fmt.Errorf("%s can't be extended: %w", name, err)
	case err != nil:
		return nil, fmt.Errorf("%s can't be extended until it passes `igris check`: %w", name, err)
	}
	return p, nil
}

// CheckSpec checks the spec file named by the owner (SPEC §9.1): inside
// the project once symlinks are resolved, a regular file of at most
// MaxSpecSize bytes, valid UTF-8. A relative file is taken from the working
// directory. It returns the file's path relative to the project root, which
// is what the session is told to read (igris never pastes the spec).
func CheckSpec(root, file string) (string, error) {
	if file == "" {
		return "", errors.New("no spec file; use `igris draft spec FILE`")
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", fmt.Errorf("spec file %s: %w", file, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("spec file %s doesn't exist; pass the path of a spec or PRD in the project", file)
	}
	if err != nil {
		return "", fmt.Errorf("resolve spec file %s: %w", file, err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", root, err)
	}
	if !inside(realRoot, real) || real == realRoot {
		return "", fmt.Errorf("spec file %s is outside the project %s; copy it into the project, or pass a file inside it", file, root)
	}
	data, err := fsafe.ReadFile(real, MaxSpecSize, false)
	switch {
	case errors.Is(err, fsafe.ErrNotRegular):
		return "", fmt.Errorf("spec file %s is not a regular file; pass a spec or PRD as a plain file", file)
	case errors.Is(err, fsafe.ErrTooLarge):
		return "", fmt.Errorf("spec file %s is over %d MiB; split it, or pass the part to plan from", file, MaxSpecSize>>20)
	case err != nil:
		return "", fmt.Errorf("read spec file %s: %w", file, err)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("spec file %s is not valid UTF-8 text; pass a Markdown or text spec", file)
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil {
		return "", fmt.Errorf("spec file %s: %w", file, err)
	}
	return rel, nil
}

// Check is what Run refuses before any session opens or the lock is taken
// (SPEC §9.1): a plan in the way of a new one, a spec file or an export
// that can't be used, and a plan to extend that is missing or invalid.
// Callers may run it first, before they create anything. file is the spec
// or export as the owner named it; format is import's --format, "" to
// detect it.
func Check(cfg *config.Config, root string, src Source, planPath, file, format string) error {
	_, err := check(cfg, root, src, planPath, file, format)
	return err
}

// checked is what check read: the parsed export of an import, the plan
// extend adds to.
type checked struct {
	export *Export
	plan   *plan.Plan
}

// check is Check that also returns what it read.
func check(cfg *config.Config, root string, src Source, planPath, file, format string) (checked, error) {
	var c checked
	if src.NewPlan() {
		if err := CheckSlot(root, planPath); err != nil {
			return c, err
		}
	}
	var err error
	switch src {
	case Goal:
	case Spec:
		_, err = CheckSpec(root, file)
	case Import:
		c.export, err = LoadImport(file, format)
	case Extend:
		c.plan, err = LoadValid(cfg, root, planPath)
	default:
		err = fmt.Errorf("unknown draft source %q", src)
	}
	return c, err
}

// Run checks where the plan goes, runs the draft session until it signals
// `igris done DRAFT`, and validates the proposal it wrote. It never writes
// the plan itself. If ctx ends first, the session is left open.
func Run(ctx context.Context, o Options) (*Result, error) {
	if o.Clock == nil {
		o.Clock = engine.SystemClock()
	}
	d := o.State
	in, err := check(o.Config, d.Root(), o.Source, o.PlanPath, o.File, o.Format)
	if err != nil {
		return nil, err
	}
	exp := in.export
	proposal := proposalPath(d, o.Source, o.PlanPath)
	text, err := o.prompt(proposal, in)
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
	if exp != nil {
		// igris parsed the export; the session reads this, never the raw
		// file (SPEC §9.1).
		data, err := exp.JSON()
		if err != nil {
			return nil, err
		}
		if _, err := d.WriteDraftFile(ImportFile, data); err != nil {
			return nil, err
		}
		if o.Out != nil {
			fmt.Fprintf(o.Out, "importing %d item(s) from a %s export (%d already done)\n", len(exp.Items), exp.Source, exp.Done())
		}
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
	res := &Result{
		Source:       o.Source,
		PlanPath:     o.PlanPath,
		ProposalPath: proposal,
		Proposed:     proposed,
		Note:         sig.Note,
		root:         d.Root(),
	}
	if o.Source == Extend {
		return res, o.extend(res)
	}
	// Parsed without the project root, so an include line is a problem:
	// accepting it would bring in files the owner never reviewed.
	p := plan.Parse(proposal, proposed, plan.Options{Columns: o.Config.Columns})
	res.Issues = p.Validate(o.Config.Rules(d.Root()))
	res.Ranks = CountRanks(p)
	return res, nil
}

// extend splices res's proposal, the new part, into the plan as it is now
// and validates the result (SPEC §9.1). A fragment igris can't splice is an
// error: nothing is offered for review.
func (o Options) extend(res *Result) error {
	root := o.State.Root()
	p, err := LoadValid(o.Config, root, o.PlanPath)
	if err != nil {
		return err
	}
	name := relTo(root, res.ProposalPath)
	x, issues := plan.Extend(p, plan.Options{Columns: o.Config.Columns, Root: root}, name, res.Proposed)
	if len(issues) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "igris can't add %s to %s: %d problem(s); nothing was written, run `igris draft extend` again:", name, relTo(root, o.PlanPath), len(issues))
		for _, is := range issues {
			b.WriteString("\n  ")
			b.WriteString(textsafe.Line(is.Error()))
		}
		return errors.New(b.String())
	}
	res.Extension = x
	res.Issues = x.Plan.Validate(o.Config.Rules(root))
	var tasks []*plan.Task
	for _, a := range x.Additions {
		tasks = append(tasks, a.Tasks...)
	}
	res.Ranks = countRanks(tasks)
	return nil
}

// prompt renders the draft prompt for o's source.
func (o Options) prompt(proposal string, in checked) (string, error) {
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
	case Spec:
		rel, err := CheckSpec(root, o.File)
		if err != nil {
			return "", err
		}
		// The name is the owner's, but a file name can hold anything.
		vars.SpecFile = textsafe.Clean(rel)
	case Import:
		exp := in.export
		vars.ImportFile = relTo(root, filepath.Join(o.State.DraftDir(), ImportFile))
		vars.ImportSource = exp.Source
		vars.ImportCount = len(exp.Items)
		vars.ImportDone = exp.Done()
	case Extend:
		// Typed into the pane: no escape sequences.
		vars.Request = strings.TrimSpace(textsafe.Clean(o.Text))
		for _, f := range in.plan.Files() {
			vars.PlanFiles = append(vars.PlanFiles, textsafe.Clean(relTo(root, f)))
		}
	}
	return prompt.RenderDraft(vars)
}

// Review is what the owner reviews: a new plan's proposal against no plan,
// as a line diff; for extend only what it adds, phase by phase, each with
// its file (SPEC §9.1).
func Review(res *Result) adapt.Review {
	x := res.Extension
	if x == nil {
		return adapt.LineReview(nil, res.Proposed)
	}
	rv := adapt.Review{Tables: true, Notes: x.Notes}
	for _, a := range x.Additions {
		s := adapt.Section{Op: adapt.Mod, Phase: a.Phase, Heading: a.Heading + " · " + relTo(res.root, a.File)}
		if a.New {
			s.Op = adapt.Add
			for _, l := range a.Prose {
				rv.Prose = append(rv.Prose, adapt.Line{Op: adapt.Add, Text: l})
			}
		}
		for _, t := range a.Tasks {
			s.Changes = append(s.Changes, adapt.Change{Op: adapt.Add, What: t.ID, New: taskLine(t)})
		}
		rv.Sections = append(rv.Sections, s)
	}
	return rv
}

// taskLine is a new task in the review: its title, deps, rank and owner,
// e.g. "Printer · deps M1-01 · opus · agent".
func taskLine(t *plan.Task) string {
	parts := []string{t.Title}
	if t.DepsText != "" && t.DepsText != "—" && t.DepsText != "-" {
		parts = append(parts, "deps "+t.DepsText)
	}
	if t.Rank != "" {
		parts = append(parts, t.Rank)
	}
	if t.OwnerText != "" {
		parts = append(parts, t.OwnerText)
	}
	return strings.Join(parts, " · ")
}

// Accept writes the reviewed proposal of res, and returns the backups it
// took. A new plan is created exclusively, so a file that appeared at the
// plan path since the review is never overwritten: nothing is written then
// and the proposal stays (SPEC §9.1). An extension first backs up each file
// it changes to .igris/draft/<file-name>.<timestamp>.bak.md, then writes
// them; if any file of the plan changed on disk since it was read, nothing
// is written.
func Accept(d *state.Dir, res *Result, now time.Time) ([]string, error) {
	if res.Extension != nil {
		return acceptExtension(d, res, now)
	}
	return nil, create(res)
}

// acceptExtension backs up and writes the files res's extension changes.
func acceptExtension(d *state.Dir, res *Result, now time.Time) ([]string, error) {
	var backups []string
	taken := map[string]bool{}
	for _, f := range res.Extension.Files() {
		name := adapt.BackupName(f.Name, now)
		for n := 2; taken[name]; n++ {
			// Two files of a split plan with one name: keep both.
			name = fmt.Sprintf("%s-%d.%s.bak.md", planName(f.Name), n, now.UTC().Format("20060102-150405"))
		}
		taken[name] = true
		path, err := d.WriteDraftFile(name, f.Old)
		if err != nil {
			return backups, fmt.Errorf("back up %s: %w; the plan is unchanged, the new part stays in %s", relTo(res.root, f.Name), err, res.ProposalPath)
		}
		backups = append(backups, path)
	}
	if err := res.Extension.Write(); err != nil {
		return backups, fmt.Errorf("%w; the new part stays in %s", err, res.ProposalPath)
	}
	return backups, nil
}

// create writes res's proposal as the new plan, exclusively.
func create(res *Result) error {
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
