package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

// hookFixture reads a captured/schema-verbatim hook payload from
// internal/agentcli/testdata/hooks — the one fixture set TestHarnessFromHookEvent
// also drives — so this table never drifts from those files.
func hookFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "agentcli", "testdata", "hooks", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestHookHarnessPublishesExecutable pins AC1 (sty_719c4a7b): for every hook
// event a harness's own scaffold installs (harnessHooks — the one table the
// builder, healer and completeness checker already share), running that event
// through bindSessionID — the shared identity/publish path context, prompt,
// stopcheck and gate all call — publishes THAT harness as the in-loop
// executable, both with the scaffold's explicit --harness flag (AC2's
// authoritative naming) and, for every fixture here, from the event sniff
// alone. The grok fixtures are real payloads captured 2026-09-27 from a live
// grok CLI hook firing in this repo; the claude and codex fixtures are
// synthetic but schema-verbatim — see
// internal/agentcli/testdata/hooks/README.md for exact provenance per file.
func TestHookHarnessPublishesExecutable(t *testing.T) {
	prevHarness := hookHarnessFlag
	t.Cleanup(func() { hookHarnessFlag = prevHarness })

	cases := []struct {
		harness, event string
		payload        []byte
	}{
		// claude: snake_case session_id, a /.claude/ transcript, hook_event_name.
		{"claude", "SessionStart", []byte(`{"session_id":"claude-sessionstart","transcript_path":"/home/u/.claude/projects/p/claude-sessionstart.jsonl","cwd":"/repo","hook_event_name":"SessionStart"}`)},
		{"claude", "UserPromptSubmit", []byte(`{"session_id":"claude-promptsubmit","transcript_path":"/home/u/.claude/projects/p/claude-promptsubmit.jsonl","cwd":"/repo","permission_mode":"default","hook_event_name":"UserPromptSubmit","prompt":"hi"}`)},
		{"claude", "Stop", []byte(`{"session_id":"claude-stop","transcript_path":"/home/u/.claude/projects/p/claude-stop.jsonl","cwd":"/repo","hook_event_name":"Stop","stop_hook_active":false}`)},
		{"claude", "PreToolUse", []byte(`{"session_id":"claude-pretooluse","transcript_path":"/home/u/.claude/projects/p/claude-pretooluse.jsonl","cwd":"/repo","permission_mode":"default","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git status"},"tool_use_id":"toolu_1"}`)},

		// grok: real captures (sty_719c4a7b AC1/AC3) — each carries grok's own
		// camelCase keys (hookEventName, sessionId, workspaceRoot) alongside
		// Claude-compatible snake_case aliases, including permission_mode on
		// UserPromptSubmit, the exact AC3 regression case.
		{"grok", "SessionStart", hookFixture(t, "grok_session_start.json")},
		{"grok", "UserPromptSubmit", hookFixture(t, "grok_prompt_submit.json")},
		{"grok", "Stop", hookFixture(t, "grok_stop.json")},
		{"grok", "PreToolUse", hookFixture(t, "grok_pre_tool_use.json")},

		// codex: snake_case envelope carrying turn_id. Codex's scaffold omits
		// Stop (harnessHooks), so there is no Stop case here.
		{"codex", "SessionStart", []byte(`{"session_id":"codex-sessionstart","turn_id":"t1","transcript_path":"/home/u/.codex/sessions/codex-sessionstart.jsonl","cwd":"/repo","hook_event_name":"SessionStart"}`)},
		{"codex", "UserPromptSubmit", []byte(`{"session_id":"codex-promptsubmit","turn_id":"t1","transcript_path":"/home/u/.codex/sessions/codex-promptsubmit.jsonl","cwd":"/repo","hook_event_name":"UserPromptSubmit"}`)},
		{"codex", "PreToolUse", []byte(`{"session_id":"codex-pretooluse","turn_id":"t1","transcript_path":"/home/u/.codex/sessions/codex-pretooluse.jsonl","cwd":"/repo","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git status"},"tool_use_id":"call_1"}`)},
	}

	// Every case's event must actually belong to that harness's installed set
	// — the same table the scaffold builder writes from — or this test would
	// silently assert something the scaffold never installs.
	for _, c := range cases {
		if !harnessHooks(c.harness).hasEvent(c.event) {
			t.Fatalf("%s does not install %s per harnessHooks — fixture/table drift", c.harness, c.event)
		}
	}

	for _, c := range cases {
		t.Run(c.harness+"/"+c.event, func(t *testing.T) {
			t.Setenv("SATELLE_HOME", t.TempDir())
			_ = os.Unsetenv(config.SessionEnv)

			// Exactly as the scaffold's command line does: --harness set.
			hookHarnessFlag = c.harness
			sid := bindSessionID(c.payload)
			if sid == "" {
				t.Fatalf("bindSessionID returned no session id for %s/%s", c.harness, c.event)
			}
			if _, exe, _ := config.ResolveSessionModel(sid, verb.SessionModelRoleInLoop); exe != c.harness {
				t.Fatalf("with --harness %s: published executable = %q, want %q", c.harness, exe, c.harness)
			}

			// And from the event sniff alone (flag removed) — every fixture
			// above carries that harness's own real fingerprint.
			hookHarnessFlag = ""
			sid2 := bindSessionID(c.payload)
			if _, exe, _ := config.ResolveSessionModel(sid2, verb.SessionModelRoleInLoop); exe != c.harness {
				t.Errorf("sniffed (no --harness): published executable = %q, want %q", exe, c.harness)
			}
		})
	}
}

