package state

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/fsafe"
	"github.com/drilonrecica/igris/internal/plan"
)

// Plan approval from igris (SPEC §6.3, V11-P1): `igris hook plan-approve`
// writes a PlanRequest for a session that presents a plan, igris answers
// with a PlanDecision. Sessions can write both files, so both are read
// bounded and shape-checked.

// Plan decision behaviors (SPEC §6.3).
const (
	PlanAllow = "allow" // approve; the hook answers Claude Code itself
	PlanDeny  = "deny"  // reject with Message as the feedback
	PlanPane  = "pane"  // the owner answers in the pane; the hook prints nothing
)

const (
	// MaxPlanText bounds the plan text a request holds: the hook reads at
	// most 1 MiB of event JSON.
	MaxPlanText = 1 << 20
	// MaxPlanFeedback bounds a rejection's feedback (SPEC §6.3).
	MaxPlanFeedback = 4 << 10
	// maxPlanRequestSize bounds a request file: the plan, JSON-escaped
	// (at most 6 bytes per byte), and the other fields.
	maxPlanRequestSize = 6*MaxPlanText + 1<<10
	// maxPlanDecisionSize bounds a decision file.
	maxPlanDecisionSize = 8*MaxPlanFeedback + 1<<10
)

// PlanRequest is a plan waiting for approval: .igris/plan-approval/<session
// uuid>.json, written by the hook.
type PlanRequest struct {
	N    string    `json:"n"` // a random ID (NewPlanN); the decision names it
	At   time.Time `json:"at"`
	Plan string    `json:"plan"` // tool_input.plan, never planFilePath
	// PID is the waiting hook's process, so igris can tell a request whose
	// hook Claude Code killed (the owner answered in the pane) from a live
	// one.
	PID int `json:"pid,omitempty"`
}

// PlanDecision is igris's answer: .igris/plan-approval/<session
// uuid>.decision.json.
type PlanDecision struct {
	N        string `json:"n"`
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"` // deny: the owner's feedback
}

var planNPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NewPlanN returns a random request ID.
func NewPlanN() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate plan request ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// PlanApprovalDir returns root/.igris/plan-approval.
func PlanApprovalDir(root string) string { return filepath.Join(root, DirName, "plan-approval") }

func planRequestPath(root, uuid string) string {
	return filepath.Join(PlanApprovalDir(root), uuid+".json")
}

func planDecisionPath(root, uuid string) string {
	return filepath.Join(PlanApprovalDir(root), uuid+".decision.json")
}

// WritePlanRequest records r for the session uuid. Like WriteAgentState it
// never creates .igris/ itself. A decision left from an earlier request is
// removed first, so it can't answer this one even by a reused ID.
func WritePlanRequest(root, uuid string, r PlanRequest) error {
	if !ValidSessionUUID(uuid) {
		return fmt.Errorf("write plan request: invalid session id %q", uuid)
	}
	if err := checkPlanRequest(r); err != nil {
		return fmt.Errorf("write plan request: %w", err)
	}
	if _, err := os.Stat(filepath.Join(root, DirName)); err != nil {
		return fmt.Errorf("write plan request: %w", err)
	}
	dir := PlanApprovalDir(root)
	if err := mkdirPrivate(dir); err != nil {
		return err
	}
	if err := removeFile(planDecisionPath(root, uuid)); err != nil {
		return fmt.Errorf("write plan request: %w", err)
	}
	data, err := marshalPlain(r)
	if err != nil {
		return fmt.Errorf("write plan request: %w", err)
	}
	return writeFileAtomic(planRequestPath(root, uuid), data)
}

// ReadPlanRequest returns the plan waiting for approval for the session
// uuid, and false if there is none.
func ReadPlanRequest(root, uuid string) (PlanRequest, bool, error) {
	if !ValidSessionUUID(uuid) {
		return PlanRequest{}, false, fmt.Errorf("read plan request: invalid session id %q", uuid)
	}
	path := planRequestPath(root, uuid)
	data, err := fsafe.ReadFile(path, maxPlanRequestSize, false)
	if errors.Is(err, fs.ErrNotExist) {
		return PlanRequest{}, false, nil
	}
	if err != nil {
		return PlanRequest{}, false, fmt.Errorf("read plan request: %w", err)
	}
	var r PlanRequest
	if err := json.Unmarshal(data, &r); err != nil {
		return PlanRequest{}, false, fmt.Errorf("read plan request %s: %w", path, err)
	}
	if err := checkPlanRequest(r); err != nil {
		return PlanRequest{}, false, fmt.Errorf("read plan request %s: %w", path, err)
	}
	return r, true, nil
}

func checkPlanRequest(r PlanRequest) error {
	switch {
	case !planNPattern.MatchString(r.N):
		return errors.New("the request ID is not igris's")
	case len(r.Plan) > MaxPlanText:
		return fmt.Errorf("the plan is over %d bytes", MaxPlanText)
	case !utf8.ValidString(r.Plan):
		return errors.New("the plan is not UTF-8 text")
	case r.PID < 0:
		return errors.New("negative pid")
	}
	return nil
}

