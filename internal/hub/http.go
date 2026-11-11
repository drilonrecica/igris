package hub

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/drilonrecica/igris/internal/serve"
)

// HandlerOptions configures the hub's HTTP handler.
type HandlerOptions struct {
	// Token returns the hub token (state.ReadHubToken); called on every
	// request, so a rotation takes effect at once.
	Token func() (string, error)
	// TLS says the listener serves TLS.
	TLS bool
	// Now is the rate limit's clock and the pages'; nil is time.Now.
	Now func() time.Time
	// Logf reports a page that could not be rendered; nil drops it.
	Logf func(format string, args ...any)
}

// Handler is the hub's HTTP handler: serve's checks (serve.Guard) with the
// hub token and the cookie igris_hub, in front of the hub's pages (SPEC
// §15.8): the runs at /, each run's pages under /p/<id>/.
func (h *Hub) Handler(o HandlerOptions) http.Handler {
	return serve.Guard(serve.GuardOptions{
		Token: o.Token, Cookie: CookieName, TLS: o.TLS, Pair: PairCommand, Now: o.Now,
		Next: serve.NewHubUI(source{h}, serve.HubUIOptions{Now: o.Now, Logf: o.Logf}),
	})
}

// source is the hub as its pages read it.
type source struct{ h *Hub }

func (s source) Runs() []serve.HubRun {
	var out []serve.HubRun
	for _, e := range s.h.Runs() {
		out = append(out, hubRun(e))
	}
	return out
}

func (s source) Lookup(id string) (serve.HubRun, bool) {
	e, ok := s.h.Lookup(id)
	if !ok {
		return serve.HubRun{}, false
	}
	return hubRun(e), true
}

// Subscribe follows the hub's merged stream as the pages' script reads
// it: of a run's protocol-1 line only its type.
func (s source) Subscribe() (<-chan serve.HubChange, func()) {
	changes, cancel := s.h.Subscribe()
	out := make(chan serve.HubChange)
	stop := make(chan struct{})
	go func() {
		defer close(out)
		for c := range changes {
			hc := serve.HubChange{Run: c.ID}
			switch c.Kind {
			case Joined:
				hc.Change = serve.HubJoined
			case Left:
				hc.Change, hc.Outcome = serve.HubLeft, c.Outcome.String()
			default:
				var m struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(c.Line, &m) != nil {
					continue
				}
				hc.Change, hc.Type = serve.HubEvent, m.Type
			}
			select {
			case out <- hc:
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	return out, func() {
		once.Do(func() {
			close(stop)
			cancel()
		})
	}
}

// hubRun is an entry as the pages show it.
func hubRun(e Entry) serve.HubRun {
	return serve.HubRun{ID: e.ID, Project: e.Project, Run: e.Run, Ended: e.Ended, Outcome: e.Outcome}
}
