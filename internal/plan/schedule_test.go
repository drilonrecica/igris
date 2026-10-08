package plan

import (
	"reflect"
	"strings"
	"testing"
)

// schedPlan builds a plan from "PHASE: ID status deps…" lines.
func schedPlan(t *testing.T, spec string) *Plan {
	t.Helper()
	var b strings.Builder
	cur := ""
	for _, ln := range strings.Split(strings.TrimSpace(spec), "\n") {
		ph, rest, _ := strings.Cut(strings.TrimSpace(ln), ":")
		f := strings.Fields(rest)
		if ph != cur {
			cur = ph
			b.WriteString("\n## " + ph + "\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n")
		}
		status := strings.ReplaceAll(f[1], "_", " ")
		b.WriteString("| " + f[0] + " | " + strings.Join(f[2:], ", ") + " | " + status + " | sonnet |\n")
	}
	p := Parse("tasks.md", []byte(b.String()), Options{})
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatalf("invalid test plan: %v", issueMsgs(issues))
	}
	return p
}

func changeStrings(cs []Change) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.String())
	}
	return out
}

func TestReadiness(t *testing.T) {
	p := schedPlan(t, `
P0: a done
P0: b skipped
P0: c in_progress
M0: d blocked a b
M0: e ready c
M0: f ready
M0: g blocked
M0: h done c
M0: i ready a
M0: j blocked c`)
	want := []string{"d: blocked → ready", "e: ready → blocked", "g: blocked → ready"}
	if got := changeStrings(p.Readiness()); !reflect.DeepEqual(got, want) {
		t.Fatalf("readiness = %q, want %q", got, want)
	}
	// Readiness is a pure query.
	if p.Task("d").Status != Blocked {
		t.Fatal("Readiness must not change the plan")
	}
}

func TestSync(t *testing.T) {
	p := schedPlan(t, `
P0: a in_progress
P0: b blocked a
M0: c blocked a b
M0: d blocked a
M0: e ready`)
	got, err := p.Sync("a", Done)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a: in progress → done", "b: blocked → ready", "d: blocked → ready"}
	if !reflect.DeepEqual(changeStrings(got), want) {
		t.Fatalf("sync = %q, want %q", changeStrings(got), want)
	}
	if p.Task("a").Status != Done || p.Task("b").Status != Ready || p.Task("c").Status != Blocked {
		t.Fatal("Sync must apply the changes")
	}
	if len(p.Readiness()) != 0 {
		t.Fatal("no drift after Sync")
	}

	// Re-opening a dependency blocks dependents again.
	got, _ = p.Sync("a", InProgress)
	want = []string{"a: done → in progress", "b: ready → blocked", "d: ready → blocked"}
	if !reflect.DeepEqual(changeStrings(got), want) {
		t.Fatalf("sync = %q, want %q", changeStrings(got), want)
	}

	// Same status: only drift is returned.
	q := schedPlan(t, "M0: x ready\nM0: y ready x")
	got, _ = q.Sync("x", Ready)
	if want := []string{"y: ready → blocked"}; !reflect.DeepEqual(changeStrings(got), want) {
		t.Fatalf("sync = %q", changeStrings(got))
	}

	if _, err := p.Sync("nope", Done); err == nil {
		t.Fatal("unknown task must fail")
	}
}

func TestApply(t *testing.T) {
	p := Parse("tasks.md", []byte(table("| a | | `skipped` (old) | sonnet | agent | |")), Options{})
	if err := p.Apply([]Change{{ID: "a", To: Done}}); err != nil {
		t.Fatal(err)
	}
	if a := p.Task("a"); a.Status != Done || a.StatusText != "done" || a.Suffix != "" {
		t.Fatalf("a = %+v", a)
	}
	if err := p.Apply([]Change{{ID: "a", To: Ready}, {ID: "zz", To: Done}}); err == nil || p.Task("a").Status != Done {
		t.Fatal("Apply must be all-or-nothing")
	}
}

func TestSelect(t *testing.T) {
	tests := []struct {
		name    string
		plan    string
		phase   string
		outcome Outcome
		task    string
		waiting []string
	}{
		{"first ready in file order", "M0: a done\nM0: b ready\nM0: c ready", "M0", Next, "b", nil},
		{"in progress wins (resume)", "M0: a ready\nM0: b in_progress", "M0", Next, "b", nil},
		{"blocked cell but deps met", "M0: a done\nM0: b blocked a", "M0", Next, "b", nil},
		{"ready cell but deps unmet is skipped", "M0: a ready\nM0: b ready a\nM0: c ready", "m0", Next, "a", nil},
		{"next after unmet", "M0: x in_progress\nM1: a ready x\nM1: b ready", "M1", Next, "b", nil},
		{"complete", "M0: a done\nM0: b skipped", "M0", Complete, "", nil},
		{"stuck on other phase", "P0: p ready\nM0: a done\nM0: b blocked p\nM0: c blocked b p", "M0", Stuck, "",
			[]string{"b waits on p (ready, phase P0)", "c waits on b (blocked, phase M0), p (ready, phase P0)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := schedPlan(t, tt.plan)
			sel, err := p.Select(tt.phase)
			if err != nil {
				t.Fatal(err)
			}
			if sel.Outcome != tt.outcome {
				t.Fatalf("outcome = %v, want %v", sel.Outcome, tt.outcome)
			}
			id := ""
			if sel.Task != nil {
				id = sel.Task.ID
			}
			if id != tt.task {
				t.Fatalf("task = %q, want %q", id, tt.task)
			}
			var waiting []string
			for _, w := range sel.Waiting {
				waiting = append(waiting, w.String())
			}
			if !reflect.DeepEqual(waiting, tt.waiting) {
				t.Fatalf("waiting = %q, want %q", waiting, tt.waiting)
			}
		})
	}
}

