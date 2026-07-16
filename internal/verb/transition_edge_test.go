package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestRefuseSkippedStepPlanBlowThrough (sty_ebd3d666 AC4): backlog→in_progress
// when the DOT only allows backlog→plan is refused naming plan.
func TestRefuseSkippedStepPlanBlowThrough(t *testing.T) {
	wire(t)
	wfDir := t.TempDir()
	body := "---\nname: gate-wf\napplies_to: [\"feature\"]\n---\n\n```dot\ndigraph w {\n  backlog [shape=Mdiamond]\n  plan [agent=executor]\n  in_progress [agent=executor]\n  done [shape=Msquare]\n  blocked [agent=reviewer]\n  backlog -> plan\n  plan -> in_progress\n  in_progress -> done\n  in_progress -> blocked\n  blocked -> in_progress\n}\n```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "gate-wf.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, "doc-sync", map[string]any{"dirs": map[string]string{"workflows": wfDir}})

	var created workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "t", "category": "feature", "body": "b", "acceptance_criteria": "1. a",
	}), &created); err != nil {
		t.Fatal(err)
	}
	// Blow-through: backlog → in_progress (skips plan).
	req, _ := json.Marshal(map[string]any{"id": created.ID, "status": "in_progress"})
	_, err := verb.Dispatch(context.Background(), "story-set", req)
	if err == nil {
		t.Fatal("expected refuse on plan blow-through")
	}
	msg := err.Error()
	for _, want := range []string{"refusing transition", "backlog→in_progress", "expected next step", "plan"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refuse message missing %q:\n%s", want, msg)
		}
	}

	// Legal edge: backlog → plan (may fail later gates; must not be edge fence).
	req, _ = json.Marshal(map[string]any{"id": created.ID, "status": "plan"})
	_, err = verb.Dispatch(context.Background(), "story-set", req)
	if err != nil && strings.Contains(err.Error(), "not an edge") {
		t.Fatalf("legal backlog→plan refused by edge fence: %v", err)
	}
}
