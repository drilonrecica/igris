package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
)

// The app (SPEC §15, §15.6) is one Bubble Tea program holding a stack of
// screens: home at the bottom, pages, the start-run wizard, the run view
// and the adapt review pushed on top. Screens are tea.Models (pointers, so
// the app can tell them apart); they move between each other with the
// messages below and leave everything else to the screen on top.

// AppOptions configure the app.
type AppOptions struct {
	Services Services
	// Start is the screen the app opens on: Home{}, or Wizard{...} for
	// `igris arise` with nothing to resume. Nil means Home{}.
	Start Start
	Mouse bool // [tui] mouse
	// Theme is [tui] theme: "dark", "light", or "auto" (also "") to go by
	// the terminal's background, detected once for the whole app.
	Theme      string
	RankColors map[string]string // [tui.rank_colors]
	// Poll is how often home looks at the project's files for changes:
	// 0 means every 2 s, a negative value turns the poll off.
	Poll time.Duration
	// Out is where the clipboard sequence (OSC 52) goes; nil means
	// os.Stdout.
	Out io.Writer
	// Ending, when set, is called once the program has ended, before App
	// waits for a run still going to stop; waiting says there is one.
	Ending func(waiting bool)
}

// Start is where the app opens: Home or Wizard.
type Start interface{ start() }

// Home opens the home screen.
type Home struct{}

// Wizard opens the start-run wizard over home, prefilled with arise's
// flags.
type Wizard struct {
	Mode        string // --mode; "" is as planned
	Through     string // --through
	ForceUnlock bool   // --force-unlock
}

func (Home) start()   {}
func (Wizard) start() {}

// AppResult is how the app ended.
type AppResult struct {
	// Stopped says a run was going when the app ended, so igris stopped
	// it; its session keeps running and `igris arise` resumes.
	Stopped bool
	Res     engine.Result // the run's result, when there was a run
	RunErr  error         // the run's error, when there was a run
}

// App shows the app until the owner quits or ctx ends. A run going at the
// end is stopped and waited for, so its lock is released before App
// returns.
func App(ctx context.Context, o AppOptions) (AppResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Bubble Tea asked the terminal for its background when the process
	// started; this is the only time igris looks at it.
	dark := lipgloss.HasDarkBackground()
	th := newTheme(lipgloss.NewRenderer(os.Stdout), darkTheme(o.Theme, func() bool { return dark }), o.RankColors)
	a := newApp(ctx, o, th)
	a.dark = dark
	// Focus reports make home look at the project again when the owner
	// comes back to the terminal.
	opts := append(programOptions(ctx, o.Mouse), tea.WithReportFocus())
	final, err := tea.NewProgram(a, opts...).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		err = nil // ctx ended: not a failure of the TUI
	}
	if f, ok := final.(*appModel); ok {
		a = f
	}
	if o.Ending != nil {
		o.Ending(a.waiting())
	}
	return a.finish(), err
}

// Minimum sizes (SPEC §15.6): below tooSmall* only a line saying so is
// drawn; the layouts need usable* to be comfortable.
const (
	tooSmallW, tooSmallH = 40, 12
	usableW, usableH     = 50, 20
)

// Messages that move between screens. Screens return them from commands
// (push, pop, replace, showHelp, quitApp).
type (
	pushMsg    struct{ s tea.Model }
	replaceMsg struct{ s tea.Model }
	// popMsg closes the top screen; msg, if any, goes to the screen that
	// comes back (e.g. what a review decided). Popping home quits.
	popMsg struct{ msg tea.Msg }
	// closeMsg closes screen s and everything above it (the run view when
	// its run is over), as a pop would.
	closeMsg struct{ s tea.Model }
	helpMsg  struct{} // show the help page for the top screen
	quitMsg  struct{} // quit the app; a run going is stopped
	// runMsg tells the app which run is going (nil: none any more), so
	// quitting stops it and App waits for it.
	runMsg struct{ h *runHandle }
	// bgMsg tells the app about work going on off the loop that holds the
	// run lock (adapt), so quitting stops it and App waits for it.
	bgMsg struct{ w *bgWork }
	// ownedMsg is a message for one screen wherever it is on the stack
	// (the poll's tick); it is dropped when the screen is gone.
	ownedMsg struct {
		owner tea.Model
		msg   tea.Msg
	}
)

