package tui

import (
	"slices"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/state"
)

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
	actModeManual
	actModePlanDefault
	actModePlanAccept
	actModePlanAuto
	actModePlanYolo
	actModeMore // More modes…: the picker lists the rest of the modes
	// igris adapt's review (review.go).
	actAccept       // replace the plan with the proposal
	actAcceptAnyway // replace it although the proposal doesn't pass check
	actReject       // keep the plan as it is
	actCopy         // copy the selected item with OSC 52 (y)
	// The home screen's actions (SPEC §15.6, home.go).
	actArise       // start a run, or resume the last one (the wizard)
	actPreview     // the dry run page
	actAriseWith   // the wizard, with the previewed settings
	actCheck       // the check page
	actDoctor      // the doctor page
	actHistory     // the history page
	actEdit        // open the screen's file in the owner's editor
	actEditConfig  // open igris.toml in the owner's editor, from Doctor
	actSettings    // the settings page
	actEditAnyway  // edit the plan although a run holds it
	actEditVi      // open the file with vi: no editor is set
	actNotify      // the notify test page; on it, Send again
	actNotifySend  // the Notify dialog's Send
	actAdapt       // igris adapt: the dialog that asks first
	actAdaptSonnet // the Adapt dialog's Adapt with sonnet
	actAdaptOpus   // the Adapt dialog's Adapt with opus
	actDraft       // Draft…: the picker of what to draft from (D)
	actDraftGoal   // the picker's From a goal
	actDraftSpec   // the picker's From a spec file…
	actDraftImport // the picker's Import an export…
	actDraftExtend // the picker's Extend the plan…
	actDraftSonnet // the draft dialog's Draft with sonnet
	actDraftOpus   // the draft dialog's Draft with opus
	actInit        // igris init
	actExample     // Plan from template…: the picker of the example plan and the templates
	actInitCreate  // the Init dialog's Create files
	actInitPick    // the picker's selected starter plan
	actUpdate      // Check for updates (u)
	actUpdateCheck // the failed check's Try again
	actUpdateRun   // the check's Update: run the install command
	actPhaseRow    // a PHASES row; the zone's option is the phase's index
	actLine        // a HEALTH or RECENT line; the zone's option is its index
	// The start-run wizard's choices (launch.go); a phase or through
	// option is the dialog's selected one.
	actWizResume   // resume the last run
	actWizPhase    // start a phase: pick it next, or the picked phase
	actWizThrough  // the picked last phase
	actWizPlanned  // run mode as planned
	actWizArise    // the summary's Arise
	actWizConfirm  // the confirming choice of a start-up question
	actDetails     // the selected task's details page (t, the card)
	actDoneConfirm // done with the note typed into a user task's done dialog
	// A plan presented for approval (SPEC §6.3, planDialog).
	actPlanApprove       // approve the plan
	actPlanReject        // ask for the feedback that rejects it
	actPlanRejectConfirm // reject it with the feedback typed
	actPlanPane          // answer it in the session's pane, which opens
	// Side sessions (SPEC §15.7, side.go); a picked row, preset, rank,
	// mode or place is the dialog's selected option.
	actSide             // S: the Side sessions page, or the dialog
	actSidePick         // the selected option of a side dialog
	actSideNew          // the page's New…
	actSideNext         // the first prompt's Next
	actSideOpen         // the summary's Open
	actSideCloseAsk     // the page's Close a session…
	actSideCloseConfirm // Close session
	actSideBack         // back to the page (Keep it)
	// An attached run view's q (SPEC §15.3, attached.go).
	actDetach    // leave the view; the run goes on in igris serve
	actStopLeave // Stop run: stop it as x does, leave once it has stopped
	// Home's Attach (SPEC §15.6): the run view of the run igris serve
	// hosts here.
	actAttach
	// Several active tasks (SPEC §15.1, cards.go).
	actCardPrev   // [: select the previous card
	actCardNext   // ]: select the next card
	actCardPick   // a card or a switcher entry; the zone's option is the card's index
	actAnswerCard // a card's [Answer…]: its task's question, else the first in line
)

// modeActs maps the mode picker's actions to their modes, in the order
// the picker lists them (SPEC §15.5, since 1.7): the common modes first,
// then the ones More modes… adds.
var modeActs = []struct {
	act         action
	mode, label string
}{
	{actModePlan, engine.ModePlan, "plan — plan first, you approve, then it implements"},
	{actModeAuto, engine.ModeAuto, "auto — Claude Code asks only about risky actions"},
	{actModeAccept, engine.ModeAccept, "accept — edits are accepted"},
	{actModeManual, engine.ModeManual, "manual — every action asks, whatever Claude Code's default is"},
	{actModeDefault, engine.ModeDefault, "default — your normal permission prompts"},
	// More modes…: the plan modes that name the mode after approval
	// (SPEC §7.1) and skip permissions.
	{actModePlanDefault, engine.ModePlanDefault, "plan:default — plan, then default"},
	{actModePlanAccept, engine.ModePlanAccept, "plan:accept — plan, then accept"},
	{actModePlanAuto, engine.ModePlanAuto, "plan:auto — plan, then auto"},
	{actModeYolo, engine.ModeYolo, "yolo [SKIP PERMISSIONS] — no permission prompts"},
	{actModePlanYolo, engine.ModePlanYolo, "plan:yolo — then [SKIP PERMISSIONS]"},
}

