package serve

import (
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// recordFunc is the Record hook shape the Pusher and the Reconciler call after
// each pass.
type recordFunc func(repoPath string, success bool, reason string)

// recordPushFunc records a hosted-push pass with the repo's work-state scope.
func recordPushFunc(home string) recordFunc {
	return scopedRecord(home, syncstate.RecordPush)
}

// recordSnapshotFunc records a snapshot pass with the repo's work-state scope.
func recordSnapshotFunc(home string) recordFunc {
	return scopedRecord(home, syncstate.RecordSnapshot)
}

// scopedRecord resolves the repo's work-state scope on every pass. A local repo's
// pass pushed nothing, so only the scope is recorded: never a success, and an
// older failure is left for the UI to hide behind the local marker.
func scopedRecord(home string, leg func(globalDir, repoPath string, success bool, reason, scope string, now time.Time) error) recordFunc {
	return func(repoPath string, success bool, reason string) {
		scope := repoWorkstateScope(repoPath)
		if scope == config.LocalScope.String() {
			_ = syncstate.RecordScope(home, repoPath, scope)
			return
		}
		_ = leg(home, repoPath, success, reason, scope, time.Now())
	}
}

// repoWorkstateScope is the work-state scope of the repo at root, or "" when its
// config cannot be read — an unknown scope keeps the recorded one.
func repoWorkstateScope(root string) string {
	cfg, _, _, err := config.LoadRepoProcess(root)
	if err != nil {
		return ""
	}
	ws, err := config.WorkstateScope(cfg)
	if err != nil {
		return ""
	}
	return ws.String()
}