func push(s tea.Model) tea.Cmd    { return func() tea.Msg { return pushMsg{s} } }
func replace(s tea.Model) tea.Cmd { return func() tea.Msg { return replaceMsg{s} } }
func pop(msg tea.Msg) tea.Cmd     { return func() tea.Msg { return popMsg{msg} } }
func closeScreen(s tea.Model) tea.Cmd {
	return func() tea.Msg { return closeMsg{s} }
}
func showHelp() tea.Msg { return helpMsg{} }
func quitApp() tea.Msg  { return quitMsg{} }

// runHandle is the one engine the app holds while its run view is on the
// stack.
type runHandle struct {
	feed *Feed
	stop context.CancelFunc // cancels the run's context
}

// stopNow stops the run and reports whether it was still going.
func (h *runHandle) stopNow() bool {
	going := true
	select {
	case <-h.feed.Ended():
		going = false
	default:
	}
	h.stop()
	return going
}

// bgWork is work off the loop that must end before the app does: stop
// asks it to end, done is closed once it has.
type bgWork struct {
	stop context.CancelFunc
	done <-chan struct{}
}

// track hands w to the app; see bgMsg.
func track(w *bgWork) tea.Cmd { return func() tea.Msg { return bgMsg{w} } }

// Async work. A screen asks for slow work with async; the app numbers the
// request and hands the result to the screen that asked, unless the screen
// has left the stack or asked again with the same key since (the newer
// answer counts, the stale one is dropped).
type (
	asyncReq struct {
		owner tea.Model
		key   string
		fn    func() tea.Msg
	}
	asyncDone struct {
		owner tea.Model
		key   string
		seq   uint64
		msg   tea.Msg
	}
	asyncKey struct {
		owner tea.Model
		key   string
	}
)

// async runs fn off the program's loop for owner, a screen on the stack;
// fn's message reaches owner only if it is the newest request for key.
func async(owner tea.Model, key string, fn func() tea.Msg) tea.Cmd {
	return func() tea.Msg { return asyncReq{owner, key, fn} }
}

// keyHelper is a screen that lists its keys for the help page.
type keyHelper interface {
	helpKeys() []helpEntry
}

// appModel is the program's model: the screen stack and what all screens
// share.
type appModel struct {
	ctx   context.Context
	o     AppOptions
	stack []tea.Model // home first; the top screen is last
	th    *theme      // detected once; every screen draws with it
	dark  bool        // the terminal's background, as detected at start
	w, h  int
	run   *runHandle // the run going, if any
	bg    []*bgWork  // work that may still hold the lock
	// stopped says quitting stopped a run that was still going; quitting
	// says the app is on its way out.
	stopped, quitting bool

	seq    uint64              // the last async request's number
	latest map[asyncKey]uint64 // the newest request per screen and key
}

func newApp(ctx context.Context, o AppOptions, th *theme) *appModel {
	if o.Start == nil {
		o.Start = Home{}
	}
	a := &appModel{ctx: ctx, o: o, th: th, dark: true, w: 80, h: 24, latest: map[asyncKey]uint64{}}
	// The wizard (Start: Wizard) opens over home once home has read the
	// project; the app starts on home either way.
	home := newHome(ctx, o.Services, th)
	home.out = o.Out
	if home.poll = o.Poll; home.poll == 0 {
		home.poll = pollEvery
	}
	if w, ok := o.Start.(Wizard); ok {
		home.pending = &w
	}
	// Home tells the app about the engine it starts at once, on the loop,
	// so a quit that follows always stops it and waits for it.
	// Once the app quits, the run it stopped stays for finish to report,
	// whatever home hears about its end meanwhile.
	home.setRun = func(h *runHandle) {
		if !a.quitting {
			a.run = h
		}
	}
	a.stack = []tea.Model{home}
	return a
}

