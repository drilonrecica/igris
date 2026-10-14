package report

import "github.com/drilonrecica/igris/internal/checks"

// Doctor is the result of `igris doctor --json`: the checks in their one
// order (SPEC §14).
type Doctor struct {
	V      int             `json:"v"`
	Checks []checks.Result `json:"checks"`
}

// NewDoctor wraps the results of the doctor checks. Checks is never nil, so
// it is `[]` in JSON.
func NewDoctor(rs []checks.Result) Doctor {
	if rs == nil {
		rs = []checks.Result{}
	}
	return Doctor{V: JSONVersion, Checks: rs}
}
