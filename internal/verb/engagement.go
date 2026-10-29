package verb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/trunk"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Classified engagement-slice errors (sty_76796b8e). liveStoryDiff returns them
// as errors (unchanged contract). story-proof maps them to exit-0 state strings
// so a functional check can report rather than fail the transition.
var (
	errNoBaseline  = errors.New("no engagement baseline")
	errEmptyHead   = errors.New("engagement baseline has empty head_sha")
	errForeignTree = errors.New("story engaged from a different working tree")
	errNoGit       = errors.New("git unavailable")
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
	// SubstrateManifest is the sorted repo-relative path of every authored
	// substrate file present at engagement (sty_7e1e2deb). Paths only, no
	// hashes — it exists so `story diff --include-substrate` can report what has
	// since VANISHED, which the mtime walk can never see. Absent on baselines
	// recorded before it existed; those report no deletions, exactly as before.
	SubstrateManifest []string `json:"substrate_manifest,omitempty"`
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
	// The tree the trunk check inspected and fast-forwarded (sty_9f3e51d1), so the
	// HEAD recorded here is the one the story starts from.
	dir := engageRepoDir()
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
	// The manifest is anchored to the repo root when git can name one, so its
	// paths are repo-relative in the same path-space story-diff resolves in.
	manifestRoot := dir
	if tree != "" {
		manifestRoot = tree
	}
	payload, _ := json.Marshal(engagementBaselinePayload{
		HeadSHA:           sha,
		Dirty:             dirty,
		To:                to,
		Worktree:          tree,
		SubstrateManifest: substrateManifest(manifestRoot, authoredDirs, substrateConfigDir),
	})
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

// StoryDiffResult is the deterministic enumeration result (no verdict).
// Shared by `satelle story diff` and the reviewer-payload Diff field (sty_a125b440).
type StoryDiffResult struct {
	StoryID string `json:"story_id"`
	// Baseline is the diff anchor: the engagement baseline, or the latest resume
	// re-anchor once the story has been parked and resumed (it moves on resume).
	Baseline string `json:"baseline_sha"`
	// EngagementSHA is the HEAD the story first engaged at. It never moves, so a
	// check that judges "since engagement" (the version stamp) reads this, not
	// Baseline (sty_1168e79f).
	EngagementSHA string   `json:"engagement_sha,omitempty"`
	DirtyAt       bool     `json:"baseline_dirty"`
	Files         []string `json:"files"`
	Stat          string   `json:"stat"`
	Patch         string   `json:"patch,omitempty"`
	Note          string   `json:"note,omitempty"`
	// Source is "live" (git re-derive) or "recorded" (change_record union).
	Source  string `json:"source,omitempty"`
	Records int    `json:"records,omitempty"`
	// ProcessRoot / ProcessRootFiles name the substrate a story engaged from a
	// linked worktree changed in the MAIN tree (sty_92e4cdbe): the process of
	// record lives there, not in the worktree. The same paths are also in Files;
	// these fields say they are relative to ProcessRoot. Omitted for a story
	// engaged in the main tree.
	ProcessRoot      string   `json:"process_root,omitempty"`
	ProcessRootFiles []string `json:"process_root_files,omitempty"`
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
		res := StoryDiffResult{
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

	res, err := liveStoryDiff(ctx, it, req.Patch, req.IncludeSubstrate)
	if err != nil {
		return nil, err
	}
	return json.Marshal(res)
}

// StoryDiff enumerates the live git slice since the story's engagement baseline
// (sty_a125b440). Same derivation as `satelle story diff` without --recorded.
// Errors on unknown story, missing baseline, empty head_sha, foreign tree, or
// git failure — the reviewer-payload resolver maps those to a no-baseline marker
// and must never fail a transition.
func StoryDiff(ctx context.Context, id string, opts StoryDiffOpts) (StoryDiffResult, error) {
	store, err := requireWorkItem()
	if err != nil {
		return StoryDiffResult{}, err
	}
	if strings.TrimSpace(id) == "" {
		return StoryDiffResult{}, fmt.Errorf("story-diff: id required")
	}
	it, err := store.Get(ctx, id)
	if err != nil {
		return StoryDiffResult{}, err
	}
	if it.Kind != workitem.KindStory {
		return StoryDiffResult{}, fmt.Errorf("story-diff: %s is not a story", id)
	}
	return liveStoryDiff(ctx, it, opts.Patch, opts.IncludeSubstrate)
}

// StoryDiffOpts selects what StoryDiff carries. The zero value is the git-only
// enumeration; IncludeSubstrate adds the gitignored substrate the story changed
// (paths, and with Patch their content), the way `satelle story diff
// --include-substrate` does.
type StoryDiffOpts struct {
	Patch            bool
	IncludeSubstrate bool
}

func liveStoryDiff(ctx context.Context, it workitem.Item, wantPatch, includeSubstrate bool) (StoryDiffResult, error) {
	sl, err := resolveEngagementSlice(ctx, it, wantPatch)
	if err != nil {
		return StoryDiffResult{}, err
	}
	files := sl.Files
	stat, patch := sl.Stat, sl.Patch
	note := "enumeration only — no pass/fail; gates decide scope"
	for _, w := range sl.Windows {
		note += fmt.Sprintf("; excluded park interval %s..%s", w.From, w.To)
	}
	var processRoot string
	var processFiles []string
	// Opt-in substrate leg only (--include-substrate). Default live path stays
	// git-only so scope-review is not polluted by mtime noise (sty_6469025e).
	if includeSubstrate {
		// A story past its terminal edge reads its recorded change_records: the
		// mtime walk has no upper bound and would attribute to it every later edit
		// another story made (sty_98a2816b). Deletions are not recorded, so a
		// closed story reports none.
		closed := targetIsTerminalStateOnly(ctx, it, it.Status)
		var local, procRel, deleted []string
		var root string
		if closed {
			local, procRel, root = recordedSubstrate(ctx, it.ID, sl.Dir)
		} else {
			if !sl.BaseAt.IsZero() {
				local, procRel, root = substrateChangedFilesSplit(sl.Dir, authoredDirs, substrateConfigDir, sl.BaseAt)
			}
			// Deletions are measured against the FIRST engagement baseline's manifest,
			// not the resume re-anchor above: the manifest says what existed when the
			// story took the tree, and that is the only thing a vanished path can be
			// compared to. Do not "fix" this to re-anchor with sinceSHA.
			deleted = substrateDeletedFiles(sl.Dir, sl.Base.SubstrateManifest)
		}
		files = append(files, local...)
		files = append(files, procRel...)
		files = append(files, deleted...)
		files = uniqueSorted(files)
		if len(procRel) > 0 {
			processRoot, processFiles = root, procRel
			note += "; process_root_files live in the main tree at " + root
		}

		// A path git already lists (a tracked substrate file) is in the git patch;
		// only the rest needs the whole-file render.
		inGit := map[string]bool{}
		for _, f := range sl.Files {
			inGit[f] = true
		}
		var added []substrateFile
		for _, p := range local {
			if !inGit[p] {
				added = append(added, substrateFile{Rel: p, Abs: filepath.Join(sl.Dir, filepath.FromSlash(p))})
			}
		}
		for _, p := range procRel {
			added = append(added, substrateFile{Rel: p, Abs: filepath.Join(root, filepath.FromSlash(p))})
		}
		var gone []string
		for _, p := range deleted {
			if !inGit[p] {
				gone = append(gone, p)
			}
		}
		if s := substrateStat(added, gone); s != "" {
			stat = strings.TrimRight(stat, "\n") + "\n" + s
		}
		if wantPatch {
			var labels []string
			if closed {
				labels = append(labels, "current content; may postdate this story's close")
			}
			if len(added)+len(gone) > 0 {
				to := time.Now()
				if closed {
					if at := closedAt(ctx, it); !at.IsZero() {
						to = at
					}
				}
				if others := concurrentEngagements(ctx, it, sl.BaseAt, to); len(others) > 0 {
					labels = append(labels, "shared-attribution: also in flight "+strings.Join(others, ", ")+"; mtime cannot tell which story wrote these")
				}
			}
			if sub := substratePatch(added, gone, labels); sub != "" {
				if patch != "" && !strings.HasSuffix(patch, "\n") {
					patch += "\n"
				}
				patch += sub
			}
		}
	}
	return StoryDiffResult{
		StoryID: it.ID,
		// The anchor actually used, so the surface never claims to have diffed
		// from a point it did not.
		Baseline:      sl.SinceSHA,
		EngagementSHA: sl.Base.HeadSHA,
		DirtyAt:       sl.Base.Dirty,
		Files:         files,
		Stat:          stat,
		Patch:         patch,
		Source:        "live",
		Note:          note,

		ProcessRoot:      processRoot,
		ProcessRootFiles: processFiles,
	}, nil
}

// engagementTreeDir is the working tree a gate judges: the invoking directory's
// git toplevel, replaced by the story's own anchor under GateStoryEnv
// (gateTreeOverride). baselineTree may be empty — the anchor then comes from the
// lease alone, or the invoking tree stands.
func engagementTreeDir(ctx context.Context, storyID, baselineTree string) string {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	if top := gitToplevel(dir); top != "" {
		dir = top
	}
	if anchor := gateTreeOverride(ctx, storyID, baselineTree, dir); anchor != "" {
		dir = anchor
	}
	return dir
}

// StoryGateTree is the working tree a gate judges the story in, resolved the way
// resolveEngagementSlice resolves it but without requiring an engagement
// baseline: the readiness edge runs before one exists. A recorded baseline's
// worktree is the anchor fallback; without one the invoking tree (or the lease
// anchor under GateStoryEnv) stands.
func StoryGateTree(ctx context.Context, storyID string) (string, error) {
	baselineTree := ""
	if base, _, _, err := firstEngagementBaseline(ctx, storyID); err == nil {
		baselineTree = base.Worktree
	}
	if _, err := os.Getwd(); err != nil {
		return "", fmt.Errorf("judged tree: %w", err)
	}
	return engagementTreeDir(ctx, storyID, baselineTree), nil
}

// engagementSlice is the live git enumeration since a story's engagement
// (or resume re-anchor). Owned by resolveEngagementSlice so story-diff and
// story-proof cannot drift on WHEN / WHICH TREE (sty_76796b8e).
type engagementSlice struct {
	SinceSHA string
	Dir      string
	Base     engagementBaselinePayload
	BaseAt   time.Time
	Files    []string
	Stat     string
	Patch    string
	// Windows are the park intervals left out of the slice.
	Windows []parkWindow
}

func resolveEngagementSlice(ctx context.Context, it workitem.Item, wantPatch bool) (engagementSlice, error) {
	base, _, baseAt, err := firstEngagementBaseline(ctx, it.ID)
	if err != nil {
		return engagementSlice{}, fmt.Errorf("%w: %v", errNoBaseline, err)
	}
	if base.HeadSHA == "" {
		return engagementSlice{}, fmt.Errorf("%w: story-diff: engagement baseline for %s has empty head_sha (git was unavailable at engage)", errEmptyHead, it.ID)
	}
	// A story resumed from a park enumerates its whole engagement minus the park
	// intervals: commits another story landed while this one sat parked are not its
	// work (sty_526d6a68), the commits it made before parking still are. The
	// worktree anchor and DirtyAt come from the engagement baseline; the substrate
	// walk (BaseAt) still starts at the resume time.
	sinceSHA := base.HeadSHA
	windows := parkWindows(ctx, it.ID, sinceSHA)
	if _, at, ok := latestResumeReanchor(ctx, it.ID); ok {
		baseAt = at
	}
	dir := engagementTreeDir(ctx, it.ID, base.Worktree)
	if aerr := refuseForeignTreeDiff(ctx, it.ID, base.Worktree, dir); aerr != nil {
		return engagementSlice{}, fmt.Errorf("%w: %v", errForeignTree, aerr)
	}
	files, stat, patch, derr := storyChangeSince(ctx, dir, sinceSHA, it.ID, windows, wantPatch)
	if derr != nil {
		return engagementSlice{}, fmt.Errorf("%w: %v", errNoGit, derr)
	}
	return engagementSlice{
		SinceSHA: sinceSHA,
		Dir:      dir,
		Base:     base,
		BaseAt:   baseAt,
		Files:    files,
		Stat:     stat,
		Patch:    patch,
		Windows:  windows,
	}, nil
}

// GateStoryEnv names the story a gate's functional check is judging. The engine
// exports it into the check's environment (sty_ab93f9a6): a check runs in the
// MAIN tree, the process of record, yet a story engaged from a linked worktree is
// anchored to that worktree, so the check could not otherwise read its change
// set. `story diff` honours it for the named story alone — see gateTreeOverride.
const GateStoryEnv = config.GateStoryEnv

type gateStoryKey struct{}

// WithGateStory marks ctx as judging storyID on a gate's behalf: the in-process
// counterpart of GateStoryEnv, for enumeration the gating process itself runs
// (the reviewer payload's diff). A context value rather than the environment, so
// the mark never outlives the call or reaches another story's enumeration.
func WithGateStory(ctx context.Context, storyID string) context.Context {
	return context.WithValue(ctx, gateStoryKey{}, storyID)
}

// gateStoryNamed reports whether GateStoryEnv or a WithGateStory mark names storyID.
func gateStoryNamed(ctx context.Context, storyID string) bool {
	if strings.TrimSpace(os.Getenv(GateStoryEnv)) == storyID {
		return true
	}
	marked, _ := ctx.Value(gateStoryKey{}).(string)
	return marked != "" && marked == storyID
}

// storyAnchor is the working tree a story is engaged from: the live lease's tree
// when one exists, else the engagement baseline's. Empty when unanchored.
func storyAnchor(ctx context.Context, storyID, baselineTree string) string {
	anchor := strings.TrimSpace(baselineTree)
	if ls, err := requireLease(); err == nil {
		if l, gerr := ls.Get(ctx, storyID); gerr == nil && strings.TrimSpace(l.Worktree) != "" {
			anchor = strings.TrimSpace(l.Worktree)
		}
	}
	return anchor
}

// gateTreeOverride resolves the tree `story diff` should read when it runs under
// a gate's functional check (sty_ab93f9a6). It returns the story's own anchor —
// not the invoking tree — only when ALL hold: GateStoryEnv or a WithGateStory
// mark names this very story,
// the story is anchored, the anchor still exists, and it is a working tree of the
// same repository as the invoking tree. Otherwise "", and the ordinary refusal
// stands, so the variable cannot redirect a diff to another story's tree or to an
// unrelated repository.
func gateTreeOverride(ctx context.Context, storyID, baselineTree, invokedTree string) string {
	if !gateStoryNamed(ctx, storyID) {
		return ""
	}
	anchor := storyAnchor(ctx, storyID, baselineTree)
	if anchor == "" || anchor == invokedTree {
		return ""
	}
	if fi, err := os.Stat(anchor); err != nil || !fi.IsDir() {
		return ""
	}
	want, got := gitCommonDir(invokedTree), gitCommonDir(anchor)
	if want == "" || want != got {
		return ""
	}
	return anchor
}

// gitCommonDir is dir's repository's shared git directory — identical for the main
// tree and every linked worktree of one repository. Empty when git cannot answer.
func gitCommonDir(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(out))
	if r, rerr := filepath.EvalSymlinks(p); rerr == nil {
		p = r
	}
	return p
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
	anchor := storyAnchor(ctx, storyID, baselineTree)
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
	files, stat, patch, err = gitDiffLeg(dir, []string{baseline}, wantPatch)
	if err != nil {
		return nil, "", "", err
	}
	return withUntracked(dir, files, stat, patch)
}

// storyChangeSince is a story's change set from sinceSHA to the current tree: what
// gitDiffSince lists, minus what is not the story's work (sty_659c4133, sty_666e6110):
//   - a commit the remote trunk already holds whose subject does not name storyID —
//     the convention satelle-substrate-only-check's channel 3 reads — that a
//     fast-forward put in range;
//   - a commit in a park window whose subject does not name storyID: another story
//     landed it while this one sat parked;
//   - what a merge of the trunk into the story's own branch brought in, other than
//     the merge's own conflict resolutions.
//
// The story's own commits and uncommitted changes always stay. With nothing to
// leave out it is exactly gitDiffSince.
func storyChangeSince(ctx context.Context, dir, sinceSHA, storyID string, windows []parkWindow, wantPatch bool) (files []string, stat, patch string, err error) {
	own, composed := walkStoryRange(ctx, dir, sinceSHA, storyID, windows)
	if !composed {
		return gitDiffSince(dir, sinceSHA, wantPatch)
	}
	// The change set is composed from its own parts, so files, stat and patch stay
	// consistent: each own commit against its first parent, then what is not committed.
	legs := make([][]string, 0, len(own)+1)
	for _, c := range own {
		revs := []string{c.SHA + "^", c.SHA}
		if c.Narrowed {
			paths := mergeResolvedPaths(dir, c.SHA)
			if len(paths) == 0 {
				continue
			}
			revs = append(revs, "--")
			revs = append(revs, paths...)
		}
		legs = append(legs, revs)
	}
	legs = append(legs, []string{"HEAD"})
	seen := map[string]bool{}
	var stats, patches strings.Builder
	for _, revs := range legs {
		f, s, p, lerr := gitDiffLeg(dir, revs, wantPatch)
		if lerr != nil {
			return nil, "", "", lerr
		}
		for _, name := range f {
			if !seen[name] {
				seen[name] = true
				files = append(files, name)
			}
		}
		stats.WriteString(s)
		patches.WriteString(p)
	}
	return withUntracked(dir, files, stats.String(), patches.String())
}

// rangeCommit is a first-parent commit that stays in a story's change set.
// Narrowed marks a merge of the trunk into the story's branch: only the paths the
// merge itself resolved stay, not what the trunk brought in.
type rangeCommit struct {
	SHA      string
	Narrowed bool
}

// splitRangeByTrunk walks sinceSHA..HEAD along the first parent and returns the
// commits that stay in the story's change set, oldest first, and whether any
// commit in range was left out as another story's published work or narrowed to
// its own resolutions.
func splitRangeByTrunk(ctx context.Context, dir, sinceSHA, storyID string) (own []string, foreign bool) {
	commits, foreign := walkStoryRange(ctx, dir, sinceSHA, storyID, nil)
	for _, c := range commits {
		own = append(own, c.SHA)
	}
	return own, foreign
}

// walkStoryRange is splitRangeByTrunk with park windows: a commit inside a window
// stays only when its subject names the story, and never when the window is All.
// With no remote trunk and no windows there is nothing to classify.
func walkStoryRange(ctx context.Context, dir, sinceSHA, storyID string, windows []parkWindow) (own []rangeCommit, composed bool) {
	ref := trunk.RemoteTrunkRef(ctx, dir, trunkCfg.Branch)
	if (ref == "" && len(windows) == 0) || storyID == "" {
		return nil, false
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "log", "--first-parent", "--reverse",
		"--format=%H%x00%P%x00%s", sinceSHA+"..HEAD").Output()
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		sha, parents, named := parts[0], strings.Fields(parts[1]), strings.Contains(parts[2], storyID)
		if inWindow, all := inParkWindow(ctx, dir, sha, windows); inWindow {
			if !named || all {
				composed = true
				continue
			}
		} else if ref != "" && trunkMergeIntoStory(ctx, dir, sha, parents, ref) {
			composed = true
			own = append(own, rangeCommit{SHA: sha, Narrowed: true})
			continue
		} else if ref != "" && !named && onTrunk(ctx, dir, sha, ref) {
			composed = true
			continue
		}
		own = append(own, rangeCommit{SHA: sha})
	}
	return own, composed
}

