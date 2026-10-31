package verb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/textdiff"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// The substrate leg of a live story diff. A story that changes gitignored
// substrate leaves git nothing to diff, so a reviewer handed only the git slice
// judged an empty change. The files here are listed by the mtime walk (or, for a
// closed story, by its recorded change_records) and rendered as whole-file adds:
// the engagement baseline holds paths, not content, so there is no older side.

const (
	// substrateBinarySniff is how much of a file is checked for a NUL byte.
	substrateBinarySniff = 8 << 10
	// concurrentScanPage is the ledger page size of the concurrent-engagement scan.
	concurrentScanPage = 1000
)

// substrateFile is one substrate path and where its bytes live: Rel is the path
// shown, Abs the file read (a worktree story's process-root paths sit in the
// main tree, not under the slice).
type substrateFile struct {
	Rel, Abs string
}

// substratePatch renders added files as whole-file adds through the shared
// unified-diff renderer and deleted paths as a header only (no baseline content
// survives to show). labels follow the section header as comment lines. The
// section is capped at changeRecordPatchLimit with a cut marker; fillDiff's own
// ceiling and offload then apply to the whole patch. It returns "" when there is
// nothing to render.
func substratePatch(added []substrateFile, deleted []string, labels []string) string {
	if len(added) == 0 && len(deleted) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# substrate (untracked; whole file shown)\n")
	for _, l := range labels {
		b.WriteString("# " + l + "\n")
	}
	cut := func() bool {
		if b.Len() <= changeRecordPatchLimit {
			return false
		}
		s := b.String()[:changeRecordPatchLimit]
		b.Reset()
		b.WriteString(s)
		fmt.Fprintf(&b, "\n... [truncated at %d bytes]\n", changeRecordPatchLimit)
		return true
	}
	for _, f := range added {
		fh, err := os.Open(f.Abs)
		if err != nil {
			continue
		}
		body, rerr := io.ReadAll(io.LimitReader(fh, int64(changeRecordPatchLimit)+1))
		fh.Close()
		if rerr != nil {
			continue
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode 100644\n", f.Rel, f.Rel)
		if bytes.IndexByte(body[:min(len(body), substrateBinarySniff)], 0) >= 0 {
			fmt.Fprintf(&b, "Binary file %s changed\n", f.Rel)
		} else {
			textdiff.Unified(&b, "/dev/null", "b/"+f.Rel, "", string(body))
		}
		if cut() {
			return b.String()
		}
	}
	for _, p := range deleted {
		fmt.Fprintf(&b, "diff --git a/%s b/%s\ndeleted file mode 100644\ndeleted file %s\n", p, p, p)
	}
	cut()
	return b.String()
}

// substrateStat lists substrate-only paths for the diff stat, mirroring the
// "# untracked:" summary the git stat carries.
func substrateStat(added []substrateFile, deleted []string) string {
	if len(added) == 0 && len(deleted) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# substrate (untracked):\n")
	for _, f := range added {
		b.WriteString(f.Rel + "\n")
	}
	for _, p := range deleted {
		b.WriteString(p + " (deleted)\n")
	}
	return b.String()
}

// recordedSubstrate is a closed story's substrate change set: its recorded
// change_record union narrowed to the substrate dirs. The live mtime walk has no
// upper bound, so it would hand a closed story every later edit another story
// made (sty_98a2816b); the records froze at the story's own transitions. Paths
// under the process root (a worktree story's main-tree substrate) come back in
// procRel with that root, the rest in local.
func recordedSubstrate(ctx context.Context, storyID, sliceRoot string) (local, procRel []string, processRoot string) {
	files, _, _, err := recordedChangeSet(ctx, storyID)
	if err != nil || len(files) == 0 {
		return nil, nil, ""
	}
	root := processRootForSubstrate()
	var prefixes []string
	dirs := []string{substrateConfigDir}
	for _, d := range authoredDirs {
		dirs = append(dirs, d)
	}
	for _, d := range dirs {
		for _, base := range []string{sliceRoot, root} {
			if rel, ok := substrateRelUnder(base, d); ok && d != "" && rel != "." {
				prefixes = append(prefixes, rel+"/")
			}
		}
	}
	for _, p := range files {
		if !hasAnyPrefix(p, prefixes) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(sliceRoot, filepath.FromSlash(p))); err != nil && root != "" {
			if _, rerr := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); rerr == nil {
				procRel = append(procRel, p)
				continue
			}
		}
		local = append(local, p)
	}
	if len(procRel) > 0 {
		processRoot = root
	}
	return local, procRel, processRoot
}

