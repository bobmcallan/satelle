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
	materializeDefault(t, repo, "skills", "satelle-story-blocked-review")
	materializeDefault(t, repo, "workflows", "done")
	materializeDefault(t, repo, "workflows", "step")

	skill := filepath.Join(repo, ".satelle", "skills", "satelle-story-blocked-review.md")
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("materialize did not land blocked-review skill: %v", err)
	}
	body, err := os.ReadFile(skill)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "reason") {
		t.Error("seeded blocked-review skill should require a reason")
	}

	wf := filepath.Join(repo, ".satelle", "workflows", "done.toml")
	wbody, err := os.ReadFile(wf)
	if err != nil {
		t.Fatal(err)
	}
	s := string(wbody)
	// A derived route DECLARES its park state and the reviewer that admits it on
	// one `park =` inline table; the topology into and out of it is synthesised by
	// the binary, so there is no edge to look for in the file (sty_3795e7f6).
	if !strings.Contains(s, "park =") || !strings.Contains(s, `"satelle-story-blocked-review"`) {
		t.Errorf("the seeded declaration of done must declare a park state gated by "+
			"satelle-story-blocked-review:\n%s", s)
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
	materializeDefault(t, repo, "skills", "satelle-story-blocked-review")
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
	// sty_7b69954a AC2: preemption is a legitimate park reason (not only world-not-ready).
	for _, want := range []string{"Preemption", "preempted-by:", "stop-request"} {
		if !strings.Contains(s, want) {
			t.Errorf("blocked-review skill missing preemption guidance %q", want)
		}
	}
}
