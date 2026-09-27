package agentcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readFixture loads a testdata/driver fixture, failing the test if it is
// missing — a moved/renamed fixture should break loudly, not silently read
// nothing and pass on zeros.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "driver", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// writeAt writes body to path, creating parent directories as needed.
func writeAt(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestClaudeDriverSnapshotDelta pins AC2 for claude against a real transcript
// capture: the first two lines share message.id (streamed chunks of the same
// message with identical usage) and must dedupe to ONE entry, not sum twice;
// the third line is a distinct message with its own usage. Snapshot A covers
// only the first two (deduped) lines; snapshot B adds the third — the delta
// is exactly the third message's own usage.
func TestClaudeDriverSnapshotDelta(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	repoRoot := "/home/example/Development/satelle"
	sessionID := "sess-claude-driver-1"
	path := filepath.Join(home, "projects", claudeProjectSlug(repoRoot), sessionID+".jsonl")

	full := readFixture(t, "claude_session.jsonl")
	lines := strings.Split(strings.TrimRight(string(full), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("fixture line count = %d, want 3", len(lines))
	}
	prefix := strings.Join(lines[:2], "\n") + "\n"

	writeAt(t, path, []byte(prefix))
	a := SessionUsageSnapshot(HarnessClaude, sessionID, repoRoot)
	if !a.Available {
		t.Fatalf("snapshot A unavailable: %s", a.UnavailableReason)
	}
	if a.FreshInputTokens != 2 || a.CacheCreationInputTokens != 10232 || a.CacheReadInputTokens != 11108 || a.OutputTokens != 228 {
		t.Fatalf("snapshot A = %+v, want fresh=2 cacheCreate=10232 cacheRead=11108 out=228", a)
	}
	if a.Turns != 1 {
		t.Fatalf("snapshot A turns = %d, want 1 (two lines share one message id)", a.Turns)
	}
	if a.Model != "claude-opus-5" {
		t.Fatalf("snapshot A model = %q", a.Model)
	}
	if a.MayUndercountInFlightTurn {
		t.Fatalf("claude must never set MayUndercountInFlightTurn — its transcript already carries the calling message's own usage synchronously (sty_81caa41b AC6: the verb layer's Pending catch-up must never apply to claude)")
	}

	writeAt(t, path, full)
	b := SessionUsageSnapshot(HarnessClaude, sessionID, repoRoot)
	if !b.Available {
		t.Fatalf("snapshot B unavailable: %s", b.UnavailableReason)
	}
	if b.Turns != 2 {
		t.Fatalf("snapshot B turns = %d, want 2", b.Turns)
	}
	deltaFresh := b.FreshInputTokens - a.FreshInputTokens
	deltaCacheCreate := b.CacheCreationInputTokens - a.CacheCreationInputTokens
	deltaCacheRead := b.CacheReadInputTokens - a.CacheReadInputTokens
	deltaOut := b.OutputTokens - a.OutputTokens
	if deltaFresh != 2 || deltaCacheCreate != 2619 || deltaCacheRead != 21340 || deltaOut != 249 {
		t.Fatalf("delta = fresh:%d cacheCreate:%d cacheRead:%d out:%d, want 2/2619/21340/249",
			deltaFresh, deltaCacheCreate, deltaCacheRead, deltaOut)
	}
	if b.CostUnavailableReason == "" {
		t.Fatalf("claude driver snapshot must name a cost-unavailable reason, got none")
	}
}

// TestClaudeDriverSnapshotSameIDResume pins AC8's same-session-id case: a
// resumed claude session keeps writing to the SAME transcript file under the
// SAME session id, so a snapshot taken after the resume already excludes
// everything before it — the high-water mark mechanics are identical to an
// ordinary delta, no separate resume-detection code needed. The fourth line
// is a constructed continuation (not a captured turn) appended to the real
// 3-line capture to stand in for the resumed turn.
func TestClaudeDriverSnapshotSameIDResume(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	repoRoot := "/home/example/Development/satelle"
	sessionID := "sess-claude-driver-1"
	path := filepath.Join(home, "projects", claudeProjectSlug(repoRoot), sessionID+".jsonl")

	preResume := readFixture(t, "claude_session.jsonl")
	writeAt(t, path, preResume)
	beforeResume := SessionUsageSnapshot(HarnessClaude, sessionID, repoRoot)
	if !beforeResume.Available {
		t.Fatalf("snapshot before resume unavailable: %s", beforeResume.UnavailableReason)
	}

	resumedTurn := `{"type": "assistant", "uuid": "44444444-0000-0000-0000-000000000004", "sessionId": "916b6026-dd37-453e-b2e8-983ce7bb3a9e", "timestamp": "2026-09-05T02:00:00.000Z", "message": {"id": "msg_resumed_1", "model": "claude-opus-5", "usage": {"input_tokens": 3, "cache_creation_input_tokens": 500, "cache_read_input_tokens": 4000, "output_tokens": 90}}}` + "\n"
	writeAt(t, path, append(append([]byte{}, preResume...), []byte(resumedTurn)...))
	afterResume := SessionUsageSnapshot(HarnessClaude, sessionID, repoRoot)
	if !afterResume.Available {
		t.Fatalf("snapshot after resume unavailable: %s", afterResume.UnavailableReason)
	}

	deltaFresh := afterResume.FreshInputTokens - beforeResume.FreshInputTokens
	deltaCacheCreate := afterResume.CacheCreationInputTokens - beforeResume.CacheCreationInputTokens
	deltaCacheRead := afterResume.CacheReadInputTokens - beforeResume.CacheReadInputTokens
	deltaOut := afterResume.OutputTokens - beforeResume.OutputTokens
	if deltaFresh != 3 || deltaCacheCreate != 500 || deltaCacheRead != 4000 || deltaOut != 90 {
		t.Fatalf("post-resume delta = fresh:%d cacheCreate:%d cacheRead:%d out:%d, want 3/500/4000/90 (only the resumed turn)",
			deltaFresh, deltaCacheCreate, deltaCacheRead, deltaOut)
	}
}

// TestClaudeDriverSnapshotUnreadable pins AC4: no transcript on disk at all
// yields Available=false with an adapter-named reason, never zeros.
func TestClaudeDriverSnapshotUnreadable(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	snap := SessionUsageSnapshot(HarnessClaude, "sess-missing", "/home/example/Development/satelle")
	if snap.Available {
		t.Fatalf("snapshot = %+v, want Available=false", snap)
	}
	if !strings.HasPrefix(snap.UnavailableReason, "claude:") {
		t.Fatalf("UnavailableReason = %q, want an adapter-named claude: reason", snap.UnavailableReason)
	}
}

// TestGrokDriverSnapshotDelta pins AC2 for grok against a real usage.json
// capture (t2, two turns) and its turn-one-only derivative (t1) — asserting
// the exact delta (turn two's own contribution) including dollars, and that
// the cumulative totals are read from the nested "session" object.
func TestGrokDriverSnapshotDelta(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	repoRoot := "/home/example/Development/satelle"
	sessionID := "sess-grok-driver-1"
	path := filepath.Join(home, "sessions", grokRepoDirName(repoRoot), sessionID, "usage.json")

	writeAt(t, path, readFixture(t, "grok_usage_t1.json"))
	a := SessionUsageSnapshot(HarnessGrok, sessionID, repoRoot)
	if !a.Available {
		t.Fatalf("snapshot A unavailable: %s", a.UnavailableReason)
	}
	if !a.MayUndercountInFlightTurn {
		t.Fatalf("grok must set MayUndercountInFlightTurn — its usage.json only updates the session summary once a turn completes (sty_81caa41b AC6)")
	}

	writeAt(t, path, readFixture(t, "grok_usage_t2.json"))
	b := SessionUsageSnapshot(HarnessGrok, sessionID, repoRoot)
	if !b.Available {
		t.Fatalf("snapshot B unavailable: %s", b.UnavailableReason)
	}

	deltaFresh := b.FreshInputTokens - a.FreshInputTokens
	deltaRead := b.CacheReadInputTokens - a.CacheReadInputTokens
	deltaWrite := b.CacheCreationInputTokens - a.CacheCreationInputTokens
	deltaOut := b.OutputTokens - a.OutputTokens
	if deltaFresh != 48495 || deltaRead != 48768 || deltaWrite != 0 || deltaOut != 691 {
		t.Fatalf("delta = fresh:%d read:%d write:%d out:%d, want 48495/48768/0/691", deltaFresh, deltaRead, deltaWrite, deltaOut)
	}
	if a.CostUSD == nil || b.CostUSD == nil {
		t.Fatalf("grok driver snapshots must report cost: a=%v b=%v", a.CostUSD, b.CostUSD)
	}
	deltaCost := *b.CostUSD - *a.CostUSD
	wantDeltaCost := 0.039360576
	if diff := deltaCost - wantDeltaCost; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("delta cost = %v, want %v", deltaCost, wantDeltaCost)
	}
	if b.Model != "grok-4.5-build" {
		t.Fatalf("model = %q, want grok-4.5-build", b.Model)
	}
	if b.Turns != 2 {
		t.Fatalf("turns = %d, want 2 (turnCount off the session summary)", b.Turns)
	}
}

// TestGrokDriverSnapshotUnreadable pins AC4 for grok.
func TestGrokDriverSnapshotUnreadable(t *testing.T) {
	t.Setenv("GROK_HOME", t.TempDir())
	snap := SessionUsageSnapshot(HarnessGrok, "sess-missing", "/home/example/Development/satelle")
	if snap.Available {
		t.Fatalf("snapshot = %+v, want Available=false", snap)
	}
	if !strings.HasPrefix(snap.UnavailableReason, "grok:") {
		t.Fatalf("UnavailableReason = %q, want an adapter-named grok: reason", snap.UnavailableReason)
	}
}

// TestGrokDriverSnapshotFirstTurnNotFlushed pins the dogfood case found at
// release: grok writes usage.json when a turn ends, so a session directory
// with no usage.json is a session still in its first turn — available with
// nothing counted yet and flagged as possibly undercounting, not unreadable.
func TestGrokDriverSnapshotFirstTurnNotFlushed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	repo := "/home/example/Development/satelle"
	if err := os.MkdirAll(filepath.Join(home, "sessions", grokRepoDirName(repo), "sess-first-turn"), 0o755); err != nil {
		t.Fatal(err)
	}
	snap := SessionUsageSnapshot(HarnessGrok, "sess-first-turn", repo)
	if !snap.Available || !snap.MayUndercountInFlightTurn || snap.Turns != 0 || snap.FreshInputTokens != 0 {
		t.Fatalf("snapshot = %+v, want available, zero turns, MayUndercountInFlightTurn", snap)
	}
	if !strings.HasPrefix(snap.CostUnavailableReason, "grok:") {
		t.Fatalf("CostUnavailableReason = %q, want an adapter-named grok: reason", snap.CostUnavailableReason)
	}
}

