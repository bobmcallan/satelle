package agentcli

import (
	"reflect"
	"strings"
	"testing"
)

func TestStopResumeIsRecordedForGrokOnly(t *testing.T) {
	r, ok := StopResumeFor(HarnessGrok)
	if !ok || r.Cap != 8 || r.Basis == "" {
		t.Fatalf("grok: %+v ok=%v", r, ok)
	}
	for _, h := range []string{HarnessClaude, HarnessPi, HarnessUnknown, "", "other"} {
		if _, ok := StopResumeFor(h); ok {
			t.Errorf("%q was given a resume path nobody recorded", h)
		}
		if got := StopResumeUnavailable(h); !strings.HasPrefix(got, "unavailable: ") {
			t.Errorf("%q: reason %q is not an explicit unavailable", h, got)
		}
	}
	if got := StopResumeUnavailable("claude"); !strings.Contains(got, "claude") {
		t.Errorf("reason does not name the harness: %s", got)
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
