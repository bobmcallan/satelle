package verb

// Substrate writes a story did not make (sty_ce858cb3). The substrate leg of a
// change set is an mtime walk, so every file touched during an engagement lands
// in it — including the bytes `satelle init` wrote and the bytes another story
// wrote through the edit gate. The rows recorded here let both callers of the
// walk (liveStoryDiff, recordChangeSet) leave those out, and only those: any
// doubt keeps the path listed.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// FileState is one substrate file's content hash and modification time.
type FileState struct {
	SHA256 string
	Mtime  time.Time
}

// SeededFile is one file a `satelle init` run created or changed. BeforeSHA256
// is empty and BeforeMtime zero for a file the run created.
type SeededFile struct {
	Path         string    `json:"path"`
	BeforeSHA256 string    `json:"before_sha256,omitempty"`
	SHA256       string    `json:"sha256"`
	BeforeMtime  time.Time `json:"before_mtime"`
}

// SubstrateSeedPayload is the payload of a KindSubstrateSeeded row. Paths are
// relative to Root, the repo the run targeted.
type SubstrateSeedPayload struct {
	Verb  string       `json:"verb"`
	Root  string       `json:"root"`
	Files []SeededFile `json:"files"`
}

// SubstrateWrittenPayload is the payload of a KindSubstrateWritten row: the
// file's hash before the gated write (empty when it did not exist).
type SubstrateWrittenPayload struct {
	Root         string `json:"root"`
	Path         string `json:"path"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
}

// SubstrateBaselinePayload is the payload of a KindSubstrateBaseline row: the
// hash of every substrate file at the story's first engagement, by path relative
// to Root.
type SubstrateBaselinePayload struct {
	Root  string            `json:"root"`
	Files map[string]string `json:"files"`
}

// SubstrateHashes is the state of every regular substrate file under dirs and
// configDir, keyed by slash path relative to root. It is the substrate walk
// (substrateWalkSplit) with each file hashed.
func SubstrateHashes(root string, dirs map[string]string, configDir string) map[string]FileState {
	local, procRel, procRoot := substrateWalkSplit(root, dirs, configDir, nil)
	out := map[string]FileState{}
	add := func(base string, rels []string) {
		for _, rel := range rels {
			abs := filepath.Join(base, filepath.FromSlash(rel))
			st, ok := fileState(abs)
			if !ok {
				continue
			}
			if r, err := filepath.Rel(root, abs); err == nil {
				out[filepath.ToSlash(r)] = st
			}
		}
	}
	add(root, local)
	add(procRoot, procRel)
	return out
}

// SeededChanges lists the files after holds that before did not, or holds with
// different content, sorted by path.
func SeededChanges(before, after map[string]FileState) []SeededFile {
	var out []SeededFile
	for _, p := range slices.Sorted(maps.Keys(after)) {
		now, was := after[p], before[p]
		if was.SHA256 == now.SHA256 {
			continue
		}
		out = append(out, SeededFile{Path: p, BeforeSHA256: was.SHA256, SHA256: now.SHA256, BeforeMtime: was.Mtime})
	}
	return out
}

// RecordSubstrateSeed appends one KindSubstrateSeeded row to ls naming files,
// relative to root. ls is passed in because init runs under no store-backed
// command and may target another repo's ledger. No files writes nothing.
func RecordSubstrateSeed(ctx context.Context, ls *ledger.Store, root string, files []SeededFile) error {
	if len(files) == 0 {
		return nil
	}
	raw, err := json.Marshal(SubstrateSeedPayload{Verb: "init", Root: root, Files: files})
	if err != nil {
		return err
	}
	_, err = ls.Append(ctx, ledger.AppendInput{
		Kind:    ledger.KindSubstrateSeeded,
		Actor:   "init",
		Body:    fmt.Sprintf("satelle init wrote %d substrate file(s) under %s", len(files), root),
		Payload: raw,
	}, time.Now())
	return err
}

// RecordSubstrateWritten appends the KindSubstrateWritten row for one gated
// write by storyID to rel under root, hashing the file as it is before the write.
func RecordSubstrateWritten(ctx context.Context, ls *ledger.Store, storyID, root, rel string) error {
	raw, err := json.Marshal(SubstrateWrittenPayload{
		Root: root, Path: rel, BeforeSHA256: fileHash(filepath.Join(root, filepath.FromSlash(rel))),
	})
	if err != nil {
		return err
	}
	_, err = ls.Append(ctx, ledger.AppendInput{
		StoryID: storyID,
		Kind:    ledger.KindSubstrateWritten,
		Actor:   "edit-gate",
		Body:    fmt.Sprintf("%s wrote substrate %s through the edit gate", storyID, rel),
		Payload: raw,
	}, time.Now())
	return err
}

// recordSubstrateBaseline ledgers the substrate hashes at a story's first
// engagement, rooted where the walk's substrate lives: the process root, or the
// engaging tree when none is wired. Best-effort like the engagement baseline: a
// story with no row has nothing dropped by the foreign-write rule.
func recordSubstrateBaseline(ctx context.Context, storyID, tree string, now time.Time) {
	root := processRootForSubstrate()
	if root == "" {
		root = tree
	}
	files := map[string]string{}
	for p, st := range SubstrateHashes(root, authoredDirs, substrateConfigDir) {
		files[p] = st.SHA256
	}
	raw, _ := json.Marshal(SubstrateBaselinePayload{Root: root, Files: files})
	appendLedgerEntry(ctx, storyID, ledger.KindSubstrateBaseline, "executor",
		fmt.Sprintf("substrate baseline: %d file(s) under %s", len(files), root), raw, now)
}

// gateWrite is one KindSubstrateWritten row: who wrote, and the bytes before.
type gateWrite struct {
	story, before string
}

// withoutForeignWrites drops from paths (slash paths relative to root) each file
// whose current bytes this story provably did not produce since `since`:
//
//   - init-owned: the file's substrate_seeded rows after since form a clean chain
//     — the first found the file unchanged in the window (its pre-write mtime is
//     not after since) or created it, each later one started from the previous
//     one's bytes — and the file still holds the last row's bytes;
//   - another story's: at least one substrate_written row after since names it,
//     none is this story's, and the first one's pre-write bytes equal this story's
//     substrate_baseline for it, so nothing changed it between this story's
//     engagement and that story's first gated write.
//
// Anything else stays listed, including every path when the ledger is unwired.
func withoutForeignWrites(ctx context.Context, storyID, root string, paths []string, since time.Time) []string {
	if len(paths) == 0 || since.IsZero() || root == "" {
		return paths
	}
	ls, err := requireLedger()
	if err != nil {
		return paths
	}
	seeds := map[string][]SeededFile{}
	_ = ls.ForEachKind(ctx, "", ledger.KindSubstrateSeeded, func(e ledger.Entry) error {
		var p SubstrateSeedPayload
		if !e.CreatedAt.After(since) || json.Unmarshal(e.Payload, &p) != nil {
			return nil
		}
		for _, f := range p.Files {
			k := canonSubstratePath(p.Root, f.Path)
			seeds[k] = append(seeds[k], f)
		}
		return nil
	})
	writes := map[string][]gateWrite{}
	_ = ls.ForEachKind(ctx, "", ledger.KindSubstrateWritten, func(e ledger.Entry) error {
		var p SubstrateWrittenPayload
		if !e.CreatedAt.After(since) || json.Unmarshal(e.Payload, &p) != nil {
			return nil
		}
		k := canonSubstratePath(p.Root, p.Path)
		writes[k] = append(writes[k], gateWrite{story: e.StoryID, before: p.BeforeSHA256})
		return nil
	})
	if len(seeds) == 0 && len(writes) == 0 {
		return paths
	}
	baseline, hasBaseline := substrateBaselineOf(ctx, ls, storyID)
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		k := canonSubstratePath(root, p)
		if initOwned(seeds[k], since, fileHash(k)) {
			continue
		}
		if hasBaseline && foreignOwned(writes[k], storyID, baseline[k]) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// initOwned is the init-ownership chain rule over one file's seed rows after
// since, oldest first, given the file's current hash.
func initOwned(rows []SeededFile, since time.Time, current string) bool {
	if len(rows) == 0 || current == "" {
		return false
	}
	if rows[0].BeforeSHA256 != "" && rows[0].BeforeMtime.After(since) {
		return false
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].BeforeSHA256 != rows[i-1].SHA256 {
			return false
		}
	}
	return current == rows[len(rows)-1].SHA256
}

// foreignOwned is the other-story rule over one file's gate writes after since,
// oldest first, given this story's engagement hash for it ("" when absent).
func foreignOwned(rows []gateWrite, storyID, atEngagement string) bool {
	if len(rows) == 0 {
		return false
	}
	for _, w := range rows {
		if w.story == storyID {
			return false
		}
	}
	return rows[0].before == atEngagement
}

// substrateBaselineOf is the story's first substrate_baseline, keyed like
// canonSubstratePath. ok is false when the story has none.
func substrateBaselineOf(ctx context.Context, ls *ledger.Store, storyID string) (map[string]string, bool) {
	rows, err := ls.ListByStory(ctx, storyID, ledger.KindSubstrateBaseline)
	if err != nil || len(rows) == 0 {
		return nil, false
	}
	var p SubstrateBaselinePayload
	if json.Unmarshal(rows[0].Payload, &p) != nil {
		return nil, false
	}
	out := make(map[string]string, len(p.Files))
	for rel, h := range p.Files {
		out[canonSubstratePath(p.Root, rel)] = h
	}
	return out, true
}

// canonSubstratePath is the absolute path of rel under root with the directory's
// symlinks resolved, so rows recorded against differently spelled roots agree.
func canonSubstratePath(root, rel string) string {
	abs := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if d, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(d, filepath.Base(abs))
	}
	return abs
}

// fileState hashes a regular file; ok is false for anything else or a read error.
func fileState(abs string) (FileState, bool) {
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return FileState{}, false
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return FileState{}, false
	}
	sum := sha256.Sum256(b)
	return FileState{SHA256: hex.EncodeToString(sum[:]), Mtime: info.ModTime()}, true
}

// fileHash is fileState's hash, "" when the file is absent or unreadable.
func fileHash(abs string) string {
	st, _ := fileState(abs)
	return st.SHA256
}
