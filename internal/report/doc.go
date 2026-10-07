// Package report is the data behind igris's read-only commands: what `check`,
// `phases`, `status` and `arise --dry-run` show. Builders return plain
// structs; they print nothing. cmd/igris (and, later, the home screen) render
// them. The JSON tags are the `--json` output, so a struct's field order is
// its key order: where a type replaced a map, fields are in key-sorted order
// to keep that output byte-identical.
//
// Text that came from outside igris (plan cells, config, command output) is
// cleaned with textsafe as it enters a report, so a renderer can print it
// without further care (SPEC §16).
package report
