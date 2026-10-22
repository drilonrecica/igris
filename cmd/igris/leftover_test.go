package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// leftoverChildren is the live children (Linux, from /proc) named like
// igris or a test binary: what a real SpawnServe would have left behind.
// Exited children not yet reaped are not live.
func leftoverChildren() []string {
	var out []string
	for _, c := range liveChildren() {
		comm := c[strings.IndexByte(c, ' ')+1:]
		if comm == "igris" || strings.HasSuffix(comm, ".test") {
			out = append(out, c)
		}
	}
	return out
}

func liveChildren() []string {
	me := strconv.Itoa(os.Getpid())
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		// "pid (comm) state ppid ..."; comm may hold spaces.
		s := string(stat)
		open, closeIdx := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if open < 0 || closeIdx < open {
			continue
		}
		comm := s[open+1 : closeIdx]
		rest := strings.Fields(s[closeIdx+1:])
		if len(rest) < 2 || rest[0] == "Z" || rest[1] != me {
			continue
		}
		out = append(out, e.Name()+" "+comm)
	}
	return out
}
