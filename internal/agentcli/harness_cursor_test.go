package agentcli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cursorCaptureEnv reads a captured KEY=VALUE environment dump from
// testdata/cursor (real cursor-agent 2026.10.01 hook children).
func cursorCaptureEnv(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "cursor", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var env []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); strings.Contains(line, "=") {
			env = append(env, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(env) == 0 {
		t.Fatalf("%s: no KEY=VALUE lines", name)
	}
	return env
}

// AC1: cursor's own markers are detected, also beside an inherited CLAUDECODE=1.
func TestCursorEnvIsDetectedAsCursor(t *testing.T) {
	for _, name := range []string{"clean-6-hook-env.txt", "6-hook-env.txt"} {
		env := cursorCaptureEnv(t, name)
		if got, ok := InLoopHarnessFromEnv(env); got != HarnessCursor || !ok {
			t.Errorf("%s: InLoopHarnessFromEnv = (%q,%v), want (cursor,true)", name, got, ok)
		}
		if !DetectSessionHarnesses(env)[HarnessCursor] {
			t.Errorf("%s: DetectSessionHarnesses lacks cursor", name)
		}
	}
	// The mixed capture really does carry an inherited claude marker; cursor wins.
	mixed := cursorCaptureEnv(t, "6-hook-env.txt")
	if !DetectSessionHarnesses(mixed)[HarnessClaude] {
		t.Fatal("6-hook-env.txt should carry the inherited CLAUDECODE=1 this test is about")
	}
	for name, env := range map[string][]string{
		"CLAUDECODE + CURSOR_AGENT":      {"CLAUDECODE=1", "CURSOR_AGENT=1"},
		"CLAUDECODE + CURSOR_INVOKED_AS": {"CLAUDECODE=1", "CURSOR_INVOKED_AS=cursor-agent"},
		"CURSOR_AGENT alone":             {"CURSOR_AGENT=1"},
		"CURSOR_INVOKED_AS alone":        {"CURSOR_INVOKED_AS=cursor-agent"},
	} {
		if got, ok := InLoopHarnessFromEnv(env); got != HarnessCursor || !ok {
			t.Errorf("%s: InLoopHarnessFromEnv = (%q,%v), want (cursor,true)", name, got, ok)
		}
	}
}

// AC1/AC5: nothing that merely looks like cursor is evidence of it.
func TestCursorLookalikesAreNotCursor(t *testing.T) {
	for name, env := range map[string][]string{
		"CLAUDE_PROJECT_DIR only":  {"CLAUDE_PROJECT_DIR=/SCRATCH/sb8"},
		"CURSOR_AGENT=0":           {"CURSOR_AGENT=0"},
		"CURSOR_AGENT empty":       {"CURSOR_AGENT="},
		"CURSOR_INVOKED_AS empty":  {"CURSOR_INVOKED_AS="},
		"desktop cursor variables": {"GUM_CHOOSE_CURSOR_BACKGROUND=212", "XCURSOR_SIZE=24", "HYPRCURSOR_SIZE=24"},
		"CURSOR_VERSION alone":     {"CURSOR_VERSION=2026.10.01-e373342"},
		"CURSOR_PROJECT_DIR alone": {"CURSOR_PROJECT_DIR=/SCRATCH/sb8"},
	} {
		if got, ok := InLoopHarnessFromEnv(env); ok {
			t.Errorf("%s: InLoopHarnessFromEnv = (%q,true), want no harness", name, got)
		}
		if got := DetectSessionHarnesses(env); len(got) != 0 {
			t.Errorf("%s: DetectSessionHarnesses = %v, want none", name, got)
		}
	}
}

// AC4: the other harnesses' environments resolve as before.
func TestCursorDoesNotDisturbOtherEnvs(t *testing.T) {
	for name, tc := range map[string]struct {
		env  []string
		want string
	}{
		"claude":      {[]string{"CLAUDECODE=1"}, HarnessClaude},
		"claude code": {[]string{"CLAUDE_CODE_ENTRYPOINT=cli"}, HarnessClaude},
		"grok":        {[]string{"GROK_AGENT=1"}, HarnessGrok},
		"pi":          {[]string{"PI_CODING_AGENT=1"}, HarnessPi},
		"claude+grok": {[]string{"CLAUDECODE=1", "GROK_AGENT=1"}, HarnessClaude},
	} {
		if got, ok := InLoopHarnessFromEnv(tc.env); got != tc.want || !ok {
			t.Errorf("%s: InLoopHarnessFromEnv = (%q,%v), want (%s,true)", name, got, ok, tc.want)
		}
	}
}

// AC2: every complete envelope in the captured cursor hook logs is cursor,
// including the Read preToolUse that carries a Claude-style tool name.
func TestCursorHookEnvelopesAreCursor(t *testing.T) {
	// 47 lines carry cursor_version, but the capture truncates hook log lines
	// to 600 characters (testdata/cursor/README.md), so only 16 parse whole.
	const minParsed = 16
	logs, err := filepath.Glob(filepath.Join("testdata", "cursor", "*-hooks.log"))
	if err != nil || len(logs) == 0 {
		t.Fatalf("no cursor hook logs: %v", err)
	}
	parsed, sawRead := 0, false
	for _, path := range logs {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			_, payload, ok := strings.Cut(line, "\t")
			if !ok || !json.Valid([]byte(payload)) {
				continue // empty or truncated by the capture
			}
			parsed++
			if got := HarnessFromHookEvent([]byte(payload)); got != HarnessCursor {
				t.Errorf("%s:%d: HarnessFromHookEvent = %q, want cursor", filepath.Base(path), i+1, got)
			}
			var env struct {
				ToolName string `json:"tool_name"`
				Event    string `json:"hook_event_name"`
			}
			_ = json.Unmarshal([]byte(payload), &env)
			if filepath.Base(path) == "4-hooks.log" && env.Event == "preToolUse" && env.ToolName == "Read" {
				sawRead = true
			}
		}
	}
	if parsed < minParsed {
		t.Errorf("parsed %d envelopes, want at least %d", parsed, minParsed)
	}
	if !sawRead {
		t.Error("4-hooks.log preToolUse Read envelope was not among the parsed envelopes")
	}
}

