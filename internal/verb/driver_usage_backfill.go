package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// DriverTriggerBackfill marks a driver_usage row written after the fact by
// story-driver-backfill (sty_8c0e7e8c).
const DriverTriggerBackfill = "backfill"

// driverWindowReader is swappable in tests, like driverSnapshotter.
var driverWindowReader = agentcli.SessionWindowUsage

// Backfill report statuses for one session.
const (
	BackfillWritten     = "backfilled"  // a derived row was appended
	BackfillUnchanged   = "unchanged"   // the same window was already backfilled; nothing appended
	BackfillUnavailable = "unavailable" // no figure could be attributed, named by Reason
	BackfillAmbiguous   = "ambiguous"   // another story shares the window; usage is not split
)

// DriverBackfillSession is one session's outcome for a story's backfill.
type DriverBackfillSession struct {
	SessionID  string `json:"session_id"`
	Executable string `json:"executable"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`

	WindowFrom string   `json:"window_from,omitempty"`
	WindowTo   string   `json:"window_to,omitempty"`
	Overlaps   []string `json:"overlaps,omitempty"`

	// The figures a backfilled row carries; zero and meaningless unless Status is
	// backfilled or unchanged-over-an-available-row.
	FreshInput int      `json:"fresh_input,omitempty"`
	CacheRead  int      `json:"cache_read,omitempty"`
	CacheWrite int      `json:"cache_write,omitempty"`
	Output     int      `json:"output,omitempty"`
	ModelCalls int      `json:"model_calls,omitempty"`
	CostUSD    *float64 `json:"cost_usd,omitempty"`

	CostUnavailableReason string `json:"cost_unavailable_reason,omitempty"`
}

// DriverBackfillReport is story-driver-backfill's result: what happened per session,
// and a note when the story had nothing to backfill.
type DriverBackfillReport struct {
	StoryID  string                  `json:"story_id"`
	Sessions []DriverBackfillSession `json:"sessions,omitempty"`
	Note     string                  `json:"note,omitempty"`
}

type driverBackfillReq struct {
	ID string `json:"id"`
}

// storyDriverBackfill attributes a closed story's driver spend from the session
// record that outlived it, for stories whose driver rows could not be read live
// because the harness had no driver-usage reader then (sty_8c0e7e8c).
func storyDriverBackfill(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	wi, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req driverBackfillReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.ID == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	item, err := wi.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	rep, err := BackfillDriverUsage(ctx, item, time.Now())
	if err != nil {
		return nil, err
	}
	return json.Marshal(rep)
}

// backfillCandidate is a session whose driver rows on a story all failed for want of
// a driver-usage reader: the only rows a backfill may stand in for.
type backfillCandidate struct {
	sessionID, executable string
}

// backfillCandidates names, in first-seen order, each session on entries that carries
// a "no driver-usage reader" unavailable row and no live-measured row — a session the
// harness did read live is already measured, and one that failed for another reason
// (a record that is gone) has nothing a backfill could read either.
func backfillCandidates(entries []ledger.Entry) []backfillCandidate {
	type seen struct {
		exe            string
		noReader, live bool
	}
	bySession := map[string]*seen{}
	var order []string
	for _, e := range entries {
		if e.Kind != ledger.KindDriverUsage {
			continue
		}
		var p DriverUsagePayload
		if json.Unmarshal(e.Payload, &p) != nil || strings.TrimSpace(p.SessionID) == "" || p.Backfilled {
			continue
		}
		s, ok := bySession[p.SessionID]
		if !ok {
			s = &seen{exe: p.Executable}
			bySession[p.SessionID] = s
			order = append(order, p.SessionID)
		}
		switch {
		case p.Available && !p.BaselineFresh:
			s.live = true
		case !p.Available && agentcli.IsNoDriverReaderReason(p.UnavailableReason):
			s.noReader = true
		}
	}
	var out []backfillCandidate
	for _, id := range order {
		if s := bySession[id]; s.noReader && !s.live {
			out = append(out, backfillCandidate{sessionID: id, executable: s.exe})
		}
	}
	return out
}

