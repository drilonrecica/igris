package config

import (
	"os"
	"testing"
)

// FuzzParse: an arbitrary igris.toml gives a config or an error, never a
// panic, and a config Parse accepts is valid.
func FuzzParse(f *testing.F) {
	example, err := os.ReadFile("../../examples/igris.toml")
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{
		string(example),
		"",
		"plan = \"tasks.md\"\npoll_interval = \"5s\"\n",
		"[run]\ncommit = \"auto\"\nverify = \"make test\"\nverify_timeout = \"15m\"\nverify_max_attempts = 3\n",
		"[models]\ngold = \"opus\"\n[columns]\n\"Depends on\" = \"Deps\"\n",
		"[claude]\nextra_args = [\"--verbose\", \"--model\", \"opus\"]\n",
		"[notify.ntfy]\nurl = \"https://ntfy.sh/x\"\ntoken = \"env:NTFY_TOKEN\"\nevents = [\"needs_input\"]\n",
		"[tui]\nmouse = false\n[notify.backend]\nenabled = false\n",
		"poll_interval = \"-1s\"\nunknown = 1\n",
		"[[models]]\nx = 1\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, err := Parse(data, "igris.toml")
		if err != nil {
			if cfg != nil {
				t.Fatalf("error %v with a config", err)
			}
			return
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Parse accepted a config that does not validate: %v", err)
		}
		_, _ = cfg.Resolve(func(string) string { return "" })
		_ = cfg.Hash()
	})
}
