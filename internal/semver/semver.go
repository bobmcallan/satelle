// Package semver is the single owner of satelle's version grammar: the strict
// MAJOR.MINOR.PATCH that release selection, the release advisory and a remote
// definition's version pin all use, and the lenient compare the changelog and
// drift gates use. It is a leaf package so every layer can import it.
package semver

import (
	"strconv"
	"strings"
)

// ParseStrict parses a strict MAJOR.MINOR.PATCH of non-negative integers. It is
// deliberately stricter than Parse: a tag that does not parse must never be
// chosen as the release to install.
func ParseStrict(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return v, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// Valid reports whether s is a strict version, with an optional leading "v".
func Valid(s string) bool {
	_, ok := ParseStrict(strings.TrimPrefix(s, "v"))
	return ok
}

// Less reports whether a sorts before b.
func Less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// Parse reads X.Y.Z leniently: a leading "v" and surrounding space are dropped,
// and a missing or non-numeric part reads as 0.
func Parse(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}

// Cmp compares two versions leniently (see Parse). Returns -1, 0, 1.
func Cmp(a, b string) int {
	ap, bp := Parse(a), Parse(b)
	for i := 0; i < 3; i++ {
		if ap[i] < bp[i] {
			return -1
		}
		if ap[i] > bp[i] {
			return 1
		}
	}
	return 0
}
