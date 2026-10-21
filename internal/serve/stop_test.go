package serve

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
)

// stopper is a run that stops when told to: it publishes run_stopped, as
// the engine does.
type stopper struct {
	srv  *Server
	stop bool // publish on stop; else only take it
}

func (s *stopper) Send(c engine.Command) {
	if c.Kind == engine.CmdStop && s.stop {
		go s.srv.Publish(engine.Event{Kind: engine.RunStopped, At: time.Now(), Detail: "stopped"})
	}
}

// stopOver runs Stop against s over net.Pipe.
func stopOver(t *testing.T, s *Server) error {
	t.Helper()
	cs, cc := net.Pipe()
	go s.ServeConn(cs)
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	return Stop(ctx, cc, "igris test")
}

func TestStop(t *testing.T) {
	run := &stopper{stop: true}
	s := New(Options{Version: "1.4.0", Sender: run})
	run.srv = s
	s.Publish(engine.Event{Kind: engine.RunStarted, Run: "r1", At: time.Now()})
	if err := stopOver(t, s); err != nil {
		t.Fatalf("Stop = %v", err)
	}
}

// A run that already ended is stopped: nothing to ask.
func TestStopEnded(t *testing.T) {
	s := New(Options{Version: "1.4.0", Sender: &stopper{}})
	s.Publish(engine.Event{Kind: engine.RunStopped, At: time.Now(), Detail: "completed"})
	if err := stopOver(t, s); err != nil {
		t.Fatalf("Stop = %v", err)
	}
}

// A connection that ends before run_stopped is ErrLost.
func TestStopLost(t *testing.T) {
	cs, cc := net.Pipe()
	go func() {
		r := bufio.NewReader(cs)
		_, _ = r.ReadString('\n') // hello
		_, _ = cs.Write([]byte(`{"v":1,"type":"snapshot","protocol":1,"server":"igris 1.4.0"}` + "\n"))
		_, _ = r.ReadString('\n') // stop
		_, _ = cs.Write([]byte(`{"v":1,"type":"ack","id":"stop"}` + "\n"))
		_ = cs.Close()
	}()
	if err := Stop(context.Background(), cc, "igris test"); !errors.Is(err, ErrLost) {
		t.Fatalf("Stop = %v, want ErrLost", err)
	}
}

// A refused stop says why.
func TestStopRefused(t *testing.T) {
	cs, cc := net.Pipe()
	go func() {
		r := bufio.NewReader(cs)
		_, _ = r.ReadString('\n')
		_, _ = cs.Write([]byte(`{"v":1,"type":"error","error":"protocol version not supported"}` + "\n"))
		_ = cs.Close()
	}()
	if err := Stop(context.Background(), cc, "igris test"); err == nil || !strings.Contains(err.Error(), "protocol version not supported") {
		t.Fatalf("Stop = %v", err)
	}
}

// Shutdown lets the last events reach the clients before it closes them.
func TestShutdownFlushes(t *testing.T) {
	s := New(Options{Version: "1.4.0"})
	p := attach(t, s)
	p.send(`{"v":1,"type":"hello","client":"igris test"}`)
	if m := p.next(); m["type"] != TypeSnapshot {
		t.Fatalf("got %v", m)
	}
	s.Publish(engine.Event{Kind: engine.RunStopped, At: time.Now(), Detail: "stopped"})
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	go func() { _ = s.Shutdown(ctx) }()
	if m := p.next(); m["type"] != string(engine.RunStopped) {
		t.Fatalf("got %v, want run_stopped", m)
	}
	if _, err := p.read(); err == nil {
		t.Fatal("the connection stayed open")
	}
	select {
	case <-p.done:
	case <-time.After(patience):
		t.Fatal("ServeConn did not return")
	}
}
