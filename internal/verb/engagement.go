package verb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	Register(&Verb{
		Name:        "story-diff",
		Description: "Enumerate files changed since a story's engagement baseline (enumeration only)",
		Invoke:      storyDiff,
	})
}

// engagementBaselinePayload is the structured ledger payload for
// KindEngagementBaseline (sty_da169e03).
type engagementBaselinePayload struct {
	HeadSHA string `json:"head_sha"`
	Dirty   bool   `json:"dirty"`
	To      string `json:"to"` // status entered when the baseline was recorded
	// Worktree is the git working tree the baseline was taken in (sty_c098dc2d).
	// Absent on baselines recorded before the anchor existed — those stay
	// UNANCHORED and story diff keeps its old CWD-resolved behaviour for them,
	// so an upgrade cannot start refusing an in-flight story's own diff.
	Worktree string `json:"worktree,omitempty"`
}

// maybeRecordEngagementBaseline ledgers a one-shot baseline when a story first
// enters an engaging (non-terminal performing) state. Idempotent: park/resume
// re-entry does not overwrite. Best-effort on git errors (still records empty
// sha with body noting the failure so the gap is visible).
func maybeRecordEngagementBaseline(ctx context.Context, item workitem.Item, from, to string, now time.Time) {
	if item.Kind != workitem.KindStory {
		return
	}
	engaging, ok := storyStatusIsEngaging(ctx, item, to)
	if !ok || !engaging {
		return
	}
	// Only on first entry into the engaging set: if already have a baseline, skip.
	if hasEngagementBaseline(ctx, item.ID) {
		return
	}
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	sha, dirty, gerr := gitHeadAndDirty(dir)
	tree := gitToplevel(dir)
	body := fmt.Sprintf("engagement baseline at %s→%s head=%s", from, to, sha)
	if dirty {
		body += " (dirty worktree)"
	}
	if tree != "" {
		body += " tree=" + tree
	}
	if gerr != nil {
		body = fmt.Sprintf("engagement baseline at %s→%s: git error: %v", from, to, gerr)
	}
	payload, _ := json.Marshal(engagementBaselinePayload{HeadSHA: sha, Dirty: dirty, To: to, Worktree: tree})
	appendLedgerEntry(ctx, item.ID, ledger.KindEngagementBaseline, "executor", body, payload, now)
}

func hasEngagementBaseline(ctx context.Context, storyID string) bool {
	ls, err := requireLedger()
	if err != nil {
		return false
	}
	entries, err := ls.ListByStory(ctx, storyID, ledger.KindEngagementBaseline)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// firstEngagementBaseline returns the oldest engagement baseline payload, body,
// and CreatedAt (for mtime-anchored substrate enumeration).
func firstEngagementBaseline(ctx context.Context, storyID string) (engagementBaselinePayload, string, time.Time, error) {
	ls, err := requireLedger()
	if err != nil {
		return engagementBaselinePayload{}, "", time.Time{}, err
	}
	entries, err := ls.ListByStory(ctx, storyID, ledger.KindEngagementBaseline)
	if err != nil {
		return engagementBaselinePayload{}, "", time.Time{}, err
	}
	if len(entries) == 0 {
		return engagementBaselinePayload{}, "", time.Time{}, fmt.Errorf("no engagement baseline recorded for %s — engage the story first (or this story predates the baseline feature)", storyID)
	}
	e := entries[0] // oldest-first
	var p engagementBaselinePayload
	if len(e.Payload) > 0 {
		_ = json.Unmarshal(e.Payload, &p)
	}
	return p, e.Body, e.CreatedAt, nil
}

func gitHeadAndDirty(dir string) (sha string, dirty bool, err error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", false, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	sha = strings.TrimSpace(string(out))
	st, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return sha, false, fmt.Errorf("git status: %w", err)
	}
	dirty = len(bytes.TrimSpace(st)) > 0
	return sha, dirty, nil
}

// storyDiffReq is the request for story-diff.
type storyDiffReq struct {
	ID               string `json:"id"`
	Patch            bool   `json:"patch,omitempty"`
	Recorded         bool   `json:"recorded,omitempty"`          // union change_record rows (sty_948ad5df)
	IncludeSubstrate bool   `json:"include_substrate,omitempty"` // opt-in substrate mtime leg (sty_6469025e)
}

// storyDiffResult is the deterministic enumeration result (no verdict).
type storyDiffResult struct {
	StoryID  string   `json:"story_id"`
	Baseline string   `json:"baseline_sha"`
	DirtyAt  bool     `json:"baseline_dirty"`
	Files    []string `json:"files"`
	Stat     string   `json:"stat"`
	Patch    string   `json:"patch,omitempty"`
	Note     string   `json:"note,omitempty"`
	// Source is "live" (git re-derive) or "recorded" (change_record union).
	Source  string `json:"source,omitempty"`
	Records int    `json:"records,omitempty"`
}

