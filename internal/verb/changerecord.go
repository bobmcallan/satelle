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
	payload := changeRecordPayload{
		From:  from,
		To:    to,
		Files: []string{},
	}
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	if top, terr := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); terr == nil {
		dir = strings.TrimSpace(string(top))
	}

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
		sub := substrateChangedFiles(dir, authoredDirs, substrateConfigDir, sinceTime)
		files = append(files, sub...)
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

func changeRecordAnchor(ctx context.Context, storyID string) (sinceSHA string, headSHA string, unavail string) {
	ls, err := requireLedger()
	if err != nil {
		return "", "", "no-baseline"
	}
	// Prefer most recent change_record head_sha.
	recs, err := ls.ListByStory(ctx, storyID, ledger.KindChangeRecord)
	if err == nil && len(recs) > 0 {
		var p changeRecordPayload
		if json.Unmarshal(recs[len(recs)-1].Payload, &p) == nil && p.HeadSHA != "" {
			return p.HeadSHA, p.HeadSHA, ""
		}
	}
	base, _, _, berr := firstEngagementBaseline(ctx, storyID)
	if berr != nil || base.HeadSHA == "" {
		return "", "", "no-baseline"
	}
	return base.HeadSHA, base.HeadSHA, ""
}

// changeRecordSinceTime returns the anchor time and whether one exists.
// ok is false when neither a change_record nor engagement baseline is present.
func changeRecordSinceTime(ctx context.Context, storyID string) (time.Time, bool) {
	ls, err := requireLedger()
	if err != nil {
		return time.Time{}, false
	}
	recs, err := ls.ListByStory(ctx, storyID, ledger.KindChangeRecord)
	if err == nil && len(recs) > 0 {
		return recs[len(recs)-1].CreatedAt, true
	}
	entries, err := ls.ListByStory(ctx, storyID, ledger.KindEngagementBaseline)
	if err == nil && len(entries) > 0 {
		return entries[0].CreatedAt, true
	}
	return time.Time{}, false
}

// substrateChangedFiles lists repo-relative paths under authored dirs and the
// substrate config dir whose mtime is strictly after since. since must be a real
// anchor. Paths outside repoRoot are skipped (path-space: repo-relative only).
// Runtime/state files under the config dir are excluded so mtime churn never
// lands in a change set.
func substrateChangedFiles(repoRoot string, dirs map[string]string, configDir string, since time.Time) []string {
	if since.IsZero() {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	walk := func(root string) {
		if strings.TrimSpace(root) == "" {
			return
		}
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if substrateRuntimeFile(info.Name()) {
				return nil
			}
			if !info.ModTime().After(since) {
				return nil
			}
			rel, rerr := filepath.Rel(repoRoot, path)
			if rerr != nil || strings.HasPrefix(rel, "..") {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if !seen[rel] {
				seen[rel] = true
				out = append(out, rel)
			}
			return nil
		})
	}
	for _, root := range dirs {
		walk(root)
	}
	walk(configDir)
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
