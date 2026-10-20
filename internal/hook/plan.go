package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

// Plan approval from igris (SPEC §6.3, V11-P1): for a session in plan mode
// the settings file has a PermissionRequest entry for ExitPlanMode that
// runs `igris hook plan-approve`. It is the one hook that waits: it hands
// the plan to igris and answers Claude Code with the owner's decision.

const (
	// PlanCommand is the event argument of the plan approval hook.
	PlanCommand = "plan-approve"
	// maxPlanInput bounds the event JSON plan-approve reads: a plan can be
	// long.
	maxPlanInput = 1 << 20
	// planPoll is how often plan-approve looks for igris's decision.
	planPoll = 200 * time.Millisecond
	// planGrace is what the settings file adds to the wait for Claude
	// Code's timeout, so the hook gives up before it is killed.
	planGrace = 30 * time.Second
)

// SetModes are the Claude Code permission modes plan-approve may switch an
// approved session to (SPEC §7.1), by the value Claude Code takes.
var SetModes = map[string]bool{"default": true, "acceptEdits": true, "auto": true}

// PlanApproval adds the plan approval entry to a session's settings file:
// Mode is the Claude Code permission mode an approval switches to (a
// SetModes key), Wait how long the hook waits for igris (> 0).
type PlanApproval struct {
	Mode string
	Wait time.Duration
}

// PlanOptions configures ApprovePlan.
type PlanOptions struct {
	Mode string        // the permission mode an approval switches to (SetModes)
	Wait time.Duration // how long to wait for igris's decision
	Now  func() time.Time
	// Poll is the decision poll interval; 0 means 200 ms.
	Poll time.Duration
	// Alive reports whether the igris that holds root's lock is still
	// running; nil checks igris.lock.
	Alive func() bool
}

// planPayload is what plan-approve reads from a PermissionRequest event.
type planPayload struct {
	SessionID string          `json:"session_id"`
	Event     string          `json:"hook_event_name"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// ApprovePlan hands the plan of the PermissionRequest event on in to the
// igris running for root and writes Claude Code's answer to out once igris
// decides: allow (with the tool input echoed as updatedInput and a setMode
// to opt.Mode) or deny with the owner's feedback. It writes nothing, so
// the pane decides, when the owner chose the pane, when opt.Wait passes,
// when igris is gone, when ctx ends (Claude Code cancels the hook) or on
// any error. The request and decision files are removed on return.
func ApprovePlan(ctx context.Context, root string, in io.Reader, out io.Writer, opt PlanOptions) error {
	if !SetModes[opt.Mode] {
		return fmt.Errorf("plan-approve: unknown mode %q", opt.Mode)
	}
	if opt.Wait <= 0 {
		return errors.New("plan-approve: no wait given")
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Poll <= 0 {
		opt.Poll = planPoll
	}
	if opt.Alive == nil {
		opt.Alive = func() bool {
			st, err := state.PeekLock(root)
			return err == nil && st.Alive
		}
	}
	data, err := io.ReadAll(io.LimitReader(in, maxPlanInput+1))
	if err != nil {
		return fmt.Errorf("plan-approve: read event: %w", err)
	}
	if len(data) > maxPlanInput {
		return errors.New("plan-approve: event larger than 1 MiB")
	}
	var p planPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("plan-approve: decode event: %w", err)
	}
	var input struct {
		Plan string `json:"plan"`
	}
	switch {
	case !state.ValidSessionUUID(p.SessionID):
		return fmt.Errorf("plan-approve: invalid session id %q", p.SessionID)
	case p.Event != "PermissionRequest" || p.ToolName != "ExitPlanMode":
		return fmt.Errorf("plan-approve: not a plan: %s for %s", p.Event, p.ToolName)
	case len(bytes.TrimSpace(p.ToolInput)) == 0 || bytes.TrimSpace(p.ToolInput)[0] != '{':
		return errors.New("plan-approve: tool_input is not an object")
	}
	if err := json.Unmarshal(p.ToolInput, &input); err != nil {
		return fmt.Errorf("plan-approve: decode tool_input: %w", err)
	}
	if !opt.Alive() {
		return nil
	}
	n, err := state.NewPlanN()
	if err != nil {
		return err
	}
	start := opt.Now()
	req := state.PlanRequest{N: n, At: start.UTC(), Plan: input.Plan, PID: os.Getpid()}
	if err := state.WritePlanRequest(root, p.SessionID, req); err != nil {
		return err
	}
	defer func() { _ = state.RemovePlanApproval(root, p.SessionID, n) }()

	tick := time.NewTicker(opt.Poll)
	defer tick.Stop()
	for {
		if d, ok, _ := state.ReadPlanDecision(root, p.SessionID); ok && d.N == n {
			return answer(out, d, p.ToolInput, opt.Mode)
		}
		if opt.Now().Sub(start) >= opt.Wait || !opt.Alive() {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// answer writes Claude Code's PermissionRequest output for decision d.
// Claude Code ignores an allow for ExitPlanMode without updatedInput
// (V11-P1), so the tool input received is echoed.
func answer(out io.Writer, d state.PlanDecision, toolInput json.RawMessage, mode string) error {
	type setMode struct {
		Type        string `json:"type"`
		Mode        string `json:"mode"`
		Destination string `json:"destination"`
	}
	type decision struct {
		Behavior           string          `json:"behavior"`
		UpdatedInput       json.RawMessage `json:"updatedInput,omitempty"`
		UpdatedPermissions []setMode       `json:"updatedPermissions,omitempty"`
		Message            string          `json:"message,omitempty"`
	}
	dec := decision{Behavior: d.Behavior}
	switch d.Behavior {
	case state.PlanAllow:
		dec.UpdatedInput = toolInput
		dec.UpdatedPermissions = []setMode{{Type: "setMode", Mode: mode, Destination: "session"}}
	case state.PlanDeny:
		dec.Message = d.Message
	default: // the pane decides
		return nil
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	err := enc.Encode(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "PermissionRequest",
		"decision":      dec,
	}})
	if err != nil {
		return fmt.Errorf("plan-approve: encode the decision: %w", err)
	}
	_, err = out.Write(b.Bytes())
	return err
}
