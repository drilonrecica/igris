package report

import (
	"time"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// RunRequest is what a run is asked to do: `igris arise`'s arguments.
type RunRequest struct {
	Phase   string // "" resumes the last run's phases
	Through string
	Mode    string // "" is as planned
}

// Confirmations are the owner's answers to arise's start-up questions for
// one launch (SPEC §14). They are never remembered between launches.
type Confirmations struct {
	Drift       bool // let igris fix readiness drift (§5.2)
	Yolo        bool // the typed `skip permissions` (§7.3)
	ForceUnlock bool // clear a stale or remote lock (--force-unlock)
}

// Prelaunch is what `igris arise` says before a run starts.
type Prelaunch struct {
	// Warnings are the start-up problems in the order arise prints them:
	// the herdr integration, tool versions, config, plan hints, then
	// ANTHROPIC_API_KEY and git. One with Confirm set is asked about; the
	// run starts only if the owner says yes.
	Warnings []checks.Result
	// Interrupted is the task an earlier run stopped during, which a
	// resumed run picks up first; "" when there is none.
	Interrupted string
}

// Step is one step of `igris init`: what it touched and what it did.
type Step struct {
	ID      string // StepConfig, ...
	Path    string // the file or directory, relative to the project root; "" for the hint
	Message string // the line `igris init` prints: "created igris.toml", "kept …"
}

// Init step IDs, in the order init runs them.
const (
	StepConfig         = "config"          // igris.toml
	StepState          = "state"           // .igris/
	StepGitignore      = "gitignore"       // the .igris/ entry in .gitignore
	StepClaudeSettings = "claude-settings" // the `igris done` allow rules
	StepExamplePlan    = "example-plan"    // init --example
	StepHerdrHint      = "herdr-hint"      // advice on herdr's Claude integration; touches nothing
)

// NotifyResult is the outcome of one test notification on one channel.
type NotifyResult struct {
	Event   string
	Channel string
	// Err is why the delivery failed, cleaned and without secrets; "" when
	// it went through.
	Err string
}

// FileStamp is the size and modification time of a watched file; the zero
// value when the file doesn't exist.
type FileStamp struct {
	Exists  bool
	Size    int64
	ModTime time.Time
}

// Stamp is a cheap fingerprint of the files the home screen watches. Two
// stamps that are == mean nothing to reload.
type Stamp struct {
	Config FileStamp // igris.toml
	Plan   FileStamp
	State  FileStamp // .igris/state.json
	Lock   FileStamp // .igris/igris.lock
	Log    FileStamp // .igris/runs.jsonl
}

// Snapshot is the project as the home screen shows it. Nothing in it fails:
// what can't be read is described.
type Snapshot struct {
	Root    string // the project root; the working directory outside a project
	Project string // the root's base name, for the header
	// Found says Root holds igris.toml or .igris/.
	Found bool

	// Config is the effective config: the defaults when igris.toml is
	// missing or invalid. NoConfig says there is no igris.toml;
	// ConfigProblems lists why it is invalid, one cleaned line each.
	Config         *config.Config
	NoConfig       bool
	ConfigProblems []string
	// ConfigPath is igris.toml's absolute path, whether or not it exists.
	ConfigPath string
	// ConfigWarnings are settings that are accepted but do nothing, one
	// cleaned line each.
	ConfigWarnings []string
	// Settings is Config laid out for the Settings page, secrets hidden.
	Settings []SettingsSection

	PlanPath    string // absolute
	PlanMissing bool
	// PlanErr is why the plan couldn't be read (other than missing).
	PlanErr string
	// Plan is the parsed plan; nil when it is missing or unreadable.
	Plan *plan.Plan
	// Issues are the validation problems; empty for a valid plan.
	Issues []Issue
	// Status is the plan's phases and tasks; nil unless the plan is valid.
	Status *StatusReport

	Run    *RunInfo        // nil when there is no run
	Lock   state.LockState // with its host and reason cleaned
	Recent []HistoryRun    // the last runs, newest first
	Stamp  Stamp           // the files as they were read
}

// Valid says igris.toml (if any) and the plan are both valid.
func (s *Snapshot) Valid() bool {
	return len(s.ConfigProblems) == 0 && s.Plan != nil && len(s.Issues) == 0
}
