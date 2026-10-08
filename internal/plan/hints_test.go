package plan

import (
	"reflect"
	"testing"
)

func TestHints(t *testing.T) {
	tests := []struct {
		name string
		in   string
		opts Options
		want []string
	}{
		{"canonical Deps", table("| a | — | ready | sonnet | agent | |"), Options{}, nil},
		{"no deps column at all", "## M0\n\n| ID | Status | Model |\n|---|---|---|\n| a | ready | sonnet |\n", Options{}, nil},
		{"Depends ignored",
			"## M0\n\n| ID | Status | Model | Depends |\n|---|---|---|---|\n| a | done | sonnet | — |\n| b | ready | sonnet | a |\n" +
				"\n## M1\n\n| ID | Status | Model | **Depends on** |\n|---|---|---|---|\n| c | ready | sonnet | b |\n",
			Options{},
			[]string{
				`tasks.md:3: column "Depends" looks like dependencies, but igris reads them only from a Deps column, so every task in phase M0 runs as if it had none; rename the column to Deps or add "Depends" = "Deps" under [columns] in igris.toml`,
				`tasks.md:10: column "Depends on" looks like dependencies, but igris reads them only from a Deps column, so every task in phase M1 runs as if it had none; rename the column to Deps or add "Depends on" = "Deps" under [columns] in igris.toml`,
			}},
		{"aliased in config", "## M0\n\n| ID | Status | Model | Depends |\n|---|---|---|---|\n| a | ready | sonnet | — |\n",
			Options{Columns: map[string]string{"Depends": "Deps"}}, nil},
		{"Verify on a user task", "## M0\n\n| ID | Status | Model | Owner | Verify |\n|---|---|---|---|---|\n| a | ready | — | user | fast |\n| b | done | — | user | fast |\n| c | ready | — | user | — |\n", Options{},
			[]string{"tasks.md:5: a: user tasks have no session, so its Verify is ignored; clear the cell"}},
		{"Timeout on a user task", "## M0\n\n| ID | Status | Model | Owner | Timeout |\n|---|---|---|---|---|\n| a | ready | — | user | 45m |\n| b | skipped | — | user | 45m |\n| c | ready | sonnet | agent | 45m |\n", Options{},
			[]string{"tasks.md:5: a: user tasks have no session, so its Timeout is ignored; clear the cell"}},
		{"Deps present next to a lookalike", "## M0\n\n| ID | Deps | Status | Model | Requires |\n|---|---|---|---|---|\n| a | — | ready | sonnet | Go 1.26 |\n", Options{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Parse("tasks.md", []byte(tt.in), tt.opts)
			if issues := p.Validate(testRules); len(issues) > 0 {
				t.Fatalf("plan must be valid: %v", issueMsgs(issues))
			}
			got := issueMsgs(p.Hints())
			if len(got) == 0 {
				got = nil
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Hints:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}
