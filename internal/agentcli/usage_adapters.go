package agentcli

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Provider-specific usage mapping (sty_c8d45201). Each function below owns ONE
// provider's field names and input semantics and maps them into the shared
// UsageResult; nothing outside this file knows a provider's wire names. A
// provider or transport that reports no usage yields Available=false with an
// adapter-named UnavailableReason — never zeros that look like measurements.
//
// Findings (what each CLI reports, per output mode):
//
//   - claude -p --output-format json | stream-json: `usage` with snake_case
//     input_tokens / output_tokens / cache_creation_input_tokens /
//     cache_read_input_tokens, DISJOINT components of one prompt.
//   - grok -p --output-format json: envelope {text, usage, modelUsage}, `usage`
//     snake_case with DISJOINT counts like Anthropic's (6435 + 512 + 24 =
//     6971 total): input_tokens / output_tokens / cache_read_input_tokens /
//     cache_creation_input_tokens / reasoning_tokens / total_tokens. Mapped by
//     claudeUsageFromMap. modelUsage is keyed by resolved model id.
//   - grok -p --output-format streaming-json: NDJSON with a {"type":"usage"}
//     line and a final {"type":"end"} line carrying the same snake_case usage
//     (+ modelUsage); mapped the same way, the last one wins.
//   - grok agent (ACP): session/prompt result._meta.usage is camelCase
//     (inputTokens / outputTokens / cachedReadTokens / cacheCreationTokens /
//     totalTokens) and inputTokens INCLUDES cachedReadTokens. A response with
//     no _meta.usage records unavailable.

// claudeUsageFromMap maps an Anthropic-shaped `usage` object. Cache fields fold
// into InputTokens on the UnwrapUsage rule (sty_8178f1c6): input + creation +
// read. A provider-reported total_tokens is trusted only when it is >= the
// derived in+out sum.
func claudeUsageFromMap(raw map[string]any, adapter string) *UsageResult {
	if !hasAnyKey(raw, "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "total_tokens") {
		u := noTokenFields(adapter)
		return &u
	}
	fresh := intValue(raw["input_tokens"])
	cacheCreate := intValue(raw["cache_creation_input_tokens"])
	cacheRead := intValue(raw["cache_read_input_tokens"])
	in := fresh + cacheCreate + cacheRead
	out := intValue(raw["output_tokens"])
	u := &UsageResult{
		InputTokens:              in,
		FreshInputTokens:         fresh,
		CacheCreationInputTokens: cacheCreate,
		CacheReadInputTokens:     cacheRead,
		OutputTokens:             out,
		Available:                true,
		CacheSplitAvailable:      true,
	}
	u.TotalTokens = trustedTotal(intValue(raw["total_tokens"]), in+out)
	return u
}

// grokUSDPerTick is grok's costUsdTicks unit: 1 tick = 1e-10 USD. Confirmed
// against the real captures — grok_acp.jsonl's costUsdTicks:94540400 and
// grok_json.json's total_cost_usd_ticks:44769840 both match their sibling
// dollar figure (0.0094540400 / 0.004476984) at this factor. This is the
// provider's own reporting unit, not a price satelle assigns.
const grokUSDPerTick = 1e-10

// grokUsageFromMap maps grok's camelCase usage, which only the ACP transport
// uses (session/prompt result._meta.usage, turn_completed). inputTokens
// includes the cached share (19663 = 11855 fresh + 7808 read), so
// Fresh = input - read - write. The cache split is available only when grok
// names at least one cache field. Headless json / streaming-json use
// snake_case with DISJOINT counts and go through claudeUsageFromMap.
func grokUsageFromMap(raw map[string]any) *UsageResult {
	if !hasAnyKey(raw, "inputTokens", "outputTokens", "cachedReadTokens", "cacheCreationTokens", "totalTokens") {
		u := noTokenFields("acp")
		return &u
	}
	in := intValue(raw["inputTokens"])
	out := intValue(raw["outputTokens"])
	read := intValue(raw["cachedReadTokens"])
	write := intValue(raw["cacheCreationTokens"])
	_, hasRead := raw["cachedReadTokens"]
	_, hasWrite := raw["cacheCreationTokens"]
	u := &UsageResult{
		InputTokens:  in,
		OutputTokens: out,
		Available:    true,
	}
	if hasRead || hasWrite {
		u.CacheSplitAvailable = true
		u.CacheReadInputTokens = read
		u.CacheCreationInputTokens = write
		u.FreshInputTokens = max(in-read-write, 0)
	}
	u.TotalTokens = trustedTotal(intValue(raw["totalTokens"]), in+out)
	if ticks, ok := raw["costUsdTicks"]; ok {
		cost := floatValue(ticks) * grokUSDPerTick
		u.CostUSD = &cost
	} else {
		u.CostUnavailableReason = "acp: _meta.usage carries no costUsdTicks"
	}
	return u
}

// usageForShape picks the provider mapper from the usage object's own field
// names: camelCase inputTokens is grok's spelling, anything else is
// Anthropic-shaped.
func usageForShape(raw map[string]any) *UsageResult {
	if _, ok := raw["inputTokens"]; ok {
		return grokUsageFromMap(raw)
	}
	return claudeUsageFromMap(raw, "claude")
}

// noTokenFields is the explicit unavailable for a `usage` object that carries
// none of the provider's token fields — an empty object is not a measurement.
func noTokenFields(adapter string) UsageResult {
	return unavailableUsage(adapter, "usage object carried none of the provider's token fields")
}

func hasAnyKey(m map[string]any, keys ...string) bool {
	_, ok := firstPresent(m, keys...)
	return ok
}

// unavailableUsage builds the explicit "no usage" result, naming the adapter
// and why. Model stays unavailable-marked by the caller when appropriate. No
// tokens means no cost either, so CostUnavailableReason mirrors the same why.
func unavailableUsage(adapter, why string) UsageResult {
	reason := fmt.Sprintf("%s adapter: %s", adapter, why)
	return UsageResult{UnavailableReason: reason, CostUnavailableReason: reason}
}

// jsonlUsage scans a JSONL stdout (grok streaming-json, claude stream-json) for
// usage lines. A usage line (a result event) is a final figure, so the last one
// wins. ok is false when no line carried usage.
func jsonlUsage(stdout []byte) (u UsageResult, ok bool) {
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var v map[string]any
		if json.Unmarshal(line, &v) != nil {
			continue
		}
		typ := lowerString(v, "type")
		if typ != "result" && typ != "usage" && typ != "end" {
			continue
		}
		got := usageFromMap(v)
		if got == nil || !got.Available {
			continue
		}
		u = *got
		ok = true
	}
	return u, ok
}

func trustedTotal(reported, derived int) int {
	if reported >= derived && reported > 0 {
		return reported
	}
	return derived
}

func firstPresent(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v, true
		}
	}
	return nil, false
}

func firstInt(m map[string]any, keys ...string) int {
	v, _ := firstPresent(m, keys...)
	return intValue(v)
}