// closedAt is when the story last entered a terminal state, from its ledger;
// zero when it never did.
func closedAt(ctx context.Context, it workitem.Item) time.Time {
	led, err := requireLedger()
	if err != nil {
		return time.Time{}
	}
	rows, err := led.ListByStory(ctx, it.ID, ledger.KindStatusTransition)
	if err != nil {
		return time.Time{}
	}
	var at time.Time
	for _, e := range rows {
		if targetIsTerminalStateOnly(ctx, it, TransitionTo(e)) {
			at = e.CreatedAt
		}
	}
	return at
}

// concurrentEngagements names the other stories whose engagement overlaps
// [from, to], so a reviewer is told the mtime walk cannot say which of them wrote
// a substrate file. The ledger owns the windows: a story's engagement_baseline
// opens one and its first later status_transition into a terminal or park state
// closes it, and a change_record inside [from, to] counts on its own. Released
// leases leave no row, so a lease read alone would miss every sibling that has
// already closed. The scan is the time-bounded ListChangedSince, oldest first
// from `from`, paged until a short page or a row past `to`; a sibling whose
// baseline precedes `from` either closes or writes inside the window (both are
// read) or is still open and therefore holds a live lease, which is added last.
// Over-warning (a sibling in its own worktree, or one that touched only code)
// is deliberate: the label warns, it never excludes.
func concurrentEngagements(ctx context.Context, it workitem.Item, from, to time.Time) []string {
	found := map[string]bool{}
	if led, err := requireLedger(); err == nil && !from.IsZero() {
		type window struct {
			open    bool
			start   time.Time
			overlap bool
		}
		wins := map[string]*window{}
		items := map[string]workitem.Item{}
		closes := func(sid, status string) bool {
			item, ok := items[sid]
			if !ok {
				if ws, werr := requireWorkItem(); werr == nil {
					item, _ = ws.Get(ctx, sid)
				}
				items[sid] = item
			}
			if item.ID == "" {
				return false
			}
			return targetIsTerminalStateOnly(ctx, item, status) || statusIsParkState(ctx, item, status)
		}
	scan:
		for offset := 0; ; {
			page, perr := led.ListChangedSince(ctx, from, concurrentScanPage, offset)
			if perr != nil {
				break
			}
			for _, e := range page {
				if e.CreatedAt.After(to) {
					// The store orders by created_at as TEXT, so rows within one
					// second can sort out of chronological order: step past a late
					// row rather than stopping at the first one.
					if e.CreatedAt.After(to.Add(2 * time.Second)) {
						break scan
					}
					continue
				}
				if e.StoryID == "" || e.StoryID == it.ID {
					continue
				}
				w := wins[e.StoryID]
				if w == nil {
					w = &window{}
					wins[e.StoryID] = w
				}
				switch e.Kind {
				case ledger.KindEngagementBaseline:
					if !w.open {
						w.open, w.start = true, e.CreatedAt
					}
				case ledger.KindChangeRecord:
					if !e.CreatedAt.Before(from) {
						w.overlap = true
					}
				case ledger.KindStatusTransition:
					if closes(e.StoryID, TransitionTo(e)) && !e.CreatedAt.Before(from) {
						// Closed inside the window; a close with no baseline in
						// this read means the window opened before `from`.
						w.overlap = true
						w.open = false
					}
				}
			}
			if len(page) < concurrentScanPage {
				break
			}
			offset += len(page)
		}
		for sid, w := range wins {
			if w.overlap || w.open {
				found[sid] = true
			}
		}
	}
	if ls, err := requireLease(); err == nil {
		if all, lerr := ls.List(ctx); lerr == nil {
			for _, l := range all {
				if l.StorySeat && l.ItemID != it.ID && !l.AcquiredAt.After(to) {
					found[l.ItemID] = true
				}
			}
		}
	}
	ids := make([]string, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
