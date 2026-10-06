package plan

import "strings"

// rangeSeps are the range delimiters, longest first (SPEC §3.4).
var rangeSeps = []string{"…", "...", ".."}

// resolveDeps parses every task's Deps cell into Task.Deps. Ranges expand to
// every task from start to end inclusive in file order, across phases.
// Unknown plain IDs and self-dependencies are kept for Validate to report;
// a range that can't be expanded is reported here and contributes nothing.
func (p *Plan) resolveDeps() {
	index := make(map[string]int, len(p.Tasks))
	for i, t := range p.Tasks {
		if _, dup := index[t.ID]; !dup {
			index[t.ID] = i
		}
	}
	for _, t := range p.Tasks {
		t.Deps = p.parseDeps(t, index)
	}
}

func (p *Plan) parseDeps(t *Task, index map[string]int) []string {
	if isNone(t.DepsText) {
		return nil
	}
	var deps []string
	seen := map[string]bool{}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			deps = append(deps, id)
		}
	}
	for _, tok := range strings.Split(t.DepsText, ",") {
		tok = strings.Trim(tok, " \t`")
		if tok == "" {
			p.addIssue(t.Line, "%s: empty entry in Deps %q; separate task IDs with single commas", t.ID, t.DepsText)
			continue
		}
		from, to, isRange := splitRange(tok, index)
		if !isRange {
			add(tok)
			continue
		}
		if from == "" || to == "" {
			p.addIssue(t.Line, "%s: malformed Deps range %q; write it as A…B with two task IDs", t.ID, tok)
			continue
		}
		a, okA := index[from]
		b, okB := index[to]
		switch {
		case !okA || !okB:
			missing := from
			if okA {
				missing = to
			}
			p.addIssue(t.Line, "%s: Deps range %q refers to unknown task %s", t.ID, tok, missing)
		case a >= b:
			p.addIssue(t.Line, "%s: Deps range %q is not in file order (%s does not come before %s); swap the endpoints", t.ID, tok, from, to)
		default:
			for _, dep := range p.Tasks[a : b+1] {
				add(dep.ID)
			}
		}
	}
	return deps
}

// splitRange splits a Deps entry into range endpoints. An entry that is
// itself a task ID is never a range, so IDs containing dots stay intact.
func splitRange(tok string, index map[string]int) (from, to string, ok bool) {
	if _, isID := index[tok]; isID {
		return "", "", false
	}
	for _, sep := range rangeSeps {
		if parts := strings.Split(tok, sep); len(parts) > 1 {
			if len(parts) != 2 {
				return "", "", true
			}
			return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
		}
	}
	return "", "", false
}
