package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/textsafe"
	"github.com/drilonrecica/igris/internal/update"
)

// Check for updates on home (SPEC §15.6, decisions.md V12-P4): `u` asks
// GitHub for the latest release, off the loop, and a dialog says what it
// found. A newer release installed with Homebrew or go install offers
// Update, which runs the package manager's command off the loop too; igris
// never replaces its own binary and never checks on its own. While a run
// holds this project's lock, Update is not offered. The run view and
// --no-tui never check.

// updateStep is where Check for updates is.
type updateStep int

const (
	updChecking updateStep = iota // asking GitHub
	updChecked                    // the check is back (or failed)
	updRunning                    // the install command runs
	updDone                       // the install command ended
)

// updateTarget is Check for updates while its dialog is open or its
// command runs. open says the dialog on screen is this one: closing it
// while the command runs leaves the command going, and the result opens
// the dialog again.
type updateTarget struct {
	step  updateStep
	open  bool
	check update.Check
	err   error // why the check failed
	res   update.Result
	stop  context.CancelFunc // cancels the check
}

// Messages for the check and the command.
type (
	updateCheckedMsg struct {
		c   update.Check
		err error
	}
	updateDoneMsg struct{ res update.Result }
)

// updateNote is what an update means for runs elsewhere (SPEC §15.6).
const updateNote = "Runs in other projects keep going; their sessions then run the new igris for igris done."

// openUpdate starts a check, or shows the dialog of the update that runs.
func (m *homeScreen) openUpdate() tea.Cmd {
	if m.svc == nil || m.dialog != nil {
		return nil
	}
	if t := m.updating; t != nil && t.step == updRunning {
		t.open = true
		m.dialog = m.updateDialog()
		return nil
	}
	return m.checkUpdate()
}

// checkUpdate asks GitHub for the latest release, off the loop.
func (m *homeScreen) checkUpdate() tea.Cmd {
	ctx, stop := context.WithCancel(m.ctx)
	m.updating = &updateTarget{step: updChecking, open: true, stop: stop}
	m.dialog = m.updateDialog()
	svc := m.svc
	return async(m, "update", func() tea.Msg {
		c, err := svc.CheckUpdate(ctx)
		return updateCheckedMsg{c, err}
	})
}

// updateBlocked says why Update isn't offered now, "" when it is: a run
// holds this project's lock (this igris's own, or another's for a
// read-only home).
func (m *homeScreen) updateBlocked() string {
	if m.run != nil || (m.snap != nil && m.snap.Lock.Alive) {
		return "a run holds this project"
	}
	return ""
}

// updateChecked shows what the check found.
func (m *homeScreen) updateChecked(msg updateCheckedMsg) tea.Cmd {
	t := m.updating
	if t == nil || t.step != updChecking {
		return nil // cancelled
	}
	t.step, t.check, t.err = updChecked, msg.c, msg.err
	if msg.err != nil {
		t.err = cleanError(msg.err)
	}
	m.showUpdate()
	return nil
}

// updatePick runs what the Check for updates dialog chose.
func (m *homeScreen) updatePick(a action) tea.Cmd {
	t := m.updating
	switch a {
	case actNone:
		return nil
	case actUpdateCheck:
		m.dialog = nil
		return m.checkUpdate()
	case actUpdateRun:
		if t.step != updChecked || t.check.Status != update.Newer || len(t.check.Install.Argv) == 0 || m.updateBlocked() != "" {
			return nil
		}
		return m.runUpdate()
	case actQuit:
		m.updating, m.dialog = nil, nil
		return quitApp
	case actCopy:
		if !hasOption(m.dialog, actCopy) {
			return nil
		}
		m.setStatus(copyNotice(m.out, strings.Join(t.check.Install.Steps, "\n")) + ": the steps to update igris")
		return nil
	}
	// Close, Not now, Cancel.
	m.dialog = nil
	if t.step == updRunning {
		t.open = false
		m.setStatus("updating igris… (u shows it)")
		return nil
	}
	if t.stop != nil {
		t.stop()
	}
	m.updating = nil
	return nil
}

// runUpdate runs the install command off the loop. It isn't cancelled
// when igris quits: a package manager stopped halfway is worse than
// waiting, so the app waits for it (bounded by the command's timeout).
func (m *homeScreen) runUpdate() tea.Cmd {
	t := m.updating
	t.step = updRunning
	m.dialog = m.updateDialog()
	ctx, svc, c := context.WithoutCancel(m.ctx), m.svc, t.check
	done := make(chan struct{})
	return tea.Batch(track(&bgWork{stop: func() {}, done: done}), async(m, "update-run", func() tea.Msg {
		defer close(done)
		return updateDoneMsg{svc.Update(ctx, c)}
	}))
}

