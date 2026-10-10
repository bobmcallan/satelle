package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

// capturedCursorHooks returns the payloads of the given hook event from the
// captured interactive cursor session (agentcli testdata 7-stop-hooks.log). The
// capture truncated lines at 600 bytes, inside transcript_path, after every field
// read here; an unterminated line is closed there so the captured values parse.
func capturedCursorHooks(t *testing.T, event string) [][]byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "agentcli", "testdata", "cursor", "7-stop-hooks.log"))
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for _, line := range strings.Split(string(b), "\n") {
		ev, body, ok := strings.Cut(line, "\t")
		if !ok || ev != event {
			continue
		}
		if !strings.HasSuffix(body, "}") {
			if i := strings.Index(body, `,"transcript_path"`); i >= 0 {
				body = body[:i] + "}"
			}
		}
		out = append(out, []byte(body))
	}
	if len(out) == 0 {
		t.Fatalf("no captured %s payload", event)
	}
	return out
}

// cursorUsageRepo isolates the stop-usage record and stamps the session; call it
// after tempRepo/stopWakeRepo, which reset the session and the satelle home.
func cursorUsageRepo(t *testing.T, session string) {
	t.Helper()
	clearHarnessEnv(t)
	t.Setenv("SATELLE_CURSOR_USAGE_DIR", t.TempDir())
	t.Setenv(config.SessionEnv, session)
	prev := hookHarnessFlag
	hookHarnessFlag = ""
	t.Cleanup(func() { hookHarnessFlag = prev })
}

// AC4: each stop is recorded once, and the session's driver spend reads them back.
func TestStopcheckRecordsCursorStopUsage(t *testing.T) {
	const session = "cursor-sess"
	_ = tempRepo(t)
	cursorUsageRepo(t, session)

	stops := capturedCursorHooks(t, "stop")
	var out strings.Builder
	for _, raw := range append(stops, stops[0]) { // the replay is the same turn
		out.Reset()
		if err := runHookStopcheck(raw, &out); err != nil {
			t.Fatal(err)
		}
	}
	snap := agentcli.SessionUsageSnapshot(agentcli.HarnessCursor, session, "")
	if !snap.Available || snap.Turns != 2 || len(snap.TurnBreakdown) != 2 || snap.Model != "composer-2.5" {
		t.Fatalf("snapshot = %+v, want two recorded turns on composer-2.5", snap)
	}
	if snap.FreshInputTokens+snap.CacheReadInputTokens != 38919 || snap.CacheReadInputTokens != 29620 || snap.OutputTokens != 289 {
		t.Errorf("tokens = fresh %d read %d out %d", snap.FreshInputTokens, snap.CacheReadInputTokens, snap.OutputTokens)
	}
}

// AC4: a turn that ends on a delivered verdict is still a recorded turn.
func TestStopcheckRecordsCursorStopWhenAVerdictIsDelivered(t *testing.T) {
	const session = "cursor-sess"
	store := stopWakeRepo(t, "0s")
	cursorUsageRepo(t, session)
	m := runningGateForSession(t, store, "sty_done", session)
	finishGateID(t, store, m.ID, "accepted plan→in_progress")

	var out strings.Builder
	if err := runHookStopcheck(capturedCursorHooks(t, "stop")[0], &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), m.ID) {
		t.Fatalf("the verdict was not delivered by this stop (the early-return path): %q", out.String())
	}
	snap := agentcli.SessionUsageSnapshot(agentcli.HarnessCursor, session, "")
	if !snap.Available || snap.Turns != 1 || snap.OutputTokens != 266 {
		t.Fatalf("snapshot = %+v, want the delivered-verdict turn recorded", snap)
	}
}

// A dispatched cursor child inherits the driver's session and must not add its
// spend to the driver's record.
func TestStopcheckDoesNotRecordADispatchedCursorStop(t *testing.T) {
	const session = "cursor-sess"
	_ = tempRepo(t)
	cursorUsageRepo(t, session)
	t.Setenv(config.DispatchAgentEnv, "performer")

	var out strings.Builder
	if err := runHookStopcheck(capturedCursorHooks(t, "stop")[0], &out); err != nil {
		t.Fatal(err)
	}
	if snap := agentcli.SessionUsageSnapshot(agentcli.HarnessCursor, session, ""); snap.Available {
		t.Fatalf("a dispatched stop was recorded against the driver: %+v", snap)
	}
}

// AC5: the model a cursor hook payload names is the session's in-loop model, with
// executable cursor — from the captured preToolUse and stop payloads alike.
func TestCursorInLoopModelFromPayload(t *testing.T) {
	_ = tempRepo(t)
	cursorUsageRepo(t, "")
	_ = os.Unsetenv(config.SessionEnv)
	for _, event := range []string{"preToolUse", "stop"} {
		raw := capturedCursorHooks(t, event)[0]
		if model, reason := resolveInLoopModel(raw, agentcli.HarnessCursor); model != "composer-2.5" || reason != "" {
			t.Errorf("%s: resolveInLoopModel = %q (%q), want composer-2.5", event, model, reason)
		}
		sid := bindSessionID(raw)
		if sid == "" {
			t.Fatalf("%s: no session id bound from the payload", event)
		}
		model, exe, reason := config.ResolveSessionModel(sid, verb.SessionModelRoleInLoop)
		if model != "composer-2.5" || exe != agentcli.HarnessCursor || reason != "" {
			t.Errorf("%s: published in-loop model = %q / %q (%q), want composer-2.5 / cursor", event, model, exe, reason)
		}
	}
}