// BackfillDriverUsage writes, for each eligible session on item, one derived
// driver_usage row attributing the session record's usage to the story's own
// engage..close window — or a named unavailable/ambiguous row when it cannot. It is
// idempotent per window: a re-run appends nothing it already wrote.
func BackfillDriverUsage(ctx context.Context, item workitem.Item, now time.Time) (DriverBackfillReport, error) {
	rep := DriverBackfillReport{StoryID: item.ID}
	ls, err := requireLedger()
	if err != nil {
		return rep, err
	}
	entries, err := ls.ListByStory(ctx, item.ID, "")
	if err != nil {
		return rep, err
	}
	cands := backfillCandidates(entries)
	if len(cands) == 0 {
		rep.Note = "no driver_usage row for this story is unavailable for want of a driver-usage reader; nothing to backfill"
		return rep, nil
	}
	clk, _ := clockFor(ctx, item)
	from, to, haveEngage, haveTerminal := costview.StoryWindow(entries, clk)
	for _, c := range cands {
		s := DriverBackfillSession{SessionID: c.sessionID, Executable: c.executable}
		switch {
		case !haveEngage:
			s.Status, s.Reason = BackfillUnavailable, "story never engaged; no window to attribute"
		case !haveTerminal:
			s.Status, s.Reason = BackfillUnavailable, "story not closed; window open"
		default:
			s = backfillSession(ctx, item, entries, c, from, to, now)
		}
		rep.Sessions = append(rep.Sessions, s)
	}
	return rep, nil
}

// backfillSession attributes one session's usage to [from, to] and appends the row.
func backfillSession(ctx context.Context, item workitem.Item, entries []ledger.Entry, c backfillCandidate, from, to, now time.Time) DriverBackfillSession {
	wf, wt := from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano)
	s := DriverBackfillSession{SessionID: c.sessionID, Executable: c.executable, WindowFrom: wf, WindowTo: wt}
	payload := DriverUsagePayload{
		SessionID: c.sessionID, Executable: c.executable,
		Trigger: DriverTriggerBackfill, Backfilled: true, WindowFrom: wf, WindowTo: wt,
		WindowKey: fmt.Sprintf("%s|%s|%s|%s|%s", item.ID, c.sessionID, DriverTriggerBackfill, wf, wt),
	}

	if overlaps := sessionWindowOverlaps(ctx, item.ID, c.sessionID, from, to, now); len(overlaps) > 0 {
		s.Status, s.Overlaps = BackfillAmbiguous, overlaps
		s.Reason = fmt.Sprintf("%s: window overlaps %s in session %s; usage not split", c.executable, strings.Join(overlaps, ", "), c.sessionID)
		payload.UnavailableReason, payload.CostUnavailableReason = s.Reason, s.Reason
	} else {
		w := driverWindowReader(c.executable, c.sessionID, changeRecordRepoRoot(), from, to)
		if !w.Available {
			s.Status, s.Reason = BackfillUnavailable, w.UnavailableReason
			payload.UnavailableReason, payload.CostUnavailableReason = w.UnavailableReason, w.CostUnavailableReason
		} else {
			calls := w.ModelCalls
			payload.Available, payload.Model = true, w.Model
			payload.FreshInput, payload.CacheRead, payload.CacheWrite, payload.Output = w.FreshInputTokens, w.CacheReadInputTokens, w.CacheCreationInputTokens, w.OutputTokens
			if w.ModelCallsUnavailableReason != "" {
				payload.ModelCallsUnavailableReason = w.ModelCallsUnavailableReason
			} else {
				payload.ModelCalls = &calls
			}
			payload.CostUSD, payload.CostUnavailableReason = w.CostUSD, w.CostUnavailableReason
			s.Status = BackfillWritten
			s.FreshInput, s.CacheRead, s.CacheWrite, s.Output, s.ModelCalls = payload.FreshInput, payload.CacheRead, payload.CacheWrite, payload.Output, calls
			s.CostUSD, s.CostUnavailableReason = w.CostUSD, w.CostUnavailableReason
		}
	}

	if backfillAlreadyRecorded(entries, payload) {
		s.Status = BackfillUnchanged
		return s
	}
	appendDriverUsageRow(ctx, item.ID, payload, now)
	return s
}

// backfillAlreadyRecorded reports whether the story already carries a backfill row
// for payload's window that a re-run would only repeat: any available row for it, or
// an unavailable one with the same reason. History is never rewritten.
func backfillAlreadyRecorded(entries []ledger.Entry, payload DriverUsagePayload) bool {
	for _, e := range entries {
		if e.Kind != ledger.KindDriverUsage {
			continue
		}
		var p DriverUsagePayload
		if json.Unmarshal(e.Payload, &p) != nil || !p.Backfilled || p.WindowKey != payload.WindowKey {
			continue
		}
		if p.Available || (!payload.Available && p.UnavailableReason == payload.UnavailableReason) {
			return true
		}
	}
	return false
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
