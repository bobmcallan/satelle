package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{
		Name:        "story-queue",
		Description: "List open queue entries (stories resting at a wait step) in the order they were recorded",
		Invoke:      storyQueue,
	})
	Register(&Verb{
		Name:        "story-queue-withdraw",
		Description: "Withdraw a story's open queue entry, with a reason recorded on the story",
		Invoke:      storyQueueWithdraw,
	})
}

// QueueEntry is one open entry of the queue: a story that entered a wait step,
// with the branch and head sha its engagement tree stood at, and when.
type QueueEntry struct {
	Story      string    `json:"story"`
	State      string    `json:"state"`
	Branch     string    `json:"branch"`
	HeadSHA    string    `json:"head_sha"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	Event      string    `json:"event"`
}

// resolveQueueEntry reads the branch and head sha a story entering the wait
// step state is queued at, from its engagement tree and with local git only.
// A tree that is missing, not a repository, or on a detached HEAD is a refusal:
// an entry with no branch or sha names nothing a release could merge.
func resolveQueueEntry(ctx context.Context, item workitem.Item, state string) (ledger.QueueEnqueuedPayload, error) {
	refuse := func(why string) error {
		return fmt.Errorf("satelle: refusing %s → %s — %s is a wait step, and entering it records a queue entry naming the story's branch and head sha, but %s", item.Status, state, state, why)
	}
	tree := queueTree(ctx, item)
	if tree == "" {
		return ledger.QueueEnqueuedPayload{}, refuse("no engagement tree resolves for " + item.ID + " (engage it from a git working tree)")
	}
	if _, err := os.Stat(tree); err != nil {
		return ledger.QueueEnqueuedPayload{}, refuse(fmt.Sprintf("its engagement tree %q is not on this machine", tree))
	}
	sha, err := gitLine(tree, "rev-parse", "HEAD")
	if err != nil {
		return ledger.QueueEnqueuedPayload{}, refuse(fmt.Sprintf("git could not read HEAD in %s: %v", tree, err))
	}
	branch, err := gitLine(tree, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ledger.QueueEnqueuedPayload{}, refuse(fmt.Sprintf("git could not read the branch in %s: %v", tree, err))
	}
	if branch == "HEAD" {
		return ledger.QueueEnqueuedPayload{}, refuse(fmt.Sprintf("%s is on a detached HEAD, so there is no branch to queue (check out the story's branch)", tree))
	}
	return ledger.QueueEnqueuedPayload{Story: item.ID, State: state, Branch: branch, HeadSHA: sha}, nil
}

// queueTree is the tree a story is queued from: the one its latest working_on
// row at this location records, else the one its engagement baseline was taken
// in, else the invoking tree.
func queueTree(ctx context.Context, item workitem.Item) string {
	if w, ok := LatestLocalWorkingOn(ctx, item.ID); ok && w.Worktree != "" {
		return w.Worktree
	}
	if base, _, _, err := firstEngagementBaseline(ctx, item.ID); err == nil && base.Worktree != "" {
		return base.Worktree
	}
	return gitToplevel(engageRepoDir())
}

func gitLine(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// queueEnqueuedInput is the ledger row a wait-step entry appends.
func queueEnqueuedInput(p ledger.QueueEnqueuedPayload) ledger.AppendInput {
	payload, _ := json.Marshal(p)
	return ledger.AppendInput{
		StoryID: p.Story,
		Kind:    ledger.KindQueueEnqueued,
		Actor:   "executor",
		Body:    fmt.Sprintf("queued at %s: branch %s head %s", p.State, p.Branch, p.HeadSHA),
		Payload: payload,
	}
}

// openQueue folds every queue event, oldest first, into the open entries in the
// order they were recorded. A story's later enqueue replaces its earlier open
// entry and takes its own place in the order; a withdrawal closes it.
func openQueue(ctx context.Context) ([]QueueEntry, error) {
	l, err := requireLedger()
	if err != nil {
		return nil, err
	}
	var events []ledger.Entry
	for _, kind := range []string{ledger.KindQueueEnqueued, ledger.KindQueueWithdrawn} {
		rows, err := listAllOfKind(ctx, l, kind)
		if err != nil {
			return nil, err
		}
		events = append(events, rows...)
	}
	ledger.SortEntries(events)
	var open []QueueEntry
	drop := func(story string) {
		for i, e := range open {
			if e.Story == story {
				open = append(open[:i], open[i+1:]...)
				return
			}
		}
	}
	for _, e := range events {
		drop(e.StoryID)
		if e.Kind != ledger.KindQueueEnqueued {
			continue
		}
		var p ledger.QueueEnqueuedPayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		open = append(open, QueueEntry{
			Story: e.StoryID, State: p.State, Branch: p.Branch, HeadSHA: p.HeadSHA,
			EnqueuedAt: e.CreatedAt, Event: e.ID,
		})
	}
	return open, nil
}

// listAllOfKind reads every row of kind across stories, oldest first, paging
// past the per-read cap.
func listAllOfKind(ctx context.Context, l *ledger.Store, kind string) ([]ledger.Entry, error) {
	var out []ledger.Entry
	after := ""
	for {
		page, err := l.List(ctx, ledger.ListFilter{Kind: kind, Limit: ledger.MaxListLimit, AfterID: after})
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < ledger.MaxListLimit {
			return out, nil
		}
		after = page[len(page)-1].ID
	}
}

// storyQueue lists the open queue entries in recorded order, optionally only
// those resting at one wait step. Read-only.
func storyQueue(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req struct {
		State string `json:"state"`
	}
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	open, err := openQueue(ctx)
	if err != nil {
		return nil, err
	}
	entries := []QueueEntry{}
	for _, e := range open {
		if req.State == "" || e.State == req.State {
			entries = append(entries, e)
		}
	}
	return json.Marshal(map[string]any{"entries": entries})
}

// storyQueueWithdraw closes a story's open queue entry and records why on the
// story's chain. It moves no status: taking the story onward is the workflow's.
func storyQueueWithdraw(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	req.ID, req.Reason = strings.TrimSpace(req.ID), strings.TrimSpace(req.Reason)
	if req.ID == "" {
		return nil, fmt.Errorf("verb: id required")
	}
	if req.Reason == "" {
		return nil, fmt.Errorf("satelle: refusing to withdraw %s's queue entry with no reason — pass --reason \"<why>\"; the reason is recorded on the story", req.ID)
	}
	l, err := requireLedger()
	if err != nil {
		return nil, err
	}
	open, err := openQueue(ctx)
	if err != nil {
		return nil, err
	}
	var entry *QueueEntry
	for i := range open {
		if open[i].Story == req.ID {
			entry = &open[i]
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("satelle: refusing to withdraw — %s has no open queue entry (see `satelle story queue`)", req.ID)
	}
	payload, _ := json.Marshal(ledger.QueueWithdrawnPayload{Story: req.ID, Reason: req.Reason})
	actor := resolveActor()
	if actor == "" {
		actor = "executor"
	}
	row, err := l.Append(ctx, ledger.AppendInput{
		StoryID: req.ID,
		Kind:    ledger.KindQueueWithdrawn,
		Actor:   actor,
		Body:    "queue entry withdrawn: " + req.Reason,
		Payload: payload,
	}, time.Now())
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"withdrawn": *entry, "reason": req.Reason, "event": row.ID})
}
