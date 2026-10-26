package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/state"
)

// actionToken and listener are what changes between runs in an Actions
// header: the tokens and the listener's port.
var (
	actionTokenRE = regexp.MustCompile(`/api/v1/action/[A-Za-z0-9_-]{43}`)
	listenerRE    = regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+`)
)

// The ntfy Actions header of each message that gets buttons (SPEC §10,
// V18-P3), as ntfy receives it: the answers of a pending question, at
// most three with Open, to that task's question on the web UI.
func TestNtfyActionsGolden(t *testing.T) {
	task := func(q engine.Question, extra ...func(*engine.Event)) func(w *web) notify.Message {
		return func(w *web) notify.Message {
			w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.TaskStarted, Task: "M1-03", Title: "Title", Model: "opus"})
			ev := engine.Event{At: w.clock.now(), Kind: engine.Asked, Task: "M1-03", Question: q, Detail: "?"}
			for _, f := range extra {
				f(&ev)
			}
			w.srv.Publish(ev)
			return notify.Message{Event: notify.NeedsInput, Project: "demo", Phase: "M1", TaskID: "M1-03", Title: "Title", What: "x", Question: string(q)}
		}
	}
	tests := []struct {
		name string
		msg  func(w *web) notify.Message
	}{
		{"commit", task(engine.QuestionCommit)},
		{"confirm_skip", task(engine.QuestionConfirmSkip)},
		{"session_lost", task(engine.QuestionSessionLost)},
		{"hook_failed", task(engine.QuestionHookFailed)},
		{"plan_approval", task(engine.QuestionPlanApproval)},
		{"confirm_reset", task(engine.QuestionConfirmReset)},
		{"integration", task(engine.QuestionIntegration)},
		{"integration_resolve", task(engine.QuestionIntegration, func(ev *engine.Event) { ev.Resolve = true })},
		{"files_in_the_way", task(engine.QuestionFilesInTheWay)},
		{"verify_failed_limit", func(w *web) notify.Message {
			w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.TaskStarted, Task: "M1-03", Title: "Title", Model: "opus"})
			w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.NeedsYou, Task: "M1-03", Reason: state.ReasonVerifyLimit})
			return notify.Message{Event: notify.VerifyFailedLimit, Project: "demo", TaskID: "M1-03", What: "x"}
		}},
		{"budget", func(w *web) notify.Message {
			w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.Asked, Question: engine.QuestionBudget, Budget: &engine.BudgetUse{Budget: "run", Limit: "20M", Used: "20M"}})
			return notify.Message{Event: notify.BudgetExceeded, Project: "demo", What: "x", Question: string(engine.QuestionBudget)}
		}},
		{"commit_batch", func(w *web) notify.Message {
			w.srv.Publish(engine.Event{At: w.clock.now(), Kind: engine.Asked, Question: engine.QuestionCommitBatch, Undo: true, Detail: "Keep the 2 commits of this run?"})
			return notify.Message{Event: notify.NeedsInput, Project: "demo", What: "run completed · keep the 2 commits?", Question: string(engine.QuestionCommitBatch)}
		}},
		{"blocked", func(w *web) notify.Message {
			return notify.Message{Event: notify.NeedsInput, Project: "demo", TaskID: "M1-03", What: "waits for a permission (Bash)"}
		}},
		{"task_overdue", func(w *web) notify.Message {
			return notify.Message{Event: notify.TaskOverdue, Project: "demo", TaskID: "M1-03", What: "running longer than its Timeout 45m"}
		}},
		{"grouped", func(w *web) notify.Message {
			return notify.Message{Event: notify.NeedsInput, Project: "demo", TaskID: "M1-03", Tasks: []string{"M1-03", "M1-05"}, What: "2 tasks need you: M1-03, M1-05"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newActionWeb(t)
			var header string
			ntfy := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				header = r.Header.Get("Actions")
			}))
			defer ntfy.Close()
			n := &notify.Ntfy{Server: ntfy.URL, Topic: "t", Actions: w.srv.Buttons(w.ts.URL + "/")}
			if err := n.Send(context.Background(), tt.msg(w)); err != nil {
				t.Fatal(err)
			}
			got := listenerRE.ReplaceAllString(actionTokenRE.ReplaceAllString(header, "/api/v1/action/<token>"), "http://<listener>")
			got = strings.ReplaceAll(got, "},{", "},\n{") + "\n"
			path := filepath.Join("testdata", "ntfy", tt.name+".golden")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path) //nolint:gosec // the test's own golden file
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("Actions header differs from %s (go test -update rewrites it):\n%s", path, got)
			}
		})
	}
}
