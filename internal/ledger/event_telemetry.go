package ledger

import "encoding/json"

// Telemetry is display-ready agent-action fields from a single ledger entry
// (sty_43d228e4 / sty_80233c10 / sty_56aae77a). Owned by ledger so the push-fed
// web UI and verb story-cost share one reader without the web package importing
// verb. A named struct (not positional returns) keeps the usage-availability
// flag from being transposed with a token count at call sites.
type Telemetry struct {
	Agent       string
	Model       string
	Outcome     string
	TokensIn    int
	TokensOut   int
	TokensTotal int
	DurationMs  int64
	// UsageAvailable is true when the row carries a measured token cost —
	// either an explicit usage_available:true on a new agent_invocation, or
	// (legacy) tokens_total > 0 when the field is absent. False means
	// unreported, never "measured zero".
	UsageAvailable bool
}

// EventTelemetry extracts Telemetry from a single ledger entry.
func EventTelemetry(e Entry) Telemetry {
	switch e.Kind {
	case KindAgentInvocation:
		return invocationTelemetry(e.Payload)
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
			total := int(mapNum(env.Data, "tokens_total"))
			avail := false
			if v, ok := env.Data["usage_available"].(bool); ok {
				avail = v
			} else if total > 0 {
				// Same legacy inference as agent_invocation: historical numbers
				// were measured; a bare zero is unknown.
				avail = true
			}
			return Telemetry{
				Outcome:        oc,
				TokensTotal:    total,
				DurationMs:     mapNum(env.Data, "duration_ms"),
				UsageAvailable: avail,
			}
		}
	case KindReviewAccept:
		return Telemetry{Outcome: "accept"}
	case KindReviewReject:
		return Telemetry{Outcome: "reject"}
	case KindGateSkipped:
		// Not an accept: nothing judged this edge (sty_d59ec6a9).
		return Telemetry{Outcome: "skipped"}
	}
	return Telemetry{}
}

// invocationTelemetry decodes an agent_invocation payload. Sole owner of the
// usage_available / legacy inference rule (sty_56aae77a): explicit boolean wins;
// absent field → tokens_total > 0 means measured (historical non-zero rows were
// real; historical 0/0 becomes unreported — the safe direction).
func invocationTelemetry(payload []byte) Telemetry {
	if len(payload) == 0 {
		return Telemetry{}
	}
	var row struct {
		Agent          string `json:"agent"`
		Model          string `json:"model"`
		TokensIn       int    `json:"tokens_in"`
		TokensOut      int    `json:"tokens_out"`
		TokensTotal    int    `json:"tokens_total"`
		DurationMs     int64  `json:"duration_ms"`
		UsageAvailable *bool  `json:"usage_available"`
	}
	if err := json.Unmarshal(payload, &row); err != nil {
		return Telemetry{}
	}
	avail := false
	if row.UsageAvailable != nil {
		avail = *row.UsageAvailable
	} else if row.TokensTotal > 0 {
		avail = true
	}
	return Telemetry{
		Agent:          row.Agent,
		Model:          row.Model,
		TokensIn:       row.TokensIn,
		TokensOut:      row.TokensOut,
		TokensTotal:    row.TokensTotal,
		DurationMs:     row.DurationMs,
		UsageAvailable: avail,
	}
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
