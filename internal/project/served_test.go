package project

import (
	"reflect"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/report"
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
	if s := p.Snapshot(); !s.Served || s.Live == nil || len(s.Live.NeedsYou) > 0 {
		t.Errorf("snapshot with a serve listening: served %v, live %+v", s.Served, s.Live)
	}
	o := p.AttachOptions(tui.NewFeed(), nil, "1.4.0")
	if !o.Attached || o.Version != "1.4.0" || o.PlanPath != p.PlanPath || o.Feed == nil || o.Backend != "fake" {
		t.Errorf("AttachOptions = %+v", o)
	}
}

// LiveRun reads who needs the owner from a served run's snapshot: the
// pending questions first, then the cards that wait, each task once.
func TestLiveRun(t *testing.T) {
	s := serve.Snapshot{
		Questions: []serve.Event{{Task: "M1-03", Question: string(engine.QuestionCommit)}},
		Active: []serve.Current{
			{Task: "M1-03", State: serve.StateNeedsYou},
			{Task: "M1-04", State: serve.StateWorking},
			{Task: "M1-05", State: serve.StateLost},
		},
		Paused: true,
	}
	got := LiveRun(s)
	want := []report.LiveNeed{{Task: "M1-03", Kind: "commit?"}, {Task: "M1-05", Kind: "session lost"}}
	if !reflect.DeepEqual(got.NeedsYou, want) || got.Paused {
		t.Errorf("LiveRun = %+v, want needs %v and not holding (tasks are active)", got, want)
	}
	if got := LiveRun(serve.Snapshot{Paused: true, Holding: true}); !got.Paused || len(got.NeedsYou) > 0 {
		t.Errorf("a holding run: %+v", got)
	}
}
