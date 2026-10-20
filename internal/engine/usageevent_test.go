package engine

import (
	"reflect"
	"testing"
	"time"
)

// The UI hears of an attempt's usage as it grows (SPEC §15.1): once per
// change, subagents included, and "unavailable" when nothing can be read.
func TestUsageEventsFeedTheCard(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn)
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		prev(ev)
		if ev.Kind != SessionOpened || ev.Task != "A-1" {
			return
		}
		s := ev.ClaudeSession
		respond(t, dir, s, ev.At.Add(time.Second), "msg_1", "claude-sonnet-5-5", 2, 30, 3, false)
		respond(t, dir, s, ev.At.Add(3*time.Second), "msg_sub", "claude-haiku-5-5", 5, 7, 1, true)
		h.clock.At(ev.At.Sub(t0)+15*time.Second, func() {
			respond(t, dir, s, h.clock.Now(), "msg_2", "claude-sonnet-5-5", 3, 40, 2, false)
		})
		h.signalAt(ev.At.Sub(t0)+30*time.Second, "A-1")
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got []Tokens
	for _, ev := range h.events {
		if ev.Kind == UsageRead && ev.Task == "A-1" {
			got = append(got, *ev.Tokens)
		}
	}
	if len(got) < 2 {
		t.Fatalf("usage events = %+v, want one per change", got)
	}
	for i := 1; i < len(got); i++ {
		if reflect.DeepEqual(got[i], got[i-1]) {
			t.Errorf("usage event %d repeats the one before: %+v", i, got[i])
		}
	}
	// Per response: input 2/3, cache read 100, cache write 10; the subagent
	// adds 5 + 100 + 10 in and 7 out.
	want := Tokens{In: 2 + 3 + 200 + 20 + 115, Out: 30 + 40 + 7, Models: []string{"claude-sonnet-5-5"}}
	if last := got[len(got)-1]; !reflect.DeepEqual(last, want) {
		t.Errorf("last usage event = %+v, want %+v", last, want)
	}
}

// A session whose hooks never reported a transcript says so once, at the
// attempt's end.
func TestUsageEventUnavailable(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn)
	h.autoSignalExcept("A-1")
	h.signalAt(30*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ev := h.event(UsageRead, "A-1")
	if ev.Tokens == nil || !ev.Tokens.Unavailable || ev.Tokens.In != 0 {
		t.Errorf("usage event = %+v", ev.Tokens)
	}
}
