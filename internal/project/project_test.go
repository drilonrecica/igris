package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/tui"
)

const testPlan = `## M0 — Test

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| M0-01 | **First** | — | ready | sonnet |
| M0-02 | **Second** | M0-01 | blocked | opus |
`

// testEnv is a world without processes or herdr: every command fails, the
// backend is fake and only the given variables are set.
func testEnv(vars map[string]string) (Env, *fake.Backend) {
	be := fake.New()
	return Env{
		Getenv:   func(k string) string { return vars[k] },
		Runner:   &runner.Fake{},
		Backend:  func(*config.Config) (backend.Backend, error) { return be, nil },
		Versions: func(context.Context, runner.Runner) []checks.Result { return nil },
	}, be
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newProject makes a project root with igris.toml (unless toml is "-") and
// the test plan, and returns it.
func newProject(t *testing.T, toml string) string {
	t.Helper()
	root := t.TempDir()
	if toml != "-" {
		writeFile(t, filepath.Join(root, "igris.toml"), toml)
	}
	writeFile(t, filepath.Join(root, "tasks.md"), testPlan)
	return root
}

func noStateDir(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, state.DirName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s/ exists (err %v); reading must never create it", state.DirName, err)
	}
}

func TestOpen(t *testing.T) {
	env, _ := testEnv(nil)

	t.Run("outside a project", func(t *testing.T) {
		dir := t.TempDir()
		p, err := Open(dir, env)
		if err != nil {
			t.Fatal(err)
		}
		if p.Found || p.Root != dir || !p.NoConfig || p.CfgErr != nil || p.Cfg == nil {
			t.Errorf("project = %+v", p)
		}
		if p.PlanPath != filepath.Join(dir, "tasks.md") {
			t.Errorf("plan path = %s", p.PlanPath)
		}
		noStateDir(t, dir)
	})

	t.Run("root above the working directory", func(t *testing.T) {
		root := newProject(t, "plan = \"docs/plan.md\"\n")
		sub := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(sub, 0o750); err != nil {
			t.Fatal(err)
		}
		p, err := Open(sub, env)
		if err != nil {
			t.Fatal(err)
		}
		if !p.Found || p.Root != root || p.NoConfig || p.PlanPath != filepath.Join(root, "docs", "plan.md") {
			t.Errorf("project = %+v", p)
		}
	})

	t.Run("invalid igris.toml is data", func(t *testing.T) {
		root := newProject(t, "plan = 3\nnope = true\n")
		p, err := Open(root, env)
		if err != nil {
			t.Fatalf("Open failed on an invalid config: %v", err)
		}
		if p.CfgErr == nil || !p.Found || p.Cfg == nil || p.Cfg.Plan != "tasks.md" {
			t.Errorf("project = %+v, want the defaults and the error", p)
		}
		if _, err := p.Launch(report.RunRequest{Phase: "M0"}); err == nil {
			t.Error("Launch accepted an invalid config")
		}
		noStateDir(t, root)
	})
}

func TestLoad(t *testing.T) {
	env, _ := testEnv(nil)

	dir := t.TempDir()
	if _, err := Load(dir, "", env); err == nil || !strings.Contains(err.Error(), "igris init") {
		t.Errorf("outside a project without a plan: err = %v, want the init hint", err)
	}
	writeFile(t, filepath.Join(dir, "plan.md"), testPlan)
	if _, err := Load(dir, "", env); err == nil {
		t.Error("plan.md is not the default plan; Load should refuse")
	}
	if p, err := Load(dir, "plan.md", env); err != nil || p.Root != dir || p.Found {
		t.Errorf("explicit plan here: %+v, %v", p, err)
	}

	bad := newProject(t, "plan = 3\n")
	if _, err := Load(bad, "", env); err == nil {
		t.Error("Load accepted an invalid config")
	}
	noStateDir(t, dir)
}

