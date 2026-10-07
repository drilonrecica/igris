// Package examples embeds the canonical example plan so `igris init --example`
// can write it; the files next to it stay readable on their own.
package examples

import _ "embed"

// Plan is examples/tasks.md, the plan `igris init --example` writes.
//
//go:embed tasks.md
var Plan []byte
