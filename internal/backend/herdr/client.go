// Package herdr is the herdr backend (SPEC §11.2). Client wraps the herdr
// CLI calls igris needs; every call goes through the injectable command
// runner with argv and a timeout, never through a shell.
package herdr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Program is the herdr executable, resolved via PATH.
const Program = "herdr"

// DefaultTimeout bounds every herdr call that has no timeout of its own
// (SPEC §11.2).
const DefaultTimeout = 10 * time.Second

// timeoutMargin is added to herdr's own --timeout so herdr reports the
// timeout (code "timeout") before the runner kills it.
const timeoutMargin = 10 * time.Second

// Limits herdr puts on `agent start --timeout` (P0-03).
const (
	minStartTimeout = 3000 * time.Millisecond // exclusive
	maxStartTimeout = 300000 * time.Millisecond
)

// Error codes herdr reports in {"error":{"code",...}} (P0-03).
const (
	CodeAgentNotReady        = "agent_not_ready"
	CodeAgentPaneBusy        = "agent_pane_busy"
	CodeAgentBlocked         = "agent_blocked"
	CodeAgentNotFound        = "agent_not_found"
	CodePaneNotFound         = "pane_not_found"
	CodeTabNotFound          = "tab_not_found"
	CodeTimeout              = "timeout"
	CodeInvalidAgentArgument = "invalid_agent_argument"
	CodeInvalidAgentTimeout  = "invalid_agent_timeout"
)

// Agent states herdr reports as agent_status.
const (
	StatusUnknown = "unknown"
	StatusIdle    = "idle"
	StatusWorking = "working"
	StatusBlocked = "blocked"
	StatusDone    = "done"
)

// Error is an error herdr reported as JSON on stderr.
type Error struct {
	Op      string // e.g. "agent prompt"
	Code    string // e.g. "agent_blocked"
	Message string
	Exit    int
}

func (e *Error) Error() string {
	return fmt.Sprintf("herdr %s: %s: %s", e.Op, e.Code, e.Message)
}

// IsCode reports whether err is (or wraps) a herdr *Error with code.
func IsCode(err error, code string) bool {
	var he *Error
	return errors.As(err, &he) && he.Code == code
}

// Agent is an agent as herdr reports it (agent start/wait/prompt).
type Agent struct {
	Name             string `json:"name"`
	Kind             string `json:"agent"`
	Status           string `json:"agent_status"`
	InteractiveReady bool   `json:"interactive_ready"`
	PaneID           string `json:"pane_id"`
	TabID            string `json:"tab_id"`
}

// Pane is a pane as herdr reports it. Agent is empty for a pane that runs
// no detected agent (e.g. a plain shell).
type Pane struct {
	PaneID string `json:"pane_id"`
	TabID  string `json:"tab_id"`
	Agent  string `json:"agent"`
	Status string `json:"agent_status"`
}

// Tab is a tab as herdr reports it.
type Tab struct {
	TabID   string `json:"tab_id"`
	Label   string `json:"label"`
	Focused bool   `json:"focused"`
}

// ServerStatus is the parsed output of `herdr status server`.
type ServerStatus struct {
	Status  string // "running" when the server is up
	Version string
}

// Client calls the herdr CLI. The zero value is not usable; call NewClient.
type Client struct {
	run     runner.Runner
	timeout time.Duration
}

// NewClient returns a client that runs herdr through r.
func NewClient(r runner.Runner) *Client {
	return &Client{run: r, timeout: DefaultTimeout}
}

// exec runs `herdr args...` and returns its stdout. A non-zero exit becomes
// a herdr *Error when stderr holds herdr's JSON error, else the runner's
// *runner.ExitError.
func (c *Client) exec(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	op := opName(args)
	res, err := c.run.Run(ctx, runner.Cmd{Name: Program, Args: args, Timeout: timeout})
	if err != nil {
		return nil, fmt.Errorf("herdr %s: %w", op, err)
	}
	if res.ExitCode == 0 {
		return res.Stdout, nil
	}
	var body struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(bytes.TrimSpace(res.Stderr), &body) == nil && body.Error != nil && body.Error.Code != "" {
		return nil, &Error{Op: op, Code: body.Error.Code, Message: body.Error.Message, Exit: res.ExitCode}
	}
	return nil, fmt.Errorf("herdr %s: %w", op, res.Err())
}

