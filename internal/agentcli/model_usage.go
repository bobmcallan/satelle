package agentcli

import (
	"encoding/json"
	"sort"
)

// ModelUnavailable marks an invocation whose transport reported no resolved
// model (ACP, a plain-text harness, or a JSON envelope without a modelUsage
// map) — an explicit marker distinct from an empty field, which would read as
// the configured alias (sty_87b86044). The same literal is recorded in
// ledger.ModelLabel; internal/ledger imports no other internal package, so the
// two constants cannot share a definition — keep them in sync by hand.
const ModelUnavailable = "unavailable"

// ModelUsage is one model's token/cost entry from a transport's modelUsage
// map (e.g. `claude -p --output-format json`, which keys usage by canonical
// model id when several models ran in one invocation — a background Haiku
// beside the main model).
type ModelUsage struct {
	ID           string
	InputTokens  int
	OutputTokens int
	// CacheCreationInputTokens/CacheReadInputTokens are the cache components of
	// this model's InputTokens, when the transport reported them per-model
	// (sty_363eaf55). Zero when unreported.
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	// CostUSD is nil when the transport did not report a per-model cost.
	CostUSD *float64
}

// parseModelUsage decodes a `modelUsage` value — a map[string]any (already
// decoded from a stream event) or JSON bytes (a claudeJSONEnvelope field) —
// into a deterministic, id-sorted []ModelUsage plus the primary entry's id.
// The primary is the entry with the highest OutputTokens; a tie goes to the
// smallest id. Entries are sorted by id BEFORE picking the primary, so Go's
// randomized map iteration order can never affect the result.
func parseModelUsage(v any) (primary string, models []ModelUsage, reported bool) {
	var m map[string]any
	switch x := v.(type) {
	case map[string]any:
		m = x
	case json.RawMessage:
		if len(x) == 0 {
			return "", nil, false
		}
		if err := json.Unmarshal(x, &m); err != nil {
			return "", nil, false
		}
	case []byte:
		if len(x) == 0 {
			return "", nil, false
		}
		if err := json.Unmarshal(x, &m); err != nil {
			return "", nil, false
		}
	default:
		return "", nil, false
	}
	if len(m) == 0 {
		return "", nil, false
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	models = make([]ModelUsage, 0, len(ids))
	for _, id := range ids {
		entry, _ := m[id].(map[string]any)
		mu := ModelUsage{ID: id}
		if entry != nil {
			mu.InputTokens = intValue(entry["inputTokens"])
			mu.OutputTokens = intValue(entry["outputTokens"])
			mu.CacheCreationInputTokens = intValue(entry["cacheCreationInputTokens"])
			mu.CacheReadInputTokens = intValue(entry["cacheReadInputTokens"])
			if c, ok := entry["costUSD"].(float64); ok {
				cost := c
				mu.CostUSD = &cost
			}
		}
		models = append(models, mu)
	}
	return pickPrimary(models), models, true
}

// pickPrimary returns the id of the entry with the highest OutputTokens.
// models must already be sorted by id ascending — a tie then resolves to the
// smallest id by construction (the first entry seen at the max wins).
func pickPrimary(models []ModelUsage) string {
	if len(models) == 0 {
		return ""
	}
	best := models[0]
	for _, mu := range models[1:] {
		if mu.OutputTokens > best.OutputTokens {
			best = mu
		}
	}
	return best.ID
}