// inParkWindow reports whether sha lies in a park interval — reachable from its
// resume head but not from its park head — and whether that interval is All.
func inParkWindow(ctx context.Context, dir, sha string, windows []parkWindow) (in, all bool) {
	for _, w := range windows {
		if isAncestor(ctx, dir, sha, w.To) && !isAncestor(ctx, dir, sha, w.From) {
			in = true
			all = all || w.All
		}
	}
	return in, all
}

// trunkMergeIntoStory reports whether sha is a merge the story made of the trunk
// into its own branch: the trunk does not hold the merge itself, yet holds every
// parent it merged in. A merge already on the trunk is another story's PR landing
// and keeps the subject rule.
func trunkMergeIntoStory(ctx context.Context, dir, sha string, parents []string, ref string) bool {
	if len(parents) < 2 || onTrunk(ctx, dir, sha, ref) {
		return false
	}
	for _, p := range parents[1:] {
		if !onTrunk(ctx, dir, p, ref) {
			return false
		}
	}
	return true
}

// mergeResolvedPaths lists the paths where the merge differs from every parent —
// its own conflict resolutions.
func mergeResolvedPaths(dir, merge string) []string {
	out, err := exec.Command("git", "-C", dir, "diff-tree", "--no-commit-id", "-r", "--cc", "--name-only", merge).Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

// onTrunk reports whether commit is reachable from ref. An error from git is
// "not on trunk": the commit then stays in the change set rather than vanishing.
func onTrunk(ctx context.Context, dir, commit, ref string) bool {
	return isAncestor(ctx, dir, commit, ref)
}

// isAncestor reports whether commit is reachable from tip. An error from git
// (an unknown or unrelated commit) answers false.
func isAncestor(ctx context.Context, dir, commit, tip string) bool {
	return exec.CommandContext(ctx, "git", "-C", dir, "merge-base", "--is-ancestor", commit, tip).Run() == nil
}

// gitDiffLeg is one tracked-files leg of a change set: name-only, --stat and
// (optionally) the patch of `git diff <revs...>`. Files are deduplicated, in git's order.
func gitDiffLeg(dir string, revs []string, wantPatch bool) (files []string, stat, patch string, err error) {
	diff := func(args ...string) ([]byte, error) {
		return exec.Command("git", append(append([]string{"-C", dir}, args...), revs...)...).Output()
	}
	// name-only (includes renames as "old => new" lines — still deterministic)
	nameOut, err := diff("diff", "--name-only")
	if err != nil {
		return nil, "", "", fmt.Errorf("git diff --name-only %s: %w", strings.Join(revs, " "), err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(nameOut), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			files = append(files, line)
		}
	}
	statOut, err := diff("diff", "--stat")
	if err != nil {
		return nil, "", "", fmt.Errorf("git diff --stat %s: %w", strings.Join(revs, " "), err)
	}
	stat = string(statOut)

	if wantPatch {
		// Force a/ b/ headers regardless of the operator's git config: compact.CompactPatch's
		// noise/whitespace offload keys off "diff --git a/X b/Y" and the ---/+++ paths, so a
		// machine with diff.mnemonicPrefix or diff.noprefix set must not change the header shape
		// underneath it (sty_75b76691 AC2).
		patchOut, err := diff("-c", "diff.mnemonicPrefix=false", "-c", "diff.noprefix=false",
			"diff", "--src-prefix=a/", "--dst-prefix=b/")
		if err != nil {
			return nil, "", "", fmt.Errorf("git diff %s: %w", strings.Join(revs, " "), err)
		}
		patch = string(patchOut)
	}
	return files, stat, patch, nil
}

// withUntracked adds the untracked, non-ignored files to a change set's tracked
// legs (AC2: pure-new-file slices must appear) and sorts the file list.
func withUntracked(dir string, files []string, stat, patch string) ([]string, string, string, error) {
	untracked, err := exec.Command("git", "-C", dir, "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, "", "", fmt.Errorf("git ls-files --others: %w", err)
	}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		seen[f] = true
	}
	for _, line := range strings.Split(string(untracked), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			files = append(files, line)
		}
	}
	sort.Strings(files)
	if len(bytes.TrimSpace(untracked)) > 0 {
		// Append untracked summary so stat alone is not empty when only new files.
		stat = strings.TrimRight(stat, "\n") + "\n# untracked:\n" + string(untracked)
	}
	return files, stat, patch, nil
}