// call runs a JSON command and decodes its .result into out.
func (c *Client) call(ctx context.Context, timeout time.Duration, out any, args ...string) error {
	stdout, err := c.exec(ctx, timeout, args...)
	if err != nil {
		return err
	}
	var body struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(stdout, &body); err != nil {
		return fmt.Errorf("herdr %s: parse output: %w", opName(args), err)
	}
	if len(body.Result) == 0 || string(body.Result) == "null" {
		return fmt.Errorf("herdr %s: output has no result", opName(args))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body.Result, out); err != nil {
		return fmt.Errorf("herdr %s: parse result: %w", opName(args), err)
	}
	return nil
}

// opName names a call by its subcommand words, e.g. "agent prompt", so
// errors don't repeat long arguments such as a task prompt.
func opName(args []string) string {
	n := min(2, len(args))
	return strings.Join(args[:n], " ")
}

func ms(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }

// ServerStatus runs `herdr status server` (plain text, not JSON).
func (c *Client) ServerStatus(ctx context.Context) (ServerStatus, error) {
	out, err := c.exec(ctx, c.timeout, "status", "server")
	if err != nil {
		return ServerStatus{}, err
	}
	var st ServerStatus
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "status":
			st.Status = strings.TrimSpace(val)
		case "version":
			st.Version = strings.TrimSpace(val)
		}
	}
	if st.Status == "" {
		return ServerStatus{}, errors.New("herdr status server: output has no status line")
	}
	return st, nil
}

// TabCreateOpts are the flags of `herdr tab create`.
type TabCreateOpts struct {
	Workspace string
	Cwd       string
	Label     string
	Env       []string // KEY=VALUE, set for the tab's process
	Focus     bool     // false passes --no-focus
}

// TabCreate creates a tab and returns it and its root pane.
func (c *Client) TabCreate(ctx context.Context, o TabCreateOpts) (Tab, Pane, error) {
	args := []string{"tab", "create", "--workspace", o.Workspace, "--cwd", o.Cwd, "--label", o.Label}
	for _, kv := range o.Env {
		args = append(args, "--env", kv)
	}
	if o.Focus {
		args = append(args, "--focus")
	} else {
		args = append(args, "--no-focus")
	}
	var r struct {
		Tab  Tab  `json:"tab"`
		Root Pane `json:"root_pane"`
	}
	if err := c.call(ctx, c.timeout, &r, args...); err != nil {
		return Tab{}, Pane{}, err
	}
	if r.Tab.TabID == "" || r.Root.PaneID == "" {
		return Tab{}, Pane{}, errors.New("herdr tab create: result has no tab_id or root pane_id")
	}
	return r.Tab, r.Root, nil
}

// AgentStart starts a Claude Code agent called name in pane with claude
// argv args and waits up to timeout for it to be ready for input. A start
// that is blocked at a prompt (e.g. folder trust) fails with code
// CodeAgentNotReady; the name stays usable.
func (c *Client) AgentStart(ctx context.Context, name, paneID string, timeout time.Duration, args []string) (Agent, error) {
	if timeout <= minStartTimeout || timeout > maxStartTimeout {
		return Agent{}, fmt.Errorf("herdr agent start: timeout %s must be more than %s and at most %s", timeout, minStartTimeout, maxStartTimeout)
	}
	// herdr can't encode control characters for the shell it types the
	// command into (invalid_agent_argument, P0-03); fail before calling it.
	for i, a := range args {
		if strings.ContainsFunc(a, unicode.IsControl) {
			return Agent{}, fmt.Errorf("herdr agent start: argument %d contains a control character (such as a newline or tab), which herdr can't pass to Claude Code; send multi-line text with a prompt instead", i+1)
		}
	}
	argv := append([]string{"agent", "start", name, "--kind", "claude", "--pane", paneID, "--timeout", ms(timeout), "--"}, args...)
	var r struct {
		Agent Agent `json:"agent"`
	}
	if err := c.call(ctx, timeout+timeoutMargin, &r, argv...); err != nil {
		return Agent{}, err
	}
	return r.Agent, nil
}