// AC5: an envelope without cursor_version keeps its classification.
func TestEnvelopeWithoutCursorVersionIsUnchanged(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"claude Read, no cursor_version": {`{"tool_name":"Read","tool_input":{"file_path":"/x"}}`, HarnessClaude},
		"empty cursor_version":           {`{"cursor_version":"","tool_name":"Read","tool_input":{}}`, HarnessClaude},
		"unrelated":                      {`{"foo":1}`, HarnessUnknown},
		"empty cursor_version only":      {`{"cursor_version":"  "}`, HarnessUnknown},
	} {
		if got := HarnessFromHookEvent([]byte(tc.raw)); got != tc.want {
			t.Errorf("%s: HarnessFromHookEvent = %q, want %q", name, got, tc.want)
		}
	}
}

// AC3: cursor has its own facts row, with cursor-named unavailables.
func TestCursorFactsRow(t *testing.T) {
	f := FactsFor(HarnessCursor)
	if f.Harness != HarnessCursor || f.BackgroundCutoff != 30*time.Second {
		t.Fatalf("cursor row = %q cutoff %s, want cursor / 30s", f.Harness, f.BackgroundCutoff)
	}
	floor := FactsFor("someharness")
	if f.CutoffBasis == floor.CutoffBasis {
		t.Error("cursor cutoff basis is the conservative floor's")
	}
	for _, want := range []string{"timeout=30000", "TIMEOUT_BEHAVIOR_BACKGROUND", "testdata/cursor"} {
		if !strings.Contains(f.CutoffBasis, want) {
			t.Errorf("cutoff basis lacks %q: %s", want, f.CutoffBasis)
		}
	}
	// sty_2439f4fd: an interactive session's stop hook delivers the verdict; print
	// mode, which dispatches no stop event, is a cursor-named unavailability with
	// the foreground fallback.
	if !f.CompletionNotification.Available {
		t.Errorf("cursor completion notification must be available on an interactive session: %+v", f.CompletionNotification)
	}
	capN, _ := StopCapFor(HarnessCursor)
	for _, want := range []string{"followup_message", "cap 4", "20-cap", "unavailable: cursor: print mode (-p)", "4-hooks.log", "holds the foreground"} {
		if !strings.Contains(f.InTurnWake, want) {
			t.Errorf("in-turn wake lacks %q: %s", want, f.InTurnWake)
		}
	}
	if capN != 4 {
		t.Errorf("recorded cursor cap = %d, the row says 4", capN)
	}
	for name, text := range map[string]string{"in-turn wake": f.InTurnWake, "completion notification": f.CompletionNotification.Reason} {
		if strings.Contains(text, "until sty_2439f4fd") {
			t.Errorf("%s still says the delivery is pending: %s", name, text)
		}
	}
	if f.PromptContext.Available || !strings.Contains(f.PromptContext.Reason, "cursor") {
		t.Errorf("prompt context = %+v, want a cursor-named unavailable", f.PromptContext)
	}
	if f.SettleNotifyOnly {
		t.Error("nothing proves cursor's stop is notification-only; SettleNotifyOnly must stay false")
	}
	if got := f.SessionContextCell(); !strings.Contains(got, "unavailable: cursor:") {
		t.Errorf("session-context cell = %q, want a cursor-named unavailable", got)
	}
	// 30s cutoff drives the wait bound below the cutoff.
	if got := AgentWaitBound([]string{HarnessCursor}); got != 25*time.Second {
		t.Errorf("AgentWaitBound(cursor) = %s, want 25s", got)
	}
}

// AC3: a cursor session with no recorded stop (print mode emits none) is a
// cursor-named unavailable that gives its cause — cursor now has a reader, so it
// is not the "no driver-usage reader" tail (sty_a3258bb3).
func TestCursorSessionUsageIsNamedUnavailable(t *testing.T) {
	t.Setenv("SATELLE_CURSOR_USAGE_DIR", t.TempDir())
	snap := SessionUsageSnapshot(HarnessCursor, "sess-1", t.TempDir())
	if snap.Available || snap.UnavailableReason != cursorNoStopReason {
		t.Fatalf("snapshot = %+v, want the cursor-named no-stop reason", snap)
	}
	if !strings.HasPrefix(snap.UnavailableReason, "cursor:") || !strings.Contains(snap.UnavailableReason, "print mode") {
		t.Errorf("reason = %q, want it to name cursor and the cause", snap.UnavailableReason)
	}
	if IsNoDriverReaderReason(snap.UnavailableReason) {
		t.Error("cursor has a driver-usage reader; its reason is not a no-reader reason")
	}
}
