package verb

import (
	"context"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// StoryMembers is the container's member set as the CLI route view reports it
// (sty_9f4f8e12): the epic: tag set for an epic-parent, parent_id links for any
// other container — epicset's answer, the same one the close guard, the close
// gate payload, the web story view and the workspace aggregate read. A story
// that is not a container returns no members and no error.
func StoryMembers(ctx context.Context, id string) ([]epicset.Ref, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	item, err := store.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("verb: members: %s: %w", id, err)
	}
	members, err := epicset.Members(ctx, store, item)
	return epicset.Refs(members), err
}

// membersSection renders the member listing appended to the route view. Empty
// for a story that is neither an epic-parent nor a parent of anything, so a leaf
// story's route reads exactly as before.
func membersSection(ctx context.Context, item workitem.Item) string {
	store, err := requireWorkItem()
	if err != nil {
		return ""
	}
	members, err := epicset.Members(ctx, store, item)
	if err == nil && len(members) == 0 && !epicset.IsEpicParent(item) {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Members\n\n")
	if err != nil {
		fmt.Fprintf(&b, "%v — the container does not close.\n", err)
		return b.String()
	}
	if epicset.IsEpicParent(item) {
		if themes := epicset.Themes(item); len(themes) == 1 {
			fmt.Fprintf(&b, "Every story carrying %s (parent_id is not membership):\n\n", themes[0])
		}
	} else {
		b.WriteString("Stories whose parent_id is this story:\n\n")
	}
	if len(members) == 0 {
		b.WriteString("none\n")
	}
	for _, m := range epicset.Refs(members) {
		fmt.Fprintf(&b, "- %s — %s\n", m.ID, m.Status)
	}
	return b.String()
}
