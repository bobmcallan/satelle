package health

import (
	"strings"
	"testing"
)

// TestIDsAreUniqueAndWellFormed pins the identifier contract: an operator
// scripts against these, so a copy-pasted duplicate — two defects collapsing
// into one id — must fail here rather than ship.
func TestIDsAreUniqueAndWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, id := range IDs() {
		if seen[id] {
			t.Errorf("duplicate finding id %q", id)
		}
		seen[id] = true
		if id == "" || strings.ToLower(id) != id || strings.Contains(id, " ") {
			t.Errorf("id %q must be lowercase, non-empty, and space-free", id)
		}
		if !strings.Contains(id, ".") {
			t.Errorf("id %q must be dotted (area.defect)", id)
		}
	}
	if len(seen) < 15 {
		t.Errorf("expected the full registry, got %d ids", len(seen))
	}
}

// TestSeverityOrdering pins the ranking Worst() and exit-code mapping depend on.
func TestSeverityOrdering(t *testing.T) {
	if !SeverityError.AtLeast(SeverityWarn) || !SeverityWarn.AtLeast(SeverityInfo) {
		t.Error("error > warn > info")
	}
	if SeverityInfo.AtLeast(SeverityWarn) {
		t.Error("info must not outrank warn")
	}
}

// TestWorstAndOK pins the aggregate: a set is OK exactly when nothing in it is
// an error, and warnings never block.
func TestWorstAndOK(t *testing.T) {
	var empty Findings
	if empty.Worst() != "" || !empty.OK() {
		t.Error("an empty set is OK with no severity")
	}
	warnOnly := Findings{Warn(IDReviewerUnsafe, "t", "d"), Info(IDLiveOK, "t", "d")}
	if warnOnly.Worst() != SeverityWarn || !warnOnly.OK() {
		t.Errorf("warnings must not block: %v", warnOnly.Worst())
	}
	mixed := append(warnOnly, Error(IDBinaryMissing, "t", "d"))
	if mixed.Worst() != SeverityError || mixed.OK() {
		t.Error("one error makes the set not OK")
	}
	if got := mixed.Details(SeverityError); len(got) != 1 || got[0] != "d" {
		t.Errorf("Details = %v", got)
	}
}

// TestRenderRefusalNamesEveryError pins the fail-closed shape: the refusal lists
// each error by its stable id, so the text an engagement refuses with can be
// matched against what a diagnostic prints.
func TestRenderRefusalNamesEveryError(t *testing.T) {
	f := Findings{
		Error(IDBinaryMissing, "Missing binary", "claude is not on PATH").WithRemediation("install it"),
		Warn(IDReviewerUnsafe, "Ceiling", "advisory only"),
		Error(IDHookAlloc, "Hook", "no binding for [nobody]"),
	}
	out := RenderRefusal("engage refused", f)
	for _, want := range []string{"engage refused (2 problem(s))", IDBinaryMissing, "install it", IDHookAlloc} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "advisory only") {
		t.Error("a warning must not appear in a refusal")
	}
	if RenderRefusal("x", Findings{Warn(IDReviewerUnsafe, "t", "d")}) != "" {
		t.Error("a warning-only set produces no refusal")
	}
}

// TestBuildersCarryMetadata pins the fluent shape the validators use.
func TestBuildersCarryMetadata(t *testing.T) {
	f := Error(IDAgentsBinding, "Bad binding", "the detail").
		WithRemediation("fix agents.toml").About("reviewer")
	if f.Severity != SeverityError || f.Artifact != "reviewer" || f.Remediation != "fix agents.toml" {
		t.Errorf("finding = %+v", f)
	}
	if !strings.Contains(f.String(), "the detail") || !strings.Contains(f.String(), "fix agents.toml") {
		t.Errorf("String = %q", f.String())
	}
}
