package cli

import (
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// recordStoryConflicts persists what a pass found about story disagreements, for
// the session-start advisory to read (sty_6e5fc41c). It writes even when the
// pass failed: a refused pull is exactly the conflict to record.
func recordStoryConflicts(a *app.App, rep passReport) {
	_ = syncstate.RecordStoryConflicts(config.GlobalDir(), a.RepoRoot, rep.Stories.Conflicts, rep.Stories.Cleared, time.Now().UTC())
}
