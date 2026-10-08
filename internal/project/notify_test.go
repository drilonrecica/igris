package project

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/drilonrecica/igris/internal/report"
)

// A failed delivery reaches the page without the webhook's secret path,
// the server's address or the ntfy token, whether the secret is in the
// URL or in the error the server sends back.
func TestNotifyTestRedactsSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request for "+r.URL.Path+" with token tk_SECRET", http.StatusBadRequest)
	}))
	defer srv.Close()
	// Discord takes https only; this server's certificate is not trusted,
	// so that delivery fails too.
	tlsSrv := httptest.NewUnstartedServer(srv.Config.Handler)
	tlsSrv.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsSrv.StartTLS()
	defer tlsSrv.Close()
	hook := tlsSrv.URL + "/api/webhooks/1/SECRETTOKEN"

	root := newProject(t, `
[notify.backend]
enabled = false
[notify.ntfy]
server = "`+srv.URL+`"
topic = "igris"
token = "env:NTFY_TOKEN"
events = ["needs_input"]
[notify.discord]
webhook_url = "env:HOOK"
events = ["needs_input"]
`)
	env, _ := testEnv(map[string]string{"NTFY_TOKEN": "tk_SECRET", "HOOK": hook})
	var got []report.NotifyResult
	err := NewServices(root, env).NotifyTest(context.Background(), func(r report.NotifyResult) { got = append(got, r) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want one per channel: %+v", len(got), got)
	}
	for _, r := range got {
		if r.Err == "" {
			t.Errorf("%s: delivery to a failing server reported ok", r.Channel)
		}
		for _, secret := range []string{"SECRETTOKEN", "tk_SECRET", srv.URL, tlsSrv.URL, hook} {
			if strings.Contains(r.Err, secret) {
				t.Errorf("%s: error %q leaks %q", r.Channel, r.Err, secret)
			}
		}
	}
}

// notify test sends every sample at once: quiet hours and the task_done
// digest don't apply to it.
func TestNotifyTestBypassesHolding(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
	}))
	defer srv.Close()
	root := newProject(t, `
[notify]
quiet = "00:00-23:59"
break_through = []
task_done_digest = 5
[notify.backend]
enabled = false
[notify.ntfy]
server = "`+srv.URL+`"
topic = "igris"
events = ["task_done", "phase_done"]
`)
	env, _ := testEnv(nil)
	var got []report.NotifyResult
	if err := NewServices(root, env).NotifyTest(context.Background(), func(r report.NotifyResult) { got = append(got, r) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(bodies) != 2 || !strings.Contains(bodies[0], "test of task_done") {
		t.Errorf("results %+v, bodies %q: want both samples sent", got, bodies)
	}
}
