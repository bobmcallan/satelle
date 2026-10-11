package agentcli

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// claudeLine is one assistant transcript line carrying message id, model and usage.
func claudeLine(id, model string, fresh, create, read, out int) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"id":%q,"model":%q,"role":"assistant","usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`+"\n",
		id, model, fresh, create, read, out)
}

const (
	locateTreeA = "/home/example/main-tree"
	locateTreeB = "/home/example/worktree"
)

// TestSessionRecordFoundUnderAnotherTree pins AC1: a session whose record is keyed
// under tree A, asked about as tree B, is found by session id and read in full —
// claude, grok and pi alike — and cursor, which keys nothing by tree, does not
// depend on repoRoot at all.
func TestSessionRecordFoundUnderAnotherTree(t *testing.T) {
	t.Run("claude", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CLAUDE_CONFIG_DIR", home)
		const sid = "sess-claude-elsewhere"
		writeAt(t, filepath.Join(home, "projects", claudeProjectSlug(locateTreeA), sid+".jsonl"), readFixture(t, "claude_session.jsonl"))
		want := SessionUsageSnapshot(HarnessClaude, sid, locateTreeA)
		if !want.Available || want.FreshInputTokens == 0 {
			t.Fatalf("baseline under its own tree = %+v", want)
		}
		for name, got := range map[string]DriverSnapshot{
			"store-wide search": SessionUsageSnapshot(HarnessClaude, sid, locateTreeB),
			"hint root":         SessionUsageSnapshotWith(HarnessClaude, sid, locateTreeB, []string{locateTreeA}),
		} {
			if !got.Available || got.FreshInputTokens != want.FreshInputTokens || got.CacheCreationInputTokens != want.CacheCreationInputTokens ||
				got.CacheReadInputTokens != want.CacheReadInputTokens || got.OutputTokens != want.OutputTokens || got.Turns != want.Turns {
				t.Errorf("%s: snapshot = %+v, want %+v", name, got, want)
			}
		}
	})

	t.Run("grok", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("GROK_HOME", home)
		const sid = "sess-grok-elsewhere"
		writeAt(t, filepath.Join(home, "sessions", grokRepoDirName(locateTreeA), sid, "usage.json"), readFixture(t, "grok_usage_t2.json"))
		want := SessionUsageSnapshot(HarnessGrok, sid, locateTreeA)
		got := SessionUsageSnapshot(HarnessGrok, sid, locateTreeB)
		if !want.Available || !got.Available || got.FreshInputTokens != want.FreshInputTokens || got.OutputTokens != want.OutputTokens ||
			got.CacheReadInputTokens != want.CacheReadInputTokens || got.Turns != 2 {
			t.Fatalf("snapshot = %+v, want %+v", got, want)
		}
		if hinted := SessionUsageSnapshotWith(HarnessGrok, sid, locateTreeB, []string{locateTreeA}); !hinted.Available || hinted.OutputTokens != want.OutputTokens {
			t.Fatalf("hint root: snapshot = %+v", hinted)
		}
	})

	t.Run("pi", func(t *testing.T) {
		installPiFixture(t, locateTreeA, readFixture(t, "pi_session.jsonl"))
		got := SessionUsageSnapshot(HarnessPi, piFixtureSession, locateTreeB)
		if !got.Available || got.FreshInputTokens != 14263 || got.OutputTokens != 351 || got.CacheReadInputTokens != 21128 || got.Turns != 3 {
			t.Fatalf("snapshot = %+v, want the 14263/351/21128 capture under tree A", got)
		}
		if hinted := SessionUsageSnapshotWith(HarnessPi, piFixtureSession, locateTreeB, []string{locateTreeA}); !hinted.Available || hinted.Turns != 3 {
			t.Fatalf("hint root: snapshot = %+v", hinted)
		}
	})

	t.Run("cursor", func(t *testing.T) {
		t.Setenv("SATELLE_CURSOR_USAGE_DIR", t.TempDir())
		const sid = "de3328ed-ba7c-44f9-9eeb-3e5d59dee4a8"
		for _, raw := range cursorStopPayloads(t) {
			if err := RecordCursorStop(sid, raw); err != nil {
				t.Fatal(err)
			}
		}
		a := SessionUsageSnapshot(HarnessCursor, sid, locateTreeA)
		b := SessionUsageSnapshot(HarnessCursor, sid, locateTreeB)
		c := SessionUsageSnapshotWith(HarnessCursor, sid, "", []string{locateTreeA})
		for name, s := range map[string]DriverSnapshot{"tree B": b, "no tree": c} {
			if !s.Available || s.FreshInputTokens != a.FreshInputTokens || s.OutputTokens != a.OutputTokens || s.Turns != a.Turns {
				t.Errorf("%s: snapshot = %+v, want %+v", name, s, a)
			}
		}
	})
}

