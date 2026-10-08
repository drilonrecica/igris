package engine

import (
	"reflect"
	"testing"
	"time"
)

func TestFakeClock(t *testing.T) {
	start := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now = %v, want %v", got, start)
	}

	var ran []string
	c.At(5*time.Second, func() { ran = append(ran, "5s at "+c.Now().Sub(start).String()) })
	c.At(2*time.Second, func() { ran = append(ran, "2s") })
	c.At(2*time.Second, func() { ran = append(ran, "2s again") })

	select {
	case at := <-c.After(2 * time.Second):
		if want := start.Add(2 * time.Second); !at.Equal(want) {
			t.Errorf("After delivered %v, want %v", at, want)
		}
	default:
		t.Fatal("After returned a channel that has not fired")
	}
	if want := []string{"2s", "2s again"}; !reflect.DeepEqual(ran, want) {
		t.Fatalf("hooks after 2s = %q, want %q", ran, want)
	}

	<-c.After(4 * time.Second) // 6s: past the 5s hook
	<-c.After(time.Hour)       // hooks run once
	want := []string{"2s", "2s again", "5s at 6s"}
	if !reflect.DeepEqual(ran, want) {
		t.Errorf("hooks = %q, want %q", ran, want)
	}
	if got, want := c.Now(), start.Add(time.Hour+6*time.Second); !got.Equal(want) {
		t.Errorf("Now = %v, want %v", got, want)
	}
}

func TestFakeClockStep(t *testing.T) {
	start := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)
	c.Step(time.Millisecond)
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("first Now = %v, want %v", got, start)
	}
	if got, want := c.Now(), start.Add(time.Millisecond); !got.Equal(want) {
		t.Fatalf("second Now = %v, want %v", got, want)
	}
}
