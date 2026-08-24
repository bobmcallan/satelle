//go:build integration

package tests

import (
	"strings"
	"testing"
)

// TestInitSeedsARouteDerivableFromTheEmbeddedDefaults proves the seeded state a
// fresh repo actually gets: init writes no route halves of its own (the
// defaults stay virtual), so the proof is that the binary still derives a legal
// route from the embedded ones.
func TestInitSeedsARouteDerivableFromTheEmbeddedDefaults(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	out, err := run(t, testBin, repo, "workflow", "validate")
	if err != nil {
		t.Fatalf("a freshly init'd repo must derive a route from the embedded defaults: %v\n%s", err, out)
	}
}

// TestRouteAuthoredPerTheReferenceValidates is the drift guard the reference
// owes its readers. The fixture below is written in exactly the form
// satelle-route-standard teaches — a category table keyed by name with ordered
// obligations and inline park/cancel/recover, a tag_obligation, step tables
// keyed by the OBLIGATION they discharge, and an always-on gate entry with
// on/for/applies_to. Unknown keys are a hard parse error, so a reference that
// drifts from the parser makes this fail rather than mislead an author.
func TestRouteAuthoredPerTheReferenceValidates(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerDispatch(t, repo)

	writeExecutorRubric(t, repo, "plan")
	writeExecutorRubric(t, repo, "code")
	writeReferenceFormRoute(t, repo, `obligations = ["raised", "planned", "coded", "closed"]`)

	out, err := run(t, testBin, repo, "workflow", "validate")
	if err != nil {
		t.Fatalf("a route authored per the reference must validate: %v\n%s", err, out)
	}
	// Validation parses the halves; deriving the route is what proves the
	// obligations actually resolve to steps and the topology sorts.
	out, err = run(t, testBin, repo, "workflow", "show", "default")
	if err != nil {
		t.Fatalf("a route authored per the reference must derive: %v\n%s", err, out)
	}
	for _, want := range []string{"backlog", "plan", "in_progress", "done", "blocked", "cancelled"} {
		if !strings.Contains(out, want) {
			t.Errorf("the derived route omits %q:\n%s", want, out)
		}
	}
}

// TestRouteNamingAStatusRatherThanAStepIsRefused is the negative half: the one
// rule the TOML form does not make obvious is that an obligation names a step
// by its TABLE KEY, never by the status the step declares. Writing the status
// instead must fail — otherwise the positive half above proves nothing about
// the rule the reference now leads with.
func TestRouteNamingAStatusRatherThanAStepIsRefused(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerDispatch(t, repo)

	writeExecutorRubric(t, repo, "plan")
	writeExecutorRubric(t, repo, "code")
	// "in_progress" is the STATUS the `coded` step declares, not a step key.
	writeReferenceFormRoute(t, repo, `obligations = ["raised", "planned", "in_progress", "closed"]`)

	out, err := run(t, testBin, repo, "workflow", "show", "default")
	if err == nil {
		t.Fatalf("an obligation naming a status rather than a step key must be refused:\n%s", out)
	}
	for _, want := range []string{"in_progress", "no discharging step"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal should name the unresolved obligation and why (%q):\n%s", want, out)
		}
	}
}

// writeReferenceFormRoute authors both halves in the reference's form, taking the
// wildcard table's obligations line so a test can vary just that.
func writeReferenceFormRoute(t *testing.T, repo, obligations string) {
	t.Helper()
	writeFile(t, repo+"/.satelle/workflows/done.toml", `[meta]
name = "done"
type = "workflow"
scope = "project"
description = "Fixture declaration of done authored in the form satelle-route-standard teaches."

["*"]
`+obligations+`
park = { state = "blocked", gate = "satelle-story-blocked-review" }
cancel = { state = "cancelled", gate = "satelle-story-cancel-review" }
recover = { step = "coded", from = ["closed"] }

[["*".tag_obligation]]
tag = "surface:ui"
obligation = "coded"
`)
	writeFile(t, repo+"/.satelle/workflows/step.toml", `[meta]
name = "step"
type = "workflow"
scope = "project"
description = "Fixture step catalogue authored in the form satelle-route-standard teaches."

[raised]
status = "backlog"
start = true

[planned]
status = "plan"
agent = "planner"
skills = ["plan"]
reviewers = ["satelle-story-intent-review"]
reviewer_agent = "reviewer"
requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
skills = ["code"]
reviewers = ["satelle-story-plan-review"]
reviewer_agent = "reviewer"
parallel = 0
requires = ["planned"]

[closed]
status = "done"
reviewers = ["satelle-story-done-review"]
reviewer_agent = "reviewer"
terminal = true
requires = ["coded"]

[[gate]]
skill = "satelle-step-summary"
agent = "reviewer"
on = ["*"]
for = ["*"]

[[gate]]
skill = "satelle-estimate-actual-review"
on = ["in_progress"]
applies_to = ["surface:ui"]
for = ["*"]
`)
}

// writeExecutorRubric seeds the project skill a step's `skills` names. Executor
// rubrics are repo-authored, so a fixture route that names one has to supply it.
func writeExecutorRubric(t *testing.T, repo, name string) {
	t.Helper()
	writeFile(t, repo+"/.satelle/skills/"+name+".md", `---
name: `+name+`
scope: project
type: skill
tags: [type:skill]
description: Fixture executor rubric for the route-reference form test.
---

# `+name+`

Do the work this step owes, then stop.
`)
}
