package agentcli

import "testing"

// TestUsageFromMapCacheTokens pins sty_8178f1c6 AC2: the ACP/stream path folds
// cache fields into InputTokens on the same rule as UnwrapUsage, and a map with
// no usage key stays unreported (nil) rather than partially measured.
func TestUsageFromMapCacheTokens(t *testing.T) {
	cases := []struct {
		name string
		v    map[string]any
		// nil means usageFromMap must return nil (unreported).
		want *UsageResult
	}{
		{
			name: "cache keys present → summed input",
			v: map[string]any{
				"usage": map[string]any{
					"input_tokens":                float64(22),
					"cache_creation_input_tokens": float64(11000),
					"cache_read_input_tokens":     float64(2500),
					"output_tokens":               float64(13394),
				},
			},
			want: &UsageResult{InputTokens: 13522, OutputTokens: 13394, TotalTokens: 26916, Available: true},
		},
		{
			name: "two-field only → unchanged behaviour",
			v: map[string]any{
				"usage": map[string]any{
					"input_tokens":  float64(54),
					"output_tokens": float64(59),
				},
			},
			want: &UsageResult{InputTokens: 54, OutputTokens: 59, TotalTokens: 113, Available: true},
		},
		{
			name: "reported total smaller than derived → derive (pre-cache total cannot re-understate)",
			v: map[string]any{
				"usage": map[string]any{
					"input_tokens":                float64(22),
					"cache_creation_input_tokens": float64(100),
					"output_tokens":               float64(10),
					"total_tokens":                float64(32), // pre-cache total (22+10)
				},
			},
			want: &UsageResult{InputTokens: 122, OutputTokens: 10, TotalTokens: 132, Available: true},
		},
		{
			name: "reported total >= derived → trust reported",
			v: map[string]any{
				"usage": map[string]any{
					"input_tokens":  float64(10),
					"output_tokens": float64(20),
					"total_tokens":  float64(50),
				},
			},
			want: &UsageResult{InputTokens: 10, OutputTokens: 20, TotalTokens: 50, Available: true},
		},
		{
			name: "no usage key → nil (unreported, not partially measured)",
			v:    map[string]any{"type": "result"},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := usageFromMap(tc.v)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("usageFromMap = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("usageFromMap = nil, want non-nil")
			}
			if got.InputTokens != tc.want.InputTokens ||
				got.OutputTokens != tc.want.OutputTokens ||
				got.TotalTokens != tc.want.TotalTokens ||
				got.Available != tc.want.Available {
				t.Errorf("usageFromMap = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestUsageFromMapCacheSplit pins sty_363eaf55 AC1: the stream-json path keeps
// fresh/cache-creation/cache-read individually readable, not just summed into
// InputTokens. It runs a real Claude stream-json "result" line — the actual
// JSONL bytes a `claude ... --output-format stream-json` process writes to
// stdout — through commandAdapter.Adapt, the same decode path Run() uses, so
// the assertion covers json.Unmarshal-into-map[string]any and the "result"
// case in adaptJSONEvent, not just usageFromMap called on a hand-built map.
func TestUsageFromMapCacheSplit(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","is_error":false,"duration_ms":4521,` +
		`"result":"the verdict text","usage":{"input_tokens":22,"cache_creation_input_tokens":11000,` +
		`"cache_read_input_tokens":2500,"output_tokens":13394}}`)

	evs := commandAdapter{}.Adapt(line, false)
	if len(evs) != 1 || evs[0].Kind != EventUsage || evs[0].Usage == nil {
		t.Fatalf("Adapt(result line) = %+v, want one EventUsage with non-nil Usage", evs)
	}
	got := evs[0].Usage
	if got.FreshInputTokens != 22 || got.CacheCreationInputTokens != 11000 || got.CacheReadInputTokens != 2500 {
		t.Errorf("cache split = fresh=%d create=%d read=%d, want 22/11000/2500",
			got.FreshInputTokens, got.CacheCreationInputTokens, got.CacheReadInputTokens)
	}
	if got.InputTokens != 13522 || got.OutputTokens != 13394 || !got.Available {
		t.Errorf("usage = %+v, want InputTokens=13522 OutputTokens=13394 Available=true", got)
	}
}

// TestUsageFromMapModelUsage pins AC2: a stream-json result event whose
// top-level modelUsage is keyed by canonical id stores both the alias
// (recorded by the caller) and the resolved id, alongside the token fields.
func TestUsageFromMapModelUsage(t *testing.T) {
	v := map[string]any{
		"usage": map[string]any{
			"input_tokens":  float64(2),
			"output_tokens": float64(11),
		},
		"modelUsage": map[string]any{
			"claude-opus-5-5": map[string]any{
				"inputTokens": float64(2), "outputTokens": float64(11), "costUSD": 0.1065628,
			},
		},
	}
	got := usageFromMap(v)
	if got == nil {
		t.Fatal("usageFromMap = nil, want non-nil")
	}
	if got.ModelResolved != "claude-opus-5-5" {
		t.Errorf("ModelResolved = %q, want claude-opus-5-5", got.ModelResolved)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "claude-opus-5-5" {
		t.Errorf("Models = %+v, want one claude-opus-5-5 entry", got.Models)
	}
}
