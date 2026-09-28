package ledger

import (
	"encoding/json"
	"strings"
)

// modelUnavailable mirrors agentcli.ModelUnavailable's literal (sty_87b86044).
// internal/ledger imports no other internal package, so the two constants
// cannot share a definition — keep this string in sync with agentcli's by hand.
const modelUnavailable = "unavailable"

// modelUnavailablePrefix leads an adapter-named reason such as
// "unavailable: grok acp reports no model" (agentcli.ModelUnavailableFor,
// sty_8e422d47) — a reason, never a measured id. Keep in sync by hand.
const modelUnavailablePrefix = modelUnavailable + ":"

// ModelLabel renders the display string for a row's model fields: the
// resolved canonical id when one was recorded, the configured alias marked
// unknown when only that was, or a bare "unknown" when neither was (a legacy
// row, or a transport that reported nothing at all). resolved ==
// modelUnavailable and resolved == "" render identically — the distinction
// between "explicitly reported no model" and "row predates this field" lives
// in storage, not in the label (sty_87b86044).
func ModelLabel(alias, resolved string) string {
	alias = strings.TrimSpace(alias)
	resolved = strings.TrimSpace(resolved)
	if resolved != "" && resolved != modelUnavailable && !strings.HasPrefix(resolved, modelUnavailablePrefix) {
		return resolved
	}
	if alias != "" {
		return alias + " (unknown)"
	}
	return "unknown"
}

// Telemetry is display-ready agent-action fields from a single ledger entry
// (sty_43d228e4 / sty_80233c10 / sty_56aae77a). Owned by ledger so the push-fed
// web UI and verb story-cost share one reader without the web package importing
// verb. A named struct (not positional returns) keeps the usage-availability
// flag from being transposed with a token count at call sites.
type Telemetry struct {
	Agent string
	Model string
	// ModelResolved is the canonical model id the transport actually ran
	// (sty_87b86044), read alongside Model (the configured alias) from an
	// agent_invocation OR a review_accept/review_reject row — both carry it
	// directly, with no join. Empty on a row written before this field existed
	// (a legacy row); ModelLabel renders that the same as an explicit
	// "unavailable" marker.
	ModelResolved string
	// ModelSource names why Model/ModelResolved were chosen — binding, step,
	// agent, inherited-in-loop, creator, or (an old row may say
	// inherited-orchestrator, a retired tier) order,
	// cli-default (config.SelectModel, sty_7069bced). Empty on a row written
	// before this field existed, or a functional-check row that invokes no
	// agent and so selects no model.
	ModelSource string
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
	// UsageUnavailableReason names the adapter and why usage was not reported
	// (sty_c8d45201); empty on a measured row or a row written before it existed.
	UsageUnavailableReason string
	// CacheSplitUnavailable marks a provider that reported no cache fields: the
	// zero split below is unreported, not measured (sty_c8d45201).
	CacheSplitUnavailable bool
	// TokensInFresh/TokensCacheWrite/TokensCacheRead split TokensIn into its
	// disjoint components (sty_363eaf55). Zero on a row written before this
	// field existed (an "unsplit" legacy row) or on a transport that reports
	// no cache split — indistinguishable from each other by design; the split
	// is additive evidence, never a correction of TokensIn.
	TokensInFresh    int
	TokensCacheWrite int
	TokensCacheRead  int
	// SystemPromptBytes/PayloadBytes are the byte lengths of the system prompt
	// and stdin payload satelle sent for this invocation (sty_363eaf55) —
	// lengths only, never content. Zero on a row written before this field
	// existed.
	SystemPromptBytes int
	PayloadBytes      int
	// OfferedToolCount is the number of tools the reviewer's harness offered
	// (sty_ef3efb51); nil when the adapter has no real figure — then
	// OfferedToolsSource carries its adapter-named reason. Never the grant length.
	OfferedToolCount   *int
	OfferedToolsSource string
	// IsolationLimitation is the adapter-named limitation recorded for the run.
	IsolationLimitation string
	// CostUSD is the invocation's dollar cost (sty_c4df7376), read from the
	// row's own cost_usd, or — for a legacy row recorded before that field
	// existed but carrying per-model costs — the sum of model_usage's own
	// cost_usd. Nil means no cost is known; CostUnavailableReason then names
	// the adapter and why, when the row recorded one.
	CostUSD               *float64
	CostUnavailableReason string
}

