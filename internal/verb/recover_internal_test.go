package verb

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/lease"
)

// deadPid is the pid of a process that has run and exited.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func recoveryLogAt(now time.Time, ago time.Duration, line string) *recoveryLog {
	at := now.Add(-ago)
	return &recoveryLog{
		Path: "/logs/dispatch/dispatch-coder-1-sty_x.log", Agent: "coder",
		Mtime: at, LastLine: at.UTC().Format(time.RFC3339Nano) + "\t" + line,
	}
}

func TestRenderRecoveryReportHeaderOnEveryUnfinishedReport(t *testing.T) {
	now := time.Now()
	dead := lease.Lease{InFlight: true, InFlightPid: deadPid(t), InFlightAt: now.Add(-time.Hour)}
	cases := map[string]recoveryInput{
		"files":         {Files: []string{"a.go", "b.go"}, Anchor: "0123456789abcdef"},
		"no files":      {Anchor: "0123456789abcdef"},
		"files unknown": {FilesErr: "no engagement baseline"},
		"dead lease":    {Lease: &dead, Files: []string{"a.go"}, Anchor: "0123456789abcdef"},
		"empty log":     {Anchor: "0123456789abcdef"},
	}
	for name, in := range cases {
		in.StoryID, in.Now = "sty_x", now
		in.Log = recoveryLogAt(now, 90*time.Second, "tool_start\ttool=Bash")
		if name == "empty log" {
			in.Log.LastLine = ""
		}
		got := renderRecoveryReport(in)
		for _, want := range []string{"ended WITHOUT a completion", "did NOT commit", "UNVERIFIED"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: report missing %q:\n%s", name, want, got)
			}
		}
		for _, f := range in.Files {
			if !strings.Contains(got, f) {
				t.Errorf("%s: report missing file %q:\n%s", name, f, got)
			}
		}
		if name != "empty log" && !strings.Contains(got, "1m30s") {
			t.Errorf("%s: report missing wall time since the last event:\n%s", name, got)
		}
	}
}

func TestRenderRecoveryReportNeverVouches(t *testing.T) {
	now := time.Now()
	got := renderRecoveryReport(recoveryInput{
		StoryID: "sty_x", Now: now, Files: []string{"a.go"}, Anchor: "abc",
		Log: recoveryLogAt(now, time.Minute, "tool_start\ttool=Bash"),
	})
	for _, claim := range []string{"complete ", "finished", "ready", "done", "verified", "coherent"} {
		// UNVERIFIED and the coherence disclaimer are the two allowed mentions.
		scrubbed := strings.ReplaceAll(strings.ReplaceAll(got, "UNVERIFIED", ""), "does not say whether the slice is coherent", "")
		scrubbed = strings.ReplaceAll(scrubbed, "ended WITHOUT a completion", "")
		if strings.Contains(strings.ToLower(scrubbed), claim) {
			t.Errorf("report vouches for the work (%q):\n%s", claim, got)
		}
	}
}

// A live transition also has no completed line yet; the lease decides, and the
// report then says nothing ended rather than framing a recovery.
func TestRecoveryStateInFlightIsNotARecovery(t *testing.T) {
	now := time.Now()
	live := lease.Lease{InFlight: true, InFlightPid: os.Getpid(), InFlightAt: now}
	in := recoveryInput{StoryID: "sty_x", Now: now, Lease: &live, Log: recoveryLogAt(now, time.Second, "tool_start\ttool=Bash")}
	if got := recoveryState(in); got != recoverStateInFlight {
		t.Fatalf("state = %q, want in-flight", got)
	}
	got := renderRecoveryReport(in)
	if strings.Contains(got, "UNVERIFIED") || !strings.Contains(got, "still in flight") {
		t.Errorf("in-flight report:\n%s", got)
	}

	live.InFlightPid = deadPid(t)
	in.Lease = &live
	if got := recoveryState(in); got != recoverStateEnded {
		t.Errorf("a dead in-flight pid is an ended dispatch, got %q", got)
	}
}

func TestIsUnfinished(t *testing.T) {
	for line, want := range map[string]bool{
		"": true, // an empty log
		"2026-09-30T05:00:00Z\ttool_start\ttool=Bash": true, // the sty_992cffc6 shape
		"2026-09-30T05:00:00Z\tfailed\terror=boom":    true,
		"2026-09-30T05:00:00Z\tcompleted\t":           false,
		"free text with no tabs":                      true,
	} {
		if got := isUnfinished(line); got != want {
			t.Errorf("isUnfinished(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestRecoveryStateNoLog(t *testing.T) {
	in := recoveryInput{StoryID: "sty_x", Now: time.Now()}
	if recoveryState(in) != recoverStateNone {
		t.Fatal("no log means nothing to recover")
	}
	if got := renderRecoveryReport(in); !strings.Contains(got, "no unfinished dispatch found") {
		t.Errorf("report: %s", got)
	}
}