func (a *appModel) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(a.stack))
	for _, s := range a.stack {
		cmds = append(cmds, s.Init())
	}
	return tea.Batch(cmds...)
}

func (a *appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		cmds := make([]tea.Cmd, len(a.stack))
		for i := range a.stack {
			cmds[i] = a.send(i, msg)
		}
		return a, tea.Batch(cmds...)
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return a, a.quit()
		}
		if a.tooSmall() {
			if msg.String() == "q" {
				return a, a.quit()
			}
			return a, nil // nothing else is on screen to act on
		}
	case tea.MouseMsg:
		if a.tooSmall() {
			return a, nil
		}
	case pushMsg:
		return a, a.push(msg.s)
	case replaceMsg:
		a.drop()
		return a, a.push(msg.s)
	case popMsg:
		if len(a.stack) <= 1 {
			return a, a.quit()
		}
		a.drop()
		// What the screen did may have changed the files home shows.
		back := a.send(len(a.stack)-1, refreshMsg{})
		if msg.msg == nil {
			return a, back
		}
		return a, tea.Batch(a.send(len(a.stack)-1, msg.msg), back)
	case closeMsg:
		i := a.index(msg.s)
		if i <= 0 {
			return a, nil // gone already; home is never closed this way
		}
		for len(a.stack) > i {
			a.drop()
		}
		return a, a.send(len(a.stack)-1, refreshMsg{})
	case tea.FocusMsg:
		if len(a.stack) == 0 {
			return a, nil
		}
		return a, a.send(0, refreshMsg{})
	case ownedMsg:
		if i := a.index(msg.owner); i >= 0 {
			return a, a.send(i, msg.msg)
		}
		return a, nil
	case configMsg:
		return a, a.applyConfig(msg.tui)
	case helpMsg:
		return a, a.push(a.helpPage())
	case quitMsg:
		return a, a.quit()
	case runMsg:
		a.run = msg.h
		return a, nil
	case bgMsg:
		a.bg = append(slices.DeleteFunc(a.bg, (*bgWork).over), msg.w)
		if a.quitting {
			msg.w.stop()
		}
		return a, nil
	case asyncReq:
		a.seq++
		seq, fn := a.seq, msg.fn
		a.latest[asyncKey{msg.owner, msg.key}] = seq
		owner, key := msg.owner, msg.key
		return a, func() tea.Msg { return asyncDone{owner, key, seq, fn()} }
	case asyncDone:
		k := asyncKey{msg.owner, msg.key}
		i := a.index(msg.owner)
		if i < 0 || a.latest[k] != msg.seq {
			return a, nil // stale: the screen is gone or asked again
		}
		delete(a.latest, k)
		return a, a.send(i, msg.msg)
	}
	if len(a.stack) == 0 {
		return a, nil
	}
	return a, a.send(len(a.stack)-1, msg)
}

// applyConfig applies a changed [tui] section live: the colors are redrawn
// in place (every screen holds the same theme) and the mouse is switched
// on or off.
func (a *appModel) applyConfig(c config.TUI) tea.Cmd {
	if a.th != nil && a.th.r != nil && (c.Theme != a.o.Theme || !maps.Equal(c.RankColors, a.o.RankColors)) {
		*a.th = *newTheme(a.th.r, darkTheme(c.Theme, func() bool { return a.dark }), c.RankColors)
	}
	a.o.Theme, a.o.RankColors = c.Theme, c.RankColors
	if c.Mouse == a.o.Mouse {
		return nil
	}
	a.o.Mouse = c.Mouse
	if c.Mouse {
		return tea.EnableMouseCellMotion
	}
	return tea.DisableMouse
}

// send gives msg to the i-th screen.
func (a *appModel) send(i int, msg tea.Msg) tea.Cmd {
	s, cmd := a.stack[i].Update(msg)
	a.stack[i] = s
	return cmd
}