func TestLaunchBuildsArisesEngine(t *testing.T) {
	root := newProject(t, "[notify.ntfy]\ntopic = \"t\"\ntoken = \"env:NTFY_TOKEN\"\n[tui]\nmouse = false\n")
	env, be := testEnv(map[string]string{"NTFY_TOKEN": "tk_secret"})
	p, err := Load(root, "", env)
	if err != nil {
		t.Fatal(err)
	}
	req := report.RunRequest{Phase: "M0", Through: "M1", Mode: "plan"}
	l, err := p.Launch(req)
	if err != nil {
		t.Fatal(err)
	}
	if l.Backend != backend.Backend(be) || l.State == nil || l.State.Root() != root {
		t.Errorf("launch = %+v", l)
	}
	var got []engine.Event
	events := func(ev engine.Event) { got = append(got, ev) }
	o := l.Options(report.Confirmations{Drift: true, Yolo: true, ForceUnlock: true}, events)
	if o.Config != p.Cfg || o.Backend != l.Backend || o.State != l.State || o.Runner != env.Runner ||
		o.Phase != "M0" || o.Through != "M1" || o.Mode != "plan" ||
		!o.ConfirmedDrift || !o.ConfirmedYolo || !o.ForceUnlock || o.Secrets.NtfyToken != "tk_secret" {
		t.Errorf("options = %+v", o)
	}
	o.Events(engine.Event{Kind: engine.Warning})
	if len(got) != 1 {
		t.Error("events are not passed on")
	}
	if o := l.Options(report.Confirmations{}, nil); o.ConfirmedDrift || o.ConfirmedYolo || o.ForceUnlock {
		t.Errorf("no answers, yet options = %+v", o)
	}
	eng, err := l.Engine(report.Confirmations{}, events)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	feed := tui.NewFeed()
	uo := l.TUIOptions(feed, nil)
	if uo.Project != filepath.Base(root) || uo.Backend != be.Name() || uo.Mode != "plan" || uo.Mouse ||
		uo.PlanPath != filepath.Join(root, "tasks.md") || uo.Feed != feed || uo.Focus == nil {
		t.Errorf("tui options = %+v", uo)
	}
	if uo.RankDurations != nil {
		t.Errorf("no run log, yet rank durations %v", uo.RankDurations)
	}
	// The live tail reads the engine's session, every poll_interval, unless
	// [tui] tail is off (SPEC §15.3).
	if uo.Tail != nil {
		t.Error("a tail without an engine to read it from")
	}
	if uo := l.TUIOptions(feed, eng); uo.Tail == nil || uo.TailEvery != p.Cfg.PollInterval.Std() {
		t.Errorf("tail %v every %s", uo.Tail != nil, uo.TailEvery)
	}
	p.Cfg.TUI.Tail = false
	if uo := l.TUIOptions(feed, eng); uo.Tail != nil {
		t.Error("[tui] tail = false, yet a tail")
	}
	p.Cfg.TUI.Tail = true
	writeFile(t, filepath.Join(root, state.DirName, "runs.jsonl"),
		`{"at":"2026-10-01T09:00:00Z","type":"run_started","detail":"phase M0"}
{"at":"2026-10-01T09:00:00Z","type":"task_started","task":"M0-01","rank":"sonnet","model":"sonnet"}
{"at":"2026-10-01T09:12:00Z","type":"task_done","task":"M0-01","rank":"sonnet","model":"sonnet"}
`)
	if d := l.TUIOptions(feed, nil).RankDurations["sonnet"]; len(d) != 1 || d[0] != 12*time.Minute {
		t.Errorf("rank durations from the log = %v", d)
	}

	// An unresolvable secret fails before anything is created.
	other := newProject(t, "[notify.ntfy]\ntopic = \"t\"\ntoken = \"env:NTFY_TOKEN\"\n")
	p2, _ := Load(other, "", env)
	p2.env.Getenv = func(string) string { return "" }
	if _, err := p2.Launch(req); err == nil || !strings.Contains(err.Error(), "NTFY_TOKEN") {
		t.Errorf("err = %v, want the missing variable", err)
	}
	noStateDir(t, other)
}

func TestPrelaunch(t *testing.T) {
	root := newProject(t, "")
	writeFile(t, filepath.Join(root, state.DirName, "state.json"),
		`{"version":1,"phases":["M0"],"config_hash":"x","current":{"task_id":"M0-01","mode":"default","claude_session":"","verify_attempts":0}}`)
	env, _ := testEnv(map[string]string{checks.APIKeyVar: "sk-test"})
	p, err := Open(root, env)
	if err != nil {
		t.Fatal(err)
	}
	pre := p.Prelaunch(context.Background(), nil)
	if pre.Interrupted != "M0-01" {
		t.Errorf("interrupted = %q", pre.Interrupted)
	}
	var ids []string
	asks := 0
	for _, w := range pre.Warnings {
		ids = append(ids, w.ID)
		if w.Confirm {
			asks++
		}
	}
	// Not a git repository (the fake runner fails git) and the API key,
	// which is the one arise asks about.
	if !reflect.DeepEqual(ids, []string{checks.IDAPIKey, checks.IDGit}) || asks != 1 {
		t.Errorf("warnings = %+v", pre.Warnings)
	}
}

