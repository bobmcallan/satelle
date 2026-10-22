package wfdot

import (
	"strings"
	"testing"
)

// sty_93359e0f: a gate declares its review mode as configuration. `review_mode`
// on a [[gate]] covers the gate (and its panel); `fail_fast` on a step lists the
// edge reviewers that run fail-fast. Both are closed, and neither is read as a
// verdict.

const failFastGate = `
[[gate]]
skill = "scoped-judge"
on = ["in_progress"]
review_mode = "fail-fast"
panel = ["reviewer", "second"]
combine = "fold-check"
`

func TestReviewModeOnAGateReachesItsScopedNode(t *testing.T) {
	spec, err := ParseRoute(readinessDone, readinessSteps+failFastGate, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	scoped := spec.ScopedReviewers("in_progress", nil)
	if len(scoped) != 1 || scoped[0].Skill != "scoped-judge" {
		t.Fatalf("scoped reviewers = %+v, want the one fail-fast gate", scoped)
	}
	if scoped[0].ReviewMode != ReviewModeFailFast {
		t.Errorf("ReviewMode = %q, want %q", scoped[0].ReviewMode, ReviewModeFailFast)
	}
	if len(scoped[0].Panel) != 2 {
		t.Errorf("a panel gate keeps its seats beside the mode, got %v", scoped[0].Panel)
	}
}

func TestReviewModeFullIsTheDefaultAndMayBeSpelled(t *testing.T) {
	for name, line := range map[string]string{"absent": "", "explicit": `review_mode = "full"`} {
		gate := "\n[[gate]]\nskill = \"scoped-judge\"\non = [\"in_progress\"]\n" + line + "\n"
		spec, err := ParseRoute(readinessDone, readinessSteps+gate, "feature", nil)
		if err != nil {
			t.Fatalf("%s: ParseRoute: %v", name, err)
		}
		scoped := spec.ScopedReviewers("in_progress", nil)
		if len(scoped) != 1 || scoped[0].ReviewMode != "" {
			t.Errorf("%s: ReviewMode = %+v, want it empty (full)", name, scoped)
		}
	}
}

func TestReviewModeUnknownValueIsRefusedOnAGate(t *testing.T) {
	bad := strings.Replace(failFastGate, `"fail-fast"`, `"quick"`, 1)
	_, err := ParseSteps(readinessSteps + bad)
	if err == nil {
		t.Fatal("want an error for an unknown review_mode")
	}
	for _, want := range []string{"step.toml", `"scoped-judge"`, "review_mode", `"quick"`, ReviewModeFull, ReviewModeFailFast} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q, got: %v", want, err)
		}
	}
}

func TestFailFastListReachesTheEntryEdgeOnly(t *testing.T) {
	steps := strings.Replace(readinessSteps, "reject_budget = 3", "reject_budget = 3\nfail_fast = [\"gate-b\"]", 1)
	spec, err := ParseRoute(readinessDone, steps, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	for _, tr := range spec.Transitions {
		switch {
		case tr.From == "backlog" && tr.To == "plan":
			if len(tr.FailFast) != 1 || tr.FailFast[0] != "gate-b" {
				t.Errorf("backlog→plan FailFast = %v, want [gate-b]", tr.FailFast)
			}
		case tr.From == "plan" && tr.To == "in_progress":
			if len(tr.FailFast) != 0 {
				t.Errorf("plan→in_progress FailFast = %v, want none (the key is per step)", tr.FailFast)
			}
		}
	}
	for _, tr := range readinessSpec(t).Transitions {
		if len(tr.FailFast) != 0 {
			t.Errorf("an absent key leaves %s→%s FailFast = %v, want none", tr.From, tr.To, tr.FailFast)
		}
	}
}

func TestFailFastNamingANonReviewerIsRefused(t *testing.T) {
	bad := strings.Replace(readinessSteps, "reject_budget = 3", "reject_budget = 3\nfail_fast = [\"nobody\"]", 1)
	_, err := ParseSteps(bad)
	if err == nil {
		t.Fatal("want an error for a fail_fast entry that is not one of the step's reviewers")
	}
	for _, want := range []string{"step.toml", `step "readied"`, "fail_fast", `"nobody"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q, got: %v", want, err)
		}
	}
}
