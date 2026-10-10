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

// Session-context and Stop encodings (sty_7d098d50). A harness whose hooks speak
// their own shape for these answers lives here, beside the deny shapes; every
// other harness takes the Claude-shaped envelopes the cli builds.

// cursorSessionContextOut is cursor-agent's sessionStart output: additional_context
// reaches the model in print mode (testdata/cursor/9-ctx-print.out).
type cursorSessionContextOut struct {
	AdditionalContext string `json:"additional_context"`
}

// SessionContextOutput returns the JSON bytes (no trailing newline) of a
// session-context answer for a harness that has its own shape, and ok=false for
// every other harness, whose answer is the Claude-shaped hookSpecificOutput
// envelope the caller already builds.
func SessionContextOutput(harness, text string) (out []byte, ok bool) {
	if harness != HarnessCursor {
		return nil, false
	}
	b, err := json.Marshal(cursorSessionContextOut{AdditionalContext: text})
	if err != nil {
		panic("agentcli: marshal session context: " + err.Error())
	}
	return b, true
}

// cursorStopOut is cursor-agent's stop output: a followup_message re-prompts the
// agent as its next turn (testdata/cursor/7-stop). A stop cannot be vetoed.
type cursorStopOut struct {
	FollowupMessage string `json:"followup_message"`
}

// defaultStopBlockOut is the top-level decision/reason Stop block Claude reads
// (sty_5e4bc568 AC6); best-effort for Grok.
type defaultStopBlockOut struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// StopOutput returns the JSON bytes (no trailing newline) answering a Stop event
// with reason: cursor re-prompts through followup_message, every other harness
// blocks through the top-level decision/reason shape. A non-block answer is nil:
// cursor treats any followup_message as a re-prompt, so its allow prints nothing
// (SilentStopAllow); the other harnesses' allow-with-note is built by the caller.
func StopOutput(harness string, block bool, reason string) []byte {
	if !block {
		return nil
	}
	var doc any = defaultStopBlockOut{Decision: "block", Reason: reason}
	if harness == HarnessCursor {
		doc = cursorStopOut{FollowupMessage: reason}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		panic("agentcli: marshal stop output: " + err.Error())
	}
	return b
}

// StopContinued reports whether a Stop event is the stop of a turn that a hook
// already continued, for a harness that says so with a counter rather than
// Claude's stop_hook_active flag: cursor's stop payload carries loop_count, 0 on
// the first stop and the number of followups already spent after that
// (testdata/cursor/7-stop-hooks.log).
func StopContinued(raw []byte) bool {
	var ev struct {
		CursorVersion string `json:"cursor_version"`
		LoopCount     int    `json:"loop_count"`
	}
	_ = json.Unmarshal(raw, &ev)
	return strings.TrimSpace(ev.CursorVersion) != "" && ev.LoopCount > 0
}

// StopWithinCap reports whether a Stop event can still spend a continuation: the
// payload counts its own continuations (cursor's loop_count) and the count is
// below harness's recorded cap (StopCapFor). A payload with no count, or a
// harness with no recorded cap, is within the cap — those harnesses are counted
// by satelle, not read off the event. At or past the cap the harness will not
// act on a followup, so a verdict emitted there would be claimed and lost
// (testdata/cursor/20-cap-hooks.log).
func StopWithinCap(harness string, raw []byte) bool {
	var ev struct {
		LoopCount *int `json:"loop_count"`
	}
	if json.Unmarshal(raw, &ev) != nil || ev.LoopCount == nil {
		return true
	}
	limit, ok := StopCapFor(harness)
	return !ok || *ev.LoopCount < limit
}

// SilentStopAllow reports whether harness's Stop allow carries no output: a
// harness whose only Stop channel re-prompts (cursor) cannot take an
// allow-with-note, because the note would become a followup turn.
func SilentStopAllow(harness string) bool { return harness == HarnessCursor }