func TestInit(t *testing.T) {
	env, _ := testEnv(nil)
	dir := t.TempDir()
	steps, err := Init(context.Background(), dir, true, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []report.Step{
		{ID: report.StepConfig, Path: "igris.toml", Message: "created igris.toml"},
		{ID: report.StepState, Path: ".igris/", Message: "ready .igris/"},
		{ID: report.StepGitignore, Path: ".gitignore", Message: "added .igris/ to .gitignore"},
		{ID: report.StepClaudeSettings, Path: ClaudeSettingsPath, Message: "allowed `igris done` in " + ClaudeSettingsPath},
		{ID: report.StepExamplePlan, Path: "tasks.md", Message: "created tasks.md (example plan)"},
		{ID: report.StepHerdrHint, Message: integrationHint},
	}
	if !reflect.DeepEqual(steps, want) {
		t.Errorf("steps =\n%+v\nwant\n%+v", steps, want)
	}
	if got := InitFiles(false); !reflect.DeepEqual(got, []string{"igris.toml", ".igris/", ".gitignore", ClaudeSettingsPath}) {
		t.Errorf("init files = %v", got)
	}

	// Again: everything is kept.
	steps, err = Init(context.Background(), dir, false, env)
	if err != nil || len(steps) != 5 {
		t.Fatalf("steps = %+v, %v", steps, err)
	}
	for _, s := range steps[:4] {
		if !strings.HasPrefix(s.Message, "kept ") && !strings.HasPrefix(s.Message, "ready ") {
			t.Errorf("second init changed something: %+v", s)
		}
	}

	// A failing step returns the steps before it.
	broken := t.TempDir()
	writeFile(t, filepath.Join(broken, ClaudeSettingsPath), "[1,2]")
	steps, err = Init(context.Background(), broken, false, env)
	if err == nil || len(steps) != 3 || steps[2].ID != report.StepGitignore {
		t.Errorf("steps = %+v, err = %v; want three steps and the error", steps, err)
	}
}

func TestSnapshot(t *testing.T) {
	ctx := context.Background()
	env, _ := testEnv(nil)

	t.Run("no project", func(t *testing.T) {
		dir := t.TempDir()
		s, err := NewServices(dir, env).Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if s.Found || !s.NoConfig || !s.PlanMissing || s.Plan != nil || s.Valid() || s.Run != nil {
			t.Errorf("snapshot = %+v", s)
		}
		noStateDir(t, dir)
	})

	t.Run("valid project", func(t *testing.T) {
		root := newProject(t, "")
		s, err := NewServices(root, env).Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !s.Found || !s.Valid() || s.Status == nil || len(s.Status.Phases) != 1 || s.Project != filepath.Base(root) {
			t.Errorf("snapshot = %+v", s)
		}
		if !s.Stamp.Config.Exists || !s.Stamp.Plan.Exists || s.Stamp.State.Exists {
			t.Errorf("stamp = %+v", s.Stamp)
		}
		noStateDir(t, root)
	})

	t.Run("API key", func(t *testing.T) {
		root := newProject(t, "")
		for _, set := range []bool{false, true} {
			vars := map[string]string{}
			if set {
				vars[checks.APIKeyVar] = "sk-test"
			}
			e, _ := testEnv(vars)
			s, err := NewServices(root, e).Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if s.APIKeySet != set {
				t.Errorf("%s set %v: APIKeySet = %v", checks.APIKeyVar, set, s.APIKeySet)
			}
		}
	})

	t.Run("invalid config and plan", func(t *testing.T) {
		root := newProject(t, "plan = 3\n")
		writeFile(t, filepath.Join(root, "tasks.md"), "## M0 — Test\n\n| ID | Task | Deps | Status | Model |\n|---|---|---|---|---|\n| M0-01 | x | M9-01 | ready | sonnet |\n")
		s, err := NewServices(root, env).Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.ConfigProblems) == 0 || len(s.Issues) == 0 || s.Status != nil || s.Valid() {
			t.Errorf("snapshot = %+v", s)
		}
	})

	t.Run("run state is read and cleaned", func(t *testing.T) {
		root := newProject(t, "")
		igris := filepath.Join(root, state.DirName)
		writeFile(t, filepath.Join(igris, "igris.lock"), `{"pid":1,"host":"far\u001b[31maway","started_at":"2026-10-01T10:00:00Z"}`)
		writeFile(t, filepath.Join(igris, "runs.jsonl"),
			`{"at":"2026-10-01T10:00:00Z","type":"run_started","detail":"phase M0"}`+"\n"+
				`{"at":"2026-10-01T10:05:00Z","type":"run_stopped","detail":"completed"}`+"\n")
		s, err := NewServices(root, env).Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !s.Lock.Held || !s.Lock.Remote || strings.ContainsRune(s.Lock.Info.Host, 0x1b) {
			t.Errorf("lock = %+v", s.Lock)
		}
		if s.Run == nil || len(s.Recent) != 1 || s.Recent[0].End != "completed" {
			t.Errorf("run = %+v, recent = %+v", s.Run, s.Recent)
		}
	})
}

