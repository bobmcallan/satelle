package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// sty_9e86f407 AC2: Codex deny shape + bash argv array + harness flag.

func TestEmitPreToolUseDenyCodex(t *testing.T) {
	var buf bytes.Buffer
	if err := emitPreToolUseDeny(&buf, "codex", "no story engaged"); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("json: %v body=%s", err, buf.String())
	}
	hso, ok := doc["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("want hookSpecificOutput: %s", buf.String())
	}
	if hso["permissionDecision"] != "deny" {
		t.Errorf("permissionDecision = %v", hso["permissionDecision"])
	}
	if hso["permissionDecisionReason"] != "no story engaged" {
		t.Errorf("reason = %v", hso["permissionDecisionReason"])
	}
	if hso["hookEventName"] != "PreToolUse" {
		t.Errorf("hookEventName = %v", hso["hookEventName"])
	}
	// Must not use Grok top-level shape.
	if _, ok := doc["decision"]; ok {
		t.Error("codex deny must not use top-level decision")
	}
}

func TestEmitPreToolUseDenyEmptyReasonFallback(t *testing.T) {
	var buf bytes.Buffer
	if err := emitPreToolUseDeny(&buf, "codex", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "permissionDecisionReason") {
		t.Fatal(buf.String())
	}
	var doc map[string]any
	_ = json.Unmarshal(buf.Bytes(), &doc)
	hso := doc["hookSpecificOutput"].(map[string]any)
	if strings.TrimSpace(hso["permissionDecisionReason"].(string)) == "" {
		t.Fatal("empty reason must be filled (Codex rejects empty deny reason)")
	}
}

func TestBashCommandFromEventArray(t *testing.T) {
	// Codex shell may send command as argv array.
	ev := `{"tool_input":{"command":["git","commit","-m","x"]},"hook_event_name":"PreToolUse"}`
	got := bashCommandFromEvent([]byte(ev))
	if got != "git commit -m x" {
		t.Fatalf("got %q", got)
	}
	// String form still works.
	ev2 := `{"tool_input":{"command":"git push origin main"}}`
	if bashCommandFromEvent([]byte(ev2)) != "git push origin main" {
		t.Fatal(bashCommandFromEvent([]byte(ev2)))
	}
	// Grok camelCase.
	ev3 := `{"toolInput":{"command":"echo hi"}}`
	if bashCommandFromEvent([]byte(ev3)) != "echo hi" {
		t.Fatal(bashCommandFromEvent([]byte(ev3)))
	}
}

func TestBashCommandFromEventEmpty(t *testing.T) {
	if bashCommandFromEvent([]byte(`{}`)) != "" {
		t.Fatal("want empty")
	}
}

// TestDenyPreToolUseRespectsHarnessFlag: --harness codex forces Claude envelope
// even when the event would sniff as claude (snake_case tool_input).
func TestDenyPreToolUseRespectsHarnessFlag(t *testing.T) {
	prev := hookHarnessFlag
	hookHarnessFlag = "codex"
	t.Cleanup(func() { hookHarnessFlag = prev })

	// Minimal cobra command for denyPreToolUse stdout.
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	raw := []byte(`{"tool_input":{"file_path":"/tmp/x.go"}}`)
	err := denyPreToolUse(cmd, raw, "no engaged story")
	if err == nil {
		t.Fatal("denyPreToolUse must return error")
	}
	var doc map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &doc); jerr != nil {
		t.Fatalf("json: %v body=%s", jerr, buf.String())
	}
	hso := doc["hookSpecificOutput"].(map[string]any)
	if hso["permissionDecision"] != "deny" {
		t.Fatalf("got %v", hso)
	}
}

// TestCodexInfraDenyUsesClaudeEnvelope: parameterized wrapper static deny for
// harness=codex matches Claude shape (used when satelle binary is missing).
func TestCodexInfraDenyUsesClaudeEnvelope(t *testing.T) {
	s := infraDenyJSON("codex")
	if !strings.Contains(s, "permissionDecision") || !strings.Contains(s, "deny") {
		t.Fatalf("codex infra deny: %s", s)
	}
	if strings.Contains(s, `"decision":"deny"`) && !strings.Contains(s, "hookSpecificOutput") {
		t.Fatalf("must not be Grok-only shape: %s", s)
	}
}
