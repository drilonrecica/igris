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
)

// YoloPhrase is what the owner types to choose skip-permissions mode
// (SPEC §7.3); a single key or click never does.
const YoloPhrase = "skip permissions"

// modeFlags maps each run mode to its Claude Code flags. Spellings were
// verified against Claude Code 2.1.291 (P0-02, SPEC §7.1). Default passes no
// permission flag: `--permission-mode` has no `default` value.
var modeFlags = map[string][]string{
	ModeDefault: nil,
	ModeAccept:  {"--permission-mode", "acceptEdits"},
	ModeAuto:    {"--permission-mode", "auto"},
	ModePlan:    {"--permission-mode", "plan"},
	ModeYolo:    {"--dangerously-skip-permissions"},
}

// ValidMode reports whether m is a run mode.
func ValidMode(m string) bool {
	_, ok := modeFlags[m]
	return ok
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
			return "", fmt.Errorf("unknown run mode %q (want default|accept|auto|plan|yolo)", m)
		}
		return m, nil
	}
	return ModeAuto, nil
}
