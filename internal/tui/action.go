package tui

import "github.com/drilonrecica/igris/internal/engine"

// action is something the owner can do in the TUI: a button, a shortcut
// key or a dialog option all end up as one.
type action int

const (
	actNone   action = iota
	actOption        // a dialog option; the zone says which
	actOpen          // focus the current session's pane
	actPause         // toggle pause-after-task
	actDone          // mark the current task done
	actQuit          // quit the TUI; sessions keep running
	actAnswerYes
	actAnswerNo
	actRetryFresh
	actRetryContinue
	actStop        // stop igris now; the Stop button asks first (actStopAsk)
	actAnswer      // reopen the dialog of the pending question
	actClose       // close a dialog or page; a question stays pending
	actMore        // show the actions the narrow bar folded away
	actSkip        // ask for a reason, then skip the current task
	actSkipConfirm // skip with the reason typed into the skip dialog
	actRetry       // ask how to retry the current task
	actStopAsk     // ask before stopping igris
	actHelp        // show every action and its key
	actField       // a dialog's text field; clicking it focuses it
	actMode        // choose the run mode for the next sessions
	actTaskMode    // choose the mode of the selected task's next session
	actYoloConfirm // switch to skip permissions with the phrase typed
	actTaskRow     // a task list row; the zone's option is the task's index
	// One action per mode in the mode picker; see modeActs.
	actModeDefault
	actModeAccept
	actModeAuto
	actModePlan
	actModeYolo
	// igris adapt's review (adapt.go).
	actAccept       // replace the plan with the proposal
	actAcceptAnyway // replace it although the proposal doesn't pass check
	actReject       // keep the plan as it is
	actCopy         // copy the selected item with OSC 52 (y)
	// The home screen's actions (SPEC §15.6, home.go).
	actArise      // start a run, or resume the last one (the wizard)
	actPreview    // the dry run page
	actAriseWith  // the wizard, with the previewed settings
	actCheck      // the check page
	actDoctor     // the doctor page
	actHistory    // the history page
	actEdit       // open the screen's file in the owner's editor
	actEditConfig // open igris.toml in the owner's editor, from Doctor
	actSettings   // the settings page
	actEditAnyway // edit the plan although a run holds it
	actEditVi     // open the file with vi: no editor is set
	actNotify     // the notify test page
	actAdapt      // igris adapt
	actInit       // igris init
	actPhaseRow   // a PHASES row; the zone's option is the phase's index
	actLine       // a HEALTH or RECENT line; the zone's option is its index
	// The start-run wizard's choices (launch.go); a phase or through
	// option is the dialog's selected one.
	actWizResume  // resume the last run
	actWizPhase   // start a phase: pick it next, or the picked phase
	actWizThrough // the picked last phase
	actWizPlanned // run mode as planned
	actWizArise   // the summary's Arise
	actWizConfirm // the confirming choice of a start-up question
)

// modeActs maps the mode picker's actions to their modes, in the order
// the picker lists them.
var modeActs = []struct {
	act         action
	mode, label string
}{
	{actModeDefault, engine.ModeDefault, "default — your normal permission prompts"},
	{actModeAccept, engine.ModeAccept, "accept — edits are accepted"},
	{actModeAuto, engine.ModeAuto, "auto — Claude Code asks only about risky actions"},
	{actModePlan, engine.ModePlan, "plan — plan first, you approve, then it implements"},
	{actModeYolo, engine.ModeYolo, "yolo [SKIP PERMISSIONS] — no permission prompts"},
}

// modeOf is the mode a mode picker action picks, if it is one.
func modeOf(a action) (string, bool) {
	for _, m := range modeActs {
		if m.act == a {
			return m.mode, true
		}
	}
	return "", false
}

// command is the engine command an action sends, if any.
func (a action) command() (engine.Command, bool) {
	switch a {
	case actPause:
		return engine.Command{Kind: engine.CmdPause}, true
	case actDone:
		return engine.Command{Kind: engine.CmdDone}, true
	case actAnswerYes:
		return engine.Command{Kind: engine.CmdAnswer, Yes: true}, true
	case actAnswerNo:
		return engine.Command{Kind: engine.CmdAnswer}, true
	case actRetryFresh:
		return engine.Command{Kind: engine.CmdRetry}, true
	case actRetryContinue:
		return engine.Command{Kind: engine.CmdRetry, Continue: true}, true
	case actStop:
		return engine.Command{Kind: engine.CmdStop}, true
	}
	return engine.Command{}, false
}

// shortcuts maps the keys of SPEC §15.3 to their actions.
var shortcuts = map[string]action{
	"o":      actOpen,
	"m":      actMode,
	"M":      actTaskMode,
	"p":      actPause,
	"d":      actDone,
	"s":      actSkip,
	"r":      actRetry,
	"x":      actStopAsk,
	"y":      actCopy,
	"?":      actHelp,
	"q":      actQuit,
	"ctrl+c": actQuit,
}

// helpEntry is one line of the help page.
type helpEntry struct{ key, name, what string }

// helpActions are the actions the help page lists, in SPEC §15.3 order.
var helpActions = []helpEntry{
	{"o", "Open session", "bring the current session's pane to the front"},
	{"m", "Mode", "run mode for the sessions launched from now on; yolo needs the typed phrase"},
	{"M", "Task mode", "mode for the selected agent task's next session, over its Mode column (not for user tasks)"},
	{"p", "Pause / Resume", "pause after the current task; igris waits until you resume"},
	{"d", "Done", "mark the current task done (your decision: verify is skipped)"},
	{"s", "Skip", "skip the current task; asks for a reason and closes its session"},
	{"r", "Retry", "close the session and start fresh or continue the conversation"},
	{"x", "Stop", "stop igris after confirming; the session stays open"},
	{"y", "Copy", "copy claude --resume for the current session, or the log line at the bottom when the log has the focus"},
	{"q", "Quit", "quit the TUI; sessions keep running, igris arise resumes"},
	{"?", "Help", "this page"},
}

// helpFocus are the focus keys the help page lists (SPEC §15.5).
var helpFocus = []helpEntry{
	{"click", "", "select a task; click it again, or press enter, for its details"},
	{"tab", "", "move between the task list, the action bar and the log"},
	{"← →", "", "move along the action bar"},
	{"↑ ↓  j k", "", "move in lists and dialogs, scroll the log"},
	{"pgup pgdn", "", "scroll a page"},
	{"enter space", "", "activate the focused element: a button, a task's details, the whole log"},
	{"1…9", "", "pick a dialog option by its number"},
	{"esc", "", "close a dialog or page with its safe choice"},
}
