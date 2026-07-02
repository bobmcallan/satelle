package verb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
// or unset dir is a no-op. Returns the stories materialized and views pruned.
func SyncStoryBacklog(ctx context.Context, store *workitem.Store, now time.Time) (materialized, pruned int, err error) {
	if store == nil || strings.TrimSpace(storyDir) == "" {
		return 0, 0, nil
	}
	items, err := storyBacklogItems(ctx, store)
	if err != nil {
		return 0, 0, err
	}
	// The stories dir's only legitimate top-level .md files are the ones we
	// generate, so any top-level sty_*.md NOT in the current backlog is stale — the
	// pre-sty_fa1e02e1 mirror leftovers, or a story that left the backlog. Remove
	// them (MaterializeOKF then writes the current set); <id>/ attachment subdirs
	// are untouched.
	pruned = pruneStoryFiles(storyDir, items)
	if err := docindex.MaterializeOKF(storyDir, "Backlog", items, now); err != nil {
		return 0, pruned, err
	}
	// One-time adoption (sty_97c53d72): implementation summaries now live WITH
	// their story (an attachment under <id>/), not in the retired
	// documents/story-implementation-summary sub-bundle — migrate any legacy
	// commit-summary-sty_*.md into the owning story's folder.
	migrateLegacySummaries(storyDir)
	return len(items), pruned, nil
}

// StorySyncReport summarises one full .satelle/stories reconciliation
// (sty_8f7b2157): the view counts plus the artifact review — orphaned artifact
// dirs (story id absent from the DB) and artifact files whose frontmatter
// contradicts their location are REPORTED, never deleted (artifacts are
// authored evidence; the operator decides).
type StorySyncReport struct {
	Materialized int      `json:"materialized"`       // backlog views written/refreshed
	Pruned       int      `json:"pruned"`             // stale/non-backlog views removed
	ArtifactDirs int      `json:"artifact_dirs"`      // <sty_id>/ dirs present
	Orphaned     []string `json:"orphaned,omitempty"` // artifact dirs with no DB story
	Problems     []string `json:"problems,omitempty"` // artifact frontmatter mismatches
}

// SyncStories runs the full stories-dir reconciliation and REVIEWS the
// artifacts (sty_8f7b2157): the shared SyncStoryBacklog core (materialize the
// backlog-only views, prune stale, migrate legacy summaries) plus a review of
// every <sty_id>/ artifact dir against the database. The dedicated
// `satelle story sync` verb calls this; reindex/serve keep calling the core.
func SyncStories(ctx context.Context, store *workitem.Store, now time.Time) (StorySyncReport, error) {
	var rep StorySyncReport
	n, pruned, err := SyncStoryBacklog(ctx, store, now)
	if err != nil {
		return rep, err
	}
	rep.Materialized, rep.Pruned = n, pruned
	ents, derr := os.ReadDir(storyDir)
	if derr != nil {
		return rep, nil // no dir yet — nothing to review
	}
	for _, de := range ents {
		if !de.IsDir() || !strings.HasPrefix(de.Name(), "sty_") {
			continue
		}
		rep.ArtifactDirs++
		id := de.Name()
		if _, gerr := store.Get(ctx, id); gerr != nil {
			rep.Orphaned = append(rep.Orphaned, id)
			continue
		}
		// Frontmatter review: an artifact claiming a DIFFERENT story than the
		// dir it lives in is a misfiled artifact — report it.
		files, _ := os.ReadDir(filepath.Join(storyDir, id))
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			body, rerr := os.ReadFile(filepath.Join(storyDir, id, f.Name()))
			if rerr != nil {
				continue
			}
			if claimed := artifactStoryClaim(string(body)); claimed != "" && claimed != id {
				rep.Problems = append(rep.Problems,
					fmt.Sprintf("%s/%s claims story %s (misfiled artifact)", id, f.Name(), claimed))
			}
		}
	}
	sort.Strings(rep.Orphaned)
	sort.Strings(rep.Problems)
	return rep, nil
}

// artifactStoryClaim reads the `story:` frontmatter key from an artifact file
// (best-effort; empty when absent or unfenced).
func artifactStoryClaim(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return ""
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && strings.TrimSpace(k) == "story" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// summaryStoryID extracts the owning story id from a legacy summary filename
// (commit-summary-sty_<8hex>.md). Empty when the name carries no story id.
var summaryStoryID = regexp.MustCompile(`(sty_[0-9a-f]{8})\.md$`)

// migrateLegacySummaries moves legacy per-story implementation summaries — the
// retired documents/story-implementation-summary sub-bundle and any root
// documents/commit-summary-*.md — into the owning story's attachment folder
// (<storyDir>/<sty_id>/), where they persist across the story's whole lifecycle
// (the backlog-only view prune leaves <id>/ dirs untouched). The emptied
// sub-bundle (its regenerated index.md/log.md included) is removed. Files whose
// name carries no story id are left in place. Best-effort and idempotent.
func migrateLegacySummaries(storyDir string) {
	docsDir := filepath.Join(filepath.Dir(storyDir), "documents")
	sub := filepath.Join(docsDir, "story-implementation-summary")
	for _, dir := range []string{sub, docsDir} {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, de := range ents {
			name := de.Name()
			if de.IsDir() || !strings.HasPrefix(name, "commit-summary") || !strings.HasSuffix(name, ".md") {
				continue
			}
			m := summaryStoryID.FindStringSubmatch(name)
			if m == nil {
				continue // no story id in the name — leave it where it is
			}
			body, rerr := os.ReadFile(filepath.Join(dir, name))
			if rerr != nil {
				continue
			}
			dest := filepath.Join(storyDir, m[1], name)
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				continue
			}
			// Attachments are authored/portable — writable, unlike the generated views.
			if err := os.WriteFile(dest, body, 0o644); err != nil {
				continue
			}
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	// Remove the retired sub-bundle once only its regenerated index/log remain.
	if ents, err := os.ReadDir(sub); err == nil {
		removable := true
		for _, de := range ents {
			if n := de.Name(); n != "index.md" && n != "log.md" {
				removable = false
				break
			}
		}
		if removable {
			_ = os.RemoveAll(sub)
		}
	}
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
func pruneStoryFiles(dir string, items []docindex.OKFItem) int {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
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
		if os.Remove(filepath.Join(dir, name)) == nil {
			n++
		}
	}
	return n
}
