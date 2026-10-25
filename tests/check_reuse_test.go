//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reuseCheckSkill opts in to check_reuse: tree and counts its own runs in a
// git-ignored file, so the count is observable and not part of the tree hash.
const reuseCheckSkill = `---
name: reuse-chk
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
check_reuse: tree
description: Test fixture — a tree-reusable check that appends a line to .counter/runs each time it really runs.
---

# Reuse check

` + "```check" + `
mkdir -p .counter
echo run >> .counter/runs
` + "```" + `
`

// holdCheckSkill refuses while the git-ignored .hold file exists. It is a second
// gate on the same edge whose state lives outside the tree hash, so the edge can
// be refused and re-presented on a byte-identical tree.
const holdCheckSkill = `---
name: hold-chk
scope: project
type: skill
tags: [type:skill, type:reviewer, type:functional-check]
description: Test fixture — refuses while .hold exists.
---

# Hold check

` + "```check" + `
if [ -e .hold ]; then echo "held: .hold exists"; exit 1; fi
` + "```" + `
`

// TestCheckReuseAcrossRepresentedEdge (sty_dc554488 AC6): one edge presented
// twice on an unchanged tree — refused by a second gate the first time — runs the
// tree-reusable check's command once, records one green suite_run, and the
// accepting round's verdict cites it.
func TestCheckReuseAcrossRepresentedEdge(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo)
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".gitignore"), ".counter/\n.hold\n")
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "reuse-chk.md"), reuseCheckSkill)
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "hold-chk.md"), holdCheckSkill)
	writeSpineFixture(t, repo, "", "", "",
		"in_progress|executor|||", "done|||reuse-chk,hold-chk|")
	mustRun(t, testBin, repo, "reindex")
	id := gateStory(t, repo, repo)

	writeFile(t, filepath.Join(repo, ".hold"), "")
	if out, err := presentDone(t, repo, id); err == nil {
		t.Fatalf("the first presentation must be refused by hold-chk:\n%s", out)
	} else if !strings.Contains(out, "hold-chk") {
		t.Fatalf("the refusal should name hold-chk:\n%s", out)
	}
	if err := os.Remove(filepath.Join(repo, ".hold")); err != nil {
		t.Fatal(err)
	}
	out, err := presentDone(t, repo, id)
	if err != nil {
		t.Fatalf("the second presentation on an unchanged tree must be accepted: %v\n%s", err, out)
	}

	runs, err := os.ReadFile(filepath.Join(repo, ".counter", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(runs), "run\n"); n != 1 {
		t.Errorf("the reuse check's command ran %d times, want 1", n)
	}
	suite := mustRun(t, testBin, repo, "ledger", "list", "--story", id, "--kind", "suite_run")
	if n := strings.Count(suite, `"kind": "suite_run"`); n != 1 {
		t.Errorf("want exactly one suite_run, got %d:\n%s", n, suite)
	}
	for _, want := range []string{`"outcome":"green"`, `"check":"reuse-chk"`, `"tree":"`, `"command_sha":"`} {
		if !strings.Contains(compactJSON(suite), want) {
			t.Errorf("suite_run lacks %s:\n%s", want, suite)
		}
	}
	verdicts := mustRun(t, testBin, repo, "ledger", "list", "--story", id, "--kind", "review_accept")
	if !strings.Contains(verdicts, "reused suite_run evt_") {
		t.Errorf("the accepting verdict must cite the reused suite_run:\n%s", verdicts)
	}
}

// compactJSON strips the whitespace a pretty-printed ledger listing adds after
// a colon, so a field assertion does not depend on the indentation.
func compactJSON(s string) string {
	return strings.NewReplacer(`": `, `":`, "\n", "", "  ", "").Replace(s)
}
