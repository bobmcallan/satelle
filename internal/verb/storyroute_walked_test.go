package verb

import (
	"strings"
	"testing"
	"time"
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
