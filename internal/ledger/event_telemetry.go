package ledger

import "encoding/json"

// EventTelemetry extracts display-ready agent-action fields from a single ledger
// entry (sty_43d228e4 / sty_80233c10). Owned by ledger so both the push-fed web
// UI and verb.story cost share one reader without the web package importing verb.
func EventTelemetry(e Entry) (agent, model, outcome string, tokensTotal int, durationMs int64) {
	switch e.Kind {
	case KindAgentInvocation:
		var row struct {
			Agent       string `json:"agent"`
			Model       string `json:"model"`
			TokensTotal int    `json:"tokens_total"`
			DurationMs  int64  `json:"duration_ms"`
		}
		if len(e.Payload) > 0 && json.Unmarshal(e.Payload, &row) == nil {
			return row.Agent, row.Model, "", row.TokensTotal, row.DurationMs
		}
	case KindTelemetryEvent:
		var env struct {
			Kind string         `json:"kind"`
			Data map[string]any `json:"data"`
		}
		if len(e.Payload) > 0 && json.Unmarshal(e.Payload, &env) == nil {
			oc, _ := env.Data["outcome"].(string)
			if oc == "" {
				oc = env.Kind
			}
			return "", "", oc, int(mapNum(env.Data, "tokens_total")), mapNum(env.Data, "duration_ms")
		}
	case KindReviewAccept:
		return "", "", "accept", 0, 0
	case KindReviewReject:
		return "", "", "reject", 0, 0
	}
	return "", "", "", 0, 0
}

func mapNum(m map[string]any, k string) int64 {
	if m == nil {
		return 0
	}
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}