// TestCodexDriverSnapshotDelta pins AC2 for codex against a real rollout
// capture that holds exactly one completed turn (one token_count event):
// snapshot A on the prefix before that event (session_meta + turn_context
// only) is Available=false — no usage yet — and snapshot B on the full file
// is the first available cumulative, read from
// event_msg.payload.info.total_token_usage with the model coming from
// turn_context.payload.model (both confirmed against the real capture, fixing
// two wrong assumptions from round 1).
func TestCodexDriverSnapshotDelta(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessionID := "01a0e225-973d-7f11-90e8-68681c9d6d67"
	path := filepath.Join(home, "sessions", "2026", "09", "27", "rollout-2026-09-27T19-15-09-"+sessionID+".jsonl")

	full := readFixture(t, "codex_rollout.jsonl")
	lines := strings.Split(strings.TrimRight(string(full), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("fixture line count = %d, want 4", len(lines))
	}
	prefix := strings.Join(lines[:2], "\n") + "\n"

	writeAt(t, path, []byte(prefix))
	a := SessionUsageSnapshot(HarnessCodex, sessionID, "")
	if a.Available {
		t.Fatalf("snapshot A = %+v, want Available=false (no token_count event yet)", a)
	}

	writeAt(t, path, full)
	b := SessionUsageSnapshot(HarnessCodex, sessionID, "")
	if !b.Available {
		t.Fatalf("snapshot B unavailable: %s", b.UnavailableReason)
	}
	if !b.MayUndercountInFlightTurn {
		t.Fatalf("codex must set MayUndercountInFlightTurn — its rollout's token_count event only lands once a turn completes (sty_81caa41b AC6)")
	}
	if b.FreshInputTokens != 3102 || b.CacheReadInputTokens != 12160 || b.OutputTokens != 157 {
		t.Fatalf("snapshot B = %+v, want fresh=3102 cacheRead=12160 out=157", b)
	}
	if b.Model != "gpt-6-astra" {
		t.Fatalf("snapshot B model = %q, want gpt-6-astra", b.Model)
	}
	if b.Turns != 1 {
		t.Fatalf("snapshot B turns = %d, want 1", b.Turns)
	}
	if b.CostUnavailableReason == "" {
		t.Fatalf("codex driver snapshot must name a cost-unavailable reason, got none")
	}
}

// TestCodexDriverSnapshotSkippedTokenCountEventKeepsBreakdownAligned pins the
// Revision 4 fix: a rollout carrying a token_count event whose
// total_token_usage map parses to Available=false (sty_81caa41b Revision 4 —
// e.g. an empty object) must NOT be counted as a turn or leave a hole in
// TurnBreakdown — len(TurnBreakdown) must always equal Turns, which the verb
// layer's turn-index credit (recordPendingCatchupFromTurnBreakdown) relies on
// to index safely. It must also not overwrite the running cumulative with the
// unusable map, which would otherwise flip a snapshot that already has real
// prior usage back to Available=false.
func TestCodexDriverSnapshotSkippedTokenCountEventKeepsBreakdownAligned(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	sessionID := "01a0e225-skip-0000-0000-000000000001"
	path := filepath.Join(home, "sessions", "2026", "09", "27", "rollout-2026-09-27T19-15-09-"+sessionID+".jsonl")

	lines := []string{
		`{"timestamp": "2026-09-27T09:15:09.647Z", "type": "session_meta", "payload": {"id": "` + sessionID + `"}}`,
		`{"timestamp": "2026-09-27T09:15:11.373Z", "type": "turn_context", "payload": {"model": "gpt-6-astra"}}`,
		`{"timestamp": "2026-09-27T09:15:18.835Z", "type": "event_msg", "payload": {"type": "token_count", "info": {"total_token_usage": {"input_tokens": 1000, "output_tokens": 50, "total_tokens": 1050}}}}`,
		// Skipped: an empty total_token_usage map parses to Available=false
		// (codexUsageFromMap's hasAnyKey check) — must not count as a turn, must
		// not overwrite the running cumulative.
		`{"timestamp": "2026-09-27T09:15:20.000Z", "type": "event_msg", "payload": {"type": "token_count", "info": {"total_token_usage": {}}}}`,
		`{"timestamp": "2026-09-27T09:15:25.000Z", "type": "event_msg", "payload": {"type": "token_count", "info": {"total_token_usage": {"input_tokens": 1800, "output_tokens": 90, "total_tokens": 1890}}}}`,
	}
	writeAt(t, path, []byte(strings.Join(lines, "\n")+"\n"))

	snap := SessionUsageSnapshot(HarnessCodex, sessionID, "")
	if !snap.Available {
		t.Fatalf("snapshot unavailable: %s (the skipped event must not poison the running cumulative)", snap.UnavailableReason)
	}
	if snap.Turns != 2 {
		t.Fatalf("snapshot.Turns = %d, want 2 (the skipped event must not count as a turn)", snap.Turns)
	}
	if len(snap.TurnBreakdown) != snap.Turns {
		t.Fatalf("len(TurnBreakdown) = %d, Turns = %d — invariant broken", len(snap.TurnBreakdown), snap.Turns)
	}
	if snap.FreshInputTokens != 1800 || snap.OutputTokens != 90 {
		t.Fatalf("snapshot cumulative = %+v, want fresh=1800 out=90 (from the last VALID event, not the skipped one)", snap)
	}
	if snap.TurnBreakdown[0].FreshInput != 1000 || snap.TurnBreakdown[0].Output != 50 {
		t.Fatalf("TurnBreakdown[0] = %+v, want the first valid turn's own totals (no prior turn to diff against)", snap.TurnBreakdown[0])
	}
	if snap.TurnBreakdown[1].FreshInput != 800 || snap.TurnBreakdown[1].Output != 40 {
		t.Fatalf("TurnBreakdown[1] = %+v, want fresh=800 out=40 (1800-1000, 90-50: diffed against the last VALID turn, not the skipped one)", snap.TurnBreakdown[1])
	}
}

// TestCodexDriverSnapshotUnreadable pins AC4 for codex.
func TestCodexDriverSnapshotUnreadable(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	snap := SessionUsageSnapshot(HarnessCodex, "sess-missing", "")
	if snap.Available {
		t.Fatalf("snapshot = %+v, want Available=false", snap)
	}
	if !strings.HasPrefix(snap.UnavailableReason, "codex:") {
		t.Fatalf("UnavailableReason = %q, want an adapter-named codex: reason", snap.UnavailableReason)
	}
}

// TestSessionUsageSnapshotUnknownHarness pins satelle-agent-agnostic §3:
// nothing unrecognised is assumed to be claude.
func TestSessionUsageSnapshotUnknownHarness(t *testing.T) {
	snap := SessionUsageSnapshot("some-future-cli", "sess-1", "")
	if snap.Available {
		t.Fatalf("snapshot = %+v, want Available=false", snap)
	}
	if strings.Contains(snap.UnavailableReason, "claude") {
		t.Fatalf("UnavailableReason = %q must not default to claude", snap.UnavailableReason)
	}
}