// AgentWait waits up to timeout for agent name to reach one of the until
// states; with none it returns at the first settled state (idle, done or
// blocked). Expiry fails with code CodeTimeout.
func (c *Client) AgentWait(ctx context.Context, name string, until []string, timeout time.Duration) (Agent, error) {
	if timeout <= 0 {
		return Agent{}, errors.New("herdr agent wait: no timeout set")
	}
	args := []string{"agent", "wait", name}
	for _, s := range until {
		args = append(args, "--until", s)
	}
	args = append(args, "--timeout", ms(timeout))
	var r struct {
		Agent Agent `json:"agent"`
	}
	if err := c.call(ctx, timeout+timeoutMargin, &r, args...); err != nil {
		return Agent{}, err
	}
	return r.Agent, nil
}

// AgentPrompt submits text (multi-line is fine) to agent name without
// waiting; the returned status is the one before the turn. An agent at an
// approval or question UI rejects it with code CodeAgentBlocked.
func (c *Client) AgentPrompt(ctx context.Context, name, text string) (Agent, error) {
	// herdr types the text into Claude Code's terminal as a paste. Escape
	// sequences in it (verify output is the session's own) could end the
	// paste and act as keystrokes, so only plain text goes in.
	text = textsafe.Clean(text)
	// No "--" before the text: herdr 0.9.1 rejects it ("unknown option"),
	// while a positional starting with "-" or "--" is taken as text.
	var r struct {
		Agent Agent `json:"agent"`
	}
	if err := c.call(ctx, c.timeout, &r, "agent", "prompt", name, text); err != nil {
		return Agent{}, err
	}
	return r.Agent, nil
}

// Sources for AgentRead.
const (
	SourceRecentUnwrapped = "recent-unwrapped" // transcript; empty while a dialog is drawn
	SourceVisible         = "visible"          // what the screen shows now
)

// AgentRead returns the last lines of agent name's terminal as plain text.
func (c *Client) AgentRead(ctx context.Context, name, source string, lines int) (string, error) {
	out, err := c.exec(ctx, c.timeout, "agent", "read", name, "--source", source, "--lines", strconv.Itoa(lines))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// PaneGet returns pane paneID; a missing pane fails with CodePaneNotFound.
func (c *Client) PaneGet(ctx context.Context, paneID string) (Pane, error) {
	var r struct {
		Pane Pane `json:"pane"`
	}
	if err := c.call(ctx, c.timeout, &r, "pane", "get", paneID); err != nil {
		return Pane{}, err
	}
	return r.Pane, nil
}

// TabFocus brings tab tabID to the front.
func (c *Client) TabFocus(ctx context.Context, tabID string) (Tab, error) {
	var r struct {
		Tab Tab `json:"tab"`
	}
	if err := c.call(ctx, c.timeout, &r, "tab", "focus", tabID); err != nil {
		return Tab{}, err
	}
	return r.Tab, nil
}

// TabClose closes tab tabID; a tab that is already closed fails with
// CodeTabNotFound.
func (c *Client) TabClose(ctx context.Context, tabID string) error {
	var r struct {
		Type string `json:"type"`
	}
	if err := c.call(ctx, c.timeout, &r, "tab", "close", tabID); err != nil {
		return err
	}
	if r.Type != "ok" {
		return fmt.Errorf("herdr tab close: unexpected result type %q", r.Type)
	}
	return nil
}

// Notification sounds herdr knows.
const (
	SoundRequest = "request"
	SoundDone    = "done"
	SoundNone    = "none"
)

// NotificationShow shows a herdr toast and reports whether it was shown.
func (c *Client) NotificationShow(ctx context.Context, title, body, sound string) (bool, error) {
	var r struct {
		Shown bool `json:"shown"`
	}
	if err := c.call(ctx, c.timeout, &r, "notification", "show", title, "--body", body, "--sound", sound); err != nil {
		return false, err
	}
	return r.Shown, nil
}

// IntegrationStatus runs `herdr integration status` (plain text) and maps
// each agent kind to its status, e.g. "claude" → "not installed".
func (c *Client) IntegrationStatus(ctx context.Context) (map[string]string, error) {
	out, err := c.exec(ctx, c.timeout, "integration", "status")
	if err != nil {
		return nil, err
	}
	st := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ": ")
		if !ok {
			continue
		}
		// "not installed (/path/to/hook)" → "not installed"
		if i := strings.LastIndex(rest, " ("); i >= 0 && strings.HasSuffix(rest, ")") {
			rest = rest[:i]
		}
		st[strings.TrimSpace(name)] = strings.TrimSpace(rest)
	}
	return st, nil
}
