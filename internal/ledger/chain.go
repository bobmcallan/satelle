package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Chain break kinds, as ChainBreak.Kind reports them.
const (
	// BreakMissingBase: the event names a base that is not one of its story's events.
	BreakMissingBase = "missing_base"
	// BreakBaseAfterEvent: the event's base is the event itself or comes after it.
	BreakBaseAfterEvent = "base_after_event"
	// BreakExtraRoot: a story has more than one event with no base.
	BreakExtraRoot = "extra_root"
	// BreakFork: events share a base and no KindChainFork record names them all.
	BreakFork = "fork"
)

// ChainBreak is one place a story's chain does not hold. Reported, never repaired.
type ChainBreak struct {
	StoryID string `json:"story_id"`
	EventID string `json:"event_id"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail"`
}

// ChainReport is the outcome of verifying one story or every story.
type ChainReport struct {
	Stories int          `json:"stories"`
	Events  int          `json:"events"`
	Breaks  []ChainBreak `json:"breaks"`
}

// VerifyChain checks one story's events, given in insertion order, and returns
// every break: a base that does not exist, a base that does not precede its
// event, more than one root, or events sharing a base with no fork record. A
// fork record is a KindChainFork event of the story whose Refs name every event
// sharing the base. It reads only entries and changes nothing.
func VerifyChain(entries []Entry) []ChainBreak {
	pos := make(map[string]int, len(entries))
	for i, e := range entries {
		pos[e.ID] = i
	}
	forks := forkRecords(entries)
	var breaks []ChainBreak
	add := func(e Entry, kind, detail string) {
		breaks = append(breaks, ChainBreak{StoryID: e.StoryID, EventID: e.ID, Kind: kind, Detail: detail})
	}
	roots := 0
	sharing := map[string][]Entry{}
	var bases []string // sharing keys in first-seen order
	for i, e := range entries {
		if e.BaseID == "" {
			roots++
			if roots > 1 {
				add(e, BreakExtraRoot, "a story has one root event; this is another with no base")
			}
			continue
		}
		bp, ok := pos[e.BaseID]
		switch {
		case !ok:
			add(e, BreakMissingBase, fmt.Sprintf("base %s is not an event of this story", e.BaseID))
		case bp >= i:
			add(e, BreakBaseAfterEvent, fmt.Sprintf("base %s does not precede this event", e.BaseID))
		}
		if _, seen := sharing[e.BaseID]; !seen {
			bases = append(bases, e.BaseID)
		}
		sharing[e.BaseID] = append(sharing[e.BaseID], e)
	}
	for _, base := range bases {
		group := sharing[base]
		if len(group) < 2 || recorded(forks, group) {
			continue
		}
		for _, e := range group[1:] {
			add(e, BreakFork, fmt.Sprintf("shares base %s with %s and no %s record names them", base, group[0].ID, KindChainFork))
		}
	}
	return breaks
}

// forkRecords returns the id sets the story's KindChainFork events name.
func forkRecords(entries []Entry) []map[string]bool {
	var out []map[string]bool
	for _, e := range entries {
		if e.Kind != KindChainFork {
			continue
		}
		var ids []string
		if json.Unmarshal(e.Refs, &ids) != nil {
			continue
		}
		set := make(map[string]bool, len(ids))
		for _, id := range ids {
			set[id] = true
		}
		out = append(out, set)
	}
	return out
}

// recorded reports whether one fork record names every event in group.
func recorded(forks []map[string]bool, group []Entry) bool {
	for _, set := range forks {
		all := true
		for _, e := range group {
			if !set[e.ID] {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// storyEvents reads a story's events in insertion (rowid) order — the order the
// chain was written in, and the one a base must precede.
func (s *Store) storyEvents(ctx context.Context, storyID string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+entryCols+` FROM evidence WHERE story_id = ? ORDER BY rowid ASC`, storyID)
	if err != nil {
		return nil, fmt.Errorf("ledger: verify: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// StoryChain returns every event of a story in insertion (rowid) order — the
// order a base must precede — with no row limit, for a reader that folds the
// whole chain. Read-only.
func (s *Store) StoryChain(ctx context.Context, storyID string) ([]Entry, error) {
	if strings.TrimSpace(storyID) == "" {
		return nil, fmt.Errorf("ledger: story_id required")
	}
	return s.storyEvents(ctx, storyID)
}

// VerifyStory verifies one story's chain. Read-only.
func (s *Store) VerifyStory(ctx context.Context, storyID string) (ChainReport, error) {
	if strings.TrimSpace(storyID) == "" {
		return ChainReport{}, fmt.Errorf("ledger: story_id required")
	}
	events, err := s.storyEvents(ctx, storyID)
	if err != nil {
		return ChainReport{}, err
	}
	rep := ChainReport{Events: len(events), Breaks: VerifyChain(events)}
	if rep.Breaks == nil {
		rep.Breaks = []ChainBreak{}
	}
	if len(events) > 0 {
		rep.Stories = 1
	}
	return rep, nil
}

// StoryIDs lists every story that has at least one event, in story-id order.
func (s *Store) StoryIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT story_id FROM evidence WHERE story_id <> '' ORDER BY story_id`)
	if err != nil {
		return nil, fmt.Errorf("ledger: story ids: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("ledger: story ids: %w", err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("ledger: story ids: %w", err)
	}
	return ids, nil
}

// VerifyAll verifies every story's chain, in story-id order. Read-only.
func (s *Store) VerifyAll(ctx context.Context) (ChainReport, error) {
	ids, err := s.StoryIDs(ctx)
	if err != nil {
		return ChainReport{}, err
	}
	rep := ChainReport{Breaks: []ChainBreak{}}
	for _, id := range ids {
		one, err := s.VerifyStory(ctx, id)
		if err != nil {
			return ChainReport{}, err
		}
		rep.Stories += one.Stories
		rep.Events += one.Events
		rep.Breaks = append(rep.Breaks, one.Breaks...)
	}
	return rep, nil
}
