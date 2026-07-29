package agentcli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// EventKind is a provider-neutral isolated-agent execution event. Events are an
// observability side channel only: the byte slice returned by Runner.Run remains
// the authoritative final response.
type EventKind string

const (
	EventStart             EventKind = "start"
	EventHeartbeat         EventKind = "heartbeat"
	EventMessage           EventKind = "message"
	EventToolStart         EventKind = "tool_start"
	EventToolEnd           EventKind = "tool_end"
	EventArtifactCandidate EventKind = "artifact_candidate"
	EventUsage             EventKind = "usage"
	EventCompleted         EventKind = "completed"
	EventFailed            EventKind = "failed"
)

// Event is the normalized execution record emitted by command and ACP runners.
// Text and Error are sanitized and bounded before emission. Metadata must contain
// only non-sensitive transport labels, never prompts, payloads, settings, or env.
type Event struct {
	Kind   EventKind         `json:"kind"`
	At     time.Time         `json:"at"`
	Text   string            `json:"text,omitempty"`
	Tool   string            `json:"tool,omitempty"`
	Status string            `json:"status,omitempty"`
	Usage  *UsageResult      `json:"usage,omitempty"`
	Error  string            `json:"error,omitempty"`
	Meta   map[string]string `json:"meta,omitempty"`
}

// EventHandler receives normalized events as they happen. Implementations may
// be called from concurrent stdout/stderr reader goroutines.
type EventHandler func(Event)

var (
	ansiPattern   = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	secretPattern = regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|password|authorization)\b\s*[:=]\s*("[^"]+"|'[^']+'|[^\s,;}]+)`)
	bearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
)

// SafeText makes provider prose suitable for progress and normalized logs.
func SafeText(s string) string {
	s = ansiPattern.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	s = RedactSecrets(s)
	const maxRunes = 240
	r := []rune(s)
	if len(r) > maxRunes {
		s = string(r[:maxRunes]) + "…"
	}
	return s
}

// RedactSecrets applies best-effort protection to explicitly enabled raw traces.
func RedactSecrets(s string) string {
	s = secretPattern.ReplaceAllString(s, "$1=[REDACTED]")
	return bearerPattern.ReplaceAllString(s, "Bearer [REDACTED]")
}

// FormatEvent renders one readable normalized diagnostic line.
func FormatEvent(ev Event) string {
	at := ev.At
	if at.IsZero() {
		at = time.Now()
	}
	var details []string
	if ev.Tool != "" {
		details = append(details, "tool="+SafeText(ev.Tool))
	}
	if ev.Status != "" {
		details = append(details, "status="+SafeText(ev.Status))
	}
	if ev.Text != "" {
		details = append(details, SafeText(ev.Text))
	}
	if ev.Error != "" {
		details = append(details, "error="+SafeText(ev.Error))
	}
	if ev.Usage != nil {
		details = append(details, fmt.Sprintf("tokens=%d", ev.Usage.TotalTokens))
	}
	return fmt.Sprintf("%s\t%s\t%s\n", at.UTC().Format(time.RFC3339Nano), ev.Kind, strings.Join(details, " "))
}

func newEvent(kind EventKind) Event {
	return Event{Kind: kind, At: time.Now().UTC()}
}

func emitEvent(fn EventHandler, ev Event) {
	if fn == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	ev.Text = SafeText(ev.Text)
	ev.Error = SafeText(ev.Error)
	fn(ev)
}

// isHiddenReasoningLine rejects raw transport records that carry hidden
// reasoning. The normalized adapters independently ignore these shapes.
func isHiddenReasoningLine(line []byte) bool {
	var v map[string]any
	if json.Unmarshal(line, &v) != nil {
		return false
	}
	return containsHiddenReasoning(v)
}

func containsHiddenReasoning(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			key := strings.ToLower(k)
			if key == "thinking" || key == "reasoning" || key == "chain_of_thought" {
				return true
			}
			if key == "sessionupdate" {
				if s, ok := child.(string); ok && strings.Contains(strings.ToLower(s), "thought") {
					return true
				}
			}
			if key == "type" {
				if s, ok := child.(string); ok {
					s = strings.ToLower(s)
					if strings.Contains(s, "thinking") || strings.Contains(s, "reasoning") {
						return true
					}
				}
			}
			if containsHiddenReasoning(child) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if containsHiddenReasoning(child) {
				return true
			}
		}
	}
	return false
}
