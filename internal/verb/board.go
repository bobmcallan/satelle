package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/board"
	"github.com/bobmcallan/satelle/internal/lease"
	"github.com/bobmcallan/satelle/internal/ledger"
)

func init() {
	Register(&Verb{
		Name:        "board-list",
		Description: "List the agent messages on every story with their state and overdue flag (read-only)",
		Invoke:      boardList,
	})
}

// BoardRow is one message on the board: the message with its folded state, the
// story it is on, and — for a message addressed to a session — whether that
// session holds a live seat.
type BoardRow struct {
	StoryID string `json:"story_id"`
	AgentMessage
	// Presence is set only for a session addressee: "seat-live" when a live seat
	// is stamped with that session id (lease.Alive), else "no-live-seat". A
	// session with no seat may still be running; the board says only what the
	// seats show.
	Presence string `json:"presence,omitempty"`
}

type boardListReq struct {
	State   string `json:"state,omitempty"`
	Overdue bool   `json:"overdue,omitempty"`
	// Session keeps the messages a session sent or is addressed by id.
	Session string `json:"session,omitempty"`
	Story   string `json:"story,omitempty"`
}

func boardList(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	ls, err := requireLedger()
	if err != nil {
		return nil, err
	}
	var req boardListReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	state := strings.TrimSpace(req.State)
	if state != "" && !board.State(state).Valid() {
		return nil, fmt.Errorf("board-list: --state %q must be waiting, delivered, answered or undeliverable", state)
	}
	session := strings.TrimPrefix(strings.TrimSpace(req.Session), "session:")
	story := strings.TrimSpace(req.Story)

	byStory := map[string][]ledger.Entry{}
	marksByStory := map[string][]ledger.Entry{}
	collect := func(kind string, into map[string][]ledger.Entry) error {
		return ls.ForEachKind(ctx, story, kind, func(e ledger.Entry) error {
			into[e.StoryID] = append(into[e.StoryID], e)
			return nil
		})
	}
	if err := collect(ledger.KindAgentMessage, byStory); err != nil {
		return nil, err
	}
	if err := collect(ledger.KindMessageMark, marksByStory); err != nil {
		return nil, err
	}

	now := time.Now()
	live := liveSessions(ctx, now)
	out := []BoardRow{}
	for storyID, entries := range byStory {
		var rows []ledger.Entry
		for _, e := range entries {
			if !board.Transcript(e) {
				rows = append(rows, e)
			}
		}
		for _, m := range foldAgentMessages(rows, marksByStory[storyID], now) {
			if (state != "" && m.State != state) || (req.Overdue && !m.Overdue) {
				continue
			}
			kind, value := board.Addressee(m.To)
			if session != "" && m.FromSessionID != session && !(kind == board.KindSession && value == session) {
				continue
			}
			row := BoardRow{StoryID: storyID, AgentMessage: m}
			if kind == board.KindSession {
				row.Presence = "no-live-seat"
				if live[value] {
					row.Presence = "seat-live"
				}
			}
			out = append(out, row)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return json.Marshal(out)
}

// liveSessions is the set of session ids that hold a live seat. Read-only —
// unlike seat listing it reaps nothing. Empty when no lease store is wired.
func liveSessions(ctx context.Context, now time.Time) map[string]bool {
	out := map[string]bool{}
	ls, err := requireLease()
	if err != nil {
		return out
	}
	leases, err := ls.List(ctx)
	if err != nil {
		return out
	}
	for _, l := range leases {
		if l.SessionID != "" && lease.Alive(l, now) {
			out[l.SessionID] = true
		}
	}
	return out
}
