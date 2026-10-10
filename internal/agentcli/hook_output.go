package agentcli

import (
	"encoding/json"
	"strings"
)

// PreToolUse deny encodings (sty_be756616). Each harness reads a different JSON
// shape from a PreToolUse hook; the shapes live here, in the provider adapter,
// so neither the hook verb nor the installed satelle-hook.sh wrapper carries a
// second copy ([[satelle-agent-agnostic]] §1).

// ClaudePreToolUseDenyOut is Claude Code's PreToolUse deny shape (sty_5e4bc568).
// Claude's schema rejects top-level decision/reason; only hookSpecificOutput is
// valid. permissionDecisionReason is the model-visible deny channel. Exported so
// a caller can decode a deny it received (the cli tests do).
type ClaudePreToolUseDenyOut struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// grokPreToolUseDenyOut is Grok Build's PreToolUse deny shape: top-level
// decision + reason (sty_e4902c51 / sty_5e4bc568).
type grokPreToolUseDenyOut struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// cursorPreToolUseDenyOut is cursor-agent's native preToolUse deny. cursor
// shows the model user_message; a bare "reason" blocks the tool but the model
// sees only cursor's generic "blocked by preToolUse hook" (probe 12, pinned by
// testdata/cursor/12-*). agent_message carries the same text so either channel
// delivers it.
type cursorPreToolUseDenyOut struct {
	Permission   string `json:"permission"`
	UserMessage  string `json:"user_message"`
	AgentMessage string `json:"agent_message"`
}

// hookDenyShape is one harness's deny encoding plus the shell glob the installed
// wrapper uses to recognise it in a verb's output.
type hookDenyShape struct {
	harness string
	match   string
	encode  func(reason string) any
}

// hookDenyShapes are the harnesses with their own deny shape, in the order the
// wrapper's case arms are generated. Every other harness — claude, pi and an
// unrecognised one — takes the claude shape (defaultHookDenyShape), so a deny
// stays effective for a harness this table does not name.
var hookDenyShapes = []hookDenyShape{
	{
		harness: HarnessGrok,
		match:   `*'"decision"'*'"deny"'*`,
		encode:  func(r string) any { return grokPreToolUseDenyOut{Decision: "deny", Reason: r} },
	},
	{
		harness: HarnessCursor,
		match:   `*'"permission"'*'"deny"'*`,
		encode: func(r string) any {
			return cursorPreToolUseDenyOut{Permission: "deny", UserMessage: r, AgentMessage: r}
		},
	},
}

var defaultHookDenyShape = hookDenyShape{
	match: `*'"permissionDecision"'*'"deny"'*`,
	encode: func(r string) any {
		var doc ClaudePreToolUseDenyOut
		doc.HookSpecificOutput.HookEventName = "PreToolUse"
		doc.HookSpecificOutput.PermissionDecision = "deny"
		doc.HookSpecificOutput.PermissionDecisionReason = r
		return doc
	},
}

func hookDenyShapeFor(harness string) hookDenyShape {
	for _, s := range hookDenyShapes {
		if s.harness == harness {
			return s
		}
	}
	return defaultHookDenyShape
}

// PreToolUseDeny returns the JSON bytes (no trailing newline) of a PreToolUse
// deny for harness carrying reason. An empty reason gets a placeholder so the
// deny always explains itself. Harnesses without their own shape get Claude's.
func PreToolUseDeny(harness, reason string) []byte {
	if strings.TrimSpace(reason) == "" {
		reason = "satelle: denied (no reason supplied)"
	}
	b, err := json.Marshal(hookDenyShapeFor(harness).encode(reason))
	if err != nil {
		// The shapes are plain string structs; Marshal cannot fail.
		panic("agentcli: marshal PreToolUse deny: " + err.Error())
	}
	return b
}

// PreToolUseDenyMatch returns the shell case-glob that recognises harness's
// PreToolUse deny in a hook verb's stdout. The wrapper passes a matching output
// through byte-for-byte.
func PreToolUseDenyMatch(harness string) string {
	return hookDenyShapeFor(harness).match
}

// HookDenyHarnesses lists the harnesses that have their own deny shape (the
// wrapper generates one case arm per entry; everything else is the default arm).
func HookDenyHarnesses() []string {
	out := make([]string, 0, len(hookDenyShapes))
	for _, s := range hookDenyShapes {
		out = append(out, s.harness)
	}
	return out
}
