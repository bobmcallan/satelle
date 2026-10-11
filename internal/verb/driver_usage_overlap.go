package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// DriverAttributionAmbiguous is the outcome name for a driver window another story
// shared: the session's usage over it cannot be split, so the row is available but
// not this story's alone. Backfill reports it as a session status and the live
// recorder as the row's Overlaps; costview reads the row as DriverRow.Ambiguous.
const DriverAttributionAmbiguous = "ambiguous"

// driverOverlapReason is the one sentence saying a driver window is shared, used by
// the backfill report and the live row alike.
func driverOverlapReason(executable string, overlaps []string, sessionID string) string {
	return fmt.Sprintf("%s: window overlaps %s in session %s; usage not split", executable, strings.Join(overlaps, ", "), sessionID)
}

// sessionWindowOverlaps names the other stories that drove sessionID and whose own
// engage..close window intersects [from, to] (inclusive, like the attribution
// itself). A story still open counts up to now. Usage inside a shared window cannot
// be split by timestamps, so the caller reports it ambiguous rather than apportion it.
func sessionWindowOverlaps(ctx context.Context, selfID, sessionID string, from, to, now time.Time) []string {
	wi, err := requireWorkItem()
	if err != nil || ledgerStore == nil {
		return nil
	}
	others := map[string]bool{}
	_ = ledgerStore.ForEachKind(ctx, "", ledger.KindDriverUsage, func(e ledger.Entry) error {
		var p DriverUsagePayload
		if e.StoryID != selfID && json.Unmarshal(e.Payload, &p) == nil && p.SessionID == sessionID {
			others[e.StoryID] = true
		}
		return nil
	})
	var out []string
	for id := range others {
		other, err := wi.Get(ctx, id)
		if err != nil {
			continue
		}
		entries, err := ledgerStore.ListByStory(ctx, id, "")
		if err != nil {
			continue
		}
		clk, _ := clockFor(ctx, other)
		start, end, haveEngage, haveTerminal := costview.StoryWindow(entries, clk)
		if !haveEngage {
			continue
		}
		if !haveTerminal {
			end = now
		}
		if !start.After(to) && !end.Before(from) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// stampDriverAttribution marks payload (an available live row) with what its
// figures cannot say: the other stories that drove its session in a window
// intersecting item's own driver window (engage to now), and the adapter's
// qualifier on subagent spend. Overlaps make the row ambiguous; a qualifier alone
// does not, since nothing shows another story shared the window.
func stampDriverAttribution(ctx context.Context, item workitem.Item, payload *DriverUsagePayload, subagentNote string, now time.Time) {
	from := now
	if ledgerStore != nil {
		if entries, err := ledgerStore.ListByStory(ctx, item.ID, ""); err == nil {
			clk, _ := clockFor(ctx, item)
			if start, _, haveEngage, _ := costview.StoryWindow(entries, clk); haveEngage {
				from = start
			}
		}
	}
	var reasons []string
	if overlaps := sessionWindowOverlaps(ctx, item.ID, payload.SessionID, from, now, now); len(overlaps) > 0 {
		payload.Overlaps = overlaps
		reasons = append(reasons, driverOverlapReason(payload.Executable, overlaps, payload.SessionID))
	}
	if subagentNote != "" {
		reasons = append(reasons, subagentNote)
	}
	payload.AttributionReason = strings.Join(reasons, "; ")
}
