package hub

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
)

// The hub follows a real serve over its socket: the run joins, its events
// reach the hub's stream, a skip-permissions mode is refused before it is
// sent, and the run's end is its outcome. Closing the hub before the end
// would only have detached it.
func TestHubFollowsAServe(t *testing.T) {
	root := t.TempDir()
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := serve.New(serve.Options{Version: "1.11.0", Project: "sinjal"})
	l, err := serve.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(l)
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		<-served
	})
	reg := state.RunEntry{V: 1, Root: root, Project: "sinjal", PID: 1, Started: t0, Socket: l.Path()}
	log := &syncBuf{}
	h := New(Options{All: true, Name: "igris 1.11.0", Out: log, Now: func() time.Time { return t0 },
		Live: func(context.Context) []state.RunEntry { return []state.RunEntry{reg} }})
	t.Cleanup(h.Close)
	stream, cancel := h.Subscribe()
	defer cancel()

	ctx, done := context.WithTimeout(context.Background(), patience)
	defer done()
	h.Scan(ctx)
	id := RunID("sinjal", root)
	if c := next(t, stream, Joined); c.ID != id || c.Project != "sinjal" {
		t.Fatalf("joined %+v", c)
	}
	e, ok := h.Lookup(id)
	if !ok {
		t.Fatal("the run is not in the hub")
	}
	if rep, status := e.Run.Request(ctx, serve.Request{Type: serve.CmdMode, ID: "1", Text: "yolo", Yes: true}); status != http.StatusForbidden {
		t.Errorf("yolo through the hub: %d %+v", status, rep)
	}
	srv.Publish(engine.Event{At: t0, Kind: engine.RunStarted, Run: "r1", Phase: "M1"})
	for {
		c := next(t, stream, Message)
		if bytes.Contains(c.Line, []byte(`"run_started"`)) {
			break
		}
	}
	srv.Publish(engine.Event{At: t0, Kind: engine.RunStopped, Detail: "completed"})
	sctx, sdone := context.WithTimeout(context.Background(), patience)
	defer sdone()
	_ = srv.Shutdown(sctx)
	if c := next(t, stream, Left); c.ID != id || c.Outcome != look.Completed {
		t.Errorf("left %+v", c)
	}
	want := "09:41:07 run joined: sinjal\n09:41:07 run ended: sinjal · completed\n"
	if log.String() != want {
		t.Errorf("log:\n%s\nwant\n%s", log, want)
	}
}
