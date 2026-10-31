package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// WorkingOnPayload is the payload of a KindWorkingOn ledger row (sty_40853d02):
// which location, session and working tree engaged the story. Location and
// Label are empty for an unbound repo. It is advisory presence: nothing reads it
// to refuse an action (sty_9c782f0e).
type WorkingOnPayload struct {
	Location string `json:"location,omitempty"`
	Label    string `json:"label,omitempty"`
	Session  string `json:"session,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	State    string `json:"state"`
}

// locationResolver names this machine's hosted location (id, label), wired from
// the CLI so verb never imports hosted — same seam as assigneeResolver. Nil
// (unwired) or empty means an unbound repo.
var locationResolver func() (id, label string)

// SetLocationResolver wires how the working_on row names this location. Pass nil
// to clear (tests / unbound).
func SetLocationResolver(f func() (id, label string)) {
	locationResolver = f
}

// ClearLocationResolver unsets the resolver (tests).
func ClearLocationResolver() {
	locationResolver = nil
}

// recordWorkingOn appends the working_on row for a story this location engaged
// at state (sty_40853d02, sty_9c782f0e), naming the session and working tree it
// was engaged from. Callers write it once the move has committed, so a refused or
// rolled-back move leaves no row. One row per (location, session, tree, state):
// a repeated engage of the same state from the same place writes none. Only a
// story carries one; a task or execution never does.
func recordWorkingOn(ctx context.Context, item workitem.Item, state string, now time.Time) {
	if item.Kind != workitem.KindStory || item.ID == "" || ledgerStore == nil {
		return
	}
	p := WorkingOnPayload{Session: config.ResolveSession(), Worktree: engagementWorktree(), State: state}
	if locationResolver != nil {
		p.Location, p.Label = locationResolver()
	}
	payload, _ := json.Marshal(p)
	if last, ok := LatestLocalWorkingOn(ctx, item.ID); ok && last.WorkingOnPayload == p {
		return
	}
	_, _ = ledgerStore.Append(ctx, ledger.AppendInput{
		StoryID: item.ID, Kind: ledger.KindWorkingOn, Actor: "executor",
		Body: WorkingOnText(p), Payload: payload,
	}, now)
}

// WorkingOnRow is one decoded working_on row.
type WorkingOnRow struct {
	WorkingOnPayload
	At time.Time
}

// LatestWorkingOn is the newest working_on row among rows written at location
// self (rows with no location are an unbound repo's, and count as self).
func LatestWorkingOn(rows []ledger.Entry, self string) (WorkingOnRow, bool) {
	var out WorkingOnRow
	found := false
	for _, e := range rows {
		if e.Kind != ledger.KindWorkingOn {
			continue
		}
		var p WorkingOnPayload
		if json.Unmarshal(e.Payload, &p) != nil || (p.Location != "" && p.Location != self) {
			continue
		}
		if !found || !e.CreatedAt.Before(out.At) {
			out, found = WorkingOnRow{WorkingOnPayload: p, At: e.CreatedAt}, true
		}
	}
	return out, found
}

// LatestLocalWorkingOn is LatestWorkingOn over the story's ledger at this
// location.
func LatestLocalWorkingOn(ctx context.Context, storyID string) (WorkingOnRow, bool) {
	if ledgerStore == nil || storyID == "" {
		return WorkingOnRow{}, false
	}
	rows, _, err := ledgerStore.ListNewest(ctx, ledger.ListFilter{StoryID: storyID, Kind: ledger.KindWorkingOn, Limit: 50})
	if err != nil {
		return WorkingOnRow{}, false
	}
	self := ""
	if locationResolver != nil {
		self, _ = locationResolver()
	}
	return LatestWorkingOn(rows, self)
}

// WorkingOnText is the one human line for a working_on row, shared by the row's
// own body and the displays that cite it.
func WorkingOnText(p WorkingOnPayload) string {
	where := p.Location
	if where == "" {
		where = "local"
	}
	if p.Label != "" {
		where += " (" + p.Label + ")"
	}
	s := "working on at " + where
	if p.Session != "" {
		s += ", session " + p.Session
	}
	if p.Worktree != "" {
		s += ", tree " + p.Worktree
	}
	return fmt.Sprintf("%s [%s]", s, p.State)
}
