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

// TestNamedReviewerBindingOnEdge (sty_a476a2f8): agent validate reports the
// binding that will run a named gate, and role=agent on a gated edge fails
// closed.
//
// A third leg proved `satelle workflow refresh --apply` stripped a legacy DOT
// model= attribute. Both the command and the attribute retire with the DOT front
// end (sty_d953c5d8): a route names its model on the agents.toml binding, so
// there is nothing left to strip.
func TestNamedReviewerBindingOnEdge(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	logPath := filepath.Join(repo, "gate-sections.log")
	stubR := filepath.Join(repo, "verdict-reviewer.sh")
	stubD := filepath.Join(repo, "verdict-deep.sh")
	if err := os.WriteFile(stubR, []byte("#!/bin/sh\necho reviewer >> '"+logPath+"'\necho '{\"decision\":\"accept\",\"notes\":\"\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stubD, []byte("#!/bin/sh\necho reviewer-deep >> '"+logPath+"'\necho '{\"decision\":\"accept\",\"notes\":\"\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".satelle", "agents.toml"), fmt.Sprintf(
		"[reviewer]\nrole=\"reviewer\"\ncommand=%q\ntools=\"read_file\"\n\n[reviewer-deep]\nrole=\"reviewer\"\ncommand=%q\ntools=\"read_file\"\nmodel=\"opus\"\n",
		stubR+" {system} {tools} {model}", stubD+" {system} {tools} {model}"))

	writeSpineFixture(t, repo, "", "", "", "done|||satelle-story-intent-review|reviewer-deep")
	mustRun(t, testBin, repo, "reindex")
	out := mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(out, "agent=reviewer-deep") || !strings.Contains(out, "effective_model=\"opus\"") {
		t.Fatalf("agent validate should report reviewer-deep/opus:\n%s", out)
	}

	// Role mismatch: agent role on gated edge
	mustWrite(t, filepath.Join(repo, ".satelle", "agents.toml"), fmt.Sprintf(
		"[reviewer]\nrole=\"reviewer\"\ncommand=%q\ntools=\"read_file\"\n\n[coder-x]\nrole=\"agent\"\ncommand=%q\ntools=\"read_file\"\n",
		stubR+" {system} {tools} {model}", stubR+" {system} {tools} {model}"))
	writeSpineFixture(t, repo, "", "", "", "done|||satelle-story-intent-review|coder-x")
	mustRun(t, testBin, repo, "reindex")
	out, err := run(t, testBin, repo, "agent", "validate")
	if err == nil && !strings.Contains(out, "FAIL") {
		t.Fatalf("role=agent on gated edge should fail validate:\n%s", out)
	}
	if !strings.Contains(out, "role=") || !strings.Contains(out, "coder-x") {
		t.Fatalf("expected named role mismatch error:\n%s", out)
	}
}

// TestNamedGateRunsNamedHarness (sty_68dafd5f): a gated edge agent=reviewer-deep
// invokes that binding's harness script, not the default [reviewer] script.
func TestNamedGateRunsNamedHarness(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		"[review]\ngate_create = false\n\n[categories]\nenforce = \"off\"\n")
	materializeDefault(t, repo, "skills", "satelle-story-intent-review")

	logPath := filepath.Join(repo, "gate-sections.log")
	stubR := filepath.Join(repo, "verdict-reviewer.sh")
	stubD := filepath.Join(repo, "verdict-deep.sh")
	if err := os.WriteFile(stubR, []byte("#!/bin/sh\necho reviewer >> '"+logPath+"'\necho '{\"decision\":\"accept\",\"notes\":\"\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stubD, []byte("#!/bin/sh\necho reviewer-deep >> '"+logPath+"'\necho '{\"decision\":\"accept\",\"notes\":\"\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, ".satelle", "agents.toml"), fmt.Sprintf(
		"[reviewer]\nrole=\"reviewer\"\ncommand=%q\ntools=\"read_file\"\n\n[reviewer-deep]\nrole=\"reviewer\"\ncommand=%q\ntools=\"read_file\"\nmodel=\"opus\"\n",
		stubR+" {system} {tools} {model}", stubD+" {system} {tools} {model}"))

	writeSpineFixture(t, repo, "", "", "", "done|||satelle-story-intent-review|reviewer-deep")
	mustRun(t, testBin, repo, "reindex")

	created := mustRun(t, testBin, repo, "story", "create",
		"--title", "Named harness story",
		"--body", "Prove agent=reviewer-deep runs its own script",
		"--acceptance", "1. done",
		"--category", "named-gate-test")
	var st struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created), &st); err != nil || st.ID == "" {
		t.Fatalf("parse create: %v\n%s", err, created)
	}
	mustRun(t, testBin, repo, "story", "set", st.ID, "--status", "done")
	logBody, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBody)
	if !strings.Contains(log, "reviewer-deep") {
		t.Fatalf("named gate must invoke reviewer-deep harness, log=%q", log)
	}
	// Default [reviewer] must not have run this edge.
	if strings.Contains(log, "reviewer\n") || strings.HasPrefix(log, "reviewer") && !strings.Contains(log, "reviewer-deep") {
		// Allow only reviewer-deep lines.
		for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
			if line == "reviewer" {
				t.Fatalf("default [reviewer] harness must not run the named edge, log=%q", log)
			}
		}
	}
}
