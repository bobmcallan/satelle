package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/storyfold"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{
		Name:        "story-archive",
		Description: "Archive stories at their workflow's start state: mark the record archived (hidden from the default list) without running a route",
		Invoke:      storyArchive,
	})
	Register(&Verb{
		Name:        "story-unarchive",
		Description: "Unarchive stories: the record returns to the default list with its status and chain as they were",
		Invoke:      storyUnarchive,
	})
}

// archiveReq is the request body for story-archive and story-unarchive.
type archiveReq struct {
	IDs    []string `json:"ids"`
	Reason string   `json:"reason,omitempty"`
}

// ArchiveSkip is an epic child left alone because it is not at its workflow's
// start state; the epic's own archive went ahead without it.
type ArchiveSkip struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// ArchiveResult is the story-archive and story-unarchive response. Unchanged
// names the stories that already were in the asked disposition (no event).
type ArchiveResult struct {
	Changed   []string      `json:"changed"`
	Unchanged []string      `json:"unchanged,omitempty"`
	Skipped   []ArchiveSkip `json:"skipped,omitempty"`
}

// storyArchive disposes of stories without running their workflow (sty_28665836).
// Archive is record disposition, not a status: the story keeps its status and its
// chain, and the one event it adds is a story_updated field change of archived,
// which the fold projects to the row — so the flag travels with the chain. Only a
// story at its workflow's start state may be archived; one that is performing,
// parked or terminal is refused, and a refusal anywhere in the request archives
// nothing. Naming an epic also archives its children at their start state; those
// elsewhere are reported in Skipped and do not block.
func storyArchive(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return setArchived(ctx, raw, true)
}

// storyUnarchive restores archived stories to the default list. Any state may be
// unarchived: it moves no status, and refuses nothing a prior archive allowed.
func storyUnarchive(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return setArchived(ctx, raw, false)
}

func setArchived(ctx context.Context, raw json.RawMessage, archive bool) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	led, err := requireLedger()
	if err != nil {
		return nil, err
	}
	var req archiveReq
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if len(req.IDs) == 0 {
		return nil, fmt.Errorf("verb: at least one story id required")
	}

	var (
		res     ArchiveResult
		targets []workitem.Item
		seen    = map[string]bool{}
	)
	add := func(it workitem.Item) {
		if seen[it.ID] {
			return
		}
		seen[it.ID] = true
		if it.Archived == archive {
			res.Unchanged = append(res.Unchanged, it.ID)
			return
		}
		targets = append(targets, it)
	}
	// Validate every named story before the first write: all or nothing.
	for _, id := range req.IDs {
		it, err := storyState(ctx, id)
		if err != nil {
			return nil, err
		}
		if it.Kind != workitem.KindStory {
			return nil, fmt.Errorf("verb: %s is a %s — only a story can be archived", id, it.Kind)
		}
		if archive && !it.Archived {
			if err := requireAtStart(ctx, it); err != nil {
				return nil, err
			}
		}
		add(it)
		if !archive || !epicset.IsEpicParent(it) {
			continue
		}
		set, err := epicset.Resolve(ctx, store, it)
		if err != nil {
			return nil, err
		}
		children, err := overlayFold(ctx, set.Children)
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			if requireAtStart(ctx, c) != nil {
				res.Skipped = append(res.Skipped, ArchiveSkip{ID: c.ID, Status: c.Status})
				continue
			}
			add(c)
		}
	}

	now := time.Now()
	actor := resolveActor()
	if actor == "" {
		actor = "executor"
	}
	verbWord := "archived"
	if !archive {
		verbWord = "unarchived"
	}
	for _, it := range targets {
		payload, err := json.Marshal(storyUpdatedPayload{
			EditorIdentity: resolveEditorIdentity(),
			Fields:         []AmendField{{Field: storyfold.FieldArchived, Old: strconv.FormatBool(!archive), New: strconv.FormatBool(archive)}},
			Reason:         req.Reason,
		})
		if err != nil {
			return nil, err
		}
		body := verbWord
		if strings.TrimSpace(req.Reason) != "" {
			body += ": " + req.Reason
		}
		if _, err := led.Append(ctx, ledger.AppendInput{
			StoryID: it.ID, Kind: ledger.KindStoryUpdated, Actor: actor, Body: body, Payload: payload,
		}, now); err != nil {
			return nil, fmt.Errorf("verb: %s %s: %w", verbWord, it.ID, err)
		}
		if _, err := rebuildCache(ctx, it.ID); err != nil {
			return nil, err
		}
		res.Changed = append(res.Changed, it.ID)
	}
	if len(res.Changed) > 0 {
		notifyChange(TopicStories)
	}
	return json.Marshal(res)
}

// requireAtStart refuses a story that is not at its governing workflow's start
// state. it carries the folded status, so a stale row cannot let a performing
// story through.
func requireAtStart(ctx context.Context, it workitem.Item) error {
	spec, _, _, ok := governingSpec(ctx, it)
	if !ok {
		return fmt.Errorf("verb: %s is %s and no workflow governs it — only a story at its workflow's start state can be archived", it.ID, it.Status)
	}
	if start := spec.Start(); start == "" || it.Status != start {
		return fmt.Errorf("verb: %s is %s — only a story at its workflow's start state (%s) can be archived", it.ID, it.Status, start)
	}
	return nil
}
