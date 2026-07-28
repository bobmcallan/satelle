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

// TestInertCodedCheckAgent_VerdictUnchangedAcrossStrip proves AC7 step 6:
// driving a transition across a coded-check scoped gate yields the same
// accept/reject before and after agent= removal. Each leg uses its own temp
// repo so engagement seats do not collide across sequential stories.
func TestInertCodedCheckAgent_VerdictUnchangedAcrossStrip(t *testing.T) {
	driveLeg := func(t *testing.T, skillBody string, skillName string, withAgent, wantAccept bool) {
		t.Helper()
		repo := t.TempDir()
		mustRun(t, testBin, repo, "init")
		writeFile(t, filepath.Join(repo, ".satelle", "skills", skillName+".md"), skillBody)
		writeFile(t, filepath.Join(repo, ".satelle", "skills", "code.md"), `---
name: code
type: skill
description: implement
---

Implement the slice.
`)
		agent := ""
		if withAgent {
			agent = `agent=reviewer, `
		}
		body := `---
name: satelle-verdict-fixture
type: workflow
scope: project
applies_to: ["verdict-fixture"]
description: fixture for verdict-unchanged across agent= strip
---

` + "```dot" + `
digraph w {
  graph [goal="fixture", vars="story"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  codecheck [` + agent + `prompt="@skill:` + skillName + `", on="done"]
  backlog -> in_progress
  in_progress -> done
}
` + "```" + `
`
		wfPath := filepath.Join(repo, ".satelle", "workflows", "satelle-verdict-fixture.md")
		writeFile(t, wfPath, body)
		mustRun(t, testBin, repo, "reindex")

		out := mustRun(t, testBin, repo, "story", "create",
			"--category", "verdict-fixture",
			"--title", "Verdict fixture story for coded-check agent neutrality",
			"--body", "Prove coded-check gate verdict is unchanged when agent= is stripped.",
			"--acceptance", "1. Gate accepts or rejects per the fixture skill.",
		)
		id := ""
		for _, f := range strings.Fields(out) {
			if strings.HasPrefix(strings.Trim(f, `",`), "sty_") {
				id = strings.Trim(f, `",`)
				break
			}
		}
		if id == "" {
			t.Fatalf("could not parse story id from:\n%s", out)
		}
		mustRun(t, testBin, repo, "story", "set", id, "--status", "in_progress")
		result, err := run(t, testBin, repo, "story", "set", id, "--status", "done")
		combined := result
		if err != nil {
			combined = result + "\n" + err.Error()
		}
		got := mustRun(t, testBin, repo, "story", "get", id)
		isDone := strings.Contains(got, `"status": "done"`)
		if wantAccept {
			if !isDone {
				t.Fatalf("want accept/done (withAgent=%v):\nset: %s\nget: %s", withAgent, combined, got)
			}
		} else if isDone {
			t.Fatalf("want reject (withAgent=%v), got done:\n%s", withAgent, got)
		}

		// When withAgent, also prove refresh strips and a second story still matches.
		if withAgent {
			mustRun(t, testBin, repo, "workflow", "refresh", "satelle-verdict-fixture", "--apply")
			b, _ := os.ReadFile(wfPath)
			if strings.Contains(string(b), `codecheck [agent=`) {
				t.Fatalf("agent= not stripped:\n%s", b)
			}
			// Fresh story after strip (same repo; prior story is terminal).
			out2 := mustRun(t, testBin, repo, "story", "create",
				"--category", "verdict-fixture",
				"--title", "Post-strip verdict fixture story for coded-check neutrality",
				"--body", "Second story after agent= strip; same coded-check skill.",
				"--acceptance", "1. Gate accepts or rejects per the fixture skill.",
			)
			id2 := ""
			for _, f := range strings.Fields(out2) {
				if strings.HasPrefix(strings.Trim(f, `",`), "sty_") {
					id2 = strings.Trim(f, `",`)
					break
				}
			}
			if id2 == "" {
				t.Fatalf("could not parse second story id from:\n%s", out2)
			}
			// Reject leaves in_progress; force-release seat (fixture may lack cancel edge).
			if !isDone {
				_, _ = run(t, testBin, repo, "story", "seat", "release", id)
			}
			mustRun(t, testBin, repo, "story", "set", id2, "--status", "in_progress")
			result2, err2 := run(t, testBin, repo, "story", "set", id2, "--status", "done")
			combined2 := result2
			if err2 != nil {
				combined2 = result2 + "\n" + err2.Error()
			}
			got2 := mustRun(t, testBin, repo, "story", "get", id2)
			isDone2 := strings.Contains(got2, `"status": "done"`)
			if wantAccept != isDone2 {
				t.Fatalf("post-strip verdict mismatch wantAccept=%v isDone=%v\nset: %s\nget: %s", wantAccept, isDone2, combined2, got2)
			}
		}
	}

	acceptSkill := `---
name: fixture-accept
type: skill
description: always-accept coded check
---

` + "```check" + `
#!/bin/sh
exit 0
` + "```" + `
`

	// Top-level-only (no t.Run): isolatedHome is keyed by top-level test name,
	// and engagement seats share one SATELLE_HOME across subtests.
	driveLeg(t, acceptSkill, "fixture-accept", true, true)
}

