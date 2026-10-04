package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// sessionContextRepo is a governed temp repo with a marked constitution, so
// runHookContext can prove the principle set actually arrives.
func sessionContextRepo(t *testing.T) string {
	t.Helper()
	testutil.IsolateHome(t)
	t.Setenv("SATELLE_CONFIG", "")
	t.Setenv("SATELLE_SESSION", "")
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, config.DefaultDataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "---\ntype: constitution\n---\n\n# Project constitution\n\nCONSTITUTION-MARKER-sty_507d3d9c\n"
	if err := os.WriteFile(filepath.Join(repo, config.DefaultDataDir, config.DefaultConstitutionName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	return repo
}

func writeHarnessTOML(t *testing.T, repo, body string) {
	t.Helper()
	path := filepath.Join(repo, config.DefaultDataDir, config.ConfigName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func grokDualPayload(event, camel, session string) []byte {
	b, err := json.Marshal(map[string]any{
		"hook_event_name": event,
		"hookEventName":   camel,
		"session_id":      session,
		"sessionId":       session,
		"cwd":             "/tmp",
	})
	if err != nil {
		panic(err)
	}
	return b
}

func parseHookContext(t *testing.T, stdout string) (event, content string, ok bool) {
	t.Helper()
	if strings.TrimSpace(stdout) == "" {
		return "", "", false
	}
	var doc struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("hook emitted non-JSON %q: %v", stdout, err)
	}
	return doc.HookSpecificOutput.HookEventName, doc.HookSpecificOutput.AdditionalContext, true
}

func runContext(t *testing.T, harness string, raw []byte) (event, content, stderr string, emitted bool) {
	t.Helper()
	var out, errb bytes.Buffer
	if err := runHookContext(&out, &errb, harness, raw); err != nil {
		t.Fatalf("runHookContext must fail open, got %v", err)
	}
	event, content, emitted = parseHookContext(t, out.String())
	return event, content, errb.String(), emitted
}

// AC1: a dual-field grok payload on the configured channel delivers the same
// session set claude and pi receive. The fixture matches captured grok stdin:
// PascalCase on hook_event_name, snake_case on hookEventName.
func TestHookContext_GrokChannelDeliversSessionSet(t *testing.T) {
	sessionContextRepo(t)
	channel := config.Config{}.SessionContextEvent("grok")
	if channel != "PostToolUse" {
		t.Fatalf("embedded grok channel = %q, want PostToolUse", channel)
	}
	event, content, _, emitted := runContext(t, "grok", grokDualPayload(channel, "post_tool_use", "sess-ac1"))
	if !emitted {
		t.Fatal("configured channel emitted nothing")
	}
	if event != channel {
		t.Errorf("emitted hookEventName = %q, want %q", event, channel)
	}
	if !strings.Contains(content, "CONSTITUTION-MARKER-sty_507d3d9c") {
		t.Errorf("constitution missing from additionalContext:\n%s", content)
	}
	if !strings.Contains(content, "satelle-agent-goals") || !strings.Contains(content, "Status is the sole proof of done") {
		t.Errorf("session principle missing from additionalContext:\n%s", content)
	}
}

func TestHookContext_EitherEventFieldMatches(t *testing.T) {
	sessionContextRepo(t)
	channel := config.Config{}.SessionContextEvent("grok")
	// Only hook_event_name equals the channel — still emits.
	if _, content, _, emitted := runContext(t, "grok", []byte(`{"hook_event_name":"`+channel+`","session_id":"only-snake-field"}`)); !emitted || !strings.Contains(content, "CONSTITUTION-MARKER-sty_507d3d9c") {
		t.Fatalf("hook_event_name-only payload must emit, emitted=%v content=%q", emitted, content)
	}
	// Only the snake_case twin: not equal to the PascalCase channel, emit nothing.
	if _, _, _, emitted := runContext(t, "grok", []byte(`{"hookEventName":"post_tool_use","session_id":"only-camel"}`)); emitted {
		t.Fatal("snake_case twin must not match the PascalCase channel")
	}
}

func TestHookContext_GrokSessionStartEmitsNothing(t *testing.T) {
	repo := sessionContextRepo(t)
	raw := grokDualPayload("SessionStart", "session_start", "sess-start")
	if _, _, _, emitted := runContext(t, "grok", raw); emitted {
		t.Fatal("grok SessionStart must not deliver; the channel is not SessionStart")
	}
	rt := config.Config{}.ResolveRuntimeDir(repo).Dir
	if _, err := os.Stat(filepath.Join(rt, "session-context")); err == nil {
		t.Fatal("a non-matching SessionStart must not create the once-marker")
	}
}

func TestHookContext_NoEventFieldStillEmits(t *testing.T) {
	sessionContextRepo(t)
	event, content, _, emitted := runContext(t, "grok", nil)
	if !emitted {
		t.Fatal("diagnostic hook context (no event) must still emit")
	}
	if event != "SessionStart" {
		t.Errorf("diagnostic rendered event = %q, want SessionStart", event)
	}
	if !strings.Contains(content, "CONSTITUTION-MARKER-sty_507d3d9c") {
		t.Errorf("diagnostic emit lost the constitution:\n%s", content)
	}
}

func TestHookContext_UnknownHarnessWithEventEmitsNothing(t *testing.T) {
	sessionContextRepo(t)
	raw := grokDualPayload("PostToolUse", "post_tool_use", "sess-unknown")
	if _, _, _, emitted := runContext(t, "some-future-cli", raw); emitted {
		t.Fatal("a harness with no configured channel must emit nothing when an event is present")
	}
	if _, _, _, emitted := runContext(t, "some-future-cli", nil); !emitted {
		t.Fatal("a harness with no channel and no event still emits the diagnostic")
	}
}

// Same repo, both budgets large enough to fit the set: grok's channel and
// claude's SessionStart carry the same additionalContext.
func TestHookContext_GrokContentEqualsClaudeWhenBudgetsFit(t *testing.T) {
	repo := sessionContextRepo(t)
	writeHarnessTOML(t, repo, `
[harness.claude]
context_limit_bytes = 1000000

[harness.grok]
context_limit_bytes = 1000000
session_context_event = "PostToolUse"
tool_context_limit_chars = 1000000
`)
	_, claude, _, ok := runContext(t, "claude", []byte(`{"hook_event_name":"SessionStart","session_id":"eq-claude"}`))
	if !ok || !strings.Contains(claude, "CONSTITUTION-MARKER-sty_507d3d9c") {
		t.Fatalf("claude SessionStart did not deliver:\n%s", claude)
	}
	_, grok, _, ok := runContext(t, "grok", grokDualPayload("PostToolUse", "post_tool_use", "eq-grok"))
	if !ok {
		t.Fatal("grok channel did not deliver")
	}
	if grok != claude {
		t.Fatalf("channel content diverged\n--- claude ---\n%s\n--- grok ---\n%s", claude, grok)
	}
}

func TestHookContext_RepoOverrideChangesEmittedEvent(t *testing.T) {
	repo := sessionContextRepo(t)
	writeHarnessTOML(t, repo, `
[harness.grok]
session_context_event = "Stop"
tool_context_limit_chars = 1000000
`)
	event, content, _, emitted := runContext(t, "grok", grokDualPayload("Stop", "stop", "sess-stop"))
	if !emitted || event != "Stop" {
		t.Fatalf("override emit = %q emitted=%v, want Stop", event, emitted)
	}
	if !strings.Contains(content, "CONSTITUTION-MARKER-sty_507d3d9c") {
		t.Errorf("override channel lost the constitution:\n%s", content)
	}
	if _, _, _, emitted := runContext(t, "grok", grokDualPayload("PostToolUse", "post_tool_use", "sess-stop-other")); emitted {
		t.Fatal("PostToolUse must not emit once the channel is Stop")
	}
}

func TestHookContext_ToolClipDegradesAndNamesCharacters(t *testing.T) {
	repo := sessionContextRepo(t)
	writeHarnessTOML(t, repo, `
[harness.grok]
session_context_event = "PostToolUse"
tool_context_limit_chars = 500
`)
	_, content, stderr, emitted := runContext(t, "grok", grokDualPayload("PostToolUse", "post_tool_use", "sess-clip"))
	if !emitted {
		t.Fatal("clipped channel emitted nothing")
	}
	if n := utf8.RuneCountInString(content); n > 500 {
		t.Fatalf("additionalContext is %d runes, want at most 500:\n%s", n, content)
	}
	if strings.Contains(content, "invent process the workflow did not configure") {
		t.Fatalf("a 500-character clip inlined a principle body:\n%s", content)
	}
	if !strings.Contains(content, "read") {
		t.Fatalf("degraded emit has no read instruction:\n%s", content)
	}
	if !strings.Contains(stderr, "500 characters") {
		t.Fatalf("stderr does not name the character budget: %q", stderr)
	}
}

func TestRenderAlwaysContent_CharacterBudgetIsRunes(t *testing.T) {
	big := "---\ndescription: a big rule\ntags: [principles:session]\n---\n" + strings.Repeat("x", 2000)
	docs := []docindex.Doc{doc("a", big), doc("b", big), doc("c", big)}
	content, omitted := renderAlwaysContent("", docs, alwaysRender{Budget: 500, Harness: "grok", Unit: "characters"})
	if len(omitted) == 0 {
		t.Fatal("500-character budget should degrade a large set")
	}
	if n := utf8.RuneCountInString(content); n > 500 {
		t.Fatalf("rendered %d runes, want <= 500:\n%s", n, content)
	}
	if strings.Contains(content, strings.Repeat("x", 40)) {
		t.Fatal("body was inlined under the character budget")
	}
	if !strings.Contains(content, "500 characters") || !strings.Contains(content, "read") {
		t.Fatalf("omit header should name the character budget and the read instruction:\n%s", content)
	}
	full, none := renderAlwaysContent("", docs, alwaysRender{Budget: 100000, Harness: "grok", Unit: "characters"})
	if len(none) != 0 || strings.Count(full, "### ") != 3 {
		t.Fatalf("a large clip should render the full set, omitted=%v", none)
	}
}

// AC3: one delivery per session|harness|channel. A non-matching SessionStart
// neither delivers nor marks; the matching channel delivers once.
func TestHookContext_OncePerSession(t *testing.T) {
	repo := sessionContextRepo(t)
	channel := config.Config{}.SessionContextEvent("grok")
	sid := "sess-once"
	if _, _, _, emitted := runContext(t, "grok", grokDualPayload("SessionStart", "session_start", sid)); emitted {
		t.Fatal("SessionStart must not deliver")
	}
	rt := filepath.Join(config.Config{}.ResolveRuntimeDir(repo).Dir, "session-context")
	if _, err := os.Stat(rt); err == nil {
		t.Fatal("SessionStart created a marker")
	}
	if _, content, _, emitted := runContext(t, "grok", grokDualPayload(channel, "post_tool_use", sid)); !emitted || !strings.Contains(content, "CONSTITUTION-MARKER-sty_507d3d9c") {
		t.Fatal("first matching event must deliver")
	}
	if _, _, _, emitted := runContext(t, "grok", grokDualPayload(channel, "post_tool_use", sid)); emitted {
		t.Fatal("second matching event in the same session must not deliver")
	}
	if _, content, _, emitted := runContext(t, "grok", grokDualPayload(channel, "post_tool_use", "sess-other")); !emitted || !strings.Contains(content, "CONSTITUTION-MARKER-sty_507d3d9c") {
		t.Fatal("a different session id must deliver again")
	}
	// Same session id, other harness: the marker key includes harness and channel.
	if _, _, _, emitted := runContext(t, "claude", []byte(`{"hook_event_name":"SessionStart","session_id":"`+sid+`"}`)); !emitted {
		t.Fatal("claude sharing the session id must still deliver")
	}
}

func TestHookContext_UnwritableRuntimeDirStillEmits(t *testing.T) {
	repo := sessionContextRepo(t)
	rt := config.Config{}.ResolveRuntimeDir(repo).Dir
	if err := os.MkdirAll(rt, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file where the marker directory must be created makes the write fail.
	if err := os.WriteFile(filepath.Join(rt, "session-context"), []byte("not-a-dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	channel := config.Config{}.SessionContextEvent("grok")
	if _, content, _, emitted := runContext(t, "grok", grokDualPayload(channel, "post_tool_use", "sess-unwritable")); !emitted || !strings.Contains(content, "CONSTITUTION-MARKER-sty_507d3d9c") {
		t.Fatal("an unwritable runtime dir must still emit")
	}
}
