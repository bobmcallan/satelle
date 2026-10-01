package verb

import (
	"context"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/epicset"
	"github.com/bobmcallan/satelle/internal/wfgovern"
)

// dropClosedEpicTags keeps a new story out of an epic that already closed
// (sty_9f4f8e12). A story filed with epic:<theme> joins that theme's set; when
// the theme's epic-parent is done or cancelled the set is sealed — stamping the
// tag on would either hold a finished container's set open or silently re-open
// the question of what it closed over. The story is filed WITHOUT that tag and
// the body gains a note naming the container, so the link is not lost, only
// not stamped. (Refusing instead would break a retrospective filing several
// proposals in one run on the first one that names a closed epic.)
//
// Applies to stories that are not themselves an epic-parent (creating the
// container carries its own theme tag). A theme with no epic-parent, or whose
// set cannot be fixed, leaves the tags alone — this adds no new refusal.
func dropClosedEpicTags(ctx context.Context, category string, tags []string, body string) ([]string, string) {
	if strings.EqualFold(strings.TrimSpace(category), epicset.ParentCategory) {
		return tags, body
	}
	store, err := requireWorkItem()
	if err != nil {
		return tags, body
	}
	var wfs []docindex.Doc
	if idx, ierr := requireDocIndex(); ierr == nil {
		wfs, _ = idx.List(ctx, "workflows")
	}
	kept := make([]string, 0, len(tags))
	var notes []string
	for _, tag := range tags {
		if !strings.HasPrefix(tag, epicset.TagPrefix) {
			kept = append(kept, tag)
			continue
		}
		container, found, perr := epicset.ParentOf(ctx, store, tag)
		if perr != nil || !found {
			kept = append(kept, tag)
			continue
		}
		if resolved, known := wfgovern.ChildResolved(wfs, container); !known || !resolved {
			kept = append(kept, tag)
			continue
		}
		notes = append(notes, fmt.Sprintf("Filed without %s: container %s is %s", tag, container.ID, container.Status))
	}
	if len(notes) == 0 {
		return tags, body
	}
	note := strings.Join(notes, "\n")
	if strings.TrimSpace(body) == "" {
		return kept, note
	}
	return kept, strings.TrimRight(body, "\n") + "\n\n" + note
}
