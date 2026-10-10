package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// A harness whose hook file cannot filter a hook by tool name
// (harnessHookSpec.NoToolMatcher — cursor's hooks.json) sends the gate and the
// commitgate EVERY tool call, where the claude and grok scaffolds forward only the
// matcher's tools. The verbs therefore classify the tool themselves, with
// agentcli.ClassifyTool as the one table of tool names (sty_7d098d50). Nothing
// here names a harness or a tool: the property is the spec's, the names are the
// adapter's.

// toolRoute is what `hook gate` does with a tool call.
type toolRoute int

const (
	// routeMatched: the harness filters by tool name, so the payload is exactly
	// what the verb has always judged.
	routeMatched toolRoute = iota
	// routeJudge: no matcher, and the tool writes or edits — judge its target.
	routeJudge
	// routeSkip: no matcher, and the tool is not the edit gate's (a read, a
	// shell — commitgate's —, a network or subprocess call, an MCP tool).
	routeSkip
	// routeUnknown: no matcher, and no table places the tool — fail closed.
	routeUnknown
)

// invokingHarness is the harness a hook verb was invoked for: the wrapper's
// --harness token, else a neutral sniff of the event envelope.
func invokingHarness(raw []byte) string {
	if h := strings.ToLower(strings.TrimSpace(hookHarnessFlag)); h != "" {
		return h
	}
	return harnessFromEvent(raw)
}

// invokingNoToolMatcher reports whether the invoking harness has no tool matcher.
func invokingNoToolMatcher(raw []byte) bool {
	return harnessHooks(invokingHarness(raw)).NoToolMatcher
}

// toolNameFromEvent pulls the tool name out of a PreToolUse event: Claude and
// cursor send tool_name, Grok toolName.
func toolNameFromEvent(raw []byte) string {
	var ev struct {
		Snake string `json:"tool_name"`
		Camel string `json:"toolName"`
	}
	_ = json.Unmarshal(raw, &ev)
	if ev.Snake != "" {
		return ev.Snake
	}
	return ev.Camel
}

// routeUnmatchedTool classifies the tool call for the edit gate. The name is
// returned for routeUnknown (it may be empty: a payload with no tool name is as
// unplaceable as a name no table knows).
func routeUnmatchedTool(raw []byte) (toolRoute, string) {
	if !invokingNoToolMatcher(raw) {
		return routeMatched, ""
	}
	name := toolNameFromEvent(raw)
	switch agentcli.ClassifyTool(name) {
	case agentcli.ClassWrite, agentcli.ClassEdit:
		return routeJudge, name
	case agentcli.ClassUnknown:
		return routeUnknown, name
	default:
		return routeSkip, name
	}
}

// shellOrMatched reports whether a commitgate call carries a shell command to
// judge: always for a harness that filters by tool name, and only for a
// shell-class tool when it does not.
func shellOrMatched(raw []byte) bool {
	if !invokingNoToolMatcher(raw) {
		return true
	}
	return agentcli.ClassifyTool(toolNameFromEvent(raw)) == agentcli.ClassShell
}

// unrecognisedToolReason is the deny text for a tool no table places.
func unrecognisedToolReason(raw []byte, name string) string {
	if name == "" {
		name = "(no tool name)"
	}
	return fmt.Sprintf("satelle: unrecognised %s tool %s; satelle cannot tell whether it mutates, so it is refused while no story is in a state that permits edits.",
		invokingHarness(raw), name)
}