func TestStampFollowsTheFiles(t *testing.T) {
	root := newProject(t, "")
	env, _ := testEnv(nil)
	svc := NewServices(root, env)
	if _, err := svc.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := svc.Stamp()
	if svc.Stamp() != before {
		t.Error("stamp changed with nothing changed")
	}
	writeFile(t, filepath.Join(root, "tasks.md"), testPlan+"\n")
	if svc.Stamp() == before {
		t.Error("stamp missed the plan change")
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "igris.toml"), later, later); err != nil {
		t.Fatal(err)
	}
	if svc.Stamp().Config == before.Config {
		t.Error("stamp missed the config change")
	}
}

func TestServicesStartAndPreview(t *testing.T) {
	ctx := context.Background()
	env, _ := testEnv(nil)

	root := newProject(t, "")
	svc := NewServices(root, env)
	r, err := svc.Start(ctx, report.RunRequest{Phase: "M0"}, report.Confirmations{}, func(engine.Event) {})
	if err != nil || r == nil {
		t.Fatalf("start: %v, %v", r, err)
	}
	dr, err := svc.Preview(ctx, report.RunRequest{Phase: "M0"})
	if err != nil || dr == nil || dr.Sessions != 2 {
		t.Errorf("preview = %+v, %v", dr, err)
	}

	bad := newProject(t, "plan = 3\n")
	if _, err := NewServices(bad, env).Start(ctx, report.RunRequest{Phase: "M0"}, report.Confirmations{}, nil); err == nil {
		t.Error("Start accepted an invalid config")
	}
	noStateDir(t, bad)
}

func TestServicesNotifyTest(t *testing.T) {
	root := newProject(t, "")
	env, be := testEnv(nil)
	var got []report.NotifyResult
	if err := NewServices(root, env).NotifyTest(context.Background(), func(r report.NotifyResult) { got = append(got, r) }); err != nil {
		t.Fatal(err)
	}
	// The herdr toast (fake here) wants the default events.
	if len(got) != len(notify.DefaultEvents) || len(be.Notifications()) != len(got) {
		t.Fatalf("results = %+v; toasts = %d", got, len(be.Notifications()))
	}
	for _, r := range got {
		if r.Err != "" || r.Channel == "" || r.Event == "" {
			t.Errorf("result = %+v", r)
		}
	}

	quiet := newProject(t, "[notify.backend]\nenabled = false\n")
	if err := NewServices(quiet, env).NotifyTest(context.Background(), func(report.NotifyResult) {}); err == nil || err.Error() != NoChannels {
		t.Errorf("err = %v, want the no-channel hint", err)
	}
}

