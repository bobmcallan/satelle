package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// cursorSessionStartContext runs the context hook for a cursor sessionStart that
// reports version, and returns the additional_context it emitted ("" when it
// emitted nothing).
func cursorSessionStartContext(t *testing.T, harness, version, session string) string {
	t.Helper()
	payload := `{"hook_event_name":"sessionStart","session_id":"` + session + `"`
	if version != "" {
		payload += `,"cursor_version":"` + version + `"`
	}
	payload += `}`
	var out, errb bytes.Buffer
	if err := runHookContext(&out, &errb, harness, []byte(payload)); err != nil {
		t.Fatalf("runHookContext must fail open, got %v", err)
	}
	if strings.TrimSpace(out.String()) == "" {
		return ""
	}
	var doc struct {
		AdditionalContext string `json:"additional_context"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not JSON: %q (%v)", out.String(), err)
	}
	return doc.AdditionalContext
}

// AC1: a cursor session whose sessionStart reports a build other than the one
// satelle's measured behavior was captured against is told so, naming both builds
// and the contracts at risk; a matching build adds nothing.
func TestHookContextCursorVersionDriftLine(t *testing.T) {
	sessionContextRepo(t)
	captured, _ := agentcli.CapturedVersion(agentcli.HarnessCursor)

	ctx := cursorSessionStartContext(t, "cursor", "2026.10.22-27dcf5f", "drift-1")
	for _, want := range []string{
		"2026.10.22-27dcf5f", captured, "satelle agent recheck cursor",
		"stop cap", "hook timeout handling", "envelope shapes", "ACP methods", "usage fields",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("drift context missing %q:\n%s", want, ctx)
		}
	}
	if !strings.Contains(ctx, "CONSTITUTION-MARKER-sty_507d3d9c") {
		t.Errorf("the drift line must ride beside the session set, not replace it:\n%s", ctx)
	}

	for name, version := range map[string]string{"matching build": captured, "no version reported": ""} {
		ctx := cursorSessionStartContext(t, "cursor", version, "no-drift-"+strings.ReplaceAll(name, " ", "-"))
		if !strings.Contains(ctx, "CONSTITUTION-MARKER-sty_507d3d9c") {
			t.Fatalf("%s: session set not delivered:\n%s", name, ctx)
		}
		if strings.Contains(ctx, "satelle agent recheck") || strings.Contains(ctx, "differs from") {
			t.Errorf("%s: no drift line expected:\n%s", name, ctx)
		}
	}
}

// AC5: a harness with no captured version records no drift line — a Claude
// session start never carries one, even when a payload happens to name a version.
func TestHookContextClaudeSessionStartHasNoDriftLine(t *testing.T) {
	sessionContextRepo(t)
	var out, errb bytes.Buffer
	raw := []byte(`{"hook_event_name":"SessionStart","session_id":"claude-1","cursor_version":"2026.10.22-27dcf5f"}`)
	if err := runHookContext(&out, &errb, "claude", raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "satelle agent recheck") {
		t.Errorf("claude session start must carry no drift line:\n%s", out.String())
	}
}
