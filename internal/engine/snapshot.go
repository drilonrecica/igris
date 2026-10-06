package engine

import (
	"context"
	"errors"

	"github.com/drilonrecica/igris/internal/config"
)

// configWatch compares igris.toml on disk with the snapshot the run was
// started with (SPEC §13). The run never adopts the file's new content; the
// watch only tells the owner that the two differ.
type configWatch struct {
	path     string
	snapshot string // hash of the run's config
	last     string // what the file held at the previous check
}

type configState int

const (
	configSame     configState = iota // nothing new since the last check
	configChanged                     // the file changed and differs from the snapshot
	configRestored                    // the file matches the snapshot again
)

func newConfigWatch(path string, snapshot *config.Config) configWatch {
	h := snapshot.Hash()
	return configWatch{path: path, snapshot: h, last: h}
}

// check reads the file and reports a change once: editing it again reports
// again, re-reading the same content does not.
func (w *configWatch) check() configState {
	cur := w.onDisk()
	if cur == w.last {
		return configSame
	}
	w.last = cur
	if cur == w.snapshot {
		return configRestored
	}
	return configChanged
}

// onDisk identifies the file's current content: the hash of the config it
// parses to (so comments and formatting don't count), the defaults' hash if
// the file is missing, or the load error if it can't be used.
func (w *configWatch) onDisk() string {
	cfg, err := config.Load(w.path)
	switch {
	case errors.Is(err, config.ErrNotFound):
		return config.Default().Hash()
	case err != nil:
		return "unusable: " + err.Error()
	}
	return cfg.Hash()
}

// checkConfig reports a config file that changed during the run. The owner
// has to look at it: a session may have edited it, e.g. to weaken verify.
func (e *Engine) checkConfig(ctx context.Context) {
	switch e.conf.check() {
	case configChanged:
		const what = "igris.toml changed during the run; igris keeps the configuration it started with"
		e.emit(Event{Kind: ConfigChanged, Detail: what + " — restart `igris arise` to apply the new one"})
		e.toast(ctx, notifyNeedsInput, what)
	case configRestored:
		e.emit(Event{Kind: ConfigRestored, Detail: "igris.toml matches the run's configuration again"})
	}
}
