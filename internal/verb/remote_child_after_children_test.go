package verb_test

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

// A step that places its parallel children remote (sty_dde8b6a4) changes where a
// child performs, never what the container waits for: while a remote child has
// not discharged the after_children obligation — its cloud dispatch failed and
// it stayed at its from-state — the container's merge step refuses entry and
// names it.
func TestAfterChildrenHoldsTheMergeForAFailedRemoteChild(t *testing.T) {
	_, _ = twoWorktrees(t)
	wf := afterChildrenWF(true)
	declared := "[coded]\nstatus = \"in_progress\"\nagent = \"coder\"\nremote_agent = \"coder-cloud\"\nlocal_tags = [\"lane:trunk\"]\n"
	stepBody := strings.Replace(wf["step"], "[coded]\nstatus = \"in_progress\"\nagent = \"executor\"\n", declared, 1)
	if stepBody == wf["step"] {
		t.Fatal("fixture did not take the remote_agent declaration")
	}
	wf["step"] = stepBody
	wireWithWorkflowsStore(t, wf)
	verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Parallel: config.ParallelEpic}})
	t.Cleanup(verb.ClearEngagementMode)

	epic := waveEpic(t)
	remote := acChild(t, epic, "fix")
	moveTo(t, epic.ID, "ready")

	_, err := dispatchRaw(t, "story-set", map[string]any{"id": epic.ID, "status": "merging"})
	if err == nil || !strings.Contains(err.Error(), remote.ID) || !strings.Contains(err.Error(), `"coded"`) {
		t.Fatalf("the merge must refuse naming the undischarged remote child %s: %v", remote.ID, err)
	}
	if got := statusOf(t, epic.ID).Status; got != "ready" {
		t.Errorf("a refused container stays at ready, got %q", got)
	}

	moveTo(t, remote.ID, "cancelled")
	moveTo(t, epic.ID, "merging")
}