// commonModes is how many of modeActs the picker shows before More modes….
const commonModes = 5

// moreModesLabel is the picker's last option until it is picked.
const moreModesLabel = "More modes…"

// modeOptions are the mode picker's options and the index of current's:
// the common modes and More modes…, or every mode when current is one of
// the others, so the current mode can be focused. -1 when current isn't
// listed ("" is As planned).
func modeOptions(current string) ([]option, int) {
	n, sel := commonModes, -1
	for i, m := range modeActs {
		if m.mode == current {
			sel = i
			n = max(n, i+1)
		}
	}
	if n > commonModes {
		n = len(modeActs)
	}
	var out []option
	for _, m := range modeActs[:n] {
		out = append(out, option{m.label, m.act})
	}
	if n < len(modeActs) {
		out = append(out, option{moreModesLabel, actModeMore})
	}
	return out, sel
}

// moreModes replaces d's More modes… with the modes it stands for and
// focuses the first of them.
func (d *dialog) moreModes() {
	i := slices.IndexFunc(d.options, func(o option) bool { return o.act == actModeMore })
	if i < 0 {
		return
	}
	var rest []option
	for _, m := range modeActs[commonModes:] {
		rest = append(rest, option{m.label, m.act})
	}
	d.options = append(d.options[:i:i], rest...)
	d.selected = i
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
	case actPlanApprove:
		return engine.Command{Kind: engine.CmdPlan, Plan: state.PlanAllow}, true
	case actPlanPane:
		return engine.Command{Kind: engine.CmdPlan, Plan: state.PlanPane}, true
	}
	return engine.Command{}, false
}

// shortcuts maps the keys of SPEC §15.3 to their actions.
var shortcuts = map[string]action{
	"o":      actOpen,
	"t":      actDetails,
	"m":      actMode,
	"M":      actTaskMode,
	"p":      actPause,
	"d":      actDone,
	"s":      actSkip,
	"r":      actRetry,
	"x":      actStopAsk,
	"S":      actSide,
	"[":      actCardPrev,
	"]":      actCardNext,
	"h":      actHistory,
	"y":      actCopy,
	"?":      actHelp,
	"q":      actQuit,
	"ctrl+c": actQuit,
}

// helpEntry is one line of the help page.
type helpEntry struct{ key, name, what string }

// helpActions are the run view's bindings the help page lists (SPEC
// §15.3, §15.5), a capital beside its lowercase.
var helpActions = []helpEntry{
	{"o", "Open session", "bring the current session's pane to the front"},
	{"[ ]", "Select card", "with several active tasks: the previous or next card; o d s r t M y act on its task"},
	{"t", "Details", "the selected task's full text and facts: the current one unless you picked another"},
	{"m", "Mode", "run mode for the sessions launched from now on; More modes… lists the plan:* modes and yolo, which needs the typed phrase"},
	{"M", "Task mode", "mode for the selected agent task's next session, over its Mode column (not for user tasks)"},
	{"p", "Pause / Resume", "pause after the current task; igris waits until you resume"},
	{"d", "Done", "mark the current task done (your decision: verify is skipped); a user task asks for an optional note"},
	{"s", "Skip", "skip the current task; asks for a reason and closes its session"},
	{"S", "Side session", "a Claude Code session of your own, not a task"},
	{"r", "Retry", "close the session and start fresh or continue the conversation"},
	{"x", "Stop", "stop igris after confirming; the session stays open"},
	{"y", "Copy", "copy claude --resume for the current session, the log line at the bottom when the log has the focus, or the igris report command on the end-of-run summary"},
	{"h", "History", "on the end-of-run summary: the History page"},
	{"?", "Help", "this page"},
	{"q", "Home / Quit", "leave the run view; sessions keep running, igris arise resumes"},
}

// helpFocus are the focus keys the help page lists (SPEC §15.5).
var helpFocus = []helpEntry{
	{"click", "", "a task for its details; the card's title or [t] Details for the current one; another card selects it"},
	{"tab", "", "move between the task list, the action bar and the log"},
	{"← →", "", "move along the action bar"},
	{"↑ ↓  j k", "", "move in lists and dialogs, scroll the log"},
	{"pgup pgdn", "", "scroll a page"},
	{"enter space", "", "activate the focused element: a button, a task's details, the whole log"},
	{"1…9", "", "pick a dialog option by its number"},
	{"esc", "", "close a dialog or page with its safe choice"},
	{"ctrl+c", "", "quit igris from anywhere; a run igris serve hosts goes on"},
}