// SelectIn chooses among a slice only (SPEC §5.5).
func TestSelectIn(t *testing.T) {
	tests := []struct {
		name    string
		plan    string
		slice   string
		outcome Outcome
		task    string
		waiting []string
	}{
		{"first ready of the slice", "M0: a ready\nM0: b ready\nM0: c ready", "c b", Next, "b", nil},
		{"in progress outside the slice is left", "M0: a in_progress\nM0: b ready", "b", Next, "b", nil},
		{"in progress in the slice wins", "M0: a ready\nM0: b in_progress", "a b", Next, "b", nil},
		{"slice satisfied, phase not", "M0: a done\nM0: b ready", "a", Complete, "", nil},
		{"no slice task in the phase", "M0: a ready", "z", Complete, "", nil},
		{"slice task waits outside the slice", "M0: a ready\nM0: b blocked a", "b", Stuck, "", []string{"b waits on a (ready, phase M0)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := schedPlan(t, tt.plan)
			in := map[string]bool{}
			for _, id := range strings.Fields(tt.slice) {
				in[id] = true
			}
			sel, err := p.SelectIn("M0", func(t *Task) bool { return in[t.ID] })
			if err != nil {
				t.Fatal(err)
			}
			id := ""
			if sel.Task != nil {
				id = sel.Task.ID
			}
			var waiting []string
			for _, w := range sel.Waiting {
				waiting = append(waiting, w.String())
			}
			if sel.Outcome != tt.outcome || id != tt.task || !reflect.DeepEqual(waiting, tt.waiting) {
				t.Errorf("SelectIn = %v %q %q, want %v %q %q", sel.Outcome, id, waiting, tt.outcome, tt.task, tt.waiting)
			}
		})
	}
}

func TestSelectUnknownPhase(t *testing.T) {
	p := schedPlan(t, "M0: a ready\nM1: b ready")
	_, err := p.Select("M9")
	if err == nil || err.Error() != `unknown phase "M9" in tasks.md; phases are: M0, M1` {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitingUnknownDep(t *testing.T) {
	p := Parse("tasks.md", []byte(table("| a | zz | blocked | sonnet | agent | |")), Options{})
	sel, _ := p.Select("M0")
	if sel.Outcome != Stuck || sel.Waiting[0].String() != "a waits on zz (unknown task)" {
		t.Fatalf("sel = %+v", sel)
	}
}

func TestPhasesThrough(t *testing.T) {
	p := schedPlan(t, "P0: a ready\nM0: b ready\nM1: c ready\nM2: d ready")
	ids := func(phs []*Phase) []string {
		var out []string
		for _, ph := range phs {
			out = append(out, ph.ID)
		}
		return out
	}
	tests := []struct {
		from, through string
		want          []string
		err           string
	}{
		{"M0", "", []string{"M0"}, ""},
		{"m0", "M2", []string{"M0", "M1", "M2"}, ""},
		{"P0", "p0", []string{"P0"}, ""},
		{"M1", "M0", nil, "--through M0 comes before M1 in the plan; give a later phase"},
		{"M9", "", nil, `unknown phase "M9" in tasks.md; phases are: P0, M0, M1, M2`},
		{"M0", "M9", nil, `unknown phase "M9" in tasks.md; phases are: P0, M0, M1, M2`},
	}
	for _, tt := range tests {
		got, err := p.PhasesThrough(tt.from, tt.through)
		if tt.err != "" {
			if err == nil || err.Error() != tt.err {
				t.Errorf("%s..%s: err = %v, want %q", tt.from, tt.through, err, tt.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(ids(got), tt.want) {
			t.Errorf("%s..%s = %v, %v; want %v", tt.from, tt.through, ids(got), err, tt.want)
		}
	}
}

func TestReset(t *testing.T) {
	const spec = `
M0: a done
M0: b in_progress a
M0: c done a
M0: d skipped x
M0: x blocked a b
M0: y ready
M0: z in_progress b`
	tests := []struct {
		id    string
		force bool
		want  []string
		err   string
	}{
		{"b", false, []string{"b: in progress → ready"}, ""},
		{"a", true, []string{"a: done → ready"}, ""}, // b, z in progress and c done stay
		{"a", false, nil, "a is done; pass --force to reset it"},
		{"d", false, nil, "d is skipped; pass --force to reset it"},
		{"d", true, []string{"d: skipped → blocked"}, ""},
		{"y", true, nil, ""},
		{"x", false, nil, ""},
		{"nope", false, nil, "task nope is not in the plan tasks.md"},
	}
	for _, tt := range tests {
		p := schedPlan(t, spec)
		got, err := p.Reset(tt.id, tt.force)
		if errText(err) != tt.err || !reflect.DeepEqual(changeStrings(got), tt.want) {
			t.Errorf("Reset(%s, %v) = %q, %v; want %q, %q", tt.id, tt.force, changeStrings(got), err, tt.want, tt.err)
		}
	}
	p := schedPlan(t, spec)
	var deps []string
	for _, d := range p.Dependents("a") {
		deps = append(deps, d.ID)
	}
	if strings.Join(deps, " ") != "b c x" {
		t.Errorf("Dependents(a) = %v", deps)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
