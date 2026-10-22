package project

import (
	"testing"

	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/tui"
)

// The snapshot says the live run is served when its socket answers (home
// offers Attach then), and AttachOptions is the attached run view.
func TestSnapshotServed(t *testing.T) {
	env, _ := testEnv(nil)
	root := newProject(t, "")
	p, err := Load(root, "", env)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := dir.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	if s := p.Snapshot(); !s.Lock.Alive || s.Served {
		t.Errorf("snapshot with a lock and no socket: alive %v, served %v", s.Lock.Alive, s.Served)
	}
	ln, err := serve.Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := serve.New(serve.Options{Version: "1.4.0", Project: p.Name()})
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	defer func() {
		if err := srv.Close(); err != nil {
			t.Error(err)
		}
		if err := <-served; err != nil {
			t.Error(err)
		}
	}()
	if s := p.Snapshot(); !s.Served {
		t.Error("snapshot with a serve listening: not served")
	}
	o := p.AttachOptions(tui.NewFeed(), nil, "1.4.0")
	if !o.Attached || o.Version != "1.4.0" || o.PlanPath != p.PlanPath || o.Feed == nil || o.Backend != "fake" {
		t.Errorf("AttachOptions = %+v", o)
	}
}
