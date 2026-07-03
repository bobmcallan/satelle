package verb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/workitem"
)

// storyKeepClosed keeps the N most-recently-updated terminal-story attachment
// dirs; storyKeepDays prunes a terminal-story dir older than N days. Both 0 (the
// default) disable pruning — today's behaviour (sty_aba7200c).
var (
	storyKeepClosed int
	storyKeepDays   int
)

// SetStoryRetention wires the archive-retention policy for the per-story
// attachment dirs under .satelle/stories. Resolved from satelle.toml at app init.
func SetStoryRetention(keepClosed, keepDays int) {
	storyKeepClosed = keepClosed
	storyKeepDays = keepDays
}

// terminalStoryStates are the story statuses that count as CLOSED for retention.
// Only a story the DB records as terminal is ever a prune candidate; anything
// else (backlog/engaged) is always kept.
var terminalStoryStates = map[string]bool{
	workitem.StatusDone: true,
	"cancelled":         true,
}

// pruneClosedStoryDirs enforces the archive-retention policy over the per-story
// attachment dirs (<sty_id>/) under storyDir (sty_aba7200c): a TERMINAL story's
// dir beyond the count/age policy is MOVED to .satelle/backups/stories/<ts>/<id>/
// (mandatory backup, mirroring rebase — never deleted in place). The override is
// absolute: a NON-terminal story's dir, and an orphan dir with no DB story, are
// never pruned here. Deterministic from the DB terminal-update times + config.
// No-op when no policy is configured. Returns the number of dirs pruned.
func pruneClosedStoryDirs(ctx context.Context, store *workitem.Store, now time.Time) (int, error) {
	if (storyKeepClosed <= 0 && storyKeepDays <= 0) || store == nil || strings.TrimSpace(storyDir) == "" {
		return 0, nil
	}
	stories, err := store.List(ctx, workitem.ListFilter{Kind: workitem.KindStory, Limit: 2000})
	if err != nil {
		return 0, err
	}
	byID := make(map[string]workitem.Item, len(stories))
	for _, s := range stories {
		byID[s.ID] = s
	}
	ents, err := os.ReadDir(storyDir)
	if err != nil {
		return 0, nil // no stories dir yet — nothing to prune
	}
	// Collect the terminal-story dirs with their terminal-update times. A dir
	// whose story is non-terminal or absent from the DB is skipped (always kept).
	type candidate struct {
		id      string
		updated time.Time
	}
	var terminal []candidate
	for _, de := range ents {
		if !de.IsDir() || !strings.HasPrefix(de.Name(), "sty_") {
			continue
		}
		s, ok := byID[de.Name()]
		if !ok || !terminalStoryStates[s.Status] {
			continue
		}
		terminal = append(terminal, candidate{id: de.Name(), updated: s.UpdatedAt})
	}
	// Most-recently-updated first, so the count policy keeps the freshest N.
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].updated.After(terminal[j].updated) })

	pruned := 0
	for rank, c := range terminal {
		prune := false
		if storyKeepClosed > 0 && rank >= storyKeepClosed {
			prune = true // beyond the N most-recent closed dirs
		}
		if storyKeepDays > 0 && now.Sub(c.updated) > time.Duration(storyKeepDays)*24*time.Hour {
			prune = true // older than the age horizon
		}
		if !prune {
			continue
		}
		if err := moveStoryDirToBackup(c.id, now); err != nil {
			return pruned, err
		}
		pruned++
	}
	return pruned, nil
}

// moveStoryDirToBackup renames a story's attachment dir into a mandatory
// timestamped backup at .satelle/backups/stories/<ts>/<id>/ — the attachment
// files are AUTHORED-ish evidence (push summaries), so they are moved, not
// deleted (sty_aba7200c).
func moveStoryDirToBackup(id string, now time.Time) error {
	ts := now.UTC().Format("20060102-150405")
	dst := filepath.Join(filepath.Dir(storyDir), "backups", "stories", ts, id)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("story archive backup dir: %w", err)
	}
	if err := os.Rename(filepath.Join(storyDir, id), dst); err != nil {
		return fmt.Errorf("story archive move %s: %w", id, err)
	}
	return nil
}
