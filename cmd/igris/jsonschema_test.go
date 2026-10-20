package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The --json output is frozen for 1.x (SPEC §19) and described by the
// schemas in docs/json/. These tests check every JSON golden against its
// schema with a small validator for the subset of JSON Schema the schemas
// use; no dependency (AGENTS §4).

var schemaDir = mustAbs("../../docs/json")

// schemaKeywords are the keywords the validator knows. A schema using any
// other is an error, so a schema can't lean on a feature nothing checks.
var schemaKeywords = map[string]bool{
	"$schema": true, "$defs": true, "title": true, "description": true,
	"type": true, "properties": true, "required": true, "items": true,
	"enum": true, "const": true, "$ref": true, "minimum": true, "format": true,
}

// validator checks a decoded JSON value against a schema. Strict also fails
// on object members the schema doesn't declare: igris's own output must be
// fully described, while readers are told to ignore unknown fields.
type validator struct {
	root   map[string]any
	strict bool
	errs   []string
}

func loadSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(schemaDir, name+".schema.json")) //nolint:gosec // a schema in the repo
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("%s.schema.json: %v", name, err)
	}
	return s
}

// validate returns the problems of the JSON document data against schema.
func validate(schema map[string]any, data []byte, strict bool) []string {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	if dec.More() {
		return []string{"more than one JSON value"}
	}
	val := &validator{root: schema, strict: strict}
	val.check(schema, v, "$")
	return val.errs
}

func (val *validator) fail(path, format string, args ...any) {
	val.errs = append(val.errs, path+": "+fmt.Sprintf(format, args...))
}

func (val *validator) check(s map[string]any, v any, path string) {
	for k := range s {
		if !schemaKeywords[k] {
			val.fail(path, "schema keyword %q is not supported", k)
		}
	}
	if ref, ok := s["$ref"].(string); ok {
		name, found := strings.CutPrefix(ref, "#/$defs/")
		defs, _ := val.root["$defs"].(map[string]any)
		def, _ := defs[name].(map[string]any)
		if !found || def == nil {
			val.fail(path, "unresolved $ref %q", ref)
			return
		}
		val.check(def, v, path)
	}
	if t, ok := s["type"]; ok && !typeMatches(t, v) {
		val.fail(path, "%s is not of type %v", describe(v), t)
		return
	}
	if c, ok := s["const"]; ok && !jsonEqual(c, v) {
		val.fail(path, "%s, want %v", describe(v), c)
	}
	if e, ok := s["enum"].([]any); ok && !slices.ContainsFunc(e, func(x any) bool { return jsonEqual(x, v) }) {
		val.fail(path, "%s is not one of %v", describe(v), e)
	}
	if m, ok := s["minimum"].(float64); ok {
		if n, isNum := v.(json.Number); isNum {
			if f, err := n.Float64(); err == nil && f < m {
				val.fail(path, "%s is below the minimum %v", n, m)
			}
		}
	}
	if s["format"] == "date-time" {
		if str, isStr := v.(string); isStr {
			if _, err := time.Parse(time.RFC3339, str); err != nil {
				val.fail(path, "%q is not an RFC 3339 time", str)
			}
		}
	}
	switch v := v.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		req, _ := s["required"].([]any)
		for _, r := range req {
			if _, ok := v[r.(string)]; !ok {
				val.fail(path, "missing required %q", r)
			}
		}
		for k, x := range v {
			ps, ok := props[k].(map[string]any)
			switch {
			case ok:
				val.check(ps, x, path+"."+k)
			case val.strict && s["properties"] != nil:
				val.fail(path, "%q is not in the schema", k)
			}
		}
	case []any:
		if items, ok := s["items"].(map[string]any); ok {
			for i, x := range v {
				val.check(items, x, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
}

func typeMatches(t, v any) bool {
	if ts, ok := t.([]any); ok {
		return slices.ContainsFunc(ts, func(x any) bool { return typeMatches(x, v) })
	}
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		_, ok := v.(json.Number)
		return ok
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		_, err := n.Int64()
		return err == nil
	}
	return false
}

// jsonEqual compares a schema value (decoded without UseNumber) with a
// document value (decoded with it).
func jsonEqual(schema, v any) bool {
	if n, ok := v.(json.Number); ok {
		f, err := n.Float64()
		return err == nil && schema == f
	}
	return schema == v
}

func describe(v any) string {
	switch v := v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("%q", v)
	}
	return fmt.Sprint(v)
}

func TestValidator(t *testing.T) {
	schema := map[string]any{
		"type": "object", "required": []any{"v", "items"},
		"properties": map[string]any{
			"v":     map[string]any{"const": float64(1)},
			"items": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/item"}},
			"at":    map[string]any{"type": "string", "format": "date-time"},
		},
		"$defs": map[string]any{"item": map[string]any{
			"type": "object", "required": []any{"n"},
			"properties": map[string]any{
				"n":    map[string]any{"type": "integer", "minimum": float64(0)},
				"kind": map[string]any{"type": "string", "enum": []any{"a", "b"}},
			},
		}},
	}
	tests := []struct {
		name, doc string
		strict    bool
		want      string // a substring of the first problem; "" for none
	}{
		{"valid", `{"v":1,"items":[{"n":0,"kind":"a"}],"at":"2026-10-14T09:00:00Z"}`, true, ""},
		{"wrong const", `{"v":2,"items":[]}`, false, "want 1"},
		{"missing", `{"v":1}`, false, `missing required "items"`},
		{"wrong type", `{"v":1,"items":{}}`, false, "not of type array"},
		{"not integer", `{"v":1,"items":[{"n":1.5}]}`, false, "not of type integer"},
		{"below minimum", `{"v":1,"items":[{"n":-1}]}`, false, "below the minimum"},
		{"enum", `{"v":1,"items":[{"n":1,"kind":"c"}]}`, false, "not one of"},
		{"bad time", `{"v":1,"items":[],"at":"yesterday"}`, false, "RFC 3339"},
		{"unknown lenient", `{"v":1,"items":[],"extra":true}`, false, ""},
		{"unknown strict", `{"v":1,"items":[{"n":1,"extra":1}]}`, true, `"extra" is not in the schema`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validate(schema, []byte(tt.doc), tt.strict)
			switch {
			case tt.want == "" && len(errs) > 0:
				t.Errorf("want no problems, got %v", errs)
			case tt.want != "" && (len(errs) == 0 || !strings.Contains(errs[0], tt.want)):
				t.Errorf("want %q, got %v", tt.want, errs)
			}
		})
	}
	bad := map[string]any{"type": "object", "oneOf": []any{}}
	if errs := validate(bad, []byte(`{}`), false); len(errs) == 0 || !strings.Contains(errs[0], "not supported") {
		t.Errorf("unsupported keyword: %v", errs)
	}
}

