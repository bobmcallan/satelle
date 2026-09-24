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
//   - codex exec --json: a `turn.completed` line carries usage {input_tokens,
//     cached_input_tokens, output_tokens, reasoning_output_tokens}; input_tokens
//     INCLUDES cached_input_tokens (OpenAI semantics). OpenAI prompt caching
//     has no cache-write concept (nothing is billed as a write), so the write
//     share is a true zero within an otherwise-available split, not a default.

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
	return u
}

// codexUsageFromMap maps a codex `turn.completed` usage object. input_tokens
// includes cached_input_tokens; codex has no cache-write field.
func codexUsageFromMap(raw map[string]any) *UsageResult {
	if !hasAnyKey(raw, "input_tokens", "output_tokens", "cached_input_tokens", "total_tokens") {
		u := noTokenFields("codex")
		return &u
	}
	in := intValue(raw["input_tokens"])
	out := intValue(raw["output_tokens"])
	u := &UsageResult{
		InputTokens:  in,
		OutputTokens: out,
		Available:    true,
	}
	if cached, ok := raw["cached_input_tokens"]; ok {
		read := intValue(cached)
		u.CacheSplitAvailable = true
		u.CacheReadInputTokens = read
		u.FreshInputTokens = max(in-read, 0)
	}
	u.TotalTokens = trustedTotal(intValue(raw["total_tokens"]), in+out)
	return u
}

// usageForShape picks the provider mapper from the usage object's own field
// names: camelCase inputTokens is grok's spelling, cached_input_tokens (or a
// turn.completed carrier) is codex's, anything else is Anthropic-shaped.
func usageForShape(raw map[string]any) *UsageResult {
	if _, ok := raw["inputTokens"]; ok {
		return grokUsageFromMap(raw)
	}
	if _, ok := raw["cached_input_tokens"]; ok {
		return codexUsageFromMap(raw)
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
// and why. Model stays unavailable-marked by the caller when appropriate.
func unavailableUsage(adapter, why string) UsageResult {
	return UsageResult{UnavailableReason: fmt.Sprintf("%s adapter: %s", adapter, why)}
}

// jsonlUsage scans a JSONL stdout (codex exec --json, grok streaming-json,
// claude stream-json) for usage lines. Codex turn.completed usages are
// per-turn and summed; any other usage line (a result event) is a final
// figure, so the last one wins. ok is false when no line carried usage.
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
		if typ != "turn.completed" && typ != "result" && typ != "usage" && typ != "end" {
			continue
		}
		got := usageFromMap(v)
		if got == nil || !got.Available {
			continue
		}
		if typ == "turn.completed" && ok {
			u = addUsage(u, *got)
		} else {
			u = *got
		}
		ok = true
	}
	return u, ok
}

// addUsage sums two per-turn usages. The split stays available only while
// both halves report it.
func addUsage(a, b UsageResult) UsageResult {
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.TotalTokens += b.TotalTokens
	a.FreshInputTokens += b.FreshInputTokens
	a.CacheCreationInputTokens += b.CacheCreationInputTokens
	a.CacheReadInputTokens += b.CacheReadInputTokens
	a.CacheSplitAvailable = a.CacheSplitAvailable && b.CacheSplitAvailable
	return a
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