// EventTelemetry extracts Telemetry from a single ledger entry.
func EventTelemetry(e Entry) Telemetry {
	switch e.Kind {
	case KindAgentInvocation:
		if IsToolPermissionRow(e) {
			// A legacy permission row carries no usage — never fed through the
			// invocation decoder, which would otherwise render it as an
			// unmeasured "unknown model" invocation (sty_8eae81ac AC4).
			return Telemetry{}
		}
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
		return verdictTelemetry(e.Payload, "accept")
	case KindReviewReject:
		return verdictTelemetry(e.Payload, "reject")
	case KindGateSkipped:
		// Not an accept: nothing judged this edge (sty_d59ec6a9).
		return Telemetry{Outcome: "skipped"}
	}
	return Telemetry{}
}

// verdictTelemetry decodes a review_accept/review_reject payload, which
// carries model and model_resolved directly on the row (sty_87b86044, no join
// to an agent_invocation entry) — the row stands alone after ledger compaction.
// Legacy rows written before this field existed decode with both empty, which
// ModelLabel renders as unknown, never back-filled.
func verdictTelemetry(payload []byte, outcome string) Telemetry {
	tel := Telemetry{Outcome: outcome}
	if len(payload) == 0 {
		return tel
	}
	var row struct {
		Model         string `json:"model"`
		ModelResolved string `json:"model_resolved"`
		ModelSource   string `json:"model_source"`
	}
	if err := json.Unmarshal(payload, &row); err == nil {
		tel.Model = row.Model
		tel.ModelResolved = row.ModelResolved
		tel.ModelSource = row.ModelSource
	}
	return tel
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
		Agent             string   `json:"agent"`
		Model             string   `json:"model"`
		ModelResolved     string   `json:"model_resolved"`
		ModelSource       string   `json:"model_source"`
		TokensIn          int      `json:"tokens_in"`
		TokensOut         int      `json:"tokens_out"`
		TokensTotal       int      `json:"tokens_total"`
		DurationMs        int64    `json:"duration_ms"`
		UsageAvailable    *bool    `json:"usage_available"`
		UnavailableReason string   `json:"usage_unavailable_reason"`
		SplitUnavailable  bool     `json:"cache_split_unavailable"`
		TokensInFresh     int      `json:"tokens_in_fresh"`
		TokensCacheWrite  int      `json:"tokens_cache_write"`
		TokensCacheRead   int      `json:"tokens_cache_read"`
		SystemPromptBytes int      `json:"system_prompt_bytes"`
		PayloadBytes      int      `json:"payload_bytes"`
		OfferedToolCount  *int     `json:"offered_tool_count"`
		OfferedSource     string   `json:"offered_tools_source"`
		IsoLimitation     string   `json:"isolation_limitation"`
		CostUSD           *float64 `json:"cost_usd"`
		CostUnavailable   string   `json:"cost_unavailable_reason"`
		// ModelUsage is read only for its per-model cost_usd — a legacy row's
		// fallback when it predates the top-level cost_usd field (sty_c4df7376).
		ModelUsage []struct {
			CostUSD *float64 `json:"cost_usd"`
		} `json:"model_usage"`
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
	cost, costReason := row.CostUSD, row.CostUnavailable
	if cost == nil {
		if c := sumModelCost(row.ModelUsage); c != nil {
			cost = c
			costReason = ""
		}
	}
	return Telemetry{
		Agent:                  row.Agent,
		Model:                  row.Model,
		ModelResolved:          row.ModelResolved,
		ModelSource:            row.ModelSource,
		TokensIn:               row.TokensIn,
		TokensOut:              row.TokensOut,
		TokensTotal:            row.TokensTotal,
		DurationMs:             row.DurationMs,
		UsageAvailable:         avail,
		UsageUnavailableReason: row.UnavailableReason,
		CacheSplitUnavailable:  row.SplitUnavailable,
		TokensInFresh:          row.TokensInFresh,
		TokensCacheWrite:       row.TokensCacheWrite,
		TokensCacheRead:        row.TokensCacheRead,
		SystemPromptBytes:      row.SystemPromptBytes,
		PayloadBytes:           row.PayloadBytes,
		OfferedToolCount:       row.OfferedToolCount,
		OfferedToolsSource:     row.OfferedSource,
		IsolationLimitation:    row.IsoLimitation,
		CostUSD:                cost,
		CostUnavailableReason:  costReason,
	}
}

// sumModelCost sums a legacy row's per-model cost_usd entries, nil when none
// reported one — never a measured zero from an unpriced model list.
func sumModelCost(models []struct {
	CostUSD *float64 `json:"cost_usd"`
}) *float64 {
	var sum float64
	found := false
	for _, m := range models {
		if m.CostUSD != nil {
			sum += *m.CostUSD
			found = true
		}
	}
	if !found {
		return nil
	}
	return &sum
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