// TestClaudeSnapshotIncludesSubagentTranscripts pins AC2: a session's subagent
// transcripts are summed with its own, one message-id map across every file — a
// message echoed into both subagent files and the parent counts once, and the
// request and turn counts use the same de-duplication.
func TestClaudeSnapshotIncludesSubagentTranscripts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	const sid = "sess-with-subagents"
	parent := filepath.Join(home, "projects", claudeProjectSlug(locateTreeA), sid+".jsonl")
	writeAt(t, parent, []byte(
		claudeLine("m-parent-1", "claude-opus-5", 1, 10, 100, 1000)+
			claudeLine("m-shared", "claude-opus-5", 2, 20, 200, 2000)))
	sub := filepath.Join(home, "projects", claudeProjectSlug(locateTreeA), sid, "subagents")
	writeAt(t, filepath.Join(sub, "agent-one.jsonl"), []byte(
		claudeLine("m-shared", "claude-opus-5", 2, 20, 200, 2000)+
			claudeLine("m-sub-1", "claude-sonnet-5", 3, 30, 300, 3000)))
	writeAt(t, filepath.Join(sub, "agent-two.jsonl"), []byte(
		claudeLine("m-shared", "claude-opus-5", 2, 20, 200, 2000)+
			claudeLine("m-sub-2", "claude-sonnet-5", 4, 40, 400, 4000)))

	got := SessionUsageSnapshot(HarnessClaude, sid, locateTreeB)
	if !got.Available {
		t.Fatalf("snapshot unavailable: %s", got.UnavailableReason)
	}
	// parent-1 + shared (once) + sub-1 + sub-2.
	if got.FreshInputTokens != 1+2+3+4 || got.CacheCreationInputTokens != 10+20+30+40 ||
		got.CacheReadInputTokens != 100+200+300+400 || got.OutputTokens != 1000+2000+3000+4000 {
		t.Fatalf("tokens = fresh %d create %d read %d out %d, want 10/100/1000/10000",
			got.FreshInputTokens, got.CacheCreationInputTokens, got.CacheReadInputTokens, got.OutputTokens)
	}
	if got.Turns != 4 || got.ModelCalls != 4 {
		t.Fatalf("turns = %d modelCalls = %d, want 4/4 (the shared message id counts once)", got.Turns, got.ModelCalls)
	}
	if got.Model != "claude-opus-5" {
		t.Fatalf("model = %q, want the parent's last assistant model", got.Model)
	}
	if got.SubagentNote != "" {
		t.Fatalf("SubagentNote = %q, want none: a claude record includes its subagents", got.SubagentNote)
	}
}

// TestClaudeSnapshotModelFallsBackToSubagent: a parent transcript with no model of its
// own names the newest subagent's.
func TestClaudeSnapshotModelFallsBackToSubagent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	const sid = "sess-subagent-model"
	proj := filepath.Join(home, "projects", claudeProjectSlug(locateTreeA))
	writeAt(t, filepath.Join(proj, sid+".jsonl"), []byte(claudeLine("m1", "", 1, 0, 0, 1)))
	writeAt(t, filepath.Join(proj, sid, "subagents", "agent-a.jsonl"), []byte(claudeLine("m2", "claude-sonnet-5", 1, 0, 0, 1)))
	if got := SessionUsageSnapshot(HarnessClaude, sid, locateTreeA); got.Model != "claude-sonnet-5" || got.Turns != 2 {
		t.Fatalf("snapshot = %+v, want model claude-sonnet-5 over 2 turns", got)
	}
}

