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
	actStop   // stop igris now (only offered inside dialogs so far)
	actAnswer // reopen the dialog of the pending question
	actClose  // close a dialog; its question stays pending
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

// shortcuts maps the keys of SPEC §15.3 that work so far to their actions.
var shortcuts = map[string]action{
	"o":      actOpen,
	"p":      actPause,
	"d":      actDone,
	"q":      actQuit,
	"ctrl+c": actQuit,
}
