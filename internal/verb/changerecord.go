package verb

// Change-record retention at enacted transitions (sty_948ad5df).
// Enumeration only — never a gate verdict.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Caps (package vars so tests may shrink them). Disk retention only —
// type:change attachments are excluded from gate payloads (sty_948ad5df).
var (
	changeRecordPatchLimit = 1 << 20 // 1 MiB
	changeRecordFileLimit  = 2000
)

// changeRecordPayload is the ledger payload for KindChangeRecord.
// Paths and counts only — never file content (sync-safe half).
type changeRecordPayload struct {
	From           string   `json:"from"`
	To             string   `json:"to"`
	HeadSHA        string   `json:"head_sha,omitempty"`
	SinceSHA       string   `json:"since_sha,omitempty"`
	Files          []string `json:"files"`
	FileCount      int      `json:"file_count"`
	FilesTruncated bool     `json:"files_truncated,omitempty"`
	PatchAttached  bool     `json:"patch_attached,omitempty"`
	PatchTruncated bool     `json:"patch_truncated,omitempty"`
	PatchName      string   `json:"patch_name,omitempty"`
	Unavailable    string   `json:"unavailable,omitempty"`
	// PartialEnumeration marks a row a binary verb wrote for the paths IT
	// mutated, rather than a transition-time enumeration of everything since the
	// last anchor (sty_30d3bd99). Anchor selection walks past these rows: a
	// partial row must never advance the anchor over changes no full enumeration
	// has seen yet.
	PartialEnumeration bool `json:"partial_enumeration,omitempty"`
	// ReanchorResume marks the row written when a story LEAVES a park state
	// (sty_526d6a68). It says why the row carries no files: nothing was
	// enumerated, the anchor was moved to the resume point so commits other
	// stories landed during the park are not attributed to this one.
	ReanchorResume bool `json:"reanchor_resume,omitempty"`
}

// authoredDirs is wired by SetAuthoredDirs (substrate roots for leg C).
var authoredDirs map[string]string

// substrateConfigDir is the resolved .satelle data dir (agents.toml, hooks, …).
// Wired by SetSubstrateConfigDir; not a key in authoredDirs.
var substrateConfigDir string

// SetAuthoredDirs wires authored-markdown roots for the substrate change leg.
func SetAuthoredDirs(dirs map[string]string) { authoredDirs = dirs }

// SetSubstrateConfigDir wires the resolved data dir (typically <repo>/.satelle)
// so non-kind config (agents.toml, constitution.md, hooks/) is enumerable.
func SetSubstrateConfigDir(dir string) { substrateConfigDir = dir }

