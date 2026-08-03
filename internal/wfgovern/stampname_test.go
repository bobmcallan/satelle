package wfgovern

import "testing"

// The derived route's name is a WORKFLOW name, not the files it is derived from
// (sty_81bb0dde). A stamp carrying the retired file-pair spelling must be
// recognisable by SHAPE so `satelle migrate` can rewrite it, and a real workflow
// name must never be mistaken for one.
func TestIsFilePairStamp(t *testing.T) {
	pair := []string{
		"done.md+step.md",     // the original spelling
		"done.toml+step.toml", // the brief post-conversion spelling
		"done.md + step.md",   // spaced, as a hand-edited tag might read
		"Done.MD+Step.MD",     // case is not identity
	}
	for _, s := range pair {
		if !IsFilePairStamp(s) {
			t.Errorf("IsFilePairStamp(%q) = false, want true", s)
		}
	}
	notPair := []string{
		"",
		DerivedRouteName,
		"gov-workflow",
		"done+step",       // no extensions: not a file list
		"done.md",         // one half is not the pair
		"step.md+done.md", // order is part of the shape
		"done.md+step.md+x.md",
		"predone.md+step.md",
	}
	for _, s := range notPair {
		if IsFilePairStamp(s) {
			t.Errorf("IsFilePairStamp(%q) = true, want false", s)
		}
	}
}

// The name an operator sees and stamps must stay a plain workflow name — one an
// applies_to list or `--workflow` could carry.
func TestDerivedRouteNameIsAName(t *testing.T) {
	if IsFilePairStamp(DerivedRouteName) {
		t.Fatalf("DerivedRouteName %q is a file pair", DerivedRouteName)
	}
	if IsRouteSource(DerivedRouteName) {
		t.Fatalf("DerivedRouteName %q collides with a route-source doc name", DerivedRouteName)
	}
}
