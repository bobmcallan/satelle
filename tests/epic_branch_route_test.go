//go:build integration

package tests

import (
	"strings"
	"testing"
)

// sty_b9d9a3df: a repo with no .satelle/workflows of its own gets the
// epic-branch lane from the shipped route. These read the route the real binary
// prints, so a gate or step left out of the shipped halves fails here.

// stepBlock returns the lines of one step in `workflow show` output: from the
// numbered step line whose status is name up to the next numbered step.
func stepBlock(t *testing.T, shown, name string) string {
	t.Helper()
	var block []string
	in := false
	for _, ln := range strings.Split(shown, "\n") {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "Always-on gates") {
			break
		}
		numbered := len(trimmed) > 2 && trimmed[0] >= '0' && trimmed[0] <= '9' && strings.Contains(trimmed[:3], ".")
		if numbered {
			in = strings.HasSuffix(trimmed, ". "+name) || strings.HasSuffix(trimmed, ". "+name+" [terminal]")
		}
		if in {
			block = append(block, ln)
		}
	}
	if len(block) == 0 {
		t.Fatalf("step %q not in:\n%s", name, shown)
	}
	return strings.Join(block, "\n")
}

// AC1: the working lane's close lists the lane check among its gates.
func TestShippedWorkingLaneCloseListsTheEpicBranchCheck(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	shown := mustRun(t, testBin, repo, "workflow", "show", "feature")
	if done := stepBlock(t, shown, "done"); !strings.Contains(done, "satelle-epic-branch-check") {
		t.Errorf("the working lane's done step lists no satelle-epic-branch-check:\n%s", done)
	}
	if inProgress := stepBlock(t, shown, "in_progress"); strings.Contains(inProgress, "satelle-epic-branch-check") {
		t.Errorf("the lane check is a close gate, not an engage gate:\n%s", inProgress)
	}
}

// AC2: both container categories merge between readiness and the close, wait on
// every child, and are performed by the shipped merge skill; the close is gated
// by the merged-check.
func TestShippedContainerRouteMergesBeforeTheClose(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	for _, category := range []string{"epic-parent", "parent"} {
		shown := mustRun(t, testBin, repo, "workflow", "show", category)
		ready := strings.Index(shown, "discharges: ready")
		merged := strings.Index(shown, "discharges: merged")
		closed := strings.Index(shown, "discharges: children-resolved")
		if ready < 0 || merged < ready || closed < merged {
			t.Fatalf("%s: want ready, merged, children-resolved in that order:\n%s", category, shown)
		}
		block := stepBlock(t, shown, "merging")
		if !strings.Contains(block, "satelle-epic-merge") {
			t.Errorf("%s: merging is not performed by satelle-epic-merge:\n%s", category, block)
		}
		if closeBlock := stepBlock(t, shown, "done"); !strings.Contains(closeBlock, "satelle-epic-merged-check") {
			t.Errorf("%s: the close is not gated by satelle-epic-merged-check:\n%s", category, closeBlock)
		}
	}
}
