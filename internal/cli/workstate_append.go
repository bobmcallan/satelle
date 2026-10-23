package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/syncstate"
)

// A story's ledger events leave a push by compare-and-append (sty_9bdf08c4): one
// Chain/Append per story, naming the last event the server accepted from this
// checkout (the story's head in the work-state cursor). The server takes the
// events only while that head is still the story's latest; otherwise nothing is
// stored and the agent is told the story moved. Nothing here merges, overwrites
// or holds. The bulk Apply path no longer carries story-scoped ledger entries.

// storyAppend is one story's local chain and where its unpublished events begin.
type storyAppend struct {
	id    string
	order []ledger.Entry // the whole local chain, first event to last
	from  int            // index of the first event the server has not accepted
	err   error          // the local chain cannot be sent as it is
}

// base is the event the unpublished events build on; empty when none was published.
func (p storyAppend) base() string {
	if p.from == 0 {
		return ""
	}
	return p.order[p.from-1].ID
}

// planStoryAppends lists the stories with events past their head. It is not driven
// by the bulk ledger mark: a story the server refused stays listed on every push
// until it is accepted. scope narrows it to the stories a scoped push names.
func planStoryAppends(ctx context.Context, a *app.App, scope scopeSet, heads map[string]string) ([]storyAppend, error) {
	ids, err := a.Store.Ledger.StoryIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list story chains: %w", err)
	}
	var plan []storyAppend
	for _, id := range ids {
		if !scope.HasLedger(id) {
			continue
		}
		events, err := a.Store.Ledger.StoryChain(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read the chain of %s: %w", id, err)
		}
		order, err := chainOrder(events)
		if err != nil {
			plan = append(plan, storyAppend{id: id, err: err})
			continue
		}
		// A head this store no longer holds (the store was replaced or restored) is
		// treated as none: the server compares the base and either resumes from its
		// own prefix of this chain or reports the story as moved, so nothing is
		// guessed and nothing is stuck.
		from := 0
		if head := heads[id]; head != "" {
			if i := slices.IndexFunc(order, func(e ledger.Entry) bool { return e.ID == head }); i >= 0 {
				from = i + 1
			}
		}
		if from < len(order) {
			plan = append(plan, storyAppend{id: id, order: order, from: from})
		}
	}
	return plan, nil
}

// chainOrder puts a story's events in chain order, each built on the one before.
// Insertion order is not it: a chain pulled from another machine can arrive out of
// order. A chain that forks, or has events no path from the first reaches, cannot
// be sent as one and is reported, never guessed at.
func chainOrder(events []ledger.Entry) ([]ledger.Entry, error) {
	byBase := make(map[string][]ledger.Entry, len(events))
	for _, e := range events {
		byBase[e.BaseID] = append(byBase[e.BaseID], e)
	}
	order := make([]ledger.Entry, 0, len(events))
	for base := ""; ; {
		next := byBase[base]
		if len(next) == 0 {
			break
		}
		if len(next) > 1 {
			return nil, fmt.Errorf("the local chain forks at %s: %d events build on it", forkLabel(base), len(next))
		}
		order = append(order, next[0])
		base = next[0].ID
	}
	if len(order) != len(events) {
		return nil, fmt.Errorf("the local chain is broken: %d of %d events are not reached from its first event", len(events)-len(order), len(events))
	}
	return order, nil
}

func forkLabel(base string) string {
	if base == "" {
		return "its start"
	}
	return base
}

func chainEvents(entries []ledger.Entry) ([]hosted.ChainEvent, error) {
	out := make([]hosted.ChainEvent, 0, len(entries))
	for _, e := range entries {
		raw, err := marshalWorkstateLedger(e)
		if err != nil {
			return nil, err
		}
		out = append(out, hosted.ChainEvent{ID: e.ID, BaseID: e.BaseID, WorkflowVersion: e.WorkflowHash, Payload: raw})
	}
	return out, nil
}