func TestEditorArgv(t *testing.T) {
	abs, err := filepath.Abs("tasks.md")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		visual, editor string
		want           []string
		wantErr        error
	}{
		{"", "", nil, ErrNoEditor},
		{"  ", "", nil, ErrNoEditor},
		{"", "vim", []string{"vim", abs}, nil},
		{"code --wait", "vim", []string{"code", "--wait", abs}, nil},
		{"", "  nano  -w ", []string{"nano", "-w", abs}, nil},
		{"emacs; rm -rf /", "", []string{"emacs;", "rm", "-rf", "/", abs}, nil}, // no shell: just words
	}
	for _, tt := range tests {
		vars := map[string]string{"VISUAL": tt.visual, "EDITOR": tt.editor}
		got, err := EditorArgv(func(k string) string { return vars[k] }, "tasks.md")
		if !errors.Is(err, tt.wantErr) || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("VISUAL=%q EDITOR=%q: %q, %v; want %q, %v", tt.visual, tt.editor, got, err, tt.want, tt.wantErr)
		}
	}

	env, _ := testEnv(map[string]string{"EDITOR": "true"})
	c, err := NewServices(t.TempDir(), env).EditCommand("x.md")
	if err != nil {
		t.Fatal(err)
	}
	if ec := c.(*editorCmd); ec.cmd.Args[0] != "true" || !filepath.IsAbs(ec.cmd.Args[1]) {
		t.Errorf("args = %q", ec.cmd.Args)
	}

	// vi is offered only when it is on PATH, and gets the absolute path.
	for _, found := range []bool{false, true} {
		env, _ := testEnv(nil)
		env.LookPath = func(name string) (string, error) {
			if !found || name != "vi" {
				return "", exec.ErrNotFound
			}
			return "/usr/bin/vi", nil
		}
		c := NewServices(t.TempDir(), env).ViCommand("x.md")
		if found != (c != nil) {
			t.Fatalf("vi on PATH %v: command %v", found, c)
		}
		if c != nil {
			if args := c.(*editorCmd).cmd.Args; len(args) != 2 || args[0] != "/usr/bin/vi" || !filepath.IsAbs(args[1]) {
				t.Errorf("vi args = %q", args)
			}
		}
	}
}

func TestReadsNeverCreateState(t *testing.T) {
	ctx := context.Background()
	root := newProject(t, "")
	env, _ := testEnv(nil)
	svc := NewServices(root, env)
	_, _ = svc.Snapshot(ctx)
	_ = svc.Stamp()
	_ = svc.Prelaunch(ctx, report.RunRequest{Phase: "M0"})
	_ = svc.Doctor(ctx)
	_, _ = svc.History(ctx, 5)
	_, _ = svc.Preview(ctx, report.RunRequest{Phase: "M0"})
	_ = svc.BackendAvailable(ctx)
	noStateDir(t, root)
}

// The snapshot carries igris.toml's path and the settings home shows:
// the owner's keys apart from the defaults, secrets hidden, warnings.
func TestSnapshotSettings(t *testing.T) {
	root := newProject(t, "[claude]\ncommand = \"claudex\"\n[notify.ntfy]\ntopic = \"t\"\ntoken = \"env:NTFY_TOKEN\"\n")
	env, _ := testEnv(map[string]string{"NTFY_TOKEN": "tk_secret"})
	s, err := NewServices(root, env).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.ConfigPath != filepath.Join(root, "igris.toml") || len(s.ConfigWarnings) != 1 {
		t.Errorf("path %q, warnings %q", s.ConfigPath, s.ConfigWarnings)
	}
	got := map[string]report.Setting{}
	for _, sec := range s.Settings {
		for _, r := range sec.Rows {
			got[sec.Name+"."+r.Key] = r
			if strings.Contains(r.Value, "tk_secret") {
				t.Errorf("%s.%s shows the secret", sec.Name, r.Key)
			}
		}
	}
	if r := got["notify.ntfy.token"]; r.Value != "set (env:NTFY_TOKEN)" || r.Default {
		t.Errorf("token: %+v", r)
	}
	if got["notify.ntfy.topic"].Default || !got["notify.ntfy.server"].Default || got["claude.command"].Default {
		t.Errorf("defaults marked wrong: %+v", got)
	}

	// An invalid igris.toml: the defaults, every one marked so.
	bad := newProject(t, "default_mode = \"fast\"\n")
	s, err = NewServices(bad, env).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range s.Settings {
		for _, r := range sec.Rows {
			if !r.Default {
				t.Errorf("invalid config: %s.%s not a default", sec.Name, r.Key)
			}
		}
	}
}
