package verb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// SyncStoryBacklog regenerates the read-only OKF backlog reference under the
// stories dir (.satelle/stories): every status:backlog story rendered as a
// generated concept file plus the reserved index.md/log.md. An engaged story
// (any non-backlog status) is NOT in the folder — the folder is the backlog. The SQLite store
// stays the SOLE story store (sty_fa1e02e1) — these files are a disposable,
// gitignored VIEW that no control logic reads (epic/parent close and the gates
// re-source from the DB); they exist only so the agent can browse the backlog on
// disk. Per-story attachment subdirs (<id>/) are left untouched, and the legacy
// pre-mirror-removal sty_*.md leftovers are cleaned up. Best-effort; a nil store
// or unset dir is a no-op. Returns the number of stories materialized.
func SyncStoryBacklog(ctx context.Context, store *workitem.Store, now time.Time) (int, error) {
	if store == nil || strings.TrimSpace(storyDir) == "" {
		return 0, nil
	}
	items, err := storyBacklogItems(ctx, store)
	if err != nil {
		return 0, err
	}
	// The stories dir's only legitimate top-level .md files are the ones we
	// generate, so any top-level sty_*.md NOT in the current backlog is stale — the
	// pre-sty_fa1e02e1 mirror leftovers, or a story that left the backlog. Remove
	// them (MaterializeOKF then writes the current set); <id>/ attachment subdirs
	// are untouched.
	pruneStoryFiles(storyDir, items)
	if err := docindex.MaterializeOKF(storyDir, "Backlog", items, now); err != nil {
		return 0, err
	}
	return len(items), nil
}

// storyBacklogItems lists the BACKLOG (status:backlog only) and renders each to
// an OKFItem. The folder is the backlog surface, not the worklist: once a story
// engages (moves to in_progress or any other status) it leaves the folder — the
// DB stays prime and the reconcile prunes its file.
func storyBacklogItems(ctx context.Context, store *workitem.Store) ([]docindex.OKFItem, error) {
	stories, err := store.List(ctx, workitem.ListFilter{
		Kind: workitem.KindStory, Status: workitem.StatusBacklog, Limit: 2000,
	})
	if err != nil {
		return nil, err
	}
	var items []docindex.OKFItem
	for _, s := range stories {
		items = append(items, storyToOKFItem(s))
	}
	return items, nil
}

// storyToOKFItem renders one story record to a read-only OKF concept: a metadata
// block (status/priority/category/parent/order tags) plus the description and
// acceptance criteria, so the on-disk backlog carries the ordering/status the
// agent browses by.
func storyToOKFItem(s workitem.Item) docindex.OKFItem {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", s.Title)
	fmt.Fprintf(&b, "- **id:** %s\n- **status:** %s\n", s.ID, s.Status)
	if s.Priority != "" {
		fmt.Fprintf(&b, "- **priority:** %s\n", s.Priority)
	}
	if s.Category != "" {
		fmt.Fprintf(&b, "- **category:** %s\n", s.Category)
	}
	if s.ParentID != "" {
		fmt.Fprintf(&b, "- **parent:** %s\n", s.ParentID)
	}
	if len(s.Tags) > 0 {
		fmt.Fprintf(&b, "- **tags:** %s\n", strings.Join(s.Tags, ", "))
	}
	if strings.TrimSpace(s.Body) != "" {
		fmt.Fprintf(&b, "\n## Description\n\n%s\n", strings.TrimRight(s.Body, "\n"))
	}
	if strings.TrimSpace(s.AcceptanceCriteria) != "" {
		fmt.Fprintf(&b, "\n## Acceptance criteria\n\n%s\n", strings.TrimRight(s.AcceptanceCriteria, "\n"))
	}
	tags := append([]string{"story", "status:" + s.Status}, s.Tags...)
	return docindex.OKFItem{
		Name:        s.ID,
		Type:        "story",
		Title:       s.Title,
		Description: firstProseLine(s.Body),
		Body:        b.String(),
		Tags:        tags,
		Timestamp:   s.UpdatedAt,
	}
}

// firstProseLine returns a one-line snippet (the first non-empty, non-heading
// line) for the index description, truncated. Empty when the body has none.
func firstProseLine(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || t[0] == '#' {
			continue
		}
		if len(t) > 160 {
			t = strings.TrimSpace(t[:160]) + "…"
		}
		return t
	}
	return ""
}

// pruneStoryFiles removes every top-level sty_*.md in dir whose id is not in the
// current backlog set — the legacy DB→disk mirror leftovers and stories that have
// left the backlog. Attachment subdirs and non-story files are untouched.
func pruneStoryFiles(dir string, items []docindex.OKFItem) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	want := make(map[string]struct{}, len(items))
	for _, it := range items {
		want[it.Name] = struct{}{}
	}
	for _, de := range ents {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasPrefix(name, "sty_") || !strings.HasSuffix(name, ".md") {
			continue
		}
		if _, keep := want[strings.TrimSuffix(name, ".md")]; keep {
			continue // MaterializeOKF (re)writes it as the generated view
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}