// push puts s on top: it gets the current size first, then starts.
func (a *appModel) push(s tea.Model) tea.Cmd {
	if s == nil {
		return nil
	}
	a.stack = append(a.stack, s)
	size := a.send(len(a.stack)-1, tea.WindowSizeMsg{Width: a.w, Height: a.h})
	return tea.Batch(size, a.stack[len(a.stack)-1].Init())
}

// drop removes the top screen and forgets what it was waiting for.
func (a *appModel) drop() {
	if len(a.stack) == 0 {
		return
	}
	top := a.stack[len(a.stack)-1]
	a.stack = a.stack[:len(a.stack)-1]
	if a.index(top) >= 0 {
		return // the same screen is still lower on the stack
	}
	for k := range a.latest {
		if k.owner == top {
			delete(a.latest, k)
		}
	}
}

// index is where s is on the stack, from the top; -1 when it isn't.
func (a *appModel) index(s tea.Model) int {
	for i := len(a.stack) - 1; i >= 0; i-- {
		if a.stack[i] == s {
			return i
		}
	}
	return -1
}

// quit ends the program. A run going is told to stop now; App waits for
// it once the terminal is back.
func (a *appModel) quit() tea.Cmd {
	a.quitting = true
	for _, w := range a.bg {
		w.stop()
	}
	if a.run != nil {
		a.stopped = a.stopped || a.run.stopNow()
	}
	return tea.Quit
}

// waiting says the app holds a run that has not ended yet.
func (a *appModel) waiting() bool {
	if a.run == nil {
		return false
	}
	select {
	case <-a.run.feed.Ended():
		return false
	default:
		return true
	}
}

// finish stops the run and the work that are still going and waits for
// them to end, so the lock is released.
func (a *appModel) finish() AppResult {
	for _, w := range a.bg {
		w.stop()
		<-w.done
	}
	if a.run == nil {
		return AppResult{}
	}
	r := AppResult{Stopped: a.run.stopNow() || a.stopped}
	<-a.run.feed.Ended()
	select {
	case <-a.run.feed.Started():
	default:
		return AppResult{} // it was still starting: nothing ran
	}
	r.Res, r.RunErr = a.run.feed.Result()
	return r
}

// over says w has ended.
func (w *bgWork) over() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

func (a *appModel) tooSmall() bool { return a.w < tooSmallW || a.h < tooSmallH }

func (a *appModel) View() string {
	if a.tooSmall() {
		lines := wrap(fmt.Sprintf("igris — terminal too small (need %d×%d, now %d×%d) · q quits", usableW, usableH, a.w, a.h), a.w)
		return strings.Join(lines[:min(len(lines), max(a.h, 1))], "\n")
	}
	if len(a.stack) == 0 {
		return ""
	}
	return a.stack[len(a.stack)-1].View()
}

// helpPage is the help for the top screen: its keys, then the keys every
// screen shares.
func (a *appModel) helpPage() tea.Model {
	var keys []helpEntry
	if len(a.stack) > 0 {
		if kh, ok := a.stack[len(a.stack)-1].(keyHelper); ok {
			keys = kh.helpKeys()
		}
	}
	th := a.th
	return newPageScreen(th, &page{title: "Help · esc closes", body: func(w int) []string {
		out := wrap("Actions: click a button, focus it and press enter, or press its key.", w)
		out = append(out, helpTable(th, keys, w)...)
		out = append(out, "", th.paint(lookTitle, "Focus"))
		return append(out, helpTable(th, appFocus, w)...)
	}})
}

// appFocus are the keys every screen of the app shares (SPEC §15.5,
// §15.6).
var appFocus = []helpEntry{
	{"click", "", "activate a button or select a row; click a row again to open it"},
	{"tab", "", "move between the screen's regions"},
	{"← →", "", "move along the action bar"},
	{"↑ ↓  j k", "", "move in lists and dialogs"},
	{"pgup pgdn", "", "scroll a page"},
	{"enter space", "", "activate the focused element"},
	{"1…9", "", "pick a dialog option by its number"},
	{"esc", "", "close a dialog, else go back"},
	{"q", "", "go back; on home, quit"},
	{"ctrl+c", "", "quit igris from anywhere; a run stops, its session keeps running"},
}