// recordChangeSet ledgers the files changed during the step just closed.
// Best-effort: never fails the transition.
func recordChangeSet(ctx context.Context, item workitem.Item, from, to string, now time.Time) {
	if item.Kind != workitem.KindStory {
		return
	}
	// Leaving a park: enumerate NOTHING and move the anchor to the resume point.
	// The park-entry row anchored at park-time HEAD, so enumerating here would
	// sweep in every commit another story landed while this one sat parked
	// (sty_526d6a68).
	if statusIsParkState(ctx, item, from) {
		recordResumeReanchor(ctx, item, from, to, now)
		return
	}
	payload := changeRecordPayload{
		From:  from,
		To:    to,
		Files: []string{},
	}
	dir := changeRecordRepoRoot()

	sinceSHA, _, unavail := changeRecordAnchor(ctx, item.ID)
	payload.SinceSHA = sinceSHA
	payload.Unavailable = unavail

	head, _, gerr := gitHeadAndDirty(dir)
	if gerr != nil && sinceSHA == "" {
		payload.Unavailable = "no-git"
	} else {
		payload.HeadSHA = head
	}

	var files []string
	var patch string
	// Substrate leg only with a real time anchor. Zero since would dump the
	// whole authored tree — not a clear absent-record state (AC6).
	sinceTime, hasSinceTime := changeRecordSinceTime(ctx, item.ID)

	if sinceSHA != "" && payload.Unavailable != "no-git" {
		f, _, p, derr := gitDiffSince(dir, sinceSHA, true)
		if derr != nil {
			if payload.Unavailable == "" {
				payload.Unavailable = "enumeration-error: " + derr.Error()
			}
		} else {
			files = append(files, f...)
			patch = p
		}
	} else if sinceSHA == "" && payload.Unavailable == "" {
		payload.Unavailable = "no-baseline"
	}

	// Substrate leg when we have a real anchor (baseline or prior record).
	// Skip when no-baseline so files stays empty (clear absent-record).
	if hasSinceTime && payload.Unavailable != "no-baseline" {
		// Process-root paths ride in the same flat list: the recorded channel is a
		// union of repo-relative strings (sty_92e4cdbe).
		local, procRel, _ := substrateChangedFilesSplit(dir, authoredDirs, substrateConfigDir, sinceTime)
		files = append(files, local...)
		files = append(files, procRel...)
	}

	files = uniqueSorted(files)
	if len(files) > changeRecordFileLimit {
		payload.FilesTruncated = true
		files = files[:changeRecordFileLimit]
	}
	payload.Files = files
	payload.FileCount = len(files)

	// Patch attachment (local only; excluded from gate payload).
	if patch != "" || len(files) > 0 {
		if len(patch) > changeRecordPatchLimit {
			patch = patch[:changeRecordPatchLimit] + fmt.Sprintf("\n... [truncated at %d bytes]", changeRecordPatchLimit)
			payload.PatchTruncated = true
		}
		// Trailer for paths not in the git patch.
		if len(files) > 0 {
			patch += "\n# files:\n" + strings.Join(files, "\n") + "\n"
		}
		name := fmt.Sprintf("change-%s-%s", from, to)
		if _, _, aerr := writeAttachedDoc(ctx, item, name, "change", patch, now); aerr == nil {
			payload.PatchAttached = true
			payload.PatchName = name
		}
	}

	body := fmt.Sprintf("change record %s→%s: %d file(s)", from, to, payload.FileCount)
	if payload.Unavailable != "" {
		body += " (" + payload.Unavailable + ")"
	}
	raw, _ := json.Marshal(payload)
	appendLedgerEntry(ctx, item.ID, ledger.KindChangeRecord, "executor", body, raw, now)
}

// recordResumeReanchor ledgers the zero-file row that moves a resumed story's
// enumeration anchor to the resume point (sty_526d6a68). The row is deliberately
// FULL, not partial: both anchor walks skip partial rows, so a partial row here
// would defeat the whole point.
func recordResumeReanchor(ctx context.Context, item workitem.Item, from, to string, now time.Time) {
	payload := changeRecordPayload{
		From:           from,
		To:             to,
		Files:          []string{},
		ReanchorResume: true,
	}
	head, _, gerr := gitHeadAndDirty(changeRecordRepoRoot())
	body := fmt.Sprintf("change record %s→%s: re-anchored at resume head=%s", from, to, head)
	if gerr != nil {
		// No HeadSHA: the anchor walk falls through to the previous full row or
		// the engagement baseline, which is the pre-existing degradation.
		payload.Unavailable = "no-git"
		body = fmt.Sprintf("change record %s→%s: re-anchor at resume unavailable (no-git)", from, to)
	} else {
		payload.HeadSHA = head
		payload.SinceSHA = head
	}
	raw, _ := json.Marshal(payload)
	appendLedgerEntry(ctx, item.ID, ledger.KindChangeRecord, "executor", body, raw, now)
}