// TestClaudeTranscriptLayoutHelpers pins the one owner of the layout rule.
func TestClaudeTranscriptLayoutHelpers(t *testing.T) {
	if got, want := ClaudeSubagentGlob("/p/projects/-x/sess.jsonl"), filepath.FromSlash("/p/projects/-x/sess/subagents/*.jsonl"); got != want {
		t.Errorf("ClaudeSubagentGlob = %q, want %q", got, want)
	}
	if !IsClaudeSubagentTranscript("/p/projects/-x/sess/subagents/agent-1.jsonl") || IsClaudeSubagentTranscript("/p/projects/-x/sess.jsonl") {
		t.Error("IsClaudeSubagentTranscript misclassifies a subagent / parent path")
	}
}

// TestSubagentNotesQualifyNonClaudeAdapters pins AC4: satelle cannot see subagent use
// in a cursor or grok record, so every available row of either carries the
// adapter-named qualifier; pi has no subagent tool or hook, so it carries none.
func TestSubagentNotesQualifyNonClaudeAdapters(t *testing.T) {
	t.Run("cursor", func(t *testing.T) {
		t.Setenv("SATELLE_CURSOR_USAGE_DIR", t.TempDir())
		const sid = "de3328ed-ba7c-44f9-9eeb-3e5d59dee4a8"
		for _, raw := range cursorStopPayloads(t) {
			if err := RecordCursorStop(sid, raw); err != nil {
				t.Fatal(err)
			}
		}
		snap := SessionUsageSnapshot(HarnessCursor, sid, "")
		const want = "cursor: subagent use undeterminable from recorded hooks; subagent spend inclusion unverified"
		if !snap.Available || snap.SubagentNote != want {
			t.Fatalf("SubagentNote = %q (available %v), want %q", snap.SubagentNote, snap.Available, want)
		}
	})

	t.Run("grok", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("GROK_HOME", home)
		const sid = "sess-grok-note"
		writeAt(t, filepath.Join(home, "sessions", grokRepoDirName(locateTreeA), sid, "usage.json"), readFixture(t, "grok_usage_t2.json"))
		snap := SessionUsageSnapshot(HarnessGrok, sid, locateTreeA)
		const want = "grok: subagent use undeterminable; subagent spend inclusion unverified"
		if !snap.Available || snap.SubagentNote != want {
			t.Fatalf("SubagentNote = %q (available %v), want %q", snap.SubagentNote, snap.Available, want)
		}
		// The note is true because the record cannot answer: no turn names a tool.
		raw := string(readFixture(t, "grok_usage_t2.json"))
		for _, field := range []string{"toolCalls", "tool_calls", "toolName", "tools", "spawn_subagent"} {
			if strings.Contains(raw, `"`+field+`"`) || strings.Contains(raw, field+`"`) {
				t.Errorf("grok_usage_t2.json names %q: the record may now carry tool names, so the qualifier needs re-deriving", field)
			}
		}
		// A session directory still inside its first turn is available and qualified too.
		writeAt(t, filepath.Join(home, "sessions", grokRepoDirName(locateTreeA), "sess-first", "placeholder"), []byte("x"))
		if first := SessionUsageSnapshot(HarnessGrok, "sess-first", locateTreeA); !first.Available || first.SubagentNote != want {
			t.Fatalf("first-turn snapshot = %+v, want available with the grok qualifier", first)
		}
	})

	t.Run("pi", func(t *testing.T) {
		installPiFixture(t, locateTreeA, readFixture(t, "pi_session.jsonl"))
		snap := SessionUsageSnapshot(HarnessPi, piFixtureSession, locateTreeA)
		if !snap.Available || snap.SubagentNote != "" {
			t.Fatalf("pi snapshot = %+v, want available with no subagent qualifier", snap)
		}
		// pi's built-in tools (pi's docs: read, bash, edit, write, grep, find, ls) include
		// no subagent spawner; the hook side is asserted in internal/cli
		// (TestPiExposesNoSubagentHook).
		for _, tool := range []string{"read", "bash", "edit", "write", "grep", "find", "ls"} {
			if c := toolClassByName[tool]; c == ClassSubprocess {
				t.Errorf("pi tool %q classifies as a subprocess spawner", tool)
			}
		}
	})
}

