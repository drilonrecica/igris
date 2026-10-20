// Package examples embeds the canonical example plan and the plan templates
// so `igris init --example` and `igris init --template` can write them; the
// files next to it stay readable on their own.
package examples

import (
	"embed"
	"io/fs"
	"sort"
	"strings"
)

// Plan is examples/tasks.md, the plan `igris init --example` writes.
//
//go:embed tasks.md
var Plan []byte

// Templates holds examples/templates/<name>/tasks.md, the plans
// `igris init --template NAME` writes (SPEC §14); the gallery in the
// repository is these same files.
//
//go:embed templates
var Templates embed.FS

// TemplateNames are the names of the templates, sorted.
func TemplateNames() []string {
	entries, err := fs.ReadDir(Templates, "templates")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Template is the plan of the template called name; ok is false for a name
// that is not one of TemplateNames (names match exactly).
func Template(name string) (data []byte, ok bool) {
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return nil, false
	}
	data, err := fs.ReadFile(Templates, "templates/"+name+"/tasks.md")
	return data, err == nil
}