// engagementStart is when the story first took its tree: the first engagement
// baseline's time. ok is false for a story that was never engaged. Engagement
// starts the measurement, so an anchor row written before it (a park/resume
// re-anchor, or a full change_record, taken while the story sat in backlog) is
// not an anchor and must never stand in for the baseline.
func engagementStart(ctx context.Context, storyID string) (time.Time, bool) {
	_, _, at, err := firstEngagementBaseline(ctx, storyID)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// engagedRows drops the rows created before the story's first engagement
// baseline. The cut is strict: the engaging edge's own row shares the
// baseline's time and stays. A story never engaged keeps every row.
func engagedRows(ctx context.Context, storyID string, recs []ledger.Entry) []ledger.Entry {
	start, ok := engagementStart(ctx, storyID)
	if !ok {
		return recs
	}
	out := make([]ledger.Entry, 0, len(recs))
	for _, r := range recs {
		if !r.CreatedAt.Before(start) {
			out = append(out, r)
		}
	}
	return out
}

// latestResumeReanchor returns the newest re-anchor row's SHA and time, if any,
// ignoring re-anchors written before the story's first engagement baseline.
// ok is false when the story never parked after engaging, or git was unavailable
// at resume.
func latestResumeReanchor(ctx context.Context, storyID string) (sha string, at time.Time, ok bool) {
	start, _ := engagementStart(ctx, storyID)
	return latestResumeReanchorSince(ctx, storyID, "", start)
}

// latestResumeReanchorExcept is latestResumeReanchor ignoring re-anchors that
// resumed INTO skipTo ("" skips none). A resume into a state is written in the
// same instant as its status_transition, so a caller asking "did anything
// engage after entering that state" must not count it.
func latestResumeReanchorExcept(ctx context.Context, storyID, skipTo string) (sha string, at time.Time, ok bool) {
	return latestResumeReanchorSince(ctx, storyID, skipTo, time.Time{})
}

// latestResumeReanchorSince is latestResumeReanchorExcept also ignoring rows
// created before notBefore (zero keeps every row).
func latestResumeReanchorSince(ctx context.Context, storyID, skipTo string, notBefore time.Time) (sha string, at time.Time, ok bool) {
	ls, err := requireLedger()
	if err != nil {
		return "", time.Time{}, false
	}
	recs, err := ls.ListByStory(ctx, storyID, ledger.KindChangeRecord)
	if err != nil {
		return "", time.Time{}, false
	}
	for i := len(recs) - 1; i >= 0; i-- {
		var p changeRecordPayload
		if json.Unmarshal(recs[i].Payload, &p) != nil || recs[i].CreatedAt.Before(notBefore) {
			continue
		}
		if p.ReanchorResume && p.HeadSHA != "" && (skipTo == "" || p.To != skipTo) {
			return p.HeadSHA, recs[i].CreatedAt, true
		}
	}
	return "", time.Time{}, false
}

// changeRecordRepoRoot resolves the repo-relative path space every change_record
// is written in: the git toplevel, falling back to the working directory.
func changeRecordRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	if top, terr := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); terr == nil {
		dir = strings.TrimSpace(string(top))
	}
	return dir
}

// RecordPathMutation ledgers a change_record row for paths a binary verb
// mutated under the engaged story. A deletion of git-ignored substrate is
// invisible to every re-derived channel — git cannot see an ignored path, and
// the mtime walk cannot see a file that is gone — so the verb that removed it is
// the only place the evidence can come from (sty_30d3bd99).
//
// The row is PARTIAL: it reports the paths this verb touched, not everything
// changed since the last anchor, so anchor selection walks past it. Empty story
// or no in-root paths writes nothing — an empty row is not evidence.
func RecordPathMutation(ctx context.Context, storyID string, absPaths []string, from, to string, now time.Time) error {
	storyID = strings.TrimSpace(storyID)
	if storyID == "" || len(absPaths) == 0 {
		return nil
	}
	root := changeRecordRepoRoot()
	var files []string
	for _, p := range absPaths {
		// Pure string math: the paths are already removed, so never Stat them.
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		files = append(files, filepath.ToSlash(rel))
	}
	files = uniqueSorted(files)
	if len(files) == 0 {
		return nil
	}
	payload := changeRecordPayload{
		From:               from,
		To:                 to,
		PartialEnumeration: true,
	}
	if len(files) > changeRecordFileLimit {
		payload.FilesTruncated = true
		files = files[:changeRecordFileLimit]
	}
	payload.Files = files
	payload.FileCount = len(files)

	ls, err := requireLedger()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	body := fmt.Sprintf("change record %s: %d path(s) mutated", from, payload.FileCount)
	_, err = ls.Append(ctx, ledger.AppendInput{
		StoryID: storyID,
		Kind:    ledger.KindChangeRecord,
		Actor:   "executor",
		Body:    body,
		Payload: raw,
	}, now)
	return err
}

