package cli

// Session-start story-sync advisory (sty_6e5fc41c): one line naming the stories
// whose local and hosted copies disagree and the stories another location holds,
// with each holder and when it was last seen. It reads recorded state only —
// what the last work-state passes wrote — and never dials, so a session start
// never waits on the server. A record older than the mirror freshness window is
// marked unverified rather than presented as current.

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// storySyncMaxIDs caps how many stories the line names before it counts the rest.
const storySyncMaxIDs = 5

// storySyncAdvisoryFor is the seam runHookContext reads the line through; tests
// replace it.
var storySyncAdvisoryFor = storySyncAdvisory

// storySyncAdvisory gathers the recorded conflicts and holds and decides the
// line. It has no error channel: it feeds `satelle hook context`, which must
// always exit 0, so unreadable state is "nothing to report".
func storySyncAdvisory(a *app.App) string {
	if a == nil {
		return ""
	}
	var conflicts map[string]syncstate.StoryConflict
	var snapshotAt time.Time
	if st, ok, _ := syncstate.Load(config.GlobalDir(), a.RepoRoot); ok {
		conflicts, snapshotAt = st.StoryConflicts, st.SnapshotLastSuccess
	}
	var holds map[string]hosted.HoldRecord
	if project := config.ResolveBoundProject(a.Config, a.RepoRoot); project != "" {
		if recorded, err := hosted.LoadHolds(config.ResolveHostedServer(a.Config), project, a.RepoRoot); err == nil {
			self := hosted.KnownLocationID(a.RepoRoot)
			holds = map[string]hosted.HoldRecord{}
			for id, rec := range recorded {
				if rec.Location != self {
					holds[id] = rec
				}
			}
		}
	}
	staleAfter, err := config.HoldStaleAfter(a.Config)
	if err != nil {
		staleAfter = config.DefaultHoldStaleAfter
	}
	return storySyncLine(conflicts, holds, snapshotAt, staleAfter, time.Now())
}

// storySyncLine is the DECISION: one advisory line, "" when no story is in
// conflict or held elsewhere. Conflicts come first, then holds, each in id
// order; at most storySyncMaxIDs stories are named. A holder is marked presumed
// dead when its last-seen is older than staleAfter. The line is marked
// unverified when the oldest record it rests on (a hold's check time, or the
// last good snapshot for the conflicts) is older than the mirror freshness
// window — or unknown.
func storySyncLine(conflicts map[string]syncstate.StoryConflict, holds map[string]hosted.HoldRecord, snapshotAt time.Time, staleAfter time.Duration, now time.Time) string {
	if len(conflicts) == 0 && len(holds) == 0 {
		return ""
	}
	var entries []string
	var oldest time.Time
	known := true
	note := func(t time.Time) {
		if t.IsZero() {
			known = false
		} else if oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
	}
	if len(conflicts) > 0 {
		note(snapshotAt)
	}
	for _, id := range slices.Sorted(maps.Keys(conflicts)) {
		entries = append(entries, fmt.Sprintf("%s in conflict (%s)", id, conflicts[id].Reason))
	}
	for _, id := range slices.Sorted(maps.Keys(holds)) {
		rec := holds[id]
		note(rec.CheckedAt)
		entries = append(entries, fmt.Sprintf("%s held by remote %s, %s", id, holderName(rec), lastSeenFor(rec, staleAfter, now)))
	}
	more := ""
	if len(entries) > storySyncMaxIDs {
		more = fmt.Sprintf(" (+%d more)", len(entries)-storySyncMaxIDs)
		entries = entries[:storySyncMaxIDs]
	}
	line := "⚠️ satelle: story sync — " + strings.Join(entries, "; ") + more
	if !known || now.Sub(oldest) > mirror.StaleAfter {
		when := "never"
		if known {
			when = oldest.UTC().Format("2006-01-02 15:04 UTC")
		}
		line += fmt.Sprintf(" (unverified (last checked %s))", when)
	}
	return line + " — run `satelle story list`; `satelle story hold takeover <id>` takes a held story once its holder is out of the way."
}

// holderName is how a holder is named: its label, else its location id.
func holderName(rec hosted.HoldRecord) string {
	if rec.Label != "" {
		return rec.Label
	}
	return rec.Location
}

// lastSeenFor renders a holder's last-seen with its age, and marks it presumed
// dead when the silence outlasts staleAfter.
func lastSeenFor(rec hosted.HoldRecord, staleAfter time.Duration, now time.Time) string {
	if strings.TrimSpace(rec.LastSeen) == "" {
		return "last seen unknown"
	}
	dead, age := hosted.HoldPresumedDead(rec.LastSeen, staleAfter, now)
	out := fmt.Sprintf("last seen %s (%s ago)", rec.LastSeen, age.Round(time.Minute))
	if dead {
		out += ", presumed dead"
	}
	return out
}

// joinAdvisoryLines joins the non-empty advisory lines into the one session
// notice, newest concern last; "" when all are empty.
func joinAdvisoryLines(lines ...string) string {
	var kept []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}
