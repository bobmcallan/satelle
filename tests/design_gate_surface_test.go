//go:build integration

package tests

import (
	"github.com/bobmcallan/satelle/internal/wfdot"
)

func skillNames(ss []wfdot.ScopedReviewer) map[string]bool {
	m := map[string]bool{}
	for _, s := range ss {
		m[s.Skill] = true
	}
	return m
}

func containsStrSlice(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