func storyDiff(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	store, err := requireWorkItem()
	if err != nil {
		return nil, err
	}
	var req storyDiffReq
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
	}
	// Gate functional checks pipe {story, from, to} on stdin with no argv id
	// (sty_da169e03 AC3). Accept story.id / story_id when id is empty.
	if strings.TrimSpace(req.ID) == "" {
		var wrap struct {
			Story struct {
				ID string `json:"id"`
			} `json:"story"`
			StoryID string `json:"story_id"`
			ID      string `json:"id"`
		}
		if err := json.Unmarshal(raw, &wrap); err == nil {
			req.ID = wrap.ID
			if req.ID == "" {
				req.ID = wrap.StoryID
			}
			if req.ID == "" {
				req.ID = wrap.Story.ID
			}
		}
	}
	if strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("story-diff: id required (pass <id> or pipe transition payload with story.id on stdin)")
	}
	it, err := store.Get(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if it.Kind != workitem.KindStory {
		return nil, fmt.Errorf("story-diff: %s is not a story", req.ID)
	}

	// --recorded: union change_record file lists (sty_948ad5df). No git re-derive.
	if req.Recorded {
		files, n, rerr := recordedChangeSet(ctx, it.ID)
		if rerr != nil {
			return nil, rerr
		}
		res := storyDiffResult{
			StoryID: it.ID,
			Files:   files,
			Records: n,
			Source:  "recorded",
			Note:    "enumeration only — recorded change_record union; no pass/fail",
		}
		if n == 0 {
			res.Note = "no change_record rows yet — engage and transition to produce records"
		}
		return json.Marshal(res)
	}

	base, _, baseAt, err := firstEngagementBaseline(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	if base.HeadSHA == "" {
		return nil, fmt.Errorf("story-diff: engagement baseline for %s has empty head_sha (git was unavailable at engage)", it.ID)
	}
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	// Resolve to git top-level when possible so paths are repo-relative.
	if top := gitToplevel(dir); top != "" {
		dir = top
	}
	if aerr := refuseForeignTreeDiff(ctx, it.ID, base.Worktree, dir); aerr != nil {
		return nil, aerr
	}
	files, stat, patch, derr := gitDiffSince(dir, base.HeadSHA, req.Patch)
	if derr != nil {
		return nil, derr
	}
	// Opt-in substrate leg only (--include-substrate). Default live path stays
	// git-only so scope-review is not polluted by mtime noise (sty_6469025e).
	if req.IncludeSubstrate && !baseAt.IsZero() {
		sub := substrateChangedFiles(dir, authoredDirs, substrateConfigDir, baseAt)
		files = uniqueSorted(append(files, sub...))
	}
	res := storyDiffResult{
		StoryID:  it.ID,
		Baseline: base.HeadSHA,
		DirtyAt:  base.Dirty,
		Files:    files,
		Stat:     stat,
		Patch:    patch,
		Source:   "live",
		Note:     "enumeration only — no pass/fail; gates decide scope",
	}
	return json.Marshal(res)
}

// refuseForeignTreeDiff enforces the lease's working-tree anchor (sty_c098dc2d).
// A story engaged from tree A diffs against a baseline taken in A; run from tree
// B it would enumerate B's unrelated changes and attribute them to the story —
// which is exactly the mis-attribution per-worktree leases exist to prevent.
//
// The LIVE lease is the authority when one exists (it is where the engagement
// actually sits); the ledger baseline is the fallback for a story whose lease
// has since been released. Either anchor absent → UNANCHORED, and the diff
// proceeds as it always did, so pre-upgrade baselines and non-git checkouts are
// unaffected.
func refuseForeignTreeDiff(ctx context.Context, storyID, baselineTree, invokedTree string) error {
	anchor := strings.TrimSpace(baselineTree)
	if ls, err := requireLease(); err == nil {
		if l, gerr := ls.Get(ctx, storyID); gerr == nil && strings.TrimSpace(l.Worktree) != "" {
			anchor = strings.TrimSpace(l.Worktree)
		}
	}
	if anchor == "" || strings.TrimSpace(invokedTree) == "" || anchor == invokedTree {
		return nil
	}
	return fmt.Errorf(
		"story-diff: %s was engaged from working tree %s but this invocation is in %s — the diff would attribute this tree's changes to the story. Run `satelle story diff %s` from %s",
		storyID, anchor, invokedTree, storyID, anchor)
}

// gitDiffSince lists name-only files and --stat from baseline to the current
// worktree (includes uncommitted tracked edits and untracked files). Optional
// full patch. File list is sorted deterministically.
func gitDiffSince(dir, baseline string, wantPatch bool) (files []string, stat, patch string, err error) {
	// name-only (includes renames as "old => new" lines — still deterministic)
	nameOut, err := exec.Command("git", "-C", dir, "diff", "--name-only", baseline).Output()
	if err != nil {
		return nil, "", "", fmt.Errorf("git diff --name-only %s: %w", baseline, err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(nameOut), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			files = append(files, line)
		}
	}
	// Untracked (not in index): pure-new-file slices must appear (AC2).
	untracked, uerr := exec.Command("git", "-C", dir, "ls-files", "--others", "--exclude-standard").Output()
	if uerr != nil {
		return nil, "", "", fmt.Errorf("git ls-files --others: %w", uerr)
	}
	for _, line := range strings.Split(string(untracked), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			files = append(files, line)
		}
	}
	sort.Strings(files)

	statOut, err := exec.Command("git", "-C", dir, "diff", "--stat", baseline).Output()
	if err != nil {
		return nil, "", "", fmt.Errorf("git diff --stat %s: %w", baseline, err)
	}
	stat = string(statOut)
	if len(bytes.TrimSpace(untracked)) > 0 {
		// Append untracked summary so stat alone is not empty when only new files.
		stat = strings.TrimRight(stat, "\n") + "\n# untracked:\n" + string(untracked)
	}

	if wantPatch {
		patchOut, err := exec.Command("git", "-C", dir, "diff", baseline).Output()
		if err != nil {
			return nil, "", "", fmt.Errorf("git diff %s: %w", baseline, err)
		}
		patch = string(patchOut)
	}
	return files, stat, patch, nil
}
