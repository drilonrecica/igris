package plan

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var testModels = map[string]string{"sonnet": "sonnet", "opus": "opus", "fable": "fable", "haiku": "haiku"}

var testRules = Rules{Models: testModels}

// table builds a one-phase plan; rows start at line 5.
func table(rows ...string) string {
	return "## M0\n\n| ID | Deps | Status | Model | Owner | Mode |\n|---|---|---|---|---|---|\n" + strings.Join(rows, "\n") + "\n"
}

func TestValidateValid(t *testing.T) {
	p := Parse("tasks.md", []byte(specExample), Options{})
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatalf("spec example must be valid: %v", issueMsgs(issues))
	}
	if err := p.Check(testRules); err != nil {
		t.Fatal(err)
	}
	in := table(
		"| a | — | done | sonnet | agent | — |",
		"| b | a | ready | — | user | |",
		"| c | a, b | blocked | opus | agent + user | yolo |",
		"| d | a…c | blocked | haiku | Agent | Accept |",
		// Finished agent tasks never get a session, so they need no model.
		"| e | | done | — | agent | |",
		"| f | | skipped (not needed) | | agent + user | |",
	)
	if issues := Parse("tasks.md", []byte(in), Options{}).Validate(testRules); len(issues) > 0 {
		t.Fatalf("unexpected issues: %v", issueMsgs(issues))
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"unknown status", table("| a | | todo | sonnet | agent | |"),
			[]string{`tasks.md:5: a: unknown status "todo"; use ready, blocked, in progress, done or skipped`}},
		{"status synonym gets a hint", table(
			"| a | | dropped | sonnet | agent | |",
			"| b | | `Completed` | sonnet | agent | |",
			"| c | | WIP: half done | sonnet | agent | |",
			"| d | | won't do (not needed) | sonnet | agent | |",
		),
			[]string{
				`tasks.md:5: a: unknown status "dropped" (did you mean skipped?); use ready, blocked, in progress, done or skipped`,
				`tasks.md:6: b: unknown status "` + "`Completed`" + `" (did you mean done?); use ready, blocked, in progress, done or skipped`,
				`tasks.md:7: c: unknown status "WIP: half done" (did you mean in progress?); use ready, blocked, in progress, done or skipped`,
				`tasks.md:8: d: unknown status "won't do (not needed)" (did you mean skipped?); use ready, blocked, in progress, done or skipped`,
			}},
		{"markdown around cells gets a hint", table(
			"| `a` | | ✅ Done | **Opus** | agent | |",
			"| b | | **ready** | `Sonnet` | agent | |",
			"| c | | ready | **Composer 2.5** | agent | |",
		),
			[]string{
				`tasks.md:5: "` + "`a`" + `" is not a valid task ID; write it as a, without markdown around it`,
				`tasks.md:5: ` + "`a`" + `: unknown status "✅ Done" (did you mean done?); use ready, blocked, in progress, done or skipped`,
				`tasks.md:5: ` + "`a`" + `: unknown model rank "**Opus**" (did you mean opus?); add it to [models] in igris.toml or use one of: fable, haiku, opus, sonnet`,
				`tasks.md:6: b: unknown status "**ready**" (did you mean ready?); use ready, blocked, in progress, done or skipped`,
				`tasks.md:6: b: unknown model rank "Sonnet" (did you mean sonnet?); add it to [models] in igris.toml or use one of: fable, haiku, opus, sonnet`,
				`tasks.md:7: c: unknown model rank "**Composer 2.5**"; add it to [models] in igris.toml or use one of: fable, haiku, opus, sonnet`,
			}},
		{"empty status", table("| a | |  | sonnet | agent | |"),
			[]string{`tasks.md:5: a: unknown status ""; use ready, blocked, in progress, done or skipped`}},
		{"invalid ID", table("| -a | | ready | sonnet | agent | |", "| a b | | ready | sonnet | agent | |"),
			[]string{
				`tasks.md:5: "-a" is not a valid task ID; use letters, digits, '.', '_' and '-', starting with a letter or digit`,
				`tasks.md:6: "a b" is not a valid task ID; use letters, digits, '.', '_' and '-', starting with a letter or digit`,
			}},
		{"missing ID", table("|  | | ready | sonnet | agent | |"),
			[]string{"tasks.md:5: row has no ID; every task needs a unique ID"}},
		{"control character in a row", table("| a | — | ready\x1b[2J | sonnet | agent | |"),
			[]string{"tasks.md:5: row contains a control character (an escape sequence?); remove it"}},
		{"control character in the heading", strings.Replace(table("| a | | ready | sonnet | agent | |"), "## M0", "## M0 \x07", 1),
			[]string{"tasks.md:1: the heading of phase M0 contains a control character (an escape sequence?); remove it"}},
		{"duplicate ID across phases", table("| a | | ready | sonnet | agent | |") + "\n## M1\n\n| ID | Status | Model |\n|---|---|---|\n| a | ready | sonnet |\n",
			[]string{"tasks.md:11: duplicate task ID a (first at line 5); task IDs must be unique across the plan"}},
		{"unknown rank", table("| a | | ready | gpt | agent | |"),
			[]string{`tasks.md:5: a: unknown model rank "gpt"; add it to [models] in igris.toml or use one of: fable, haiku, opus, sonnet`}},
		{"model left open by adapt", table("| a | | ready | ? | agent | |"),
			[]string{`tasks.md:5: a: model not set yet ("?"); fill in one of: fable, haiku, opus, sonnet`}},
		{"user with model", table("| a | | ready | sonnet | user | |"),
			[]string{`tasks.md:5: a: user tasks must have Model —, not "sonnet" (igris runs no session for them)`}},
		{"agent without model", table("| a | | ready | — | agent | |", "| b | | ready | | agent + user | |", "| c | | in progress | — | agent | |", "| d | | blocked | — | agent | |"),
			[]string{
				"tasks.md:5: a: agent task needs a Model (a rank from [models], e.g. sonnet); use Owner user for tasks without a session",
				"tasks.md:6: b: agent + user task needs a Model (a rank from [models], e.g. sonnet); use Owner user for tasks without a session",
				"tasks.md:7: c: agent task needs a Model (a rank from [models], e.g. sonnet); use Owner user for tasks without a session",
				"tasks.md:8: d: agent task needs a Model (a rank from [models], e.g. sonnet); use Owner user for tasks without a session",
			}},
		{"agent without model in a table without Owner", "## M0\n\n| ID | Status | Model |\n|---|---|---|\n| a | ready | — |\n",
			[]string{
				"tasks.md:5: a: agent task needs a Model (a rank from [models], e.g. sonnet); this table has no Owner column, so every task is an agent task; add an Owner column with user for tasks without a session",
			}},
		{"unknown owner", table("| a | | ready | sonnet | robot | |"),
			[]string{`tasks.md:5: a: unknown owner "robot"; use agent, user or agent + user`}},
		{"unknown mode", table("| a | | ready | sonnet | agent | turbo |"),
			[]string{`tasks.md:5: a: unknown mode "turbo"; use default, accept, auto, plan, yolo or —`}},
		{"unknown dep", table("| a | zz | blocked | sonnet | agent | |"),
			[]string{"tasks.md:5: a depends on unknown task zz; check the ID or remove it from Deps"}},
		{"invalid dep token", table("| a | b c | blocked | sonnet | agent | |"),
			[]string{`tasks.md:5: a: Deps entry "b c" is not a task ID; list task IDs separated by commas`}},
		{"self dep", table("| a | a | blocked | sonnet | agent | |"),
			[]string{"tasks.md:5: a depends on itself; remove it from Deps"}},
		{"self dep through range", table("| a | | done | sonnet | agent | |", "| b | a…c | blocked | sonnet | agent | |", "| c | | done | sonnet | agent | |"),
			[]string{"tasks.md:6: b depends on itself; remove it from Deps"}},
		{"bad range", table("| a | | done | sonnet | agent | |", "| b | a… | blocked | sonnet | agent | |"),
			[]string{`tasks.md:6: b: malformed Deps range "a…"; write it as A…B with two task IDs`}},
		{"two-task cycle", table("| a | b | blocked | sonnet | agent | |", "| b | a | blocked | sonnet | agent | |"),
			[]string{"tasks.md:5: dependency cycle a → b → a (each task waits on the next); remove one of these dependencies"}},
		{"cycle reported from its first task", table(
			"| x | | done | sonnet | agent | |",
			"| c | x, a | blocked | sonnet | agent | |",
			"| a | b | blocked | sonnet | agent | |",
			"| b | c | blocked | sonnet | agent | |"),
			[]string{"tasks.md:6: dependency cycle c → a → b → c (each task waits on the next); remove one of these dependencies"}},
		{"two cycles", table(
			"| a | b | blocked | sonnet | agent | |",
			"| b | a, c | blocked | sonnet | agent | |",
			"| c | b | blocked | sonnet | agent | |"),
			[]string{
				"tasks.md:5: dependency cycle a → b → a (each task waits on the next); remove one of these dependencies",
				"tasks.md:6: dependency cycle b → c → b (each task waits on the next); remove one of these dependencies",
			}},
		{"cycle across phases via range", "## P0\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| p1 | m2 | blocked | sonnet |\n\n## M0\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| m1 | | done | sonnet |\n| m2 | p1…m1 | blocked | sonnet |\n",
			[]string{"tasks.md:5: dependency cycle p1 → m2 → p1 (each task waits on the next); remove one of these dependencies"}},
		{"duplicate phase", table("| a | | ready | sonnet | agent | |") + "\n## M0\n\n| ID | Status | Model |\n|---|---|---|\n| b | ready | sonnet |\n",
			[]string{"tasks.md:7: duplicate phase ID M0 (first at line 1); phase IDs must be unique"}},
		{"second table in a phase", table("| a | | ready | sonnet | agent | |") + "\n| ID | Status | Model |\n|---|---|---|\n| b | ready | sonnet |\n",
			[]string{"tasks.md:7: phase M0 has a second task table (first at line 3); a phase may contain only one"}},
		{"no task table, but one without Status", "## M0\n\n| ID | Task | Model | State |\n|---|---|---|---|\n| a | x | sonnet | ready |\n",
			[]string{`tasks.md:3: no task table found: this table has an ID column but no Status column; rename that column to Status or alias it under [columns] in igris.toml (e.g. "State" = "Status")`}},
		{"no task table, but one without Model and one without Status", "## M0\n\n| ID | Status | Agent |\n|---|---|---|\n| a | ready | sonnet |\n\n| ID | Model | State |\n|---|---|---|\n| b | sonnet | ready |\n",
			[]string{
				`tasks.md:3: no task table found: this table has an ID column but no Model column; rename that column to Model or alias it under [columns] in igris.toml (e.g. "Agent" = "Model")`,
				`tasks.md:7: no task table found: this table has an ID column but no Status column; rename that column to Status or alias it under [columns] in igris.toml (e.g. "State" = "Status")`,
			}},
		{"second table under a ### heading", "## V1\n\n### M0\n\n| ID | Status | Model |\n|---|---|---|\n| a | ready | sonnet |\n\n### M1\n\n| ID | Status | Model |\n|---|---|---|\n| b | ready | sonnet |\n",
			[]string{`tasks.md:11: phase V1 has a second task table (first at line 5); only "##" headings start a phase, so make the heading at line 9 a "##" heading`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := issueMsgs(Parse("tasks.md", []byte(tt.in), Options{}).Validate(testRules))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("issues:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestValidateCollectsAllSortedByLine(t *testing.T) {
	in := "| ID | Status | Model |\n|---|---|---|\n| z | ready | sonnet |\n\n" + // outside a phase (line 1)
		table(
			"| a | b | todo | gpt | agent | |",
			"| b | a | ready | sonnet | user | |",
			"| a | | ready | sonnet | agent | |",
		)
	got := issueMsgs(Parse("tasks.md", []byte(in), Options{}).Validate(testRules))
	want := []string{
		`tasks.md:1: task table outside a phase; put it under a "## <phase ID> — <title>" heading`,
		`tasks.md:9: a: unknown status "todo"; use ready, blocked, in progress, done or skipped`,
		`tasks.md:9: a: unknown model rank "gpt"; add it to [models] in igris.toml or use one of: fable, haiku, opus, sonnet`,
		"tasks.md:9: dependency cycle a → b → a (each task waits on the next); remove one of these dependencies",
		`tasks.md:10: b: user tasks must have Model —, not "sonnet" (igris runs no session for them)`,
		"tasks.md:11: duplicate task ID a (first at line 9); task IDs must be unique across the plan",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("issues:\n got %q\nwant %q", got, want)
	}
}

func TestValidateDoesNotModifyPlan(t *testing.T) {
	p := Parse("tasks.md", []byte(table("| a | | todo | sonnet | agent | |")), Options{})
	before := len(p.issues)
	p.Validate(testRules)
	p.Validate(testRules)
	if len(p.issues) != before {
		t.Fatal("Validate must not accumulate issues on the plan")
	}
}

func TestCheckAndInvalid(t *testing.T) {
	p := Parse("tasks.md", []byte(table("| a | | todo | sonnet | agent | |", "| b | | ready | gpt | agent | |")), Options{})
	err := p.Check(testRules)
	var inv *Invalid
	if !errors.As(err, &inv) || len(inv.Issues) != 2 {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(err.Error(), "plan has 2 problem(s)") || !strings.Contains(err.Error(), "\n  tasks.md:6: b: unknown model rank") {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.md")
	if err := os.WriteFile(path, []byte(specExample), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Path != path || len(p.Tasks) != 4 {
		t.Fatalf("plan = %+v", p)
	}
	if issues := p.Validate(testRules); len(issues) > 0 && !strings.HasPrefix(issues[0].Error(), path+":") {
		t.Fatalf("issues must carry the path: %v", issues)
	}
	if _, err := Load(filepath.Join(dir, "missing.md"), Options{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
}

func TestIssueError(t *testing.T) {
	if got := (Issue{File: "f.md", Msg: "m"}).Error(); got != "f.md: m" {
		t.Fatal(got)
	}
	if got := (Issue{File: "f.md", Line: 3, Msg: "m"}).Error(); got != "f.md:3: m" {
		t.Fatal(got)
	}
}

func TestOptionalColumns(t *testing.T) {
	in := "## M1\n\n| ID | Status | Model | Owner | verify | Timeout | Context | Spec |\n|---|---|---|---|---|---|---|---|\n" +
		"| M1-01 | ready | sonnet | agent | `Fast` | ` 45m ` | `a.go`, b/, , a.go, ./a.go, b | §1 |\n" +
		"| M1-02 | ready | sonnet | agent | — | - | | §2 |\n" +
		"| M1-03 | ready | sonnet | agent | none | | | |\n"
	p := Parse("tasks.md", []byte(in), Options{})
	t1, t2, t3 := p.Task("M1-01"), p.Task("M1-02"), p.Task("M1-03")
	if t1.Verify != "fast" || t1.TimeoutText != "45m" || t1.ContextText != "`a.go`, b/, , a.go, ./a.go, b" || strings.Join(t1.Context, "|") != "a.go|b/" {
		t.Errorf("M1-01 = verify %q, timeout %q, context %q %q", t1.Verify, t1.TimeoutText, t1.ContextText, t1.Context)
	}
	if t2.Verify != "" || t2.TimeoutText != "" || t2.ContextText != "" || t2.Context != nil {
		t.Errorf("M1-02 has values: %+v", t2)
	}
	if t3.Verify != VerifyNone {
		t.Errorf("M1-03 verify = %q, want none", t3.Verify)
	}
	if _, ok := t1.Extra["Verify"]; ok || len(t1.Extra) != 1 || t1.Extra["Spec"] != "§1" {
		t.Errorf("Extra = %v; want only Spec", t1.Extra)
	}
	// Aliased like the other columns.
	p = Parse("tasks.md", []byte(strings.Replace(in, "verify", "Check", 1)), Options{Columns: map[string]string{"check": "Verify"}})
	if p.Task("M1-01").Verify != "fast" {
		t.Errorf("aliased Verify = %q", p.Task("M1-01").Verify)
	}
}

// A v0.3 plan whose own column is named Context (or Verify, Timeout) keeps
// it as an extra column through a [columns] alias to another name.
func TestOldSameNamedColumnAliasedAway(t *testing.T) {
	in := "## M1\n\n| ID | Status | Model | Context |\n|---|---|---|---|\n| M1-01 | ready | sonnet | see the old design, part 2 |\n"
	p := Parse("tasks.md", []byte(in), Options{Columns: map[string]string{"Context": "Background"}})
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatalf("issues: %q", issueMsgs(issues))
	}
	t1 := p.Task("M1-01")
	if t1.ContextText != "" || t1.Context != nil || t1.Extra["Background"] != "see the old design, part 2" {
		t.Errorf("Context %q %q, Extra %v", t1.ContextText, t1.Context, t1.Extra)
	}
}

func TestVerifyProfileValidation(t *testing.T) {
	const head = "## M1\n\n| ID | Status | Model | Owner | Verify |\n|---|---|---|---|---|\n"
	rules := Rules{Models: testModels, Verify: []string{"fast", "default"}}
	tests := []struct {
		name string
		row  string
		want []string
	}{
		{"known, case-insensitive", "| a | ready | sonnet | agent | FAST |", nil},
		{"none", "| a | ready | sonnet | agent | none |", nil},
		{"not set", "| a | ready | sonnet | agent | — |", nil},
		{"unknown", "| a | ready | sonnet | agent | fsat |", []string{`tasks.md:5: a: unknown verify profile "fsat"; define it under [verify] in igris.toml or use one of: default, fast, none`}},
		{"unknown on a done task", "| a | done | sonnet | agent | fsat |", nil},
		{"unknown on a skipped task", "| a | skipped | sonnet | agent | fsat |", nil},
		{"unknown on a user task", "| a | ready | — | user | fsat |", nil},
		{"none in backticks with spaces", "| a | ready | sonnet | agent | ` none ` |", nil},
		{"profile in backticks with spaces", "| a | ready | sonnet | agent | ` fast ` |", nil},
		{"a command", "| a | ready | sonnet | agent | make test |", []string{`tasks.md:5: a: Verify "make test" names a profile from [verify], never a command; put the command under [verify] in igris.toml and write the profile name here`}},
		{"a script path", "| a | ready | sonnet | agent | ./scripts/check |", []string{`tasks.md:5: a: Verify "./scripts/check" names a profile from [verify], never a command; put the command under [verify] in igris.toml and write the profile name here`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := issueMsgs(Parse("tasks.md", []byte(head+tt.row+"\n"), Options{}).Validate(rules))
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("issues = %q, want %q", got, tt.want)
			}
		})
	}
	// Without profiles in config only none is allowed.
	got := issueMsgs(Parse("tasks.md", []byte(head+"| a | ready | sonnet | agent | default |\n"), Options{}).Validate(testRules))
	if len(got) != 1 || !strings.HasSuffix(got[0], "or use one of: none") {
		t.Errorf("issues = %q", got)
	}
}

func TestTimeoutValidation(t *testing.T) {
	const head = "## M1\n\n| ID | Status | Model | Owner | Timeout |\n|---|---|---|---|---|\n"
	tests := []struct {
		name string
		row  string
		want time.Duration
		msgs []string
	}{
		{"minutes", "| a | ready | sonnet | agent | 45m |", 45 * time.Minute, nil},
		{"compound, backticks", "| a | ready | sonnet | agent | `1h30m` |", 90 * time.Minute, nil},
		{"not set", "| a | ready | sonnet | agent | — |", 0, nil},
		{"bare number", "| a | ready | sonnet | agent | 45 |", 0, []string{`tasks.md:5: a: Timeout "45" is not a duration; write e.g. 45m or 1h30m`}},
		{"words", "| a | ready | sonnet | agent | an hour |", 0, []string{`tasks.md:5: a: Timeout "an hour" is not a duration; write e.g. 45m or 1h30m`}},
		{"zero", "| a | ready | sonnet | agent | 0s |", 0, []string{`tasks.md:5: a: Timeout "0s" must be at least 1s; write e.g. 45m or 1h30m`}},
		{"negative", "| a | blocked | sonnet | agent | -5m |", 0, []string{`tasks.md:5: a: Timeout "-5m" must be at least 1s; write e.g. 45m or 1h30m`}},
		{"under a second", "| a | ready | sonnet | agent | 500ms |", 0, []string{`tasks.md:5: a: Timeout "500ms" must be at least 1s; write e.g. 45m or 1h30m`}},
		{"one second", "| a | ready | sonnet | agent | 1s |", time.Second, nil},
		{"too large", "| a | ready | sonnet | agent | 9999999999h |", 0, []string{`tasks.md:5: a: Timeout "9999999999h" is too large; write e.g. 45m or 1h30m`}},
		{"backticks and spaces quoted trimmed", "| a | ready | sonnet | agent | ` 45 ` |", 0, []string{`tasks.md:5: a: Timeout "45" is not a duration; write e.g. 45m or 1h30m`}},
		{"bad on a done task", "| a | done | sonnet | agent | 45 |", 0, nil},
		{"bad on a skipped task", "| a | skipped | sonnet | agent | 45 |", 0, nil},
		{"bad on a user task", "| a | ready | — | user | 45 |", 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Parse("tasks.md", []byte(head+tt.row+"\n"), Options{})
			got := issueMsgs(p.Validate(testRules))
			if strings.Join(got, "\n") != strings.Join(tt.msgs, "\n") {
				t.Errorf("issues = %q, want %q", got, tt.msgs)
			}
			if d := p.Task("a").Timeout; d != tt.want {
				t.Errorf("Timeout = %s, want %s", d, tt.want)
			}
		})
	}
}
