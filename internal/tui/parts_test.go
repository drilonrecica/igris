package tui

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
)

func TestZones(t *testing.T) {
	var z zones
	z.add(rect{0, 0, 10, 10}, target{region: regionLog})
	z.add(rect{2, 2, 3, 1}, target{act: actPause})
	z.add(rect{0, 0, 0, 1}, target{act: actQuit}) // empty: ignored
	var sub zones
	sub.add(rect{0, 1, 4, 1}, target{act: actOption, option: 1})
	z.merge(sub, 20, 5)
	tests := []struct {
		x, y int
		want target
		ok   bool
	}{
		{0, 0, target{region: regionLog}, true},
		{2, 2, target{act: actPause}, true},
		{4, 2, target{act: actPause}, true},
		{5, 2, target{region: regionLog}, true},
		{10, 0, target{}, false},
		{23, 6, target{act: actOption, option: 1}, true},
		{20, 5, target{}, false},
	}
	for _, tt := range tests {
		got, ok := z.at(tt.x, tt.y)
		if got != tt.want || ok != tt.ok {
			t.Errorf("at(%d,%d) = %+v %v, want %+v %v", tt.x, tt.y, got, ok, tt.want, tt.ok)
		}
	}
}

func TestFeedKeepsOrderAndEndsOnce(t *testing.T) {
	f := NewFeed()
	select {
	case <-f.Started():
		t.Fatal("started before any event")
	default:
	}
	f.Push(engine.Event{Kind: engine.RunStarted})
	f.Push(engine.Event{Kind: engine.PhaseStarted})
	<-f.Started()
	ctx := context.Background()
	b, ok := f.next(ctx, false)
	if !ok || b.ended || len(b.events) != 2 || b.events[1].Kind != engine.PhaseStarted {
		t.Fatalf("batch = %+v %v", b, ok)
	}
	f.Push(engine.Event{Kind: engine.RunStopped})
	f.End(engine.Result{Outcome: engine.Completed}, nil)
	f.End(engine.Result{}, errors.New("ignored"))
	<-f.Ended()
	b, ok = f.next(ctx, false)
	if !ok || !b.ended || len(b.events) != 1 {
		t.Fatalf("last batch = %+v %v", b, ok)
	}
	if res, err := f.Result(); res.Outcome != engine.Completed || err != nil {
		t.Errorf("result = %v %v", res, err)
	}
	if _, ok := f.next(ctx, true); ok {
		t.Error("feed delivered after the end")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok := NewFeed().next(cctx, false); ok {
		t.Error("next ignored a cancelled context")
	}
}

func TestText(t *testing.T) {
	if got := fit("abcdef", 4); got != "abc…" {
		t.Errorf("fit = %q", got)
	}
	if got := fit("abc", 3); got != "abc" {
		t.Errorf("fit = %q", got)
	}
	if got := fit("✓ ok", 0); got != "" {
		t.Errorf("fit = %q", got)
	}
	if got := wrap("one two three four", 9); !reflect.DeepEqual(got, []string{"one two", "three", "four"}) {
		t.Errorf("wrap = %q", got)
	}
	if got := wrap("abcdefghij", 4); !reflect.DeepEqual(got, []string{"abc", "def", "ghij"}) {
		t.Errorf("wrap long word = %q", got)
	}
	if got := wrap("x", 1); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("wrap tiny = %q", got)
	}
	if got := pad("ab", 4); got != "ab  " {
		t.Errorf("pad = %q", got)
	}
}