// updateDone shows how the command ended: in its dialog when that is open
// or nothing else is, else on the status line.
func (m *homeScreen) updateDone(msg updateDoneMsg) tea.Cmd {
	t := m.updating
	if t == nil || t.step != updRunning {
		return nil
	}
	t.step, t.res = updDone, msg.res
	if msg.res.Err != nil {
		m.setStatus("update failed: " + textsafe.Line(msg.res.Err.Error()))
	} else {
		m.setStatus(textsafe.Line(msg.res.Message) + " Quit and start igris again.")
	}
	if t.open || m.dialog == nil {
		m.showUpdate()
	} else {
		m.updating = nil
	}
	return nil
}

// showUpdate puts the dialog for the update's state on screen.
func (m *homeScreen) showUpdate() {
	m.updating.open = true
	m.dialog = m.updateDialog()
}

// updateDialog is the dialog for the update's state.
func (m *homeScreen) updateDialog() *dialog {
	t := m.updating
	d := &dialog{cancel: actClose, pre: true, wide: true, options: []option{{"Close", actClose}}}
	c := t.check
	switch t.step {
	case updChecking:
		d.title = "Check for updates"
		d.detail = "Asking GitHub for the latest igris release…"
		d.options = []option{{"Cancel", actClose}}
	case updRunning:
		d.title = "Updating igris…"
		d.detail = "Running: " + c.Install.Command() + "\nThis can take a few minutes (at most 10). Closing this dialog leaves it running; quitting igris waits for it."
	case updDone:
		if t.res.Err != nil {
			d.title = "Update failed"
			d.detail = updateFailure(t.res)
			break
		}
		d.title = "igris updated"
		d.detail = textsafe.Line(t.res.Message) + " Quit and start igris again.\n\n" + updateNote
		d.options = []option{{"Quit igris", actQuit}, {"Close", actClose}}
	default: // updChecked
		m.checkedDialog(d)
	}
	return d
}

// checkedDialog fills d in with what the check found.
func (m *homeScreen) checkedDialog(d *dialog) {
	t := m.updating
	c := t.check
	if t.err != nil {
		d.title = "Check for updates failed"
		d.detail = t.err.Error() // a failed request says to check the connection
		d.options = []option{{"Try again", actUpdateCheck}, {"Close", actClose}}
		return
	}
	current := textsafe.Line(c.Current)
	switch c.Status {
	case update.Dev:
		d.title = "Development build"
		d.detail = "This igris (" + current + ") is a development build, so igris doesn't update it. The latest release is " + c.Latest + ":\n" + c.URL()
		return
	case update.UpToDate:
		d.title = "igris is up to date"
		d.detail = "igris " + current + " is the latest release."
		if update.Compare(c.Current, c.Latest) > 0 {
			d.detail = "igris " + current + " is newer than the latest release, " + c.Latest + "."
		}
		return
	}
	d.title = "igris " + c.Latest + " is out"
	in := c.Install
	var b strings.Builder
	b.WriteString(current + " → " + c.Latest)
	if in.Method == update.Archive {
		b.WriteString(" · igris can't tell how it was installed, so it runs nothing.\n" + c.URL() + "\nDownload, verify and install it by hand:\n")
		for _, s := range in.Steps {
			b.WriteString("  " + s + "\n")
		}
		b.WriteString("The ssh-keygen line must print Good \"file\" signature for igris.")
		d.detail = b.String()
		// The steps wrap at the dialog's width, so copying them from the
		// terminal would break them; y copies them whole (OSC 52).
		d.options = []option{{"Copy steps (y)", actCopy}, {"Close", actClose}}
		return
	}
	b.WriteString(" · installed with " + in.Method.String() + "\n")
	blocked := m.updateBlocked()
	if blocked != "" {
		b.WriteString("Update after the run ends: " + blocked + ". The update runs:\n")
	} else {
		b.WriteString("Update runs:\n")
	}
	b.WriteString("  " + in.Command() + "\n\n" + updateNote + "\n" + c.URL())
	d.detail = b.String()
	if blocked == "" {
		d.options = []option{{"Update", actUpdateRun}, {"Not now", actClose}}
	}
}

// updateFailure is a failed command's dialog text: the error, the end of
// its output and the hint.
func updateFailure(res update.Result) string {
	var b strings.Builder
	b.WriteString(textsafe.Line(res.Err.Error()))
	if len(res.Tail) > 0 {
		b.WriteString("\n\n")
		for _, l := range res.Tail {
			b.WriteString("  " + textsafe.Line(l) + "\n")
		}
	}
	if res.Hint != "" {
		b.WriteString("\n" + textsafe.Line(res.Hint))
	}
	return strings.TrimRight(b.String(), "\n")
}
