package config

import (
	"strings"
	"testing"
)

func TestRetiredSubstrateKnownCases(t *testing.T) {
	dot, ok := LookupRetired("satelle-dot-standard")
	if !ok || dot.Replacement != "satelle-route-standard" {
		t.Fatalf("dot-standard entry: %+v ok=%v", dot, ok)
	}
	coc, ok := LookupRetired("satelle-configuration-over-code")
	if !ok || coc.Replacement != "" {
		t.Fatalf("configuration-over-code entry: %+v ok=%v", coc, ok)
	}
	if _, ok := LookupRetired("satelle-route-standard"); ok {
		t.Fatal("live name must not appear in RetiredSubstrate")
	}
}

func TestRetiredSubstrateConsistentWithEmbedded(t *testing.T) {
	live := map[string]bool{}
	for _, d := range EmbeddedDefaults() {
		live[d.Name] = true
	}
	for name, e := range RetiredSubstrate {
		if live[name] {
			t.Errorf("%q is both retired and a live embedded default", name)
		}
		if e.Replacement != "" && !live[e.Replacement] {
			t.Errorf("%q replacement %q is not a live embedded default", name, e.Replacement)
		}
		if e.Version == "" {
			t.Errorf("%q missing Version", name)
		}
	}
}

func TestFormatRetirementMessage(t *testing.T) {
	rename := FormatRetirementMessage("satelle-dot-standard", RetiredSubstrate["satelle-dot-standard"])
	if !strings.Contains(rename, "renamed to [[satelle-route-standard]]") || !strings.Contains(rename, "satelle migrate --yes") {
		t.Fatalf("rename message: %s", rename)
	}
	gone := FormatRetirementMessage("satelle-configuration-over-code", RetiredSubstrate["satelle-configuration-over-code"])
	if !strings.Contains(gone, "no replacement") || !strings.Contains(gone, "by hand") {
		t.Fatalf("removal message: %s", gone)
	}
}
