package ledger

import "encoding/json"

// IsToolPermissionRow reports whether e is a tool-permission decision — the
// current KindToolPermission kind, or a legacy KindAgentInvocation row written
// before that kind existed, carrying a tool/decision/decided_by payload and no
// usage fields (sty_8eae81ac AC4). This is the single owner every
// KindAgentInvocation walk consults so a permission event never inflates a
// cost total or an unmeasured-row count: verb.ComputeStoryCost,
// verb.ComputeSkillRollup, verb.ComputeStoryActual, EventTelemetry (below) and
// the web timeline's model chip.
func IsToolPermissionRow(e Entry) bool {
	if e.Kind == KindToolPermission {
		return true
	}
	if e.Kind != KindAgentInvocation || len(e.Payload) == 0 {
		return false
	}
	var row struct {
		DecidedBy      string   `json:"decided_by"`
		Decision       string   `json:"decision"`
		Tool           string   `json:"tool"`
		TokensTotal    int      `json:"tokens_total"`
		UsageAvailable *bool    `json:"usage_available"`
		CostUSD        *float64 `json:"cost_usd"`
	}
	if json.Unmarshal(e.Payload, &row) != nil {
		return false
	}
	if row.DecidedBy == "" || row.Decision == "" || row.Tool == "" {
		return false
	}
	return row.TokensTotal == 0 && row.UsageAvailable == nil && row.CostUSD == nil
}
