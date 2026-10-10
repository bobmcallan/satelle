package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/trunk"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Start-of-work trunk check wiring (sty_9f3e51d1): the [trunk] table and the
// invoking tree reach the verb through package globals, the same shape as
// SetWorktreeConfig. An empty trunkRepo is the unwired state: the check does
// nothing and the engagement baseline keeps reading the process's working
// directory.
var (
	trunkCfg  config.TrunkConfig
	trunkRepo string
	// trunkOut is where the engage-time report is printed. Stderr, like the
	// other engage-time notices (hold.go), so `--json` stdout stays parseable.
	trunkOut io.Writer = os.Stderr
)

// SetTrunkConfig wires the repo's [trunk] declaration and the invoking tree the
// check runs in and the engagement baseline is taken in.
func SetTrunkConfig(cfg config.TrunkConfig, repoRoot string) {
	trunkCfg = cfg
	trunkRepo = repoRoot
}

// ClearTrunkConfig resets the wiring (tests).
func ClearTrunkConfig() {
	trunkCfg, trunkRepo = config.TrunkConfig{}, ""
}

// SetTrunkOutput redirects the engage-time report; nil restores stderr (tests).
func SetTrunkOutput(w io.Writer) {
	if w == nil {
		w = os.Stderr
	}
	trunkOut = w
}

// engageRepoDir is the directory an engage inspects: the wired invoking tree,
// else the process's working directory. The trunk check and the engagement
// baseline both read it, so a fast-forward and the HEAD recorded after it can
// never be about two different trees.
func engageRepoDir() string {
	if trunkRepo != "" {
		return trunkRepo
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// checkTrunkAtEngage runs the start-of-work trunk check for an item about to
// enter the engaging state `to` for the first time — the condition under which
// maybeRecordEngagementBaseline records a baseline — and fast-forwards a behind
// trunk. Anything but a level (or nothing-to-compare) trunk is printed to
// trunkOut; for an existing story it is also ledgered. An error is returned
// only for a state the [trunk] refuse set names, after the line is printed;
// the caller dispatches nothing then. edge names the move in that error.
//
// It is a no-op when the seam is unwired, the check is switched off, the item
// is not a story, `to` is not engaging, or a baseline already exists (a
// park/resume re-entry).
func checkTrunkAtEngage(ctx context.Context, item workitem.Item, to, edge string) (trunk.Report, error) {
	if trunkRepo == "" || !trunkCfg.Enabled() || item.Kind != workitem.KindStory {
		return trunk.Report{}, nil
	}
	if engaging, ok := storyStatusIsEngaging(ctx, item, to); !ok || !engaging {
		return trunk.Report{}, nil
	}
	if item.ID != "" && hasEngagementBaseline(ctx, item.ID) {
		return trunk.Report{}, nil
	}
	rep := trunk.Check(ctx, trunkRepo, trunk.Options{Branch: trunkCfg.Branch, FastForward: true})
	if rep.Quiet() {
		return rep, nil
	}
	fmt.Fprintln(trunkOut, rep.Line())
	if item.ID != "" {
		recordTrunkCheck(ctx, item.ID, rep, time.Now())
	}
	// A behind trunk that was brought in is resolved, not found.
	if !trunkCfg.Refuses(string(rep.State)) || rep.FastForwarded {
		return rep, nil
	}
	return rep, fmt.Errorf("%s refused: trunk %s — %s", edge, rep.Detail(), rep.Hint())
}

// recordTrunkCheck ledgers what the check found. Level, skipped and a check
// that did not run write nothing.
func recordTrunkCheck(ctx context.Context, storyID string, rep trunk.Report, now time.Time) {
	if rep.Quiet() {
		return
	}
	payload, _ := json.Marshal(rep)
	appendLedgerEntry(ctx, storyID, ledger.KindTrunkCheck, "executor", rep.Line(), payload, now)
}
