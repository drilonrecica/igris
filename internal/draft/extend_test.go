package draft

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// extendFragment adds a task to M0 and a new phase M1 to goodProposal.
const extendFragment = `# What the session adds

## M0

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M0-04 | **README** — usage | M0-03 | blocked | sonnet | agent |

## M1 — Renaming

The core.

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M1-01 | **Read EXIF dates** | M0-G | blocked | opus | agent | — |
| M1-G | **M1 gate** | M1-01 | blocked | sonnet | agent + user | — |

## Draft notes

- M0-04 sonnet: well specified
- M1-01 opus: picks the EXIF library
`

// goodProposal with extendFragment accepted.
var extendedPlan = strings.Replace(goodProposal,
	"| M0-G | **M0 gate** | M0-01…M0-03 | blocked | sonnet | agent + user | — |\n",
	"| M0-G | **M0 gate** | M0-01…M0-03 | blocked | sonnet | agent + user | — |\n"+
		"| M0-04 | **README** — usage | M0-03 | blocked | sonnet | agent |  |\n", 1) +
	"\n## M1 — Renaming\n\nThe core.\n\n| ID | Task | Deps | Status | Model | Owner | Mode |\n|---|---|---|---|---|---|---|\n" +
	"| M1-01 | **Read EXIF dates** | M0-G | blocked | opus | agent | — |\n| M1-G | **M1 gate** | M1-01 | blocked | sonnet | agent + user | — |\n"

// finishExtend makes the extend session write fragment and signal done as
// soon as the prompt arrives.
func (h *harness) finishExtend(fragment string) {
	h.be.SetAutoSignal(func(context.Context, string) error {
		if err := os.WriteFile(ExtendPath(h.dir, h.plan), []byte(fragment), 0o600); err != nil {
			return err
		}
		return h.dir.WriteSignal(state.Signal{ID: ID, Action: state.ActionDone, Note: "added M1"})
	})
}