func changeRecordAnchor(ctx context.Context, storyID string) (sinceSHA string, headSHA string, unavail string) {
	ls, err := requireLedger()
	if err != nil {
		return "", "", "no-baseline"
	}
	// Prefer the most recent FULL change_record's head_sha. Partial rows carry no
	// SHA anyway; skipping them explicitly keeps the two anchor walks symmetric.
	recs, err := ls.ListByStory(ctx, storyID, ledger.KindChangeRecord)
	if err == nil {
		if p, ok := lastFullChangeRecord(engagedRows(ctx, storyID, recs)); ok && p.HeadSHA != "" {
			return p.HeadSHA, p.HeadSHA, ""
		}
	}
	base, _, _, berr := firstEngagementBaseline(ctx, storyID)
	if berr != nil || base.HeadSHA == "" {
		return "", "", "no-baseline"
	}
	return base.HeadSHA, base.HeadSHA, ""
}

// lastFullChangeRecord returns the newest row that is a FULL enumeration, i.e.
// not a partial row written by a binary verb for the paths it touched. recs is
// oldest-first; the walk is newest-first. ok is false when every row is partial.
func lastFullChangeRecord(recs []ledger.Entry) (changeRecordPayload, bool) {
	for i := len(recs) - 1; i >= 0; i-- {
		var p changeRecordPayload
		if json.Unmarshal(recs[i].Payload, &p) != nil {
			continue
		}
		if p.PartialEnumeration {
			continue
		}
		return p, true
	}
	return changeRecordPayload{}, false
}

// changeRecordSinceTime returns the anchor time and whether one exists.
// ok is false when neither a change_record nor engagement baseline is present.
//
// PARTIAL rows are skipped. A partial row states only the paths one verb
// touched, so letting it anchor would advance the mtime window past authored
// substrate no full enumeration has seen — edit a skill, prune, transition, and
// the edit would silently drop out of the change set (sty_30d3bd99).
func changeRecordSinceTime(ctx context.Context, storyID string) (time.Time, bool) {
	ls, err := requireLedger()
	if err != nil {
		return time.Time{}, false
	}
	recs, err := ls.ListByStory(ctx, storyID, ledger.KindChangeRecord)
	if err == nil {
		recs = engagedRows(ctx, storyID, recs)
		for i := len(recs) - 1; i >= 0; i-- {
			var p changeRecordPayload
			if json.Unmarshal(recs[i].Payload, &p) == nil && p.PartialEnumeration {
				continue
			}
			return recs[i].CreatedAt, true
		}
	}
	entries, err := ls.ListByStory(ctx, storyID, ledger.KindEngagementBaseline)
	if err == nil && len(entries) > 0 {
		return entries[0].CreatedAt, true
	}
	return time.Time{}, false
}

// substrateWalk lists repo-relative paths under authored dirs and the substrate
// config dir that satisfy keep (nil keeps every file). Paths outside repoRoot
// are skipped (path-space: repo-relative only). Runtime/state files under the
// config dir are excluded so mtime churn never lands in a change set.
func substrateWalk(repoRoot string, dirs map[string]string, configDir string, keep func(os.FileInfo) bool) []string {
	local, _, _ := substrateWalkSplit(repoRoot, dirs, configDir, keep)
	return local
}

// processRootForSubstrate names the main tree whose authored substrate is the
// process of record for every worktree (sty_ddbe2669). The root has one owner,
// app.App.ProcessRoot, and arrives through the existing wiring in internal/cli
// (SetProcessProbe, else SetWorktreeConfig) — it is never worked out here, so
// with neither wired it is "" and out-of-tree paths are dropped.
func processRootForSubstrate() string {
	if processProbe != nil && strings.TrimSpace(processProbe.ProcessRoot) != "" {
		return processProbe.ProcessRoot
	}
	if worktreeWired {
		return worktreeRoot
	}
	return ""
}

