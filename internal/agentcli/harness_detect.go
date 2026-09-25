package agentcli

import (
	"encoding/json"
	"strings"
)

// Harness tokens recorded in model and harness telemetry. HarnessUnknown is
// the answer for anything unrecognised — nothing is assumed to be Claude
// (satelle-agent-agnostic §3).
const (
	HarnessClaude  = "claude"
	HarnessGrok    = "grok"
	HarnessCodex   = "codex"
	HarnessUnknown = "unknown"
)

// sessionMarker is one provider's in-loop environment marker. The provider's
// env-var names live here, in the adapter package, and nowhere else.
type sessionMarker struct {
	harness string
	key     string
	prefix  bool // key is a prefix rather than an exact name
	match   func(val string) bool
}

func nonEmptyNotZero(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && v != "0"
}

func nonEmpty(v string) bool { return strings.TrimSpace(v) != "" }

// sessionMarkers lists what each in-loop harness exports to its shell and hook
// children. Codex: CODEX_THREAD_ID is exported to the shell tool's
// environment; CODEX_SANDBOX / CODEX_SANDBOX_NETWORK_DISABLED are set when it
// runs a command sandboxed.
var sessionMarkers = []sessionMarker{
	{harness: HarnessClaude, key: "CLAUDECODE", match: func(v string) bool { return strings.TrimSpace(v) == "1" }},
	{harness: HarnessClaude, key: "CLAUDE_CODE_", prefix: true, match: func(string) bool { return true }},
	{harness: HarnessGrok, key: "GROK_AGENT", match: nonEmptyNotZero},
	{harness: HarnessCodex, key: "CODEX_THREAD_ID", match: nonEmpty},
	{harness: HarnessCodex, key: "CODEX_SANDBOX", match: nonEmpty},
	{harness: HarnessCodex, key: "CODEX_SANDBOX_NETWORK_DISABLED", match: nonEmpty},
}

// DetectSessionHarnesses reports which harnesses' session markers appear in
// environ (KEY=VALUE entries). PATH is never probed.
func DetectSessionHarnesses(environ []string) map[string]bool {
	out := map[string]bool{}
	for _, e := range environ {
		key, val, _ := strings.Cut(e, "=")
		for _, m := range sessionMarkers {
			hit := key == m.key
			if m.prefix {
				hit = strings.HasPrefix(key, m.key)
			}
			if hit && m.match(val) {
				out[m.harness] = true
			}
		}
	}
	return out
}

// InLoopHarnessFromEnv reports whether environ carries any in-loop harness
// marker, and the first harness it names (claude, grok, codex order).
func InLoopHarnessFromEnv(environ []string) (string, bool) {
	found := DetectSessionHarnesses(environ)
	for _, h := range []string{HarnessClaude, HarnessGrok, HarnessCodex} {
		if found[h] {
			return h, true
		}
	}
	return "", false
}

// claudeToolNames are tool names only Claude Code emits (Codex documents
// Bash, apply_patch and aliases; Grok uses camelCase envelopes).
var claudeToolNames = map[string]bool{
	"Edit": true, "MultiEdit": true, "Write": true, "Read": true, "NotebookEdit": true,
}

// HarnessFromHookEvent classifies a hook event envelope, each adapter
// fingerprinting its own shape:
//
//   - grok: camelCase toolInput with no snake_case tool_input.
//   - codex: snake_case envelope carrying turn_id (Claude Code's envelope has
//     no turn_id).
//   - claude: snake_case envelope carrying a Claude-only field — permission_mode,
//     a .claude transcript_path, or a Claude-only tool name.
//   - anything else: unknown. Never claude by default.
//
// This is a sniff for unflagged invocations; the scaffolded wrapper's
// --harness flag stays authoritative.
func HarnessFromHookEvent(raw []byte) string {
	var top struct {
		ToolInputSnake json.RawMessage `json:"tool_input"`
		ToolInputCamel json.RawMessage `json:"toolInput"`
		TurnID         json.RawMessage `json:"turn_id"`
		PermissionMode json.RawMessage `json:"permission_mode"`
		Transcript     string          `json:"transcript_path"`
		ToolName       string          `json:"tool_name"`
	}
	if json.Unmarshal(raw, &top) != nil {
		return HarnessUnknown
	}
	present := func(m json.RawMessage) bool {
		s := strings.TrimSpace(string(m))
		return s != "" && s != "null"
	}
	snake, camel := present(top.ToolInputSnake), present(top.ToolInputCamel)
	if camel && !snake {
		return HarnessGrok
	}
	if camel && snake {
		return HarnessUnknown
	}
	if present(top.TurnID) {
		return HarnessCodex
	}
	if present(top.PermissionMode) || strings.Contains(top.Transcript, "/.claude/") || claudeToolNames[top.ToolName] {
		return HarnessClaude
	}
	return HarnessUnknown
}
