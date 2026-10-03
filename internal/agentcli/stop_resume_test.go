package agentcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The captured grok dogfood (testdata/hooks/grok_resume_dogfood.json): resuming
// a finished session with a new prompt runs a fresh turn in the SAME session.
// This is the grok-side evidence a stand-in binary cannot give (sty_eac9b28d AC4).
func TestGrokResumeDogfoodShowsAFreshTurnInTheSameSession(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "hooks", "grok_resume_dogfood.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Command   string `json:"command"`
		SessionID string `json:"sessionId"`
		PriorTurn struct {
			Text string `json:"text"`
		} `json:"priorTurn"`
		Resume struct {
			Prompt     string `json:"prompt"`
			Text       string `json:"text"`
			SessionID  string `json:"sessionId"`
			StopReason string `json:"stopReason"`
		} `json:"resume"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	const sid = "01a1017e-bf68-7110-8006-d3dc51df44d9"
	if d.SessionID != sid || d.Resume.SessionID != sid {
		t.Errorf("session id = %q / %q, want %s in both", d.SessionID, d.Resume.SessionID, sid)
	}
	if !strings.Contains(d.Command, sid) {
		t.Errorf("command does not resume session %s: %s", sid, d.Command)
	}
	if d.Resume.Prompt != "Reply with the single word resumed." || !strings.Contains(d.Command, d.Resume.Prompt) {
		t.Errorf("resume prompt not captured: %q in %q", d.Resume.Prompt, d.Command)
	}
	if d.Resume.Text != "resumed" || d.Resume.StopReason != "end_turn" {
		t.Errorf("fresh turn = %q (%s), want %q (end_turn)", d.Resume.Text, d.Resume.StopReason, "resumed")
	}
	if d.PriorTurn.Text != "hello" {
		t.Errorf("prior turn = %q, want the earlier %q turn the resume continues", d.PriorTurn.Text, "hello")
	}
}

func TestStopResumeIsRecordedForGrokOnly(t *testing.T) {
	r, ok := StopResumeFor(HarnessGrok)
	if !ok || r.Cap != 8 || r.Basis == "" {
		t.Fatalf("grok: %+v ok=%v", r, ok)
	}
	for _, h := range []string{HarnessPi, HarnessUnknown, "", "other"} {
		if _, ok := StopResumeFor(h); ok {
			t.Errorf("%q was given a resume path nobody recorded", h)
		}
		if got := StopResumeUnavailable(h); !strings.HasPrefix(got, "unavailable: ") {
			t.Errorf("%q: reason %q is not an explicit unavailable", h, got)
		}
	}
	if got := StopResumeUnavailable("other"); !strings.Contains(got, "other") {
		t.Errorf("reason does not name the harness: %s", got)
	}
}

// claude records its own budget and resume form beside grok's (sty_7e4393fc).
func TestClaudeStopResumeIsRecorded(t *testing.T) {
	t.Setenv("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", "")
	r, ok := StopResumeFor(HarnessClaude)
	if !ok || r.Cap != 8 || r.Basis == "" {
		t.Fatalf("claude: %+v ok=%v", r, ok)
	}
	if got, want := r.Argv("sid", "the verdict", ""), []string{"claude", "-p", "the verdict", "--resume", "sid"}; !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
	if got, want := r.Argv("sid", "p", "acceptEdits"), []string{"claude", "-p", "p", "--resume", "sid", "--permission-mode", "acceptEdits"}; !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

// The cap follows the environment the harness enforces it from, so a low cap
// set for a dogfood is the one the hook counts against; junk leaves the default.
func TestClaudeStopCapFollowsTheEnvironment(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int
	}{{"2", 2}, {" 3 ", 3}, {"", 8}, {"0", 8}, {"-1", 8}, {"many", 8}} {
		t.Setenv("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", c.env)
		if r, _ := StopResumeFor(HarnessClaude); r.Cap != c.want {
			t.Errorf("cap with %q = %d, want %d", c.env, r.Cap, c.want)
		}
	}
	t.Setenv("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", "2")
	if r, _ := StopResumeFor(HarnessGrok); r.Cap != 8 {
		t.Errorf("grok cap = %d: claude's override leaked", r.Cap)
	}
}

// The resumed claude is not mistaken for a child of the session it resumes, but
// keeps its budget override and its credentials.
func TestClaudeResumeEnvKeepsBudgetAndCredentials(t *testing.T) {
	env := []string{"PATH=/usr/bin", "CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_STOP_HOOK_BLOCK_CAP=2", "CLAUDE_CODE_OAUTH_TOKEN=t", "GROK_AGENT=1"}
	got := ResumeEnv(HarnessClaude, env)
	want := []string{"PATH=/usr/bin", "CLAUDE_CODE_STOP_HOOK_BLOCK_CAP=2", "CLAUDE_CODE_OAUTH_TOKEN=t", "GROK_AGENT=1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("env = %q, want %q", got, want)
	}
}

// The documented headless resume: -p carries the prompt, --resume the session.
func TestGrokResumeArgv(t *testing.T) {
	r, _ := StopResumeFor(HarnessGrok)
	if got, want := r.Argv("sid", "the verdict", ""), []string{"grok", "-p", "the verdict", "--resume", "sid"}; !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
	if got, want := r.Argv("sid", "p", "bypassPermissions"), []string{"grok", "-p", "p", "--resume", "sid", "--permission-mode", "bypassPermissions"}; !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestResumeEnvDropsOnlyThatHarnessesMarkers(t *testing.T) {
	env := []string{"PATH=/usr/bin", "GROK_AGENT=1", "CLAUDECODE=1", "HOME=/h", "PI_SESSION_ID=x"}
	got := ResumeEnv(HarnessGrok, env)
	want := []string{"PATH=/usr/bin", "CLAUDECODE=1", "HOME=/h", "PI_SESSION_ID=x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("env = %q, want %q", got, want)
	}
	if got := ResumeEnv(HarnessGrok, []string{"GROK_AGENT=0", "A=b"}); len(got) != 2 {
		t.Errorf("an unset marker (0) was dropped: %q", got)
	}
}
