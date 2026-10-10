package agentcli

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// sty_2439f4fd: a gate started from a cursor Shell call and cursor's stop hook of
// the same conversation resolve one session id, and a turn's stop budget is the
// one probe 20 measured.

const probe21ConversationID = "17e98b90-f524-4ba9-a53e-eddfcff87713"

// AC1: the Shell child's CURSOR_CONVERSATION_ID and the stop payload's
// conversation_id (and session_id) of probe 21 are one id, each read by the cursor
// adapter.
func TestCursorSessionIDFromProbe21(t *testing.T) {
	shellEnv := strings.Fields(cursorRead(t, "21-shell-env.txt"))
	if got := SessionIDFromEnv(shellEnv); got != probe21ConversationID {
		t.Errorf("SessionIDFromEnv(21-shell-env.txt) = %q, want %q", got, probe21ConversationID)
	}
	stop := strings.TrimSpace(cursorRead(t, "21-stop.log"))
	if got := HookSessionID([]byte(stop)); got != probe21ConversationID {
		t.Errorf("HookSessionID(21-stop.log) = %q, want %q", got, probe21ConversationID)
	}
	if !strings.Contains(stop, `"session_id": "`+probe21ConversationID+`"`) {
		t.Errorf("the capture's session_id is not the conversation id: %s", stop)
	}
}

// The hook side prefers conversation_id and falls back to session_id on a payload
// that names itself cursor's; nothing else is claimed.
func TestCursorHookSessionID(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"conversation_id wins":            {`{"conversation_id":"conv","session_id":"sess","cursor_version":"x"}`, "conv"},
		"conversation_id without version": {`{"conversation_id":" conv "}`, "conv"},
		"session_id fallback":             {`{"session_id":"sess","cursor_version":"2026.10.01"}`, "sess"},
		"claude payload is not claimed":   {`{"session_id":"sess","transcript_path":"/h/.claude/p/s.jsonl"}`, ""},
		"grok payload is not claimed":     {`{"sessionId":"sess","hookEventName":"Stop"}`, ""},
		"not JSON":                        {`nope`, ""},
		"empty":                           {``, ""},
	} {
		if got := HookSessionID([]byte(tc.raw)); got != tc.want {
			t.Errorf("%s: HookSessionID(%s) = %q, want %q", name, tc.raw, got, tc.want)
		}
	}
}

// Nothing but the cursor adapter answers from the environment.
func TestSessionIDFromEnvOnlyCursorAnswers(t *testing.T) {
	for name, env := range map[string][]string{
		"none":          nil,
		"claude":        {"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=abc"},
		"grok":          {"GROK_AGENT=1", "GROK_SESSION_ID=abc"},
		"pi":            {"PI_CODING_AGENT=1", "PI_SESSION_ID=abc"},
		"cursor marker": {"CURSOR_AGENT=1", "CURSOR_INVOKED_AS=cursor-agent"},
		"empty id":      {"CURSOR_CONVERSATION_ID= "},
	} {
		if got := SessionIDFromEnv(env); got != "" {
			t.Errorf("%s: SessionIDFromEnv = %q, want none", name, got)
		}
	}
	if got := SessionIDFromEnv([]string{"PATH=/usr/bin", "CURSOR_CONVERSATION_ID= abc "}); got != "abc" {
		t.Errorf("SessionIDFromEnv = %q, want the trimmed id", got)
	}
}

// A caller that wants a bare shell clears the session-id variable with the markers.
func TestSessionMarkerEnvNamesIncludeSessionIDVariable(t *testing.T) {
	found := false
	for _, k := range SessionMarkerEnvNames() {
		found = found || k == "CURSOR_CONVERSATION_ID"
	}
	if !found {
		t.Errorf("SessionMarkerEnvNames lacks the cursor session-id variable: %v", SessionMarkerEnvNames())
	}
}

// loopCounts reads the loop_count of each line of a probe-20 hook log.
func loopCounts(t *testing.T, name string) []int {
	t.Helper()
	re := regexp.MustCompile(`"loop_count":(\d+)`)
	var out []int
	for _, line := range strings.Split(cursorRead(t, name), "\n") {
		if line == "" {
			continue
		}
		m := re.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("%s: no loop_count in %.80s", name, line)
		}
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	return out
}

