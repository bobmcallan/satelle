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
// which location, session and working tree took a fresh engagement seat on the
// story. Location and Label are empty for an unbound repo. It is visibility
// only; nothing reads it to refuse an engage.
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

// recordSeatTaken appends the working_on row for a seat this call newly took on a
// story at this location (sty_40853d02), naming the session and working tree the
// seat was taken under. Callers write it once the move the seat was taken for has
// committed: a gate that rejects, or a write that rolls back, releases the seat
// and leaves no row. Best-effort like every ledger row; only a story occupies a
// seat, so a task or execution never carries one.
func recordSeatTaken(ctx context.Context, item workitem.Item, state string, now time.Time) {
	if item.Kind != workitem.KindStory || item.ID == "" {
		return
	}
	p := WorkingOnPayload{Session: config.ResolveSession(), Worktree: engagementWorktree(), State: state}
	if locationResolver != nil {
		p.Location, p.Label = locationResolver()
	}
	payload, _ := json.Marshal(p)
	appendLedgerEntry(ctx, item.ID, ledger.KindWorkingOn, "executor", WorkingOnText(p), payload, now)
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
