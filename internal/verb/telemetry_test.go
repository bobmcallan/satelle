package verb_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
)

// TestStoryLogWritesTelemetryEvent pins AC1 (sty_b73c3236): the generic
// `story-log` verb writes a KindTelemetryEvent ledger row carrying the caller's
// kind + data.
func TestStoryLogWritesTelemetryEvent(t *testing.T) {
	wire(t)
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "T"}), &created)

	resp := call(t, "story-log", map[string]any{
		"id": created.ID, "kind": "step-self-report",
		"data": map[string]any{"step": "in_progress", "tokens_total": 42000},
	})
	var out map[string]any
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatal(err)
	}
	if out["recorded"] != true {
		t.Errorf("expected recorded=true, got %+v", out)
	}

	var entries []map[string]any
	json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": created.ID, "kind": "telemetry_event"}), &entries)
	if len(entries) != 1 {
		t.Fatalf("expected 1 telemetry_event entry, got %d: %+v", len(entries), entries)
	}
	payload, _ := entries[0]["payload"].(map[string]any)
	if payload["kind"] != "step-self-report" {
		t.Errorf("payload kind = %+v, want step-self-report", payload["kind"])
	}
}

// TestStoryLogRequiresKind: a missing --kind is refused, not silently dropped.
func TestStoryLogRequiresKind(t *testing.T) {
	wire(t)
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "T"}), &created)

	if _, err := verb.Dispatch(context.Background(), "story-log", mustJSON(t, map[string]any{"id": created.ID})); err == nil {
		t.Error("story-log without kind should be refused")
	}
}

// TestAppendTelemetryRefusesSecretKey and RefusesSecretValue pin AC4: a data
// field that looks like a credential — by key name or by value shape — is
// refused outright and never written to the ledger.
func TestAppendTelemetryRefusesSecretKey(t *testing.T) {
	wire(t)
	err := verb.AppendTelemetry(context.Background(), "sty_x", "executor", "quality-check",
		map[string]any{"api_key": "whatever"})
	if err == nil {
		t.Fatal("a credential-ish key must be refused")
	}
}

func TestAppendTelemetryRefusesSecretValue(t *testing.T) {
	wire(t)
	err := verb.AppendTelemetry(context.Background(), "sty_x", "executor", "quality-check",
		map[string]any{"note": "sk-abcdefghijklmnopqrstuvwxyz0123456789ABCD"})
	if err == nil {
		t.Fatal("a token-shaped value must be refused")
	}
}

// TestAppendTelemetryAllowsOrdinaryData: a normal event with plain names/numbers
// is written without incident — the secret gate must not be over-broad.
func TestAppendTelemetryAllowsOrdinaryData(t *testing.T) {
	wire(t)
	err := verb.AppendTelemetry(context.Background(), "sty_x", "reviewer", "agent-retry",
		map[string]any{"skill": "satelle-story-plan-review", "attempt": 2, "attempts": 3, "outcome": "timeout"})
	if err != nil {
		t.Fatalf("ordinary telemetry data should be accepted, got: %v", err)
	}
}