// TestBindSessionIDSkipsInLoopPublishWhenDispatched pins AC4 (sty_719c4a7b): a
// dispatched process — SATELLE_DISPATCH_AGENT/STEP/ITEM set, inheriting the
// driver's SATELLE_SESSION stamp exactly as agentstep.Invoke overlays it on
// every ExpectPerform child (both the command and ACP transports apply the
// same env via composeEnv — internal/agentcli/agentcli.go and acp.go) — never
// publishes role in-loop over the driver's own file, whatever harness/model
// the dispatch's own hook payload carries.
func TestBindSessionIDSkipsInLoopPublishWhenDispatched(t *testing.T) {
	prevHarness := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prevHarness })

	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, "driver-sess")

	// The driver (say, a real grok TUI session) publishes its own model first.
	config.PublishSessionModel("driver-sess", verb.SessionModelRoleInLoop, "grok-4.7", "grok", "")
	beforeModel, beforeExe, _ := config.ResolveSessionModel("driver-sess", verb.SessionModelRoleInLoop)
	if beforeExe != "grok" {
		t.Fatalf("setup: driver in-loop file = (%q,%q), want grok", beforeModel, beforeExe)
	}

	// A claude -p dispatch inherits SATELLE_SESSION=driver-sess and carries the
	// dispatch markers.
	t.Setenv(config.DispatchAgentEnv, "coder")
	t.Setenv(config.DispatchStepEnv, "in_progress")
	t.Setenv(config.DispatchItemEnv, "sty_test")

	claudePayload := []byte(`{"session_id":"driver-sess","transcript_path":"/home/u/.claude/projects/p/driver-sess.jsonl","cwd":"/repo","permission_mode":"default","hook_event_name":"SessionStart"}`)
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
		sid := bindSessionID(claudePayload)
		if sid != "driver-sess" {
			t.Fatalf("%s: dispatched process must still inherit the driver's session id, got %q", event, sid)
		}
		afterModel, afterExe, _ := config.ResolveSessionModel("driver-sess", verb.SessionModelRoleInLoop)
		if afterModel != beforeModel || afterExe != beforeExe {
			t.Fatalf("%s: dispatched hook overwrote the driver's in-loop file: before=(%q,%q) after=(%q,%q)",
				event, beforeModel, beforeExe, afterModel, afterExe)
		}
	}
}

