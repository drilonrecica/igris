package tui

import (
	"context"
	"slices"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Notify test in the app (SPEC §10, §15.6): a dialog names the channels
// and asks first; the page then lists one row per event and channel and
// fills each in as its delivery ends. Cancel cancels the context. The
// service sends nothing but the test samples, and notify has already
// removed the secrets from the errors; the page cleans the text.

// notifyChannels are the channels the test sends to, in the page's order.
var notifyChannels = []string{"ntfy", "discord", "backend"}

// channelLabel names a channel as the page shows it: the backend's toast
// by the backend's name (herdr, tmux).
func (m *homeScreen) channelLabel(name string) string {
	if name == "backend" {
		return m.backendName() + " toast"
	}
	return name
}

// notifyRow is one event on one channel. state is the delivery's
// outcome; until then it is sending.
type notifyRow struct {
	event, channel string
	state          notifyState
	err            string
}

type notifyState int

const (
	notifySending notifyState = iota
	notifyOK
	notifyFailed
	notifyUnsent // the test ended before this delivery did
)

// plannedNotifyRows are the deliveries the test is expected to make for
// the project's channels, in event order: ntfy and discord with their
// events, the herdr toast with the default ones when herdr is there. A
// delivery the service makes that isn't listed is added when it arrives.
func (m *homeScreen) plannedNotifyRows() []notifyRow {
	if m.snap == nil || m.snap.Config == nil {
		return nil
	}
	n := m.snap.Config.Notify
	events := map[string][]string{}
	if n.Ntfy.Topic != "" {
		events["ntfy"] = n.Ntfy.Events
	}
	if n.Discord.WebhookURL != "" {
		events["discord"] = n.Discord.Events
	}
	if n.Backend.Enabled && m.herdr() {
		for _, ev := range notify.DefaultEvents {
			events["backend"] = append(events["backend"], string(ev))
		}
	}
	var rows []notifyRow
	for _, ev := range notify.AllEvents {
		for _, ch := range notifyChannels {
			if slices.Contains(events[ch], string(ev)) {
				rows = append(rows, notifyRow{event: string(ev), channel: ch})
			}
		}
	}
	return rows
}

// openNotify asks before test messages are sent, naming the channels.
func (m *homeScreen) openNotify() tea.Cmd {
	if m.svc == nil || m.snap == nil || m.notifying || m.dialog != nil {
		return nil
	}
	rows := m.plannedNotifyRows()
	var names []string
	for _, ch := range notifyChannels {
		if slices.ContainsFunc(rows, func(r notifyRow) bool { return r.channel == ch }) {
			names = append(names, m.channelLabel(ch))
		}
	}
	title := "Send test messages?"
	if len(names) > 0 {
		title = "Send " + plural(len(rows), "test message") + " to " + strings.Join(names, ", ") + "?"
	}
	m.notifying = true
	m.dialog = &dialog{
		title:   title,
		detail:  "One sample per event is sent on each channel; a failed one is retried once.",
		options: []option{{"Send", actNotifySend}, {"Cancel", actClose}},
		cancel:  actClose,
	}
	return nil
}

// notifyPick runs what the Notify dialog chose.
func (m *homeScreen) notifyPick(a action) tea.Cmd {
	if a == actNone {
		return nil
	}
	m.notifying, m.dialog = false, nil
	if a == actNotifySend {
		return push(newNotifyScreen(m))
	}
	return nil
}

// ---- the page ----

type notifyNewsMsg struct {
	results []report.NotifyResult
	ended   bool
	err     error
}

// notifyScreen fills the rows in as the deliveries end.
type notifyScreen struct {
	home  *homeScreen
	w, h  int
	p     *page
	zones zones

	run     *notifyRun
	stop    context.CancelFunc
	rows    []notifyRow
	ended   bool
	stopped bool  // Cancel was pressed
	err     error // why nothing could be sent
}

func newNotifyScreen(home *homeScreen) *notifyScreen {
	s := &notifyScreen{home: home, w: 80, h: 24}
	s.p = &page{body: s.body}
	return s
}

// Init starts the test off the loop.
func (s *notifyScreen) Init() tea.Cmd {
	h := s.home
	ctx, stop := context.WithCancel(h.ctx)
	s.stop, s.run, s.rows, s.ended, s.stopped, s.err = stop, newNotifyRun(), h.plannedNotifyRows(), false, false, nil
	svc, r := h.svc, s.run
	go func() {
		err := svc.NotifyTest(ctx, r.add)
		r.end(err)
		stop()
	}()
	return s.wait()
}

// wait delivers the next news to this screen, wherever it is on the stack.
func (s *notifyScreen) wait() tea.Cmd {
	r := s.run
	return func() tea.Msg { return ownedMsg{s, r.next()} }
}

func (s *notifyScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"n", "Send again", "send the test messages again, when the last test is over"},
		{"esc q", "Cancel / Close", "cancel a test that is still sending; close the page once it is over"},
		{"↑ ↓ pgup pgdn", "Scroll", "the rows"},
	}
}

