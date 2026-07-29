package agentcli

import (
	"bytes"
	"encoding/json"
	"strings"
)

// commandAdapter maps known JSONL transport shapes into the common event model.
// Unknown and malformed lines degrade to safe generic messages.
type commandAdapter struct{}

func (commandAdapter) Adapt(line []byte, stderr bool) []Event {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || isHiddenReasoningLine(trimmed) {
		return nil
	}
	var v map[string]any
	if json.Unmarshal(trimmed, &v) != nil {
		ev := newEvent(EventMessage)
		ev.Text = string(trimmed)
		if stderr {
			ev.Meta = map[string]string{"stream": "stderr"}
		}
		return []Event{ev}
	}
	if evs := adaptJSONEvent(v); len(evs) > 0 {
		return evs
	}
	// A JSON object that is not a known progressive shape may be the final
	// provider envelope. Do not print the whole object (it can contain payloads
	// or settings); completion/usage remain owned by the runner.
	return nil
}

func adaptJSONEvent(v map[string]any) []Event {
	typ := lowerString(v, "type")
	switch typ {
	case "assistant":
		return messageEvents(v)
	case "content_block_delta":
		if delta, ok := v["delta"].(map[string]any); ok {
			if strings.Contains(lowerString(delta, "type"), "thinking") {
				return nil
			}
			if text := stringValue(delta["text"]); text != "" {
				ev := newEvent(EventMessage)
				ev.Text = text
				return []Event{ev}
			}
		}
	case "content_block_start":
		block, _ := v["content_block"].(map[string]any)
		if lowerString(block, "type") == "tool_use" {
			ev := newEvent(EventToolStart)
			ev.Tool = firstString(block, "name", "id")
			ev.Status = "running"
			return []Event{ev}
		}
	case "content_block_stop":
		ev := newEvent(EventToolEnd)
		ev.Status = "completed"
		return []Event{ev}
	case "result":
		if usage := usageFromMap(v); usage != nil {
			ev := newEvent(EventUsage)
			ev.Usage = usage
			return []Event{ev}
		}
	case "item.started", "item_started":
		ev := newEvent(EventToolStart)
		ev.Tool = itemLabel(v)
		ev.Status = "running"
		return []Event{ev}
	case "item.completed", "item_completed":
		if text := nestedString(v, "item", "text"); text != "" {
			ev := newEvent(EventMessage)
			ev.Text = text
			return []Event{ev}
		}
		ev := newEvent(EventToolEnd)
		ev.Tool = itemLabel(v)
		ev.Status = "completed"
		return []Event{ev}
	case "message", "assistant_message", "status":
		return messageEvents(v)
	}

	// Codex and compatible peers sometimes put the event name under event/type
	// and the record under item.
	eventName := lowerString(v, "event")
	if strings.Contains(eventName, "tool") {
		kind := EventToolStart
		status := lowerString(v, "status")
		if status == "completed" || status == "failed" || strings.Contains(eventName, "completed") {
			kind = EventToolEnd
		}
		ev := newEvent(kind)
		ev.Tool = firstString(v, "tool", "name", "title")
		ev.Status = status
		return []Event{ev}
	}
	return nil
}

func messageEvents(v map[string]any) []Event {
	for _, key := range []string{"text", "message", "content", "delta"} {
		if text := textValue(v[key]); text != "" {
			ev := newEvent(EventMessage)
			ev.Text = text
			return []Event{ev}
		}
	}
	return nil
}

func textValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		if strings.Contains(lowerString(x, "type"), "thinking") {
			return ""
		}
		for _, key := range []string{"text", "content", "message"} {
			if s := textValue(x[key]); s != "" {
				return s
			}
		}
	case []any:
		var parts []string
		for _, child := range x {
			if s := textValue(child); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

func usageFromMap(v map[string]any) *UsageResult {
	raw, _ := v["usage"].(map[string]any)
	if raw == nil {
		return nil
	}
	u := &UsageResult{
		InputTokens:  intValue(raw["input_tokens"]),
		OutputTokens: intValue(raw["output_tokens"]),
	}
	u.TotalTokens = intValue(raw["total_tokens"])
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	if u.TotalTokens == 0 {
		return nil
	}
	return u
}

func itemLabel(v map[string]any) string {
	if item, ok := v["item"].(map[string]any); ok {
		return firstString(item, "name", "type", "id")
	}
	return firstString(v, "name", "tool", "id")
}

func lowerString(v map[string]any, key string) string {
	return strings.ToLower(stringValue(v[key]))
}

func firstString(v map[string]any, keys ...string) string {
	for _, key := range keys {
		if s := stringValue(v[key]); s != "" {
			return s
		}
	}
	return ""
}

func nestedString(v map[string]any, parent, child string) string {
	m, _ := v[parent].(map[string]any)
	return stringValue(m[child])
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func intValue(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case json.Number:
		n, _ := x.Int64()
		return int(n)
	case int:
		return x
	default:
		return 0
	}
}
