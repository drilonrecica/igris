package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/report"
)

// notifySnap is the sinjal project with ntfy and discord set up for two
// events each.
func notifySnap() *report.Snapshot {
	s := homeSnap("ready")
	s.Config.Notify.Backend.Enabled = false
	s.Config.Notify.Ntfy.Events = []string{"needs_input", "phase_done"}
	s.Config.Notify.Discord.WebhookURL = "env:HOOK"
	s.Config.Notify.Discord.Events = []string{"needs_input"}
	return s
}

// notifyHome is a home on notifySnap with a screen size.
func notifyHome(svc *fakeServices, w, h int) *homeScreen {
	home := homeAt(&theme{}, w, h, homeFixture{snap: notifySnap(), doctorDone: true})
	home.svc = svc
	return home
}

// startNotify opens the screen over home and takes news until the test
// has ended.
func startNotify(t *testing.T, home *homeScreen) *notifyScreen {
	t.Helper()
	s := newNotifyScreen(home)
	s.Update(tea.WindowSizeMsg{Width: home.w, Height: home.h})
	s.Init()
	for !s.ended {
		s.Update(s.run.next())
	}
	return s
}

func TestNotifyDialogNamesChannels(t *testing.T) {
	svc := &fakeServices{}
	home := notifyHome(svc, 120, 40)
	home.activate(actNotify)
	d := home.dialog
	if d == nil || !home.notifying {
		t.Fatal("n didn't open the notify dialog")
	}
	if want := "Send 3 test messages to ntfy, discord?"; d.title != want {
		t.Errorf("title = %q, want %q", d.title, want)
	}
	if got := strings.Join(labels(d), " · "); got != "Send · Cancel" {
		t.Errorf("options = %s", got)
	}
	home.pick(actClose)
	if home.dialog != nil || home.notifying || svc.called("NotifyTest") != 0 {
		t.Error("Cancel sent messages or left the dialog open")
	}

	home.snap.Config.Notify.Backend.Enabled = true // herdr is reachable in the fixture
	home.activate(actNotify)
	if !strings.Contains(home.dialog.title, "ntfy, discord, herdr toast?") {
		t.Errorf("title = %q lacks the toast", home.dialog.title)
	}
	home.pick(actClose)

	home.snap.Config.Notify.Webhook.URL = "env:WH"
	home.snap.Config.Notify.Webhook.Events = []string{"phase_done"}
	home.activate(actNotify)
	if !strings.Contains(home.dialog.title, "ntfy, discord, webhook, herdr toast?") {
		t.Errorf("title = %q lacks the webhook", home.dialog.title)
	}
	home.pick(actClose)

	home.snap.Config.Notify.Slack.WebhookURL = "env:SL"
	home.snap.Config.Notify.Gotify.Server, home.snap.Config.Notify.Gotify.Token = "https://g", "env:GT"
	home.activate(actNotify)
	if !strings.Contains(home.dialog.title, "ntfy, discord, webhook, slack, gotify, herdr toast?") {
		t.Errorf("title = %q lacks slack and gotify", home.dialog.title)
	}
}

