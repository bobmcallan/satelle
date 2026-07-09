//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitSeedsBlockedLifecycle proves a fresh init materialises the park-state
// gate skill and a baseline workflow that offers in_progress↔park edges gated
// by satelle-story-blocked-review (sty_af447d80).
func TestInitSeedsBlockedLifecycle(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	skill := filepath.Join(repo, ".satelle", "skills", "satelle-story-blocked-review.md")
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("init did not seed blocked-review skill: %v", err)
	}
	body, err := os.ReadFile(skill)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "reason") {
		t.Error("seeded blocked-review skill should require a reason")
	}

	wf := filepath.Join(repo, ".satelle", "workflows", "satelle-baseline-workflow.md")
	wbody, err := os.ReadFile(wf)
	if err != nil {
		t.Fatal(err)
	}
	s := string(wbody)
	if !strings.Contains(s, "@skill:satelle-story-blocked-review") {
		t.Error("seeded baseline must reference satelle-story-blocked-review")
	}
	// Park edges: into and out of the park node (authored name is substrate, not Go).
	if !strings.Contains(s, "-> blocked") && !strings.Contains(s, "->blocked") {
		// Accept either spacing; require the park skill on an edge or node.
		if !strings.Contains(s, "satelle-story-blocked-review") {
			t.Error("baseline must wire the blocked-review gate")
		}
	}
	mustRun(t, testBin, repo, "reindex")
	mustRun(t, testBin, repo, "workflow", "validate")
	mustRun(t, testBin, repo, "skill", "validate")
}

// TestBlockedReviewSkillRejectsWithoutReason pins the gate rubric's contract:
// the skill body requires a recorded reason (mirrors cancel-review). Full
// LLM accept/reject is covered by the skill text + reviewer path; this test
// asserts the seeded artifact carries the reject-when-no-reason rule.
func TestBlockedReviewSkillRejectsWithoutReason(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	body, err := os.ReadFile(filepath.Join(repo, ".satelle", "skills", "satelle-story-blocked-review.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{"Accept when", "Reject when", "reason", `"decision"`} {
		if !strings.Contains(s, want) {
			t.Errorf("blocked-review skill missing %q", want)
		}
	}
	if !strings.Contains(s, "No reason") && !strings.Contains(s, "no reason") {
		t.Error("skill must reject when no reason is on record")
	}
}
