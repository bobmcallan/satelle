package verb_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestRefuseSkippedStepPlanBlowThrough (sty_ebd3d666 AC4): backlog→in_progress
// when the DOT only allows backlog→plan is refused naming plan.
func TestRefuseSkippedStepPlanBlowThrough(t *testing.T) {
	wire(t)
	wfDir := t.TempDir()
	body := routeHalves(
		"## feature\n- raised\n- planned\n- coded\n- closed\npark: blocked\n",
		"## backlog\nstart: true\nprovides: raised\n\n"+
			"## plan\nagent: executor\nprovides: planned\nrequires: raised\n\n"+
			"## in_progress\nagent: executor\nprovides: coded\nrequires: planned\n\n"+
			"## done\nterminal: true\nprovides: closed\nrequires: coded\n")
	writeRouteFiles(t, wfDir, body)
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

	// sty_39e2d9df AC3: the refusal is STRUCTURED — the rule that fired, why it
	// fired here, and where the story may go instead. With the graph derived there
	// is no workflow file to open, so this is the only place that answer lives.
	var ref wfgovern.Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("refusal must be a wfgovern.Refusal, got %T: %v", err, err)
	}
	if ref.Rule != wfgovern.RuleSkippedStep {
		t.Errorf("rule = %q; want %q", ref.Rule, wfgovern.RuleSkippedStep)
	}
	if ref.From != "backlog" || ref.To != "in_progress" || ref.Item != created.ID || ref.Workflow != wfgovern.DerivedRouteName {
		t.Errorf("refusal = %+v; want the edge, item and governing workflow named", ref)
	}
	if strings.TrimSpace(ref.Why) == "" {
		t.Error("a structured refusal must say WHY the rule applied here")
	}
	if len(ref.Alternatives) != 1 || ref.Alternatives[0] != "plan" {
		t.Errorf("alternatives = %v; want the legal moves ([plan])", ref.Alternatives)
	}

	// Legal edge: backlog → plan (may fail later gates; must not be edge fence).
	req, _ = json.Marshal(map[string]any{"id": created.ID, "status": "plan"})
	_, err = verb.Dispatch(context.Background(), "story-set", req)
	if err != nil && strings.Contains(err.Error(), wfgovern.RuleSkippedStep) {
		t.Fatalf("legal backlog→plan refused by edge fence: %v", err)
	}
}
