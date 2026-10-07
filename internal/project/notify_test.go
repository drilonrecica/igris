package project

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
	hook := srv.URL + "/api/webhooks/1/SECRETTOKEN"

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
		for _, secret := range []string{"SECRETTOKEN", "tk_SECRET", srv.URL, hook} {
			if strings.Contains(r.Err, secret) {
				t.Errorf("%s: error %q leaks %q", r.Channel, r.Err, secret)
			}
		}
	}
}
