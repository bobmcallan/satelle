//go:build integration

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNamedReviewerBindingOnEdge (sty_a476a2f8): agent validate reports the
// binding that will run a named gate; role=agent on a gated edge fails closed;
// workflow refresh strips legacy model=.
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

	mustWrite(t, filepath.Join(repo, ".satelle", "workflows", "named-gate.md"), `---
name: named-gate
type: workflow
scope: project
applies_to: ["named-gate-test"]
description: Test workflow for named reviewer binding allocation.
---

`+"```dot"+`
digraph named_gate {
  backlog [shape=Mdiamond]
  done [shape=Msquare]
  backlog -> done [agent=reviewer-deep, prompt="@skill:satelle-story-intent-review"]
}
`+"```"+`
`)
	mustRun(t, testBin, repo, "reindex")
	out := mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(out, "agent=reviewer-deep") || !strings.Contains(out, "effective_model=\"opus\"") {
		t.Fatalf("agent validate should report reviewer-deep/opus:\n%s", out)
	}

	// Role mismatch: agent role on gated edge
	mustWrite(t, filepath.Join(repo, ".satelle", "agents.toml"), fmt.Sprintf(
		"[reviewer]\nrole=\"reviewer\"\ncommand=%q\ntools=\"read_file\"\n\n[coder-x]\nrole=\"agent\"\ncommand=%q\ntools=\"read_file\"\n",
		stubR+" {system} {tools} {model}", stubR+" {system} {tools} {model}"))
	mustWrite(t, filepath.Join(repo, ".satelle", "workflows", "bad-role.md"), `---
name: bad-role
type: workflow
scope: project
applies_to: ["bad-role-test"]
description: Workflow that mis-allocates a performer on a gate edge.
---

`+"```dot"+`
digraph bad {
  backlog [shape=Mdiamond]
  done [shape=Msquare]
  backlog -> done [agent=coder-x, prompt="@skill:satelle-story-intent-review"]
}
`+"```"+`
`)
	mustRun(t, testBin, repo, "reindex")
	out, err := run(t, testBin, repo, "agent", "validate")
	if err == nil && !strings.Contains(out, "FAIL") {
		t.Fatalf("role=agent on gated edge should fail validate:\n%s", out)
	}
	if !strings.Contains(out, "role=") || !strings.Contains(out, "coder-x") {
		t.Fatalf("expected named role mismatch error:\n%s", out)
	}

	// model= strip via refresh --apply
	mustWrite(t, filepath.Join(repo, ".satelle", "agents.toml"), fmt.Sprintf(
		"[reviewer]\nrole=\"reviewer\"\ncommand=%q\ntools=\"read_file\"\n",
		stubR+" {system} {tools} {model}"))
	mustWrite(t, filepath.Join(repo, ".satelle", "workflows", "legacy-model.md"), `---
name: legacy-model
type: workflow
scope: project
applies_to: ["legacy-model-test"]
description: Workflow carrying legacy model= attribute for format-drift coverage.
---

`+"```dot"+`
digraph leg {
  backlog [shape=Mdiamond]
  done [shape=Msquare]
  backlog -> done [agent=reviewer, prompt="@skill:satelle-story-intent-review", model="opus"]
}
`+"```"+`
`)
	mustRun(t, testBin, repo, "reindex")
	out = mustRun(t, testBin, repo, "workflow", "refresh", "legacy-model", "--apply")
	body, _ := os.ReadFile(filepath.Join(repo, ".satelle", "workflows", "legacy-model.md"))
	// Look only inside the fenced dot block — description text may mention model=.
	dot := string(body)
	if i := strings.Index(dot, "```dot"); i >= 0 {
		dot = dot[i:]
		if j := strings.Index(dot[6:], "```"); j >= 0 {
			dot = dot[:6+j]
		}
	}
	if strings.Contains(dot, "model=") {
		t.Fatalf("refresh --apply should strip model= from DOT:\n%s\nreport:\n%s", body, out)
	}
}
