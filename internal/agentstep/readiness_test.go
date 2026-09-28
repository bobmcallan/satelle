package agentstep

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_5262592e: a readiness performer that also plans may answer with an explicit
// reject decision and no artifact when its premise check fails. Under an attempt
// policy that answer must reach DispatchExecutor as a PerformerReject on the FIRST
// attempt — never be treated as a malformed artifact and repaired.
func TestArtifactAttemptsRejectDecisionIsNotRepaired(t *testing.T) {
	primary := &attemptRunner{runs: []attemptRun{
		{out: `{"decision":"reject","notes":"premise wrong: AC2 names a flag that does not exist"}`},
	}}
	stronger := &attemptRunner{}
	g, _ := attemptedEngine(t, attemptedDispatchSkill, primary, stronger)
	attached := 0
	g.SetArtifactAttacher(func(_ context.Context, _ workitem.Item, name, typ, _ string) (string, string, error) {
		attached++
		return name, typ, nil
	})
	_, err := g.DispatchExecutor(context.Background(), attemptedItem(), "plan")
	var rej *verb.PerformerReject
	if !errors.As(err, &rej) || !strings.Contains(rej.Notes, "AC2 names a flag") {
		t.Fatalf("want a PerformerReject carrying the notes, got %v", err)
	}
	if len(primary.requests) != 1 || len(stronger.requests) != 0 {
		t.Errorf("a reject is an answer, not a repair trigger: primary %d, stronger %d calls", len(primary.requests), len(stronger.requests))
	}
	if attached != 0 {
		t.Errorf("no artifact may be attached for a reject, attached %d", attached)
	}
}

// The definition edits ride the GATE payload, oldest first, windowed to the most
// recent definitionEditCount with each value capped; a story with none carries no
// key at all, and an unwired resolver injects nothing.
func TestGatePayloadIncludesDefinitionEdits(t *testing.T) {
	gateOnce := func(t *testing.T, resolver func(ctx context.Context, itemID string) []DefinitionEdit) string {
		t.Helper()
		g, r := newEngine(t, `{"decision":"accept"}`, fakeDocs{workflow: planEdgeWorkflow, skillBody: "rubric", skillFound: true})
		if resolver != nil {
			g.SetDefinitionEditsResolver(resolver)
		}
		if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_de", Status: "plan"}, "in_progress"); err != nil {
			t.Fatal(err)
		}
		return r.got.Payload
	}

	t.Run("edits ride with before, after and actor", func(t *testing.T) {
		var gotID string
		payload := gateOnce(t, func(_ context.Context, itemID string) []DefinitionEdit {
			gotID = itemID
			return []DefinitionEdit{{Field: "acceptance_criteria", Old: "OLD-AC-TEXT", New: "NEW-AC-TEXT", Actor: "driver", At: "2026-09-28T10:00:00Z"}}
		})
		if gotID != "sty_de" {
			t.Errorf("resolver asked for %q, want sty_de", gotID)
		}
		for _, want := range []string{`"definition_edits"`, "OLD-AC-TEXT", "NEW-AC-TEXT", `"actor":"driver"`, `"field":"acceptance_criteria"`} {
			if !strings.Contains(payload, want) {
				t.Errorf("payload missing %s: %s", want, payload)
			}
		}
	})

	t.Run("windowed and capped", func(t *testing.T) {
		var edits []DefinitionEdit
		for i := 0; i < definitionEditCount+5; i++ {
			edits = append(edits, DefinitionEdit{Field: "title", Old: fmt.Sprintf("OLD-%02d", i), New: strings.Repeat("x", definitionEditValueCeiling*2)})
		}
		payload := gateOnce(t, func(context.Context, string) []DefinitionEdit { return edits })
		if strings.Contains(payload, "OLD-00") || !strings.Contains(payload, fmt.Sprintf("OLD-%02d", definitionEditCount+4)) {
			t.Error("only the most recent edits ride")
		}
		if strings.Contains(payload, strings.Repeat("x", definitionEditValueCeiling+1)) {
			t.Error("each value must be capped")
		}
	})

	t.Run("none or unwired carries no key", func(t *testing.T) {
		for name, resolver := range map[string]func(context.Context, string) []DefinitionEdit{
			"empty":   func(context.Context, string) []DefinitionEdit { return nil },
			"unwired": nil,
		} {
			if payload := gateOnce(t, resolver); strings.Contains(payload, "definition_edits") {
				t.Errorf("%s: payload must carry no definition_edits key: %s", name, payload)
			}
		}
	})
}
