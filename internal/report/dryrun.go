package report

// StepKind says what a dry-run step is.
type StepKind string

const (
	StepSession   StepKind = "session"    // a session would launch
	StepUser      StepKind = "user"       // a task that waits for the owner
	StepPhaseDone StepKind = "phase_done" // a phase completed
	StepNote      StepKind = "note"       // lines of the walk: stuck phase, mode change, ...
	StepWarning   StepKind = "warning"    // the walk itself hit a problem; Lines[0] is it
)

// DryStep is one line of the walk. Sessions and user tasks are numbered.
type DryStep struct {
	Kind    StepKind
	N       int // 1-based position among sessions and user tasks; 0 for the others
	Task    string
	Rank    string
	Model   string
	Mode    string
	Title   string
	Resumed bool   // the task was in progress: it gets a fresh session
	Phase   string // StepPhaseDone
	Lines   []string
}

// DryRun is the result of `igris arise --dry-run` (SPEC §14): what a run
// would launch, with which model and mode, and what it would warn about.
type DryRun struct {
	// Interrupted is the task an earlier run stopped during, if any.
	Interrupted string
	Warnings    []string
	// Scope names the phases walked ("A", "A through B"); empty until the walk
	// started, so a dry run that failed before it has none.
	Scope    string
	Steps    []DryStep
	Sessions int
	Users    int
}
