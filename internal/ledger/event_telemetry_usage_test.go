package ledger

import "testing"

// TestEventTelemetry_AdapterAndModelUsage pins that the sole usage decoder
// carries the row's adapter and per-model token split, so costview prices from
// them without parsing the payload itself (sty_6267abb7 AC10).
func TestEventTelemetry_AdapterAndModelUsage(t *testing.T) {
	payload := []byte(`{"agent":"reviewer","adapter":"claude","model":"opus","usage_available":true,
		"tokens_in":300,"tokens_out":50,"tokens_in_fresh":100,"tokens_cache_read":150,"tokens_cache_write":50,
		"model_usage":[
			{"id":"claude-opus-5-5","tokens_in":250,"tokens_out":40,"tokens_cache_read":150,"tokens_cache_write":50,"cost_usd":0.5},
			{"id":"claude-haiku-5-5","tokens_in":50,"tokens_out":10}]}`)
	tel := EventTelemetry(Entry{Kind: KindAgentInvocation, Payload: payload})
	if tel.Adapter != "claude" {
		t.Errorf("Adapter = %q, want claude", tel.Adapter)
	}
	want := []ModelTokens{
		{ID: "claude-opus-5-5", TokensIn: 250, TokensOut: 40, TokensCacheRead: 150, TokensCacheWrite: 50},
		{ID: "claude-haiku-5-5", TokensIn: 50, TokensOut: 10},
	}
	if len(tel.ModelUsage) != len(want) {
		t.Fatalf("ModelUsage = %+v, want %+v", tel.ModelUsage, want)
	}
	for i := range want {
		if tel.ModelUsage[i] != want[i] {
			t.Errorf("ModelUsage[%d] = %+v, want %+v", i, tel.ModelUsage[i], want[i])
		}
	}
	if tel.CostUSD == nil || *tel.CostUSD != 0.5 {
		t.Errorf("legacy per-model cost fallback lost: %v", tel.CostUSD)
	}
}
