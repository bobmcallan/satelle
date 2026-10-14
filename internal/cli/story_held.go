package cli

// `story get` and `story list` mark a story another location holds (sty_6e5fc41c)
// from the local hold registry the work-state passes keep — recorded state only,
// so reading a story never dials the server.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/verb"
)

// heldMark is the `held` object a story row carries when another location holds
// it. Text is the human line, and what the compact (text) rendering shows.
type heldMark struct {
	Holder       string `json:"holder"`
	Location     string `json:"location"`
	Label        string `json:"label,omitempty"`
	LastSeen     string `json:"last_seen,omitempty"`
	CheckedAt    string `json:"checked_at,omitempty"`
	PresumedDead bool   `json:"presumed_dead,omitempty"`
	Text         string `json:"text"`
}

// dispatchWithHeld is dispatch for the read verbs that mark held stories.
func dispatchWithHeld(cmd *cobra.Command, name string, req map[string]any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	resp, err := verb.Dispatch(cmd.Context(), name, body)
	if err != nil {
		return err
	}
	if a, aerr := appFrom(cmd); aerr == nil && a != nil {
		resp = markHeldStories(resp, heldMarks(a, time.Now()))
	}
	return renderResponse(cmd, name, requestStoryID(req), resp)
}

// heldMarks reads the registry: story id → mark, for the stories another
// location holds. Empty when the repo is not bound or nothing is held.
func heldMarks(a *app.App, now time.Time) map[string]heldMark {
	project := config.ResolveBoundProject(a.Config, a.RepoRoot)
	if project == "" {
		return nil
	}
	recorded, err := hosted.LoadHolds(config.ResolveHostedServer(a.Config), project, a.RepoRoot)
	if err != nil || len(recorded) == 0 {
		return nil
	}
	staleAfter, err := config.HoldStaleAfter(a.Config)
	if err != nil {
		staleAfter = config.DefaultHoldStaleAfter
	}
	self := hosted.KnownLocationID(a.RepoRoot)
	marks := map[string]heldMark{}
	for id, rec := range recorded {
		if rec.Location == self {
			continue
		}
		marks[id] = newHeldMark(rec, staleAfter, now)
	}
	return marks
}

func newHeldMark(rec hosted.HoldRecord, staleAfter time.Duration, now time.Time) heldMark {
	dead, _ := hosted.HoldPresumedDead(rec.LastSeen, staleAfter, now)
	m := heldMark{Holder: "remote " + holderName(rec), Location: rec.Location, Label: rec.Label, LastSeen: rec.LastSeen, PresumedDead: dead}
	if !rec.CheckedAt.IsZero() {
		m.CheckedAt = rec.CheckedAt.UTC().Format(time.RFC3339)
	}
	seen := rec.LastSeen
	if strings.TrimSpace(seen) == "" {
		seen = "unknown"
	}
	m.Text = fmt.Sprintf("held: %s, last seen %s", m.Holder, seen)
	if dead {
		m.Text += ", presumed dead"
	}
	return m
}

// markHeldStories adds a `held` member to the story object (or each object of a
// list) whose id has a mark. The member is spliced onto the row's own JSON, so
// every other field keeps its place. A response that is neither, or has no held
// story in it, is returned as it came.
func markHeldStories(raw json.RawMessage, marks map[string]heldMark) json.RawMessage {
	if len(marks) == 0 {
		return raw
	}
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "{"):
		if out, ok := spliceHeld(json.RawMessage(trimmed), marks); ok {
			return out
		}
	case strings.HasPrefix(trimmed, "["):
		var rows []json.RawMessage
		if json.Unmarshal(raw, &rows) != nil {
			return raw
		}
		changed := false
		for i, row := range rows {
			if out, ok := spliceHeld(row, marks); ok {
				rows[i], changed = out, true
			}
		}
		if changed {
			if out, err := json.Marshal(rows); err == nil {
				return out
			}
		}
	}
	return raw
}

func spliceHeld(row json.RawMessage, marks map[string]heldMark) (json.RawMessage, bool) {
	var w struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(row, &w) != nil {
		return row, false
	}
	m, ok := marks[w.ID]
	if !ok {
		return row, false
	}
	s := strings.TrimSpace(string(row))
	end := strings.LastIndex(s, "}")
	if !strings.HasPrefix(s, "{") || end < 0 {
		return row, false
	}
	mb, err := json.Marshal(m)
	if err != nil {
		return row, false
	}
	sep := ","
	if strings.TrimSpace(s[1:end]) == "" {
		sep = ""
	}
	return json.RawMessage(s[:end] + sep + `"held":` + string(mb) + s[end:]), true
}
