package agentartifact

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Change is one story-configuration change a read-only performer reports in its
// final response for satelle to apply on the record (sty_af0b1761). The set of
// operations and the tag prefixes allowed are the route step's declaration; this
// package only decodes the report. Title, Body, Acceptance and Remove are
// decoded so the applier can refuse them by name rather than as unknown fields.
type Change struct {
	Op         string   `json:"op"`
	Story      string   `json:"story,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Title      string   `json:"title,omitempty"`
	Body       string   `json:"body,omitempty"`
	Acceptance string   `json:"acceptance,omitempty"`
	Category   string   `json:"category,omitempty"`
	Remove     []string `json:"remove,omitempty"`
}

// reportKeys are the top-level keys an object may carry and still be a
// performer's response: the artifact envelope, a verdict line, and the changes
// report. An object with any other key is some other JSON the response quoted.
var reportKeys = map[string]bool{
	"artifact": true, "decision": true, "notes": true, "reasoning": true,
	"reviewed": true, "reviewed_truncated": true, "changes": true,
}

// DecodeChanges extracts the `changes` list from a final agent response: the
// last object carrying a `changes` array whose keys are all report keys, so it
// rides beside the artifact envelope or a verdict line, or stands alone.
// reported is false when the response carries no such object. An unknown field
// inside a change, or a change that is not an object, is an error: a malformed
// report is never silently dropped. Provider-neutral: every adapter hands its
// final text to this seam.
func DecodeChanges(out []byte) (changes []Change, reported bool, err error) {
	var raw json.RawMessage
	for _, obj := range jsonObjects(out) {
		var envelope map[string]json.RawMessage
		if json.Unmarshal(obj, &envelope) != nil {
			continue
		}
		list, ok := envelope["changes"]
		if !ok || !isReport(envelope) {
			continue
		}
		if t := bytes.TrimSpace(list); len(t) == 0 || t[0] != '[' {
			continue
		}
		raw, reported = list, true
	}
	if !reported {
		return nil, false, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, true, fmt.Errorf("changes: expected a list: %w", err)
	}
	for i, item := range items {
		dec := json.NewDecoder(bytes.NewReader(item))
		dec.DisallowUnknownFields()
		var c Change
		if err := dec.Decode(&c); err != nil {
			return nil, true, fmt.Errorf("changes[%d]: %w", i, err)
		}
		changes = append(changes, c)
	}
	return changes, true, nil
}

func isReport(envelope map[string]json.RawMessage) bool {
	for k := range envelope {
		if !reportKeys[k] {
			return false
		}
	}
	return true
}
