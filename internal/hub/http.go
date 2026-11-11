package hub

import (
	"fmt"
	"net/http"
	"strings"
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
	// Now is the rate limit's clock; nil is time.Now.
	Now func() time.Time
}

// Handler is the hub's HTTP handler: serve's checks (serve.Guard) with the
// hub token and the cookie igris_hub, in front of the hub's pages.
func (h *Hub) Handler(o HandlerOptions) http.Handler {
	return serve.Guard(serve.GuardOptions{
		Token: o.Token, Cookie: CookieName, TLS: o.TLS, Pair: PairCommand, Now: o.Now,
		Next: http.HandlerFunc(h.page),
	})
}

// page answers a paired device: for now the runs the hub follows, as text.
func (h *Hub) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "igris: not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var b strings.Builder
	runs := h.Runs()
	fmt.Fprintf(&b, "igris hub: this device is paired; %s.\n", count(len(runs)))
	for _, e := range runs {
		fmt.Fprintf(&b, "  %s · %s\n", e.Project, e.ID)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// count is "no live run", "1 live run", "3 live runs".
func count(n int) string {
	switch n {
	case 0:
		return "no live run"
	case 1:
		return "1 live run"
	}
	return fmt.Sprintf("%d live runs", n)
}
