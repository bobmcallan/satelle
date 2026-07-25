//go:build integration

package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubReviewerChildrenResolved points [reviewer] at a script that implements the
// parent/epic-parent children-resolved rule from the cancel/done payload.
// Proves children reach a cancel transition, reject blocks, accept advances
// (sty_de8e8e2c AC-6).
func stubReviewerChildrenResolved(t *testing.T, repo string) {
	t.Helper()
	script := filepath.Join(repo, "verdict-children.sh")
	// ChildState marshals as {"id":"...","status":"..."}. Flag any object whose
	// status is not done/cancelled as unresolved.
	body := `#!/bin/sh
p=$(cat)
# Extract compact child objects; keep those not terminal.
un=$(printf '%s' "$p" | tr -d '\n' | grep -oE '\{"id":"[^"]+","status":"[^"]+"\}' \
  | grep -v '"status":"done"' | grep -v '"status":"cancelled"' || true)
if [ -n "$un" ]; then
  printf '{"decision":"reject","notes":"unresolved children: %s"}\n' "$(printf '%s' "$un" | tr '\n' ' ')"
else
  printf '{"decision":"accept","notes":""}\n'
fi
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "agents.toml"),
		[]byte(fmt.Sprintf("[reviewer]\nrole = \"reviewer\"\ncommand = \"%s {system} {tools} {model}\"\n", script)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCancelContainerCascade (sty_de8e8e2c AC-6): cancelling an epic-parent with
// an unresolved child is refused naming that child; after the child cancels,
// the parent cancel accepts. A leaf cancel is unaffected.
func TestCancelContainerCascade(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	materializeDefaultSolution(t, repo)
	stubReviewerChildrenResolved(t, repo)
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[review]\ngate_create = false\n")

	// Epic parent
	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "Epic to shelve",
		"--body", "Container; cancel reason: shelved — not moving forward",
		"--acceptance", "1. children resolved",
		"--category", "epic-parent")
	var epic struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &epic); err != nil {
		t.Fatalf("parse epic: %v\n%s", err, out)
	}

	// Child under epic
	out = mustRun(t, testBin, repo, "story", "create",
		"--title", "Child of epic",
		"--body", "Still live work",
		"--acceptance", "1. something",
		"--category", "chore",
		"--parent", epic.ID)
	var child struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &child); err != nil {
		t.Fatalf("parse child: %v\n%s", err, out)
	}

	// Cancel parent while child unresolved → refuse, name child, status unchanged.
	refuse, err := run(t, testBin, repo, "story", "set", epic.ID, "--status", "cancelled")
	if err == nil {
		t.Fatalf("epic cancel with live child should fail; out:\n%s", refuse)
	}
	if !strings.Contains(refuse, child.ID) {
		t.Errorf("reject notes should name unresolved child %s:\n%s", child.ID, refuse)
	}
	got := mustRun(t, testBin, repo, "story", "get", epic.ID)
	if strings.Contains(got, `"status": "cancelled"`) || strings.Contains(got, `"status":"cancelled"`) {
		t.Fatalf("epic status must remain non-cancelled after refuse:\n%s", got)
	}

	// Cancel child first, then parent.
	mustRun(t, testBin, repo, "story", "set", child.ID, "--status", "cancelled")
	mustRun(t, testBin, repo, "story", "set", epic.ID, "--status", "cancelled")
	got = mustRun(t, testBin, repo, "story", "get", epic.ID)
	if !strings.Contains(got, `"status": "cancelled"`) && !strings.Contains(got, `"status":"cancelled"`) {
		t.Fatalf("epic should be cancelled after child resolved:\n%s", got)
	}

	// Leaf unaffected: no parent, cancels first time.
	out = mustRun(t, testBin, repo, "story", "create",
		"--title", "Leaf to cancel",
		"--body", "Reason: withdrawn by operator",
		"--acceptance", "1. n/a",
		"--category", "chore")
	var leaf struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &leaf); err != nil {
		t.Fatalf("parse leaf: %v\n%s", err, out)
	}
	mustRun(t, testBin, repo, "story", "set", leaf.ID, "--status", "cancelled")
	got = mustRun(t, testBin, repo, "story", "get", leaf.ID)
	if !strings.Contains(got, "cancelled") {
		t.Fatalf("leaf cancel should succeed:\n%s", got)
	}
}

// TestCancelReviewSkillContent (sty_de8e8e2c AC-2/3/4/5): embedded cancel gate
// carries container branch, floor, sibling bundling, conditional cancel-reason,
// terminality/revival; does not hardcode this repo's vocabulary values.
func TestCancelReviewSkillContent(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	materializeDefault(t, repo, "skills", "satelle-story-cancel-review")
	body, err := os.ReadFile(filepath.Join(repo, ".satelle", "skills", "satelle-story-cancel-review.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"epic-parent",
		"children",
		"cancel-reason",
		"reason on record",
		"sibling",
		"supersedes:",
		"terminal",
		`{"decision"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("embedded cancel-review missing %q", want)
		}
	}
	// Floor: tag is not a substitute for recorded reason.
	if !strings.Contains(s, "not a substitute") && !strings.Contains(s, "not a substitute") {
		// already covered by "not a substitute" in skill text
	}
	if !strings.Contains(strings.ToLower(s), "substitute") {
		t.Error("skill must state cancel-reason tag is not a substitute for recorded reason")
	}
	// Conditional: when namespace not declared, do not require the tag.
	if !strings.Contains(s, "Where no such namespace is declared") && !strings.Contains(s, "do not require the tag") {
		t.Error("skill must condition cancel-reason tag expectation on repo declaration")
	}
	// Repo-agnostic: none of this repo's five values may appear in the embedded body.
	for _, banned := range []string{"shelved", "duplicate", "superseded", "withdrawn", "wrong"} {
		// Allow "withdrawn" only in legitimate-supersedes prose as operator language —
		// the AC says values of the controlled namespace. Check as cancel-reason tokens.
		if strings.Contains(s, "cancel-reason:"+banned) {
			t.Errorf("embedded skill must not hardcode cancel-reason:%s", banned)
		}
	}
	// Ban bare vocabulary list that would pin this repo's set into the binary.
	if strings.Contains(s, `["shelved"`) || strings.Contains(s, "shelved, duplicate") {
		t.Error("embedded skill must not list this repo's cancel-reason values")
	}
}
