package side

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// The presets' first prompts (SPEC §6.10): what the dialog's First prompt
// field holds when the owner picks About this task or igris problem. Every
// part is cleaned (plan text and run-log details are untrusted) and the
// whole fits MaxPrompt with room left to type.

// promptRoom is what a prefill leaves of MaxPrompt for the owner's words.
const promptRoom = 1 << 10

// MaxEvents is how many run-log events the igris-problem prompt lists.
const MaxEvents = 10

// TaskInfo is what the About this task preset says about the task.
type TaskInfo struct {
	ID, Title string
	Phase     string // the phase ID
	PhaseName string // its heading's title; "" for none
	PlanPath  string // the plan's absolute path
	Text      string // the Task cell
	Running   bool   // a run's session works on it now
}

// TaskPrefill is the About this task first prompt: the task's ID, title,
// phase, the plan's path, its text and whether it runs now, then
// "Question: " for the owner to finish.
func TaskPrefill(t TaskInfo) string {
	phase := textsafe.Line(t.Phase)
	if n := textsafe.Line(t.PhaseName); n != "" {
		phase += " — " + n
	}
	running := "no"
	if t.Running {
		running = "yes, a session is working on it now"
	}
	head := fmt.Sprintf("I have a question about a task of this project's igris plan.\n\nTask: %s — %s\nPhase: %s\nPlan: %s\nRunning now: %s\n\nTask text:\n",
		textsafe.Line(t.ID), textsafe.Line(t.Title), phase, textsafe.Line(t.PlanPath), running)
	const tail = "\n\nQuestion: "
	text := cut(strings.TrimSpace(textsafe.Clean(t.Text)), MaxPrompt-promptRoom-len(head)-len(tail))
	return CleanPrompt(head + text + tail)
}

// IgrisInfo is what the igris problem preset says about igris.
type IgrisInfo struct {
	Version string
	Backend string
	// Run is the run going here, nil for none.
	Run *RunInfo
	// Events are the run log's last events, oldest first; the prompt
	// lists the last MaxEvents of them.
	Events []state.Event
	// Doctor are doctor's results; nil when they couldn't be had.
	Doctor []checks.Result
}

// RunInfo is the run state the igris problem preset reports.
type RunInfo struct {
	Phase string // "" between phases
	Task  string // the current task, "" between tasks
	Agent string // its agent state, "" when not known
}

// IgrisPrefill is the igris problem first prompt: igris's version, the
// backend, the run state, the last run-log events (type, task, detail),
// doctor's summary and where to read more without changing the plan, then
// "Problem: ". It never holds config values or secrets: run-log details
// never do (AGENTS §6), and doctor's checks are named, not quoted.
func IgrisPrefill(i IgrisInfo) string {
	var b strings.Builder
	b.WriteString("I have a problem with igris, the tool that runs this project's plan one Claude Code session per task.\n\n")
	fmt.Fprintf(&b, "igris version: %s\nBackend: %s\n", orNone(textsafe.Line(i.Version), "unknown"), orNone(textsafe.Line(i.Backend), "unknown"))
	if r := i.Run; r == nil {
		b.WriteString("Run: none\n")
	} else {
		fmt.Fprintf(&b, "Run: phase %s, current task %s, agent state %s\n",
			orNone(textsafe.Line(r.Phase), "—"), orNone(textsafe.Line(r.Task), "none (between tasks)"), orNone(textsafe.Line(r.Agent), "unknown"))
	}
	events := i.Events[max(len(i.Events)-MaxEvents, 0):]
	if len(events) == 0 {
		b.WriteString("\nThe run log has no events.\n")
	} else {
		fmt.Fprintf(&b, "\nThe last %d run-log events, oldest first:\n", len(events))
		for _, e := range events {
			line := textsafe.Line(string(e.Type))
			if e.Task != "" {
				line += " " + textsafe.Line(e.Task)
			}
			if d := textsafe.Line(e.Detail); d != "" {
				line += ": " + cut(d, 300)
			}
			b.WriteString("- " + line + "\n")
		}
	}
	b.WriteString("\n" + doctorSummary(i.Doctor))
	b.WriteString("\nRead .igris/runs.jsonl and .igris/state.json to look into it; don't change the plan.\n\nProblem: ")
	return CleanPrompt(b.String())
}

// doctorSummary is doctor's counts and which checks aren't ok, by ID: a
// check's message may quote igris.toml, so the session is sent to `igris
// doctor` for the details.
func doctorSummary(rs []checks.Result) string {
	if rs == nil {
		return "Doctor: not run.\n"
	}
	warns, fails := 0, 0
	var lines []string
	for _, r := range rs {
		switch r.Level {
		case checks.Warn:
			warns++
		case checks.Fail:
			fails++
		default:
			continue
		}
		lines = append(lines, "- "+string(r.Level)+": "+orNone(textsafe.Line(r.ID), "unnamed check"))
	}
	if warns+fails == 0 {
		return "Doctor: ok.\n"
	}
	return fmt.Sprintf("Doctor: %d warning(s), %d failure(s); `igris doctor` says what and what fixes it:\n%s\n", warns, fails, strings.Join(lines, "\n"))
}

func orNone(s, none string) string {
	if s == "" {
		return none
	}
	return s
}

// cut shortens s to at most n bytes on a character boundary, ending in
// "…" when it had to.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	n = max(n-len("…"), 0)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