func (s *notifyScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
	case notifyNewsMsg:
		return s, s.news(msg)
	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "n":
			return s, s.act(actNotify)
		case "esc", "q":
			return s, s.act(actClose)
		case "?":
			return s, showHelp
		case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
			s.p.key(k)
		}
	case tea.MouseMsg:
		if a, ok := pageMouse(s.p, s.zones, msg); ok {
			return s, s.act(a)
		}
	}
	return s, nil
}

// act runs a footer action: Send again, or Cancel / Close.
func (s *notifyScreen) act(a action) tea.Cmd {
	switch a {
	case actNotify:
		if s.ended {
			return s.Init()
		}
	case actClose:
		if s.ended {
			return pop(nil)
		}
		s.stopped = true
		s.stop()
	}
	return nil
}

// news puts the deliveries that ended into their rows.
func (s *notifyScreen) news(msg notifyNewsMsg) tea.Cmd {
	for _, res := range msg.results {
		s.settle(res)
	}
	if !msg.ended {
		return s.wait()
	}
	s.ended, s.err = true, msg.err
	for i := range s.rows {
		if s.rows[i].state == notifySending {
			s.rows[i].state = notifyUnsent
		}
	}
	return nil
}

// settle records res on its row, adding the row if it wasn't expected.
func (s *notifyScreen) settle(res report.NotifyResult) {
	i := slices.IndexFunc(s.rows, func(r notifyRow) bool { return r.event == res.Event && r.channel == res.Channel })
	if i < 0 {
		s.rows = append(s.rows, notifyRow{event: res.Event, channel: res.Channel})
		i = len(s.rows) - 1
	}
	r := &s.rows[i]
	r.state, r.err = notifyOK, ""
	if res.Err != "" {
		r.state, r.err = notifyFailed, textsafe.Line(res.Err)
	}
}

func (s *notifyScreen) View() string {
	title := "Notify test"
	switch {
	case !s.ended && s.stopped:
		title += " · cancelling…"
	case !s.ended:
		title += " · sending…"
	default:
		title += " · " + s.summary()
	}
	s.p.title = title
	s.p.buttons = nil
	s.p.closeLabel = "Cancel"
	if s.ended {
		s.p.buttons = []pageButton{{"Send again", "Again", actNotify}}
		s.p.closeLabel = ""
	}
	lines, z := s.p.render(s.home.th, s.w, s.h)
	s.zones = z
	return strings.Join(lines, "\n")
}

// summary says how the test ended, in words.
func (s *notifyScreen) summary() string {
	var ok, failed int
	for _, r := range s.rows {
		switch r.state {
		case notifyOK:
			ok++
		case notifyFailed:
			failed++
		}
	}
	switch {
	case s.err != nil && ok+failed == 0:
		return "nothing sent"
	case failed > 0:
		return plural(failed, "failure")
	case s.stopped:
		return "cancelled"
	}
	return "all sent"
}

func (s *notifyScreen) body(w int) []string {
	th := s.home.th
	var out []string
	if len(s.rows) == 0 && s.err == nil {
		out = append(out, th.paint(lookDim, fit("starting the test…", w)))
	}
	evW := 0
	for _, r := range s.rows {
		evW = max(evW, textWidth(r.event))
	}
	for _, r := range s.rows {
		label := s.home.channelLabel(r.channel)
		prefix := "  " + pad(r.event, evW) + "  " + pad(label, 11) + "  "
		switch r.state {
		case notifySending:
			out = append(out, prefix+th.paint(lookDim, "· sending"))
		case notifyOK:
			out = append(out, prefix+th.paint(lookAccent, glyphs[plan.Done]+" ok"))
		case notifyUnsent:
			out = append(out, prefix+th.paint(lookDim, "· not sent"))
		default:
			out = append(out, painted(th, lookAlert, prefix, glyphs[plan.Blocked]+" FAILED: "+r.err, w)...)
		}
	}
	if s.err != nil {
		out = append(out, "")
		out = append(out, painted(th, lookAlert, "", s.err.Error(), w)...)
	}
	if s.stopped && s.ended {
		out = append(out, "", th.paint(lookDim, "Cancelled: the rows still sending were not sent."))
	}
	return out
}

// notifyRun carries the deliveries from the service's goroutine to the
// screen: add and end never block, next waits for news.
type notifyRun struct {
	mu      sync.Mutex
	results []report.NotifyResult
	final   notifyNewsMsg
	wake    chan struct{}
	done    chan struct{}
}

func newNotifyRun() *notifyRun {
	return &notifyRun{wake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (r *notifyRun) add(res report.NotifyResult) {
	r.mu.Lock()
	r.results = append(r.results, res)
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// end records how the test ended; called once.
func (r *notifyRun) end(err error) {
	if err != nil {
		err = cleanError(err)
	}
	r.mu.Lock()
	r.final = notifyNewsMsg{ended: true, err: err}
	r.mu.Unlock()
	close(r.done)
}

func (r *notifyRun) next() notifyNewsMsg {
	select {
	case <-r.wake:
	case <-r.done:
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	msg := notifyNewsMsg{results: r.results}
	r.results = nil
	select {
	case <-r.done:
		f := r.final
		f.results = msg.results
		return f
	default:
		return msg
	}
}

// cleanError is err with its text cleaned of escape sequences.
func cleanError(err error) error { return cleanedError(textsafe.Line(err.Error())) }

type cleanedError string

func (e cleanedError) Error() string { return string(e) }