// WritePlanDecision answers the request of the session uuid. root's
// plan-approval/ exists: the request is in it.
func WritePlanDecision(root, uuid string, d PlanDecision) error {
	if !ValidSessionUUID(uuid) {
		return fmt.Errorf("write plan decision: invalid session id %q", uuid)
	}
	if err := checkPlanDecision(d); err != nil {
		return fmt.Errorf("write plan decision: %w", err)
	}
	if err := mkdirPrivate(PlanApprovalDir(root)); err != nil {
		return err
	}
	data, err := marshalPlain(d)
	if err != nil {
		return fmt.Errorf("write plan decision: %w", err)
	}
	return writeFileAtomic(planDecisionPath(root, uuid), data)
}

// ReadPlanDecision returns igris's answer for the session uuid, and false
// if there is none.
func ReadPlanDecision(root, uuid string) (PlanDecision, bool, error) {
	if !ValidSessionUUID(uuid) {
		return PlanDecision{}, false, fmt.Errorf("read plan decision: invalid session id %q", uuid)
	}
	path := planDecisionPath(root, uuid)
	data, err := fsafe.ReadFile(path, maxPlanDecisionSize, false)
	if errors.Is(err, fs.ErrNotExist) {
		return PlanDecision{}, false, nil
	}
	if err != nil {
		return PlanDecision{}, false, fmt.Errorf("read plan decision: %w", err)
	}
	var d PlanDecision
	if err := json.Unmarshal(data, &d); err != nil {
		return PlanDecision{}, false, fmt.Errorf("read plan decision %s: %w", path, err)
	}
	if err := checkPlanDecision(d); err != nil {
		return PlanDecision{}, false, fmt.Errorf("read plan decision %s: %w", path, err)
	}
	return d, true, nil
}

func checkPlanDecision(d PlanDecision) error {
	switch {
	case !planNPattern.MatchString(d.N):
		return errors.New("the request ID is not igris's")
	case d.Behavior != PlanAllow && d.Behavior != PlanDeny && d.Behavior != PlanPane:
		return fmt.Errorf("unknown behavior %q", d.Behavior)
	case len(d.Message) > MaxPlanFeedback:
		return fmt.Errorf("the feedback is over %d bytes", MaxPlanFeedback)
	case !utf8.ValidString(d.Message):
		return errors.New("the feedback is not UTF-8 text")
	}
	return nil
}

// RemovePlanApproval removes the request of the session uuid and its
// decision, if the request is still the one with ID n: the hook that wrote
// it is done. A newer request is left alone. Missing files are not an
// error.
func RemovePlanApproval(root, uuid, n string) error {
	if !ValidSessionUUID(uuid) {
		return fmt.Errorf("remove plan request: invalid session id %q", uuid)
	}
	if r, ok, err := ReadPlanRequest(root, uuid); err == nil && ok && r.N != n {
		return nil
	}
	if err := removeFile(planRequestPath(root, uuid)); err != nil {
		return fmt.Errorf("remove plan request: %w", err)
	}
	if err := removeFile(planDecisionPath(root, uuid)); err != nil {
		return fmt.Errorf("remove plan request: %w", err)
	}
	return nil
}

// PlansDir returns root/.igris/plans, where every plan presented for
// approval is kept (SPEC §13).
func PlansDir(root string) string { return filepath.Join(root, DirName, "plans") }

var planFilePattern = regexp.MustCompile(`^-a[1-9][0-9]{0,5}-[1-9][0-9]{0,3}\.md$`)

// ValidPlanFile reports whether path is a plan file SavePlan returns for
// task id: .igris/plans/<id>-a<attempt>-<n>.md.
func ValidPlanFile(path, id string) bool {
	rest, ok := strings.CutPrefix(path, filepath.Join(DirName, "plans", id))
	return ok && plan.ValidID(id) && planFilePattern.MatchString(rest)
}

// ReadPlanFile returns the text of the plan file path (ValidPlanFile)
// under root: a regular file of at most MaxPlanText bytes.
func ReadPlanFile(root, path string) (string, error) {
	data, err := fsafe.ReadFile(filepath.Join(root, path), MaxPlanText+1, false)
	if err != nil {
		return "", fmt.Errorf("read plan: %w", err)
	}
	return string(data), nil
}

// SavePlan keeps text, a plan the session of task id presented in attempt,
// as .igris/plans/<ID>-a<attempt>-<n>.md with the first n from 1 that is
// free (an earlier run's attempt may have the same number). It returns the
// path relative to the project root.
func (d *Dir) SavePlan(id string, attempt int, text string) (string, error) {
	if err := checkSignalID(id); err != nil {
		return "", fmt.Errorf("save plan: %w", err)
	}
	dir := PlansDir(d.root)
	if err := mkdirPrivate(dir); err != nil {
		return "", err
	}
	data := []byte(text)
	if !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	for n := 1; n < 10000; n++ {
		name := id + "-a" + strconv.Itoa(attempt) + "-" + strconv.Itoa(n) + ".md"
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm) //nolint:gosec // igris's own state directory; id validated
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("save plan: %w", err)
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("save plan %s: %w", path, werr)
		}
		return filepath.Join(DirName, "plans", name), nil
	}
	return "", fmt.Errorf("save plan: too many plans for %s attempt %d in %s", id, attempt, dir)
}

// marshalPlain encodes v as one JSON line without HTML escaping: a plan is
// full of < and >.
func marshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