// TestInertCodedCheckAgent_VerdictRejectAcrossStrip is the reject twin of
// VerdictUnchangedAcrossStrip (separate top-level so SATELLE_HOME/seat isolate).
func TestInertCodedCheckAgent_VerdictRejectAcrossStrip(t *testing.T) {
	// Re-use the same drive logic by calling the accept test's pattern inline.
	// (Keep self-contained to avoid exporting helpers.)
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "fixture-reject.md"), `---
name: fixture-reject
type: skill
description: always-reject coded check
---

`+"```check"+`
#!/bin/sh
echo reject-me
exit 1
`+"```"+`
`)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "code.md"), `---
name: code
type: skill
description: implement
---

Implement the slice.
`)
	wfPath := filepath.Join(repo, ".satelle", "workflows", "satelle-verdict-fixture.md")
	writeFile(t, wfPath, `---
name: satelle-verdict-fixture
type: workflow
scope: project
applies_to: ["verdict-fixture"]
description: fixture for reject verdict across agent= strip
---

`+"```dot"+`
digraph w {
  graph [goal="fixture", vars="story"]
  rankdir=LR
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:code"]
  done [shape=Msquare]
  codecheck [agent=reviewer, prompt="@skill:fixture-reject", on="done"]
  backlog -> in_progress
  in_progress -> done
}
`+"```"+`
`)
	mustRun(t, testBin, repo, "reindex")

	mk := func() string {
		out := mustRun(t, testBin, repo, "story", "create",
			"--category", "verdict-fixture",
			"--title", "Reject verdict fixture for coded-check agent neutrality",
			"--body", "Reject path before and after agent= strip.",
			"--acceptance", "1. Gate rejects per the fixture skill.",
		)
		for _, f := range strings.Fields(out) {
			if strings.HasPrefix(strings.Trim(f, `",`), "sty_") {
				return strings.Trim(f, `",`)
			}
		}
		t.Fatalf("no story id in:\n%s", out)
		return ""
	}
	id1 := mk()
	mustRun(t, testBin, repo, "story", "set", id1, "--status", "in_progress")
	_, _ = run(t, testBin, repo, "story", "set", id1, "--status", "done")
	got1 := mustRun(t, testBin, repo, "story", "get", id1)
	if strings.Contains(got1, `"status": "done"`) {
		t.Fatalf("with agent=: want reject, got done:\n%s", got1)
	}
	// Reject leaves the story in_progress; free the seat for the post-strip story.
	// Fixture has no cancel edge, so force-release the lease.
	mustRun(t, testBin, repo, "story", "seat", "release", id1)

	mustRun(t, testBin, repo, "workflow", "refresh", "satelle-verdict-fixture", "--apply")
	b, _ := os.ReadFile(wfPath)
	if strings.Contains(string(b), `codecheck [agent=`) {
		t.Fatalf("agent= not stripped:\n%s", b)
	}

	id2 := mk()
	mustRun(t, testBin, repo, "story", "set", id2, "--status", "in_progress")
	_, _ = run(t, testBin, repo, "story", "set", id2, "--status", "done")
	got2 := mustRun(t, testBin, repo, "story", "get", id2)
	if strings.Contains(got2, `"status": "done"`) {
		t.Fatalf("after strip: want reject, got done:\n%s", got2)
	}
}
