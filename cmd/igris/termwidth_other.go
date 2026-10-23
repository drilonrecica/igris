//go:build !linux && !darwin

package main

import "os"

// termWidth can't tell here; COLUMNS can.
func termWidth(*os.File) int { return 0 }