// goldenSchemas maps a JSON golden's name to the schema of its output. A
// golden no rule matches fails TestJSONGoldensMatchSchemas, so a new --json
// output can't go without a schema.
var goldenSchemas = []struct {
	re     *regexp.Regexp
	schema string
}{
	{regexp.MustCompile(`^(invalid|format2)_(phases|status)_json$`), "invalid-plan"},
	{regexp.MustCompile(`_check_json$`), "check"},
	{regexp.MustCompile(`_phases_json$`), "phases"},
	{regexp.MustCompile(`_status_json$`), "status"},
	{regexp.MustCompile(`^history(_v1)?_json$`), "history"},
	{regexp.MustCompile(`^history(_v1)?_[A-Z][^_]*_json$`), "history-task"},
	{regexp.MustCompile(`^report(_[^_]+)*_json$`), "report"},
	{regexp.MustCompile(`^usage(_[^_]+)*_json$`), "usage"},
	{regexp.MustCompile(`_?doctor_json$`), "doctor"},
}

func TestJSONGoldensMatchSchemas(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(mustAbs("testdata/golden"), "*_json.golden"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no JSON goldens: %v", err)
	}
	used := map[string]bool{}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".golden")
		t.Run(name, func(t *testing.T) {
			i := slices.IndexFunc(goldenSchemas, func(g struct {
				re     *regexp.Regexp
				schema string
			}) bool {
				return g.re.MatchString(name)
			})
			if i < 0 {
				t.Fatalf("no schema for %s; add a rule to goldenSchemas", name)
			}
			schema := goldenSchemas[i].schema
			used[schema] = true
			data, err := os.ReadFile(f) //nolint:gosec // a golden under testdata/
			if err != nil {
				t.Fatal(err)
			}
			_, out, ok := strings.Cut(string(data), "--- stdout\n")
			out, _, ok2 := strings.Cut(out, "--- stderr\n")
			if !ok || !ok2 {
				t.Fatalf("%s has no stdout section", name)
			}
			for _, e := range validate(loadSchema(t, schema), []byte(out), true) {
				t.Errorf("%s against %s.schema.json: %s", name, schema, e)
			}
		})
	}
	// Every schema is exercised by some golden.
	schemas, _ := filepath.Glob(filepath.Join(schemaDir, "*.schema.json"))
	for _, s := range schemas {
		if name := strings.TrimSuffix(filepath.Base(s), ".schema.json"); !used[name] {
			t.Errorf("no golden checks %s.schema.json", name)
		}
	}
}

