package cli

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// semver is a version with semver's order: build metadata plays no part in it.
type semver struct {
	core [3]int
	pre  []string
}

// parseSemver reads MAJOR.MINOR.PATCH[-PRE][+BUILD], with or without a leading v.
func parseSemver(s string) (semver, bool) {
	s, _, _ = strings.Cut(strings.TrimPrefix(s, "v"), "+")
	s, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != len(semver{}.core) {
		return semver{}, false
	}
	var v semver
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return semver{}, false
		}
		v.core[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

// compare is negative when v comes before other, 0 when they rank the same, and
// positive when it comes after. A pre-release comes before its release.
func (v semver) compare(other semver) int {
	if c := slices.Compare(v.core[:], other.core[:]); c != 0 {
		return c
	}
	switch {
	case len(v.pre) == 0 && len(other.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(other.pre) == 0:
		return -1
	}
	return slices.CompareFunc(v.pre, other.pre, comparePre)
}

// comparePre orders two pre-release identifiers: numbers by value and before
// words, words as text.
func comparePre(a, b string) int {
	an, aErr := strconv.Atoi(a)
	bn, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil:
		return cmp.Compare(an, bn)
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	}
	return strings.Compare(a, b)
}
