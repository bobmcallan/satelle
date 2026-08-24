package verb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/logsread"
)

// TestRouteOutcomeRoundTrip: renderOutcome WRITES the outcome heading and
// walkedFromOutcomes READS it, so the two must be pinned together (sty_6e4f7fd8).
// Without this a heading tweak would make the reader return nothing — the drift
// guard would report "no drift" forever and no test would fail.
func TestRouteOutcomeRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC)
	var b strings.Builder
	b.WriteString(routeOutcomesHeading + "\n")
	b.WriteString(renderOutcome("sty_round01", "backlog", "plan", []ReviewerVerdict{{Skill: "s", Accept: true}}, nil, now))
	b.WriteString(renderOutcome("sty_round01", "plan", "in_progress", nil, nil, now))
	b.WriteString(renderOutcome("sty_round01", "in_progress", "integration", []ReviewerVerdict{{Skill: "s", Accept: false, Notes: "no"}}, nil, now))

	got := walkedFromOutcomes(b.String())
	want := []string{"backlog", "plan", "in_progress", "integration"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("walked = %v, want %v — renderOutcome's heading and walkedFromOutcomes have drifted apart", got, want)
	}
}

func TestWalkedFromOutcomesEdges(t *testing.T) {
	// No outcome half at all (a story that has never transitioned).
	if got := walkedFromOutcomes("# Route\n\nsome plan half\n"); got != nil {
		t.Errorf("never-transitioned story walked = %v, want nil", got)
	}
	// A rejected edge still counts as walked-from, and the em dash inside the
	// result must not be mistaken for the separator.
	body := routeOutcomesHeading + "\n\n### release → done — rejected · 2026-08-06T00:00:00Z\n\n- **x** — REJECT\n"
	if got := strings.Join(walkedFromOutcomes(body), ","); got != "release,done" {
		t.Errorf("walked = %q, want release,done", got)
	}
}

func TestDispatchLogRelFromPointer(t *testing.T) {
	now := time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC)
	repo := t.TempDir()
	rt := t.TempDir()
	disp := filepath.Join(rt, "dispatch")
	if err := os.MkdirAll(disp, 0o755); err != nil {
		t.Fatal(err)
	}
	name := logsread.FormatDispatchName("planner", "sty_round01", now.UnixNano()-1)
	if err := os.WriteFile(filepath.Join(disp, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rt, filepath.Join(repo, ".satelle", "logs")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	got := dispatchLogRel("sty_round01", now)
	want := ".satelle/logs/dispatch/" + name
	if got != want {
		t.Fatalf("log rel = %q, want %q", got, want)
	}
	body := renderOutcome("sty_round01", "backlog", "plan", []ReviewerVerdict{{Skill: "s", Accept: true}}, nil, now)
	if !strings.Contains(body, "- log: `"+want+"`") {
		t.Fatalf("renderOutcome missing log clause:\n%s", body)
	}
	if err := os.Remove(filepath.Join(disp, name)); err != nil {
		t.Fatal(err)
	}
	if got := dispatchLogRel("sty_round01", now); got != "" {
		t.Fatalf("empty dispatch dir must omit clause, got %q", got)
	}
}
