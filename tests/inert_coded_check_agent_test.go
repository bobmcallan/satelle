//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInertCodedCheckAgent_DiscoverRefreshConsultative proves sty_4cebc624:
// format-drift reports inert agent= on a coded-check scoped node; refresh
// dry-run leaves the file unchanged; --apply strips agent=; format-drift is
// CLEAN after; an LLM-skill gate with agent= is not flagged.
func TestInertCodedCheckAgent_DiscoverRefreshConsultative(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	// Coded-check skill (fence) + LLM skill (no fence).
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "fixture-coded-check.md"), `---
name: fixture-coded-check
type: skill
description: functional check fixture
---

# Fixture coded check

`+"```check"+`
#!/bin/sh
exit 0
`+"```"+`
`)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "fixture-llm-review.md"), `---
name: fixture-llm-review
type: skill
description: LLM gate fixture
---

# Fixture LLM review

Return JSON {"decision": "accept", "notes": "ok"}.
`)

	// Old-shape workflow: inert agent= on coded-check node; LLM node keeps agent=.
	wfPath := filepath.Join(repo, ".satelle", "workflows", "satelle-inert-fixture.md")
	oldBody := `---
name: satelle-inert-fixture
type: workflow
applies_to: ["inert-fixture"]
description: fixture — inert agent= on coded-check node
---

` + "```dot" + `
digraph w {
  graph [goal="fixture", vars="story"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  codecheck [agent=reviewer, prompt="@skill:fixture-coded-check", on="done"]
  llmgate [agent=reviewer, prompt="@skill:fixture-llm-review", on="done"]
  backlog -> in_progress [agent=reviewer, prompt="@skill:satelle-story-intent-review"]
  in_progress -> done
}
` + "```" + `
`
	writeFile(t, wfPath, oldBody)
	mustRun(t, testBin, repo, "reindex")

	// AC1/AC2: discover via format-drift; precise (coded only).
	drift := mustRun(t, testBin, repo, "workflow", "format-drift", "satelle-inert-fixture")
	if !strings.Contains(drift, "inert_coded_check_agent") {
		t.Fatalf("want inert_coded_check_agent finding:\n%s", drift)
	}
	if !strings.Contains(drift, "codecheck") {
		t.Fatalf("finding must name codecheck node:\n%s", drift)
	}
	if strings.Contains(drift, "llmgate") {
		t.Fatalf("LLM gate must not be flagged:\n%s", drift)
	}

	// AC3: refresh without --apply is consultative — file bytes unchanged.
	before, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	dry := mustRun(t, testBin, repo, "workflow", "refresh", "satelle-inert-fixture")
	if !strings.Contains(dry, "Dry-run") && !strings.Contains(strings.ToLower(dry), "dry-run") {
		// help text says Dry-run only
		if !strings.Contains(dry, "proposed refresh") && !strings.Contains(dry, "--apply") {
			t.Logf("refresh dry-run output:\n%s", dry)
		}
	}
	afterDry, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(afterDry) {
		t.Fatal("refresh without --apply must not rewrite authored substrate")
	}

	// Apply: strips agent= from codecheck.
	mustRun(t, testBin, repo, "workflow", "refresh", "satelle-inert-fixture", "--apply")
	after, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(after)
	if strings.Contains(body, `codecheck [agent=`) {
		t.Fatalf("codecheck still has agent= after --apply:\n%s", body)
	}
	if !strings.Contains(body, `prompt="@skill:fixture-coded-check"`) {
		t.Fatalf("codecheck prompt lost:\n%s", body)
	}
	if !strings.Contains(body, `llmgate [agent=reviewer`) && !strings.Contains(body, `agent=reviewer, prompt="@skill:fixture-llm-review"`) {
		t.Fatalf("LLM llmgate agent= must remain:\n%s", body)
	}

	// Clean after apply.
	clean := mustRun(t, testBin, repo, "workflow", "format-drift", "satelle-inert-fixture")
	if strings.Contains(clean, "inert_coded_check_agent") {
		t.Fatalf("after apply, inert finding must be gone:\n%s", clean)
	}
}