func TestNotifyRowsFillLive(t *testing.T) {
	release := make(chan struct{})
	svc := &fakeServices{notifyFn: func(_ context.Context, emit func(report.NotifyResult)) error {
		emit(report.NotifyResult{Event: "needs_input", Channel: "ntfy"})
		<-release
		emit(report.NotifyResult{Event: "needs_input", Channel: "discord", Err: "discord answered 400"})
		emit(report.NotifyResult{Event: "phase_done", Channel: "ntfy"})
		return nil
	}}
	home := notifyHome(svc, 100, 24)
	s := newNotifyScreen(home)
	s.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	s.Init()
	s.Update(s.run.next())
	v := s.View()
	for _, want := range []string{"Notify test · sending…", "needs_input", "ntfy", "✓ ok", "discord      · sending", "phase_done", "[Cancel]"} {
		if !strings.Contains(v, want) {
			t.Errorf("mid-test view lacks %q:\n%s", want, v)
		}
	}
	close(release)
	for !s.ended {
		s.Update(s.run.next())
	}
	v = s.View()
	for _, want := range []string{"1 failure", "⨯ FAILED: discord answered 400", "[Send again]", "[Close]"} {
		if !strings.Contains(v, want) {
			t.Errorf("final view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "sending") || strings.Contains(v, "[Cancel]") {
		t.Errorf("final view still sending:\n%s", v)
	}
}

func TestNotifyCancelAndSendAgain(t *testing.T) {
	svc := &fakeServices{notifyFn: func(ctx context.Context, emit func(report.NotifyResult)) error {
		emit(report.NotifyResult{Event: "needs_input", Channel: "ntfy"})
		<-ctx.Done()
		return ctx.Err()
	}}
	home := notifyHome(svc, 100, 24)
	s := newNotifyScreen(home)
	s.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	s.Init()
	s.Update(s.run.next())
	s.Update(adaptKey("esc")) // Cancel
	if v := s.View(); !strings.Contains(v, "cancelling…") {
		t.Errorf("view after Cancel:\n%s", v)
	}
	for !s.ended {
		s.Update(s.run.next())
	}
	v := s.View()
	for _, want := range []string{"cancelled", "· not sent", "were not sent"} {
		if !strings.Contains(v, want) {
			t.Errorf("cancelled view lacks %q:\n%s", want, v)
		}
	}

	s.Update(adaptKey("n")) // Send again starts a second test
	for svc.called("NotifyTest") < 2 {
		time.Sleep(time.Millisecond)
	}
	s.Update(s.run.next())
	if s.ended || !strings.Contains(s.View(), "sending…") {
		t.Errorf("Send again didn't start a new test:\n%s", s.View())
	}
	s.Update(adaptKey("esc"))
	for !s.ended {
		s.Update(s.run.next())
	}
	_, cmd := s.Update(adaptKey("esc"))
	if cmd == nil {
		t.Error("esc after the end didn't close the page")
	}
}

func TestNotifyNothingToSend(t *testing.T) {
	svc := &fakeServices{notifyErr: errors.New("no channel is set up for that\x1b[31m")}
	s := startNotify(t, notifyHome(svc, 100, 24))
	v := s.View()
	if !strings.Contains(v, "nothing sent") || !strings.Contains(v, "no channel is set up for that") || strings.Contains(v, "\x1b") {
		t.Errorf("view:\n%q", v)
	}
}

// Text the page shows is cleaned, and the secrets notify removed stay out.
func TestNotifyCleansErrors(t *testing.T) {
	svc := &fakeServices{notify: []report.NotifyResult{
		{Event: "needs_input", Channel: "discord", Err: "post https://[redacted]/: \x1b]0;pwned\x07boom"},
	}}
	s := startNotify(t, notifyHome(svc, 100, 24))
	v := s.View()
	if strings.Contains(v, "\x1b") || strings.Contains(v, "\x07") {
		t.Errorf("escape sequence in view:\n%q", v)
	}
	if !strings.Contains(v, "FAILED: post https://[redacted]/") {
		t.Errorf("view:\n%s", v)
	}
}

func TestNotifyFromHome(t *testing.T) {
	svc := &fakeServices{notify: []report.NotifyResult{{Event: "needs_input", Channel: "ntfy"}}}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) { return notifySnap(), nil }
	a, home := pollApp(t, svc)
	press(a, "n")
	if !home.notifying {
		t.Fatal("n didn't open the dialog")
	}
	press(a, "enter") // Send is the default
	s, ok := top(a).(*notifyScreen)
	if !ok || svc.called("NotifyTest") != 1 {
		t.Fatalf("top %T, NotifyTest called %d times", top(a), svc.called("NotifyTest"))
	}
	if v := a.View(); !strings.Contains(v, "all sent") && !strings.Contains(v, "not sent") {
		t.Errorf("view:\n%s", v)
	}
	press(a, "esc")
	if len(a.stack) != 1 || top(a) == s {
		t.Errorf("esc left %d screens", len(a.stack))
	}
}

func TestNotifyGolden(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 20}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("dialog_%dx%d", w, h), func(t *testing.T) {
			home := notifyHome(&fakeServices{}, w, h)
			home.activate(actNotify)
			view := home.View()
			checkFits(t, view, w, h)
			golden(t, fmt.Sprintf("notify_dialog_%dx%d", w, h), view)
		})
		t.Run(fmt.Sprintf("page_%dx%d", w, h), func(t *testing.T) {
			svc := &fakeServices{notify: []report.NotifyResult{
				{Event: "needs_input", Channel: "ntfy"},
				{Event: "needs_input", Channel: "discord", Err: "discord answered 400 Bad Request"},
				{Event: "phase_done", Channel: "ntfy"},
			}}
			s := startNotify(t, notifyHome(svc, w, h))
			view := s.View()
			checkFits(t, view, w, h)
			golden(t, fmt.Sprintf("notify_page_%dx%d", w, h), view)
		})
	}
}