// AC3: the recorded cursor cap is what probe 20 measured, and the stop timeout the
// scaffold installs is the one probe 20 showed is needed.
func TestCursorStopBudgetMatchesProbe20(t *testing.T) {
	counts := loopCounts(t, "20-cap-hooks.log")
	if want := []int{0, 1, 2, 3, 4}; len(counts) != len(want) {
		t.Fatalf("20-cap stop loop_counts = %v, want %v", counts, want)
	}
	for i, n := range counts {
		if n != i {
			t.Errorf("20-cap stop %d had loop_count %d", i, n)
		}
	}
	last := counts[len(counts)-1]
	if got, ok := StopCapFor(HarnessCursor); !ok || got != last {
		t.Errorf("StopCapFor(cursor) = %d,%v, want the last loop_count a followup took, %d", got, ok, last)
	}
	if strings.TrimSpace(cursorRead(t, "20-cap.count")) != "5" {
		t.Errorf("20-cap.count = %q, want 5 stops", cursorRead(t, "20-cap.count"))
	}
	if !strings.Contains(stopResumes[HarnessCursor].Basis, "20-cap") {
		t.Errorf("the cursor budget does not cite probe 20-cap: %s", stopResumes[HarnessCursor].Basis)
	}

	// Default hook timeout: the 75s stop hook never delivered its followup.
	if got := loopCounts(t, "20-slow-default-hooks.log"); len(got) != 1 || got[0] != 0 {
		t.Errorf("20-slow-default stops = %v, want only loop_count 0", got)
	}
	if strings.Contains(cursorRead(t, "20-slow-default-hooks.json"), "timeout") {
		t.Error("20-slow-default must be the run with no timeout")
	}
	// timeout 1800: the followup landed — a second stop at loop_count 1.
	if got := loopCounts(t, "20-slow-1800-hooks.log"); len(got) != 2 || got[1] != 1 {
		t.Errorf("20-slow-1800 stops = %v, want loop_count 0 then 1", got)
	}
	if !strings.Contains(cursorRead(t, "20-slow-1800-hooks.json"), `"timeout": 1800`) {
		t.Error("20-slow-1800 must be the run with timeout 1800")
	}
}

// AC3: the cap accessor answers for every recorded harness, the resume accessor
// only for one with a resume path, and a payload with no loop_count is never
// held to a cap.
func TestStopCapAndResumeAccessors(t *testing.T) {
	t.Setenv("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", "")
	for h, want := range map[string]int{HarnessCursor: 4, HarnessClaude: 8, HarnessGrok: 8} {
		if got, ok := StopCapFor(h); !ok || got != want {
			t.Errorf("StopCapFor(%s) = %d,%v, want %d", h, got, ok, want)
		}
	}
	for _, h := range []string{HarnessPi, HarnessUnknown, "", "other"} {
		if got, ok := StopCapFor(h); ok {
			t.Errorf("StopCapFor(%q) = %d: a cap nobody recorded", h, got)
		}
	}

	if r, ok := StopResumeFor(HarnessCursor); ok {
		t.Errorf("cursor was given a resume path nobody recorded: %+v", r)
	}
	if got := StopResumeUnavailable(HarnessCursor); !strings.Contains(got, "cursor") || !strings.HasPrefix(got, "unavailable: ") {
		t.Errorf("reason = %q, want a cursor-named unavailable", got)
	}
	for _, h := range []string{HarnessClaude, HarnessGrok} {
		if r, ok := StopResumeFor(h); !ok || r.Argv == nil || r.Cap != 8 {
			t.Errorf("%s resume changed: %+v ok=%v", h, r, ok)
		}
	}
	// The claude override reaches the cap accessor the way it reaches the resume one.
	t.Setenv("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", "3")
	if got, _ := StopCapFor(HarnessClaude); got != 3 {
		t.Errorf("StopCapFor(claude) with the override = %d, want 3", got)
	}
}

func TestStopWithinCap(t *testing.T) {
	t.Setenv("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", "")
	for name, tc := range map[string]struct {
		harness, raw string
		want         bool
	}{
		"cursor 0":                  {HarnessCursor, `{"loop_count":0}`, true},
		"cursor 3":                  {HarnessCursor, `{"loop_count":3}`, true},
		"cursor 4 is the cap":       {HarnessCursor, `{"loop_count":4}`, false},
		"cursor 9":                  {HarnessCursor, `{"loop_count":9}`, false},
		"cursor without loop_count": {HarnessCursor, `{"hook_event_name":"stop"}`, true},
		"claude without loop_count": {HarnessClaude, `{"stop_hook_active":true}`, true},
		"grok without loop_count":   {HarnessGrok, `{"hookEventName":"Stop"}`, true},
		"no cap recorded":           {HarnessUnknown, `{"loop_count":99}`, true},
		"not JSON":                  {HarnessCursor, `nope`, true},
	} {
		if got := StopWithinCap(tc.harness, []byte(tc.raw)); got != tc.want {
			t.Errorf("%s: StopWithinCap(%s, %s) = %v, want %v", name, tc.harness, tc.raw, got, tc.want)
		}
	}
}