// substrateRelUnder is the slash path of path under root, or false when path is
// not under root (or root is unset).
func substrateRelUnder(root, path string) (string, bool) {
	if strings.TrimSpace(root) == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// substrateWalkSplit is substrateWalk that also reports the substrate a story
// engaged from a linked worktree made in the MAIN tree (sty_92e4cdbe). The walk
// roots are the process-of-record dirs, so for a worktree slice they sit outside
// sliceRoot: local holds the paths under sliceRoot, processRel those under the
// process root (relative to it), and processRoot names that root — "" when
// processRel is empty, so a main-tree story reports nothing extra. Any other path
// is dropped.
//
// processRel is attributed by mtime to whichever story asks, including edits
// another story engaged in the main tree made in the same window; it is
// enumeration a gate judges, the same attribution the main-tree case already has.
func substrateWalkSplit(sliceRoot string, dirs map[string]string, configDir string, keep func(os.FileInfo) bool) (local, processRel []string, processRoot string) {
	root := processRootForSubstrate()
	seenLocal, seenProc := map[string]bool{}, map[string]bool{}
	walk := func(dir string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if substrateRuntimeFile(info.Name()) {
				return nil
			}
			if keep != nil && !keep(info) {
				return nil
			}
			if rel, ok := substrateRelUnder(sliceRoot, path); ok {
				if !seenLocal[rel] {
					seenLocal[rel] = true
					local = append(local, rel)
				}
			} else if rel, ok := substrateRelUnder(root, path); ok && !seenProc[rel] {
				seenProc[rel] = true
				processRel = append(processRel, rel)
			}
			return nil
		})
	}
	for _, d := range dirs {
		walk(d)
	}
	walk(configDir)
	sort.Strings(local)
	sort.Strings(processRel)
	if len(processRel) > 0 {
		processRoot = root
	}
	return local, processRel, processRoot
}

// substrateChangedFiles lists repo-relative paths under authored dirs and the
// substrate config dir whose mtime is strictly after since. since must be a real
// anchor.
func substrateChangedFiles(repoRoot string, dirs map[string]string, configDir string, since time.Time) []string {
	local, _, _ := substrateChangedFilesSplit(repoRoot, dirs, configDir, since)
	return local
}

// substrateChangedFilesSplit is substrateChangedFiles that also returns the
// process-root substrate paths (see substrateWalkSplit).
func substrateChangedFilesSplit(repoRoot string, dirs map[string]string, configDir string, since time.Time) (local, processRel []string, processRoot string) {
	if since.IsZero() {
		return nil, nil, ""
	}
	return substrateWalkSplit(repoRoot, dirs, configDir, func(info os.FileInfo) bool {
		return info.ModTime().After(since)
	})
}

// substrateManifest snapshots every authored substrate path that exists now
// (sty_7e1e2deb). The mtime leg can only see files that still EXIST, so a slice
// whose whole content is deleting git-ignored substrate is invisible to every
// re-derived channel. Recording the set at engagement gives the deletion
// something to be measured against.
func substrateManifest(repoRoot string, dirs map[string]string, configDir string) []string {
	return substrateWalk(repoRoot, dirs, configDir, nil)
}

// substrateDeletedFiles returns the manifest paths that are no longer on disk
// under repoRoot. A path we cannot stat for any other reason is skipped — a
// permission error is not evidence of a deletion.
func substrateDeletedFiles(repoRoot string, manifest []string) []string {
	var out []string
	for _, rel := range manifest {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || strings.HasPrefix(rel, "../") || rel == ".." || filepath.IsAbs(rel) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(repoRoot, filepath.FromSlash(rel))); os.IsNotExist(err) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// substrateRuntimeFile reports state/log files that must not enter a change set
// from mtime churn under the data dir.
func substrateRuntimeFile(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "deployed.version", "repo.path":
		return true
	}
	if strings.HasSuffix(n, ".db") || strings.HasSuffix(n, ".db-wal") ||
		strings.HasSuffix(n, ".db-shm") || strings.HasSuffix(n, ".log") {
		return true
	}
	return false
}

// recordedChangeSet unions every change_record files list for a story.
func recordedChangeSet(ctx context.Context, storyID string) (files []string, records int, err error) {
	ls, err := requireLedger()
	if err != nil {
		return nil, 0, err
	}
	recs, err := ls.ListByStory(ctx, storyID, ledger.KindChangeRecord)
	if err != nil {
		return nil, 0, err
	}
	var all []string
	for _, e := range recs {
		var p changeRecordPayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		all = append(all, p.Files...)
	}
	return uniqueSorted(all), len(recs), nil
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
