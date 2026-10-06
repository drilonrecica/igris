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
)

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
	"p":      actPause,
	"d":      actDone,
	"s":      actSkip,
	"r":      actRetry,
	"x":      actStopAsk,
	"?":      actHelp,
	"q":      actQuit,
	"ctrl+c": actQuit,
}

// helpEntry is one line of the help page.
type helpEntry struct{ key, name, what string }

// helpActions are the actions the help page lists, in SPEC §15.3 order.
var helpActions = []helpEntry{
	{"o", "Open session", "bring the current session's pane to the front"},
	{"p", "Pause / Resume", "pause after the current task; igris waits until you resume"},
	{"d", "Done", "mark the current task done (your decision: verify is skipped)"},
	{"s", "Skip", "skip the current task; asks for a reason and closes its session"},
	{"r", "Retry", "close the session and start fresh or continue the conversation"},
	{"x", "Stop", "stop igris after confirming; the session stays open"},
	{"q", "Quit", "quit the TUI; sessions keep running, igris arise resumes"},
	{"?", "Help", "this page"},
}

// helpFocus are the focus keys the help page lists (SPEC §15.5).
var helpFocus = []helpEntry{
	{"tab", "", "move between the task list, the action bar and the log"},
	{"← →", "", "move along the action bar"},
	{"↑ ↓  j k", "", "move in lists and dialogs, scroll the log"},
	{"pgup pgdn", "", "scroll a page"},
	{"enter space", "", "activate the focused element"},
	{"1…9", "", "pick a dialog option by its number"},
	{"esc", "", "close a dialog or page with its safe choice"},
}