// publishStoryEvents appends each planned story that is not withheld and returns
// how many stories it published. A story the server refused or that cannot be sent
// is reported on w and does not stop the others; the returned error counts them so
// the push exits non-zero.
func publishStoryEvents(ctx context.Context, w io.Writer, a *app.App, client *hosted.Client, server, project string, plan []storyAppend, held map[string]string) (int, error) {
	var conn *hosted.ChainConn
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()
	published, failed := 0, 0
	for _, p := range plan {
		if _, withheld := held[p.id]; withheld {
			continue
		}
		if p.err != nil {
			fmt.Fprintf(w, "the story %s was not published: %v\n", p.id, p.err)
			failed++
			continue
		}
		if conn == nil {
			var err error
			if conn, err = client.OpenChain(ctx); err != nil {
				return published, fmt.Errorf("open the story chain: %w", err)
			}
		}
		err := appendStory(ctx, w, a, conn, server, project, p)
		switch {
		case errors.Is(err, hosted.ErrLoginRequired):
			return published, err
		case err != nil:
			failed++
		default:
			published++
		}
	}
	if failed > 0 {
		return published, fmt.Errorf("%d stor(y/ies) not published — see above", failed)
	}
	return published, nil
}

// withoutStoryEntries drops the ledger entries that belong to a story: those go by
// Chain/Append. Entries with no story stay on the bulk path.
func withoutStoryEntries(raws []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(raws))
	for _, raw := range raws {
		var w struct {
			StoryID string `json:"story_id"`
		}
		if json.Unmarshal(raw, &w) == nil && w.StoryID != "" {
			continue
		}
		out = append(out, raw)
	}
	return out
}

// appendStory sends one story's unpublished events. When the server's latest event
// is one this checkout already holds — its own earlier publish, whose head was
// never saved — it resumes from that event once; the same chain is continued, not
// merged. Any other stale base is the agent's to read and decide.
func appendStory(ctx context.Context, w io.Writer, a *app.App, conn *hosted.ChainConn, server, project string, p storyAppend) error {
	from := p.from
	for resumed := false; ; resumed = true {
		base := ""
		if from > 0 {
			base = p.order[from-1].ID
		}
		if from >= len(p.order) {
			return acceptStory(w, a, server, project, p.id, base)
		}
		events, err := chainEvents(p.order[from:])
		if err != nil {
			fmt.Fprintf(w, "the story %s was not published: %v\n", p.id, err)
			return err
		}
		_, err = conn.AppendStory(ctx, project, p.id, base, events)
		if err == nil {
			return acceptStory(w, a, server, project, p.id, p.order[len(p.order)-1].ID)
		}
		var stale *hosted.StaleBaseError
		if !errors.As(err, &stale) {
			fmt.Fprintf(w, "the story %s was not published: %v\n", p.id, err)
			return err
		}
		i := slices.IndexFunc(p.order, func(e ledger.Entry) bool { return e.ID == stale.LatestID })
		if resumed || i < 0 {
			reason := fmt.Sprintf("moved since %s: re-read and decide (server is at %s, %d event(s) not seen)", stale.Since(), stale.LatestID, stale.Missing)
			fmt.Fprintf(w, "the story %s %s\n", p.id, reason)
			_ = syncstate.RecordStoryConflicts(config.GlobalDir(), a.RepoRoot, map[string]string{p.id: reason}, nil, time.Now().UTC())
			return err
		}
		fmt.Fprintf(w, "the story %s is already published up to %s: resuming from it\n", p.id, stale.LatestID)
		if err := hosted.SaveStoryHead(server, project, a.RepoRoot, p.id, stale.LatestID); err != nil {
			return fmt.Errorf("save story head: %w", err)
		}
		from = i + 1
	}
}

// acceptStory records the story's new head and clears any conflict recorded for it.
func acceptStory(w io.Writer, a *app.App, server, project, storyID, head string) error {
	if head != "" {
		if err := hosted.SaveStoryHead(server, project, a.RepoRoot, storyID, head); err != nil {
			fmt.Fprintf(w, "the story %s was published but its head was not saved: %v\n", storyID, err)
			return fmt.Errorf("save story head: %w", err)
		}
	}
	_ = syncstate.RecordStoryConflicts(config.GlobalDir(), a.RepoRoot, nil, []string{storyID}, time.Now().UTC())
	return nil
}
