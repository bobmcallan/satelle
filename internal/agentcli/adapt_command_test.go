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