// TestSessionRecordNotFoundOrAmbiguousReasons pins AC6: a record found in no tree, or
// in more than one, is a named unavailable for each adapter — never a zero.
func TestSessionRecordNotFoundOrAmbiguousReasons(t *testing.T) {
	const sid = "sess-reasons"
	const other = "/home/example/third-tree"
	setup := map[string]func(t *testing.T, trees ...string){
		HarnessClaude: func(t *testing.T, trees ...string) {
			home := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", home)
			for _, tree := range trees {
				writeAt(t, filepath.Join(home, "projects", claudeProjectSlug(tree), sid+".jsonl"), []byte(claudeLine("m", "claude-opus-5", 1, 0, 0, 1)))
			}
		},
		HarnessGrok: func(t *testing.T, trees ...string) {
			home := t.TempDir()
			t.Setenv("GROK_HOME", home)
			for _, tree := range trees {
				writeAt(t, filepath.Join(home, "sessions", grokRepoDirName(tree), sid, "usage.json"), readFixture(t, "grok_usage_t1.json"))
			}
		},
		HarnessPi: func(t *testing.T, trees ...string) {
			home := t.TempDir()
			t.Setenv("PI_CODING_AGENT_DIR", home)
			for _, tree := range trees {
				writeAt(t, filepath.Join(home, "sessions", piSessionDirName(tree), "2026-09-30T02-22-37-333Z_"+sid+".jsonl"), readFixture(t, "pi_session.jsonl"))
			}
		},
	}
	for _, adapter := range []string{HarnessClaude, HarnessGrok, HarnessPi} {
		adapter := adapter
		t.Run(adapter+" not found", func(t *testing.T) {
			setup[adapter](t) // an empty store
			snap := SessionUsageSnapshotWith(adapter, sid, locateTreeB, []string{locateTreeA})
			if snap.Available || snap.FreshInputTokens != 0 {
				t.Fatalf("snapshot = %+v, want unavailable", snap)
			}
			for _, want := range []string{adapter + ": session ", sid + " not found under ", "store-wide search"} {
				if !strings.Contains(snap.UnavailableReason, want) {
					t.Errorf("reason %q lacks %q", snap.UnavailableReason, want)
				}
			}
			for _, tree := range []string{locateTreeB, locateTreeA} {
				var dir string
				switch adapter {
				case HarnessClaude:
					dir = claudeProjectSlug(tree)
				case HarnessGrok:
					dir = grokRepoDirName(tree)
				default:
					dir = piSessionDirName(tree)
				}
				if !strings.Contains(snap.UnavailableReason, dir) {
					t.Errorf("reason %q does not name the tried tree %s (%s)", snap.UnavailableReason, tree, dir)
				}
			}
			if snap.CostUnavailableReason != snap.UnavailableReason {
				t.Errorf("CostUnavailableReason = %q, want the same reason", snap.CostUnavailableReason)
			}
		})
		t.Run(adapter+" ambiguous", func(t *testing.T) {
			setup[adapter](t, locateTreeA, other)
			snap := SessionUsageSnapshot(adapter, sid, locateTreeB)
			if snap.Available || !strings.HasPrefix(snap.UnavailableReason, adapter+": ambiguous session record for "+sid) {
				t.Fatalf("snapshot = %+v, want an %s ambiguous-record reason", snap, adapter)
			}
			// A hint (or the story's own tree) that names one of them resolves it.
			if resolved := SessionUsageSnapshotWith(adapter, sid, locateTreeB, []string{locateTreeA}); !resolved.Available {
				t.Fatalf("hinted snapshot = %+v, want the hinted tree's record", resolved)
			}
		})
	}

	t.Run("cursor not found", func(t *testing.T) {
		t.Setenv("SATELLE_CURSOR_USAGE_DIR", t.TempDir())
		snap := SessionUsageSnapshot(HarnessCursor, sid, locateTreeA)
		if snap.Available || !strings.HasPrefix(snap.UnavailableReason, "cursor: no stop-hook usage recorded") {
			t.Fatalf("snapshot = %+v, want the cursor no-record reason", snap)
		}
	})
}