// draft extend end to end with the fake backend: the session is told the
// plan and the request, igris splices the new part in memory, the review
// lists only additions with their files, and Accept backs the plan up and
// adds the new part, every other byte kept (SPEC §9.1).
func TestRunExtend(t *testing.T) {
	h := newHarness(t)
	write(t, h.plan, goodProposal)
	h.finishExtend(extendFragment)
	res, err := h.run(context.Background(), Extend, "rename by \x1b[31mEXIF date")
	if err != nil {
		t.Fatal(err)
	}
	p := h.be.Prompts(ID)
	if len(p) != 1 {
		t.Fatalf("prompts = %d", len(p))
	}
	for _, want := range []string{"# Extend the plan `tasks.md`", "\nrename by EXIF date\n", ".igris/draft/tasks.extend.md", "```\ntasks.md\n```"} {
		if !strings.Contains(p[0], want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if res.ProposalPath != ExtendPath(h.dir, h.plan) || res.Note != "added M1" || res.Extension == nil {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Issues) != 0 || res.Ranks != (Ranks{Set: 3}) {
		t.Fatalf("issues %v, ranks %+v", res.Issues, res.Ranks)
	}

	rv := Review(res)
	var lines []string
	for _, s := range rv.Sections {
		lines = append(lines, mark(s.Op)+" "+s.Heading)
		for _, c := range s.Changes {
			lines = append(lines, "  "+mark(c.Op)+" "+c.String())
		}
	}
	want := []string{
		"~ M0 — Foundation · tasks.md",
		"  + M0-04 README · deps M0-03 · sonnet · agent",
		"+ M1 — Renaming · tasks.md",
		"  + M1-01 Read EXIF dates · deps M0-G · opus · agent",
		"  + M1-G M1 gate · deps M1-01 · sonnet · agent + user",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("review:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if !rv.Tables || len(rv.Notes) != 2 || rv.Notes[1] != "- M1-01 opus: picks the EXIF library" {
		t.Errorf("review = %+v", rv)
	}
	if added, _ := adapt.Counts(rv.Prose); added != 3 { // "", "The core.", "" before the table
		t.Errorf("prose = %+v", rv.Prose)
	}
	if data, _ := os.ReadFile(h.plan); string(data) != goodProposal { //nolint:gosec // test temp dir
		t.Fatal("Run changed the plan")
	}

	backups, err := Accept(h.dir, res, t0)
	if err != nil {
		t.Fatal(err)
	}
	wantBackup := filepath.Join(h.dir.DraftDir(), "tasks.20261021-120000.bak.md")
	if len(backups) != 1 || backups[0] != wantBackup {
		t.Fatalf("backups = %v", backups)
	}
	if data, _ := os.ReadFile(wantBackup); string(data) != goodProposal { //nolint:gosec // test temp dir
		t.Errorf("backup = %q", data)
	}
	if data, _ := os.ReadFile(h.plan); string(data) != extendedPlan { //nolint:gosec // test temp dir
		t.Errorf("plan:\n%s\nwant:\n%s", data, extendedPlan)
	}
	if _, err := os.Stat(res.ProposalPath); err != nil {
		t.Errorf("the new part is gone: %v", err)
	}
	if p, err := LoadValid(h.cfg(), h.root, h.plan); err != nil || len(p.Tasks) != 7 {
		t.Fatalf("extended plan: %v", err)
	}
}

// A `?` rank is the extended plan's problem: the review shows it and asks
// the second confirmation; the fragment is still offered.
func TestRunExtendOpenRank(t *testing.T) {
	h := newHarness(t)
	write(t, h.plan, goodProposal)
	h.finishExtend(strings.Replace(extendFragment, "| blocked | opus | agent | — |", "| blocked | ? | agent | — |", 1))
	res, err := h.run(context.Background(), Extend, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 1 || res.Issues[0].Line != 26 || !strings.Contains(res.Issues[0].Msg, `"?"`) || res.Ranks != (Ranks{Set: 2, Open: 1}) {
		t.Fatalf("issues %v, ranks %+v", res.Issues, res.Ranks)
	}
	if p := h.be.Prompts(ID); !strings.Contains(p[0], "The owner hasn't said what to add yet") {
		t.Error("prompt doesn't ask for the request")
	}
}

// A fragment igris can't splice is an error naming its problems: nothing
// is offered and the plan is untouched.
func TestRunExtendRefusesFragment(t *testing.T) {
	h := newHarness(t)
	write(t, h.plan, goodProposal)
	h.finishExtend(strings.Replace(extendFragment, "| M0-04 |", "| M0-03 |", 1))
	_, err := h.run(context.Background(), Extend, "x")
	want := "igris can't add .igris/draft/tasks.extend.md to tasks.md: 1 problem(s); nothing was written, run `igris draft extend` again:\n" +
		"  .igris/draft/tasks.extend.md:7: task M0-03 is already in the plan ("
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v", err)
	}
	if data, _ := os.ReadFile(h.plan); string(data) != goodProposal { //nolint:gosec // test temp dir
		t.Fatal("plan changed")
	}
}

// The session finishing without the file is an error, as for the others.
func TestRunExtendWithoutFile(t *testing.T) {
	h := newHarness(t)
	write(t, h.plan, goodProposal)
	h.finishOnPrompt("")
	if _, err := h.run(context.Background(), Extend, "x"); err == nil || !strings.Contains(err.Error(), "without writing .igris/draft/tasks.extend.md") {
		t.Fatalf("err = %v", err)
	}
}

// A split plan: rows go into the included file their phase lives in, a new
// phase into the main plan; both are backed up.
func TestRunExtendSplitPlan(t *testing.T) {
	h := newHarness(t)
	main := "<!-- igris:format 2 -->\n# Photo renamer\n\n<!-- igris:include phases/m0.md -->\n"
	inc := strings.Replace(goodProposal, "## Draft notes\n\n- M0-02 opus: decides the layout\n- M0-03 sonnet: well specified\n- M0-G sonnet: runs the checks\n", "", 1)
	if err := os.MkdirAll(filepath.Join(h.root, "phases"), 0o750); err != nil {
		t.Fatal(err)
	}
	write(t, h.plan, main)
	write(t, filepath.Join(h.root, "phases", "m0.md"), inc)
	h.finishExtend(extendFragment)
	res, err := h.run(context.Background(), Extend, "x")
	if err != nil {
		t.Fatal(err)
	}
	if p := h.be.Prompts(ID); !strings.Contains(p[0], "```\ntasks.md\nphases/m0.md\n```") {
		t.Error("prompt lacks the plan's files")
	}
	if s := Review(res).Sections; len(s) != 2 || s[0].Heading != "M0 — Foundation · phases/m0.md" || s[1].Heading != "M1 — Renaming · tasks.md" {
		t.Fatalf("sections = %+v", s)
	}
	backups, err := Accept(h.dir, res, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 2 || filepath.Base(backups[0]) != "tasks.20261021-120000.bak.md" || filepath.Base(backups[1]) != "m0.20261021-120000.bak.md" {
		t.Fatalf("backups = %v", backups)
	}
	if data, _ := os.ReadFile(filepath.Join(h.root, "phases", "m0.md")); !strings.Contains(string(data), "| M0-04 | **README** — usage | M0-03 | blocked | sonnet | agent |  |\n") { //nolint:gosec // test temp dir
		t.Errorf("included file = %s", data)
	}
	if data, _ := os.ReadFile(h.plan); !strings.HasPrefix(string(data), main+"\n## M1 — Renaming\n") { //nolint:gosec // test temp dir
		t.Errorf("main plan = %s", data)
	}
}

// A plan edited during the review is not overwritten: the backup is taken,
// nothing is written and the new part stays.
func TestAcceptExtendPlanChanged(t *testing.T) {
	h := newHarness(t)
	write(t, h.plan, goodProposal)
	h.finishExtend(extendFragment)
	res, err := h.run(context.Background(), Extend, "x")
	if err != nil {
		t.Fatal(err)
	}
	write(t, h.plan, goodProposal+"edited\n")
	_, err = Accept(h.dir, res, t0)
	if !errors.Is(err, plan.ErrConcurrentEdit) || !strings.Contains(err.Error(), "the new part stays in "+res.ProposalPath) {
		t.Fatalf("err = %v", err)
	}
	if data, _ := os.ReadFile(h.plan); string(data) != goodProposal+"edited\n" { //nolint:gosec // test temp dir
		t.Fatal("plan overwritten")
	}
}

// mark is the review's marker of op.
func mark(op adapt.Op) string {
	return map[adapt.Op]string{adapt.Add: "+", adapt.Del: "-", adapt.Mod: "~"}[op]
}