// TestSchemaCheckDir checks --json output saved from real runs against the
// schemas, as the release gates do (docs/check-v07.md): each NAME.json or
// NAME-anything.json in $IGRIS_SCHEMA_CHECK_DIR against NAME.schema.json,
// strictly. Skipped when the variable isn't set.
func TestSchemaCheckDir(t *testing.T) {
	dir := os.Getenv("IGRIS_SCHEMA_CHECK_DIR")
	if dir == "" {
		t.Skip("IGRIS_SCHEMA_CHECK_DIR not set")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no .json files in %s: %v", dir, err)
	}
	for _, f := range files {
		name, _, _ := strings.Cut(strings.TrimSuffix(filepath.Base(f), ".json"), "-")
		if _, err := os.Stat(filepath.Join(schemaDir, name+".schema.json")); err != nil {
			continue // not --json output (a saved state.json, say)
		}
		data, err := os.ReadFile(f) //nolint:gosec // a file the gate saved
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range validate(loadSchema(t, name), data, true) {
			t.Errorf("%s against %s.schema.json: %s", filepath.Base(f), name, e)
		}
		t.Logf("%s: valid against %s.schema.json", filepath.Base(f), name)
	}
}

// TestJSONRunGoldens adds the goldens the plan fixtures can't give: doctor,
// and status with a run (a selection, signals, an unreadable signal).
func TestJSONRunGoldens(t *testing.T) {
	plan, err := os.ReadFile("testdata/plans/drift.md")
	if err != nil {
		t.Fatal(err)
	}
	goldenDir := mustAbs("testdata/golden")
	t.Run("drift_doctor_json", func(t *testing.T) {
		dir := t.TempDir()
		//nolint:gosec // a fixture copied into the test's own temp dir
		if err := os.WriteFile(filepath.Join(dir, "tasks.md"), plan, 0o600); err != nil {
			t.Fatal(err)
		}
		code, out, errb := doctorIn(t, dir, "--json")
		compareGolden(t, goldenDir, "drift_doctor_json", dir, code, out, errb)
	})
	t.Run("run_status_json", func(t *testing.T) {
		dir := inDir(t)
		//nolint:gosec // a fixture copied into the test's own temp dir
		if err := os.WriteFile(filepath.Join(dir, "tasks.md"), plan, 0o600); err != nil {
			t.Fatal(err)
		}
		writeRunState(t, dir, "state.json", `{"version":2,"started_at":"2026-10-07T09:30:00Z","phases":["A","B"],"through":"B",`+
			`"selection":{"from":"A-3","until":"B-2"},"config_hash":"h",`+
			`"current":{"task_id":"B-1","mode":"yolo","claude_session":"s","session":{"backend":"tmux","pane_id":"%3"},"started_at":"2026-10-07T09:31:00Z"}}`)
		writeRunState(t, dir, "signals/B-1.json", `{"id":"B-1","action":"done","note":"n","at":"2026-10-07T09:32:00Z"}`)
		writeRunState(t, dir, "signals/A-4.json", `{"id":"A-4","action":"skip","note":"not needed","at":"2026-10-07T09:33:00Z"}`)
		writeRunState(t, dir, "signals/B-2.json", `{not json`)
		code, out, errb := runCmd("status", "--json")
		compareGolden(t, goldenDir, "run_status_json", dir, code, out, errb)
	})
}

// compareGolden compares a command's result with goldenDir/name.golden, or
// rewrites it under -update. dir is replaced by <dir>.
func compareGolden(t *testing.T, goldenDir, name, dir string, code int, stdout, stderr string) {
	t.Helper()
	got := fmt.Sprintf("exit: %d\n--- stdout\n%s--- stderr\n%s", code, stdout, stderr)
	got = strings.ReplaceAll(got, dir, "<dir>")
	golden := filepath.Join(goldenDir, name+".golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden) //nolint:gosec // a golden under testdata/
	if err != nil {
		t.Fatalf("%v; run `go test ./cmd/igris -run TestJSONRunGoldens -update`", err)
	}
	if got != string(want) {
		t.Errorf("output changed:\n%s\nwant:\n%s", got, want)
	}
}
