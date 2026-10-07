//go:build race

package tui

// raceBuild says the race detector is on; it slows drawing down several
// times over, so the frame-time budget isn't checked.
const raceBuild = true
