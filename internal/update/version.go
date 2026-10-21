package update

import (
	"regexp"
	"strconv"
	"strings"
)

// version is a parsed X.Y.Z[-pre].
type version struct {
	n   [3]int
	pre string // "" for a release
}

var (
	semver = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)
	// pseudo is the end of a Go pseudo-version: a UTC time and a 12-digit
	// commit hash (v1.1.1-0.20261020232616-74dcdb86e12b).
	pseudo = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)
	// describe is `git describe`'s commits-since-tag form, as `make build`
	// stamps it (v1.0.0-12-g12d9801).
	describe = regexp.MustCompile(`-\d+-g[0-9a-f]+$`)
)

// parse reads v, with or without a leading "v"; ok is false for anything
// that isn't X.Y.Z with an optional pre-release.
func parse(v string) (version, bool) {
	m := semver.FindStringSubmatch(strings.TrimPrefix(v, "v"))
	if m == nil {
		return version{}, false
	}
	var out version
	for i := range out.n {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return version{}, false
		}
		out.n[i] = n
	}
	out.pre = m[4]
	return out, true
}

// Development says v is not a version a release carries: "dev" (no
// ldflags, no module version), "(devel)", a Go pseudo-version, a build
// from a dirty tree, a `git describe` form between tags, or anything that
// doesn't parse. A pre-release (1.2.0-rc.1) is not one.
func Development(v string) bool {
	if strings.Contains(v, "dirty") {
		return true
	}
	if _, ok := parse(v); !ok {
		return true
	}
	return pseudo.MatchString(v) || describe.MatchString(v)
}

// Compare compares two versions that aren't development builds: -1, 0 or
// +1 as a is older than, the same as or newer than b. A pre-release is
// older than its release. A version that doesn't parse counts as oldest.
func Compare(a, b string) int {
	va, okA := parse(a)
	vb, okB := parse(b)
	switch {
	case !okA && !okB:
		return 0
	case !okA:
		return -1
	case !okB:
		return 1
	}
	for i := range va.n {
		if va.n[i] != vb.n[i] {
			return sign(va.n[i] - vb.n[i])
		}
	}
	return comparePre(va.pre, vb.pre)
}

// comparePre orders pre-releases as semver does: none is newest, then
// identifier by identifier, numbers numerically and before words.
func comparePre(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		na, errA := strconv.Atoi(as[i])
		nb, errB := strconv.Atoi(bs[i])
		switch {
		case errA == nil && errB == nil:
			if na != nb {
				return sign(na - nb)
			}
		case errA == nil:
			return -1
		case errB == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return sign(len(as) - len(bs))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