// TestBindSessionIDSkipsInLoopPublishWithSpawnMarkerAlone pins AC4
// (sty_719c4a7b): the step-summary spawn (agentstep.Engine.Summarise, via
// buildRequest) carries no SATELLE_DISPATCH_AGENT/STEP/ITEM three-tuple — it
// has no agent/step/item context of its own to scope an edit grant to — only
// the generic SATELLE_DISPATCH_SPAWN marker buildRequest stamps on every
// request it assembles. isDispatchedProcess must recognise that marker alone,
// or a step-summary child's own hooks would still publish role in-loop over
// the driver's file, exactly like an unmarked dispatch.
func TestBindSessionIDSkipsInLoopPublishWithSpawnMarkerAlone(t *testing.T) {
	prevHarness := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prevHarness })

	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, "driver-sess")

	config.PublishSessionModel("driver-sess", verb.SessionModelRoleInLoop, "grok-4.7", "grok", "")
	beforeModel, beforeExe, _ := config.ResolveSessionModel("driver-sess", verb.SessionModelRoleInLoop)
	if beforeExe != "grok" {
		t.Fatalf("setup: driver in-loop file = (%q,%q), want grok", beforeModel, beforeExe)
	}

	t.Setenv(config.SpawnEnv, "1")

	claudePayload := []byte(`{"session_id":"driver-sess","transcript_path":"/home/u/.claude/projects/p/driver-sess.jsonl","cwd":"/repo","permission_mode":"default","hook_event_name":"SessionStart"}`)
	sid := bindSessionID(claudePayload)
	if sid != "driver-sess" {
		t.Fatalf("spawn-marked process must still inherit the driver's session id, got %q", sid)
	}
	afterModel, afterExe, _ := config.ResolveSessionModel("driver-sess", verb.SessionModelRoleInLoop)
	if afterModel != beforeModel || afterExe != beforeExe {
		t.Fatalf("spawn-marked hook overwrote the driver's in-loop file: before=(%q,%q) after=(%q,%q)",
			beforeModel, beforeExe, afterModel, afterExe)
	}
}

// TestResolveInLoopModelUnknownReasonNamesHarness pins AC6 (sty_719c4a7b): a
// grok payload carrying no model publishes the explicit "unknown" marker plus
// an adapter-named reason that names grok (from agentcli's capability table —
// never a silent zero, satelle-agent-agnostic §2), while a claude payload
// whose transcript resolves a model publishes that model with no reason.
func TestResolveInLoopModelUnknownReasonNamesHarness(t *testing.T) {
	grokPayload := []byte(`{"sessionId":"grok-nomode","hookEventName":"UserPromptSubmit","cwd":"/repo"}`)
	model, reason := resolveInLoopModel(grokPayload, "grok")
	if model != "unknown" {
		t.Fatalf("grok payload with no model: model = %q, want unknown", model)
	}
	if !strings.Contains(reason, "grok") {
		t.Fatalf("grok payload with no model: reason = %q, want it to name grok", reason)
	}

	claudePayload := []byte(`{"session_id":"claude-modelled","model":"claude-opus-5-5","permission_mode":"default"}`)
	model, reason = resolveInLoopModel(claudePayload, "claude")
	if model != "claude-opus-5-5" || reason != "" {
		t.Fatalf("claude payload with a reported model: got (%q,%q), want (claude-opus-5-5, \"\")", model, reason)
	}

	// An adapter this repo's capability table has no row for still gets an
	// adapter-named reason, never silence.
	model, reason = resolveInLoopModel([]byte(`{}`), "some-future-harness")
	if model != "unknown" || !strings.Contains(reason, "some-future-harness") {
		t.Fatalf("uncatalogued harness: got (%q,%q), want unknown + a reason naming it", model, reason)
	}
}
