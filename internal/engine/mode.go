// Package engine holds igris's run logic: mode resolution, the Claude Code
// command line and the run loop.
package engine

import (
	"fmt"
	"strings"
)

// Run modes (SPEC §7.1).
const (
	ModeDefault = "default"
	ModeAccept  = "accept"
	ModeAuto    = "auto"
	ModePlan    = "plan"
	ModeYolo    = "yolo"

	// A plan mode that names the mode after approval (SPEC §7.1); plain
	// plan switches to [plan_mode] then.
	ModePlanDefault = "plan:default"
	ModePlanAccept  = "plan:accept"
	ModePlanAuto    = "plan:auto"
	ModePlanYolo    = "plan:yolo"
)

// Modes are the run modes in the order igris lists them.
var Modes = []string{ModeDefault, ModeAccept, ModeAuto, ModePlan, ModeYolo, ModePlanDefault, ModePlanAccept, ModePlanAuto, ModePlanYolo}

// ModeList is Modes for messages: "default|accept|…".
var ModeList = strings.Join(Modes, "|")

// YoloPhrase is what the owner types to choose skip-permissions mode
// (SPEC §7.3); a single key or click never does.
const YoloPhrase = "skip permissions"

// SkipNote says what the confirmation lets sessions do.
const SkipNote = "Sessions in yolo mode run with --dangerously-skip-permissions, and plan:yolo sessions without permission prompts once their plan is approved: Claude Code acts without asking."

// modeFlags maps each run mode to its Claude Code flags. Spellings were
// verified against Claude Code 2.1.291 (P0-02, SPEC §7.1). Default passes no
// permission flag: `--permission-mode` has no `default` value. Claude Code
// ignores a setMode to bypassPermissions in a session started without
// --allow-dangerously-skip-permissions (verified with 2.1.304, V11-02), so
// plan:yolo, and only it, starts with it.
var modeFlags = map[string][]string{
	ModeDefault:     nil,
	ModeAccept:      {"--permission-mode", "acceptEdits"},
	ModeAuto:        {"--permission-mode", "auto"},
	ModePlan:        {"--permission-mode", "plan"},
	ModePlanDefault: {"--permission-mode", "plan"},
	ModePlanAccept:  {"--permission-mode", "plan"},
	ModePlanAuto:    {"--permission-mode", "plan"},
	ModePlanYolo:    {"--permission-mode", "plan", "--allow-dangerously-skip-permissions"},
	ModeYolo:        {"--dangerously-skip-permissions"},
}

// ValidMode reports whether m is a run mode.
func ValidMode(m string) bool {
	_, ok := modeFlags[m]
	return ok
}

// IsPlan reports whether m is a plan mode: the session plans first and
// the owner approves (SPEC §7.1).
func IsPlan(m string) bool { return m == ModePlan || strings.HasPrefix(m, ModePlan+":") }

// SkipsPermissions reports whether a session in mode m runs, or can come
// to run, without permission prompts: yolo, and plan:yolo, whose session
// starts allowed to skip them. Both need the run's typed confirmation
// (SPEC §7.3) and show the red badge.
func SkipsPermissions(m string) bool { return m == ModeYolo || m == ModePlanYolo }

// After is the mode a session in plan mode m switches to once igris
// approves its plan: the part after "plan:", or then for plain plan; ""
// when m is not a plan mode.
func After(m, then string) string {
	switch {
	case m == ModePlan:
		return then
	case IsPlan(m):
		return strings.TrimPrefix(m, ModePlan+":")
	}
	return ""
}

// ResolveMode picks the run mode of one task (SPEC §7.2): the owner's
// per-task override, the task's Mode column, the mode chosen for this run,
// the config's default_mode, then "auto" (the config default, SPEC §12) as a
// last resort. Empty values are skipped; the chosen value must be a known
// mode.
func ResolveMode(taskOverride, taskMode, runMode, defaultMode string) (string, error) {
	for _, m := range []string{taskOverride, taskMode, runMode, defaultMode} {
		m = strings.ToLower(strings.TrimSpace(m))
		if m == "" {
			continue
		}
		if !ValidMode(m) {
			return "", fmt.Errorf("unknown run mode %q (want %s)", m, ModeList)
		}
		return m, nil
	}
	return ModeAuto, nil
}
