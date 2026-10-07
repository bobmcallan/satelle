package snapsync

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/subsync"
)

// Action is what a pull does with one path. The set is the whole three-way table
// over (base, local, remote); nothing else decides what a pull touches.
type Action int

const (
	// InSync: local already equals the snapshot.
	InSync Action = iota
	// Write: apply the snapshot's bytes (remote changed and local is untouched,
	// or the file is missing locally).
	Write
	// Delete: the snapshot dropped the file and local is untouched — remove it.
	Delete
	// KeepLocal: only this machine changed (or added) the file — leave it; the
	// next push publishes it.
	KeepLocal
	// LocalDeleted: only this machine deleted the file — the next push drops it.
	LocalDeleted
	// Conflict: changed on both sides. The local file is kept, the remote bytes
	// are parked for the operator or agent to merge, and the path is unmerged.
	Conflict
	// Incomplete: the snapshot names bytes the hosted copy does not hold yet (an
	// upload still in flight, or an older binary pushed over it). Nothing is
	// changed for the path, and its base is left alone.
	Incomplete
	// RemovedStale (no base only): a local file the snapshot does not name whose
	// bytes equal that path's abandoned hosted head — a stale rehydrated copy of
	// a file whose deletion was published. Moved aside with a backup.
	RemovedStale
	// LocalOnly (no base only): a local file nothing has published.
	LocalOnly
	// DeletedRemotelyKept: the snapshot dropped the file but this machine
	// changed it since — kept, and republished by the next push.
	DeletedRemotelyKept
	// StillUnmerged: a conflict from an earlier pull that has not been resolved.
	StillUnmerged
)

// Entry is the decision for one path. Remote is the snapshot's sha for it.
type Entry struct {
	Path   string
	Action Action
	Remote string
}

// PullPlan is the outcome of PlanPull: a decision per path and the base this
// machine will hold once every decision is applied as planned.
type PullPlan struct {
	Entries []Entry
	Base    hosted.AreaBase
}

// Count returns how many entries carry the action.
func (p PullPlan) Count(a Action) int {
	n := 0
	for _, e := range p.Entries {
		if e.Action == a {
			n++
		}
	}
	return n
}

// PlanPull decides, per path, how to bring local in line with the effective
// snapshot (remote, at remoteVersion) given what this machine last synced.
//
//	base   — what was last synced; nil on the first pull (no base).
//	local  — sha of each file in the area on disk now (the push-side view).
//	heads  — sha of each hosted head in the area, INCLUDING heads the snapshot no
//	         longer names; used to tell an incomplete upload from a real one and
//	         to recognise a stale copy of an abandoned head.
func PlanPull(base *hosted.AreaBase, remote Record, remoteVersion int, local, heads map[string]string) PullPlan {
	var b hosted.AreaBase
	if base != nil {
		b = *base
	}
	nb := hosted.AreaBase{Version: remoteVersion, Files: map[string]string{}}
	var entries []Entry
	add := func(p string, a Action, remoteSHA string) {
		entries = append(entries, Entry{Path: p, Action: a, Remote: remoteSHA})
	}
	paths := map[string]struct{}{}
	for p := range remote.Files {
		paths[p] = struct{}{}
	}
	for p := range b.Files {
		paths[p] = struct{}{}
	}
	for p := range b.Unmerged {
		paths[p] = struct{}{}
	}
	for p := range local {
		paths[p] = struct{}{}
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	for _, p := range sorted {
		rsha, inRemote := remote.Files[p]
		bsha, inBase := b.Files[p]
		lsha, inLocal := local[p]

		if old, un := b.Unmerged[p]; un {
			// An earlier conflict nobody has resolved: leave everything about the
			// path as it was (the base sha stays old, the marker stays).
			if nb.Unmerged == nil {
				nb.Unmerged = map[string]string{}
			}
			nb.Unmerged[p] = old
			if inBase {
				nb.Files[p] = bsha
			}
			add(p, StillUnmerged, old)
			continue
		}

		if inRemote {
			switch {
			case inLocal && lsha == rsha:
				nb.Files[p] = rsha
				add(p, InSync, rsha)
			case !inLocal && base != nil && inBase && bsha == rsha:
				nb.Files[p] = bsha
				add(p, LocalDeleted, rsha)
			case !inLocal, base != nil && inBase && lsha == bsha && bsha != rsha:
				// Remote changed and local is untouched (or absent).
				if heads[p] != rsha {
					if inBase {
						nb.Files[p] = bsha
					}
					add(p, Incomplete, rsha)
					break
				}
				nb.Files[p] = rsha
				add(p, Write, rsha)
			case base != nil && inBase && bsha == rsha:
				nb.Files[p] = bsha
				add(p, KeepLocal, rsha)
			default:
				// Changed on both sides, added differently on both, or no base to
				// say which side moved.
				if inBase {
					nb.Files[p] = bsha
				}
				if heads[p] != rsha {
					add(p, Incomplete, rsha)
					break
				}
				if nb.Unmerged == nil {
					nb.Unmerged = map[string]string{}
				}
				nb.Unmerged[p] = rsha
				add(p, Conflict, rsha)
			}
			continue
		}

		// Not in the snapshot.
		switch {
		case base != nil && inBase && !inLocal:
			// Gone on both sides.
		case base != nil && inBase && lsha == bsha:
			add(p, Delete, "")
		case base != nil && inBase:
			add(p, DeletedRemotelyKept, "")
		case !inLocal:
			// An abandoned hosted head with no local copy: nothing to do.
		case base != nil:
			add(p, KeepLocal, "") // a local addition
		case heads[p] != "" && heads[p] == lsha:
			add(p, RemovedStale, "")
		default:
			add(p, LocalOnly, "")
		}
	}
	return PullPlan{Entries: entries, Base: nb}
}

// ErrBehind is the push refusal for a machine whose view is out of date: the
// hosted effective snapshot is not the one it last synced from, or it still has
// hosted changes it has not applied.
type ErrBehind struct {
	Area      string
	Base      int
	Effective int
	// Pending lists paths with remote changes this machine has not applied; set
	// when the versions agree but a pull would still change something.
	Pending []string
	// PullCmd is the command that brings this machine up to date.
	PullCmd string
}

func (e *ErrBehind) Error() string {
	if len(e.Pending) > 0 {
		return fmt.Sprintf("sync %s: hosted changes not yet applied here (%s) — run %q first", e.Area, joinMax(e.Pending, 3), e.PullCmd)
	}
	return fmt.Sprintf("sync %s: hosted copy is at snapshot %d, this machine last synced snapshot %d — run %q first", e.Area, e.Effective, e.Base, e.PullCmd)
}

// ErrUnmerged is the push refusal while a conflict copy is still on disk.
type ErrUnmerged struct {
	Area  string
	Paths []string
	// Copies maps each path to its conflict copy (relative to the data dir).
	Copies map[string]string
}

func (e *ErrUnmerged) Error() string {
	p := e.Paths[0]
	more := ""
	if len(e.Paths) > 1 {
		more = fmt.Sprintf(" (and %d more)", len(e.Paths)-1)
	}
	return fmt.Sprintf("sync %s: unmerged %s%s — merge it and delete %s first", e.Area, p, more, e.Copies[p])
}

func joinMax(s []string, n int) string {
	if len(s) <= n {
		return strings.Join(s, ", ")
	}
	return strings.Join(s[:n], ", ") + fmt.Sprintf(" and %d more", len(s)-n)
}

// Resolve clears every unmerged marker whose conflict copy is gone: deleting the
// copy is how the operator says "merged". The remote sha becomes that path's
// base, so the local (merged) file now differs from base and the next push
// publishes it. It reports whether anything changed.
func Resolve(b hosted.AreaBase, copyExists func(path string) bool) (hosted.AreaBase, bool) {
	changed := false
	for p, rsha := range b.Unmerged {
		if copyExists(p) {
			continue
		}
		if b.Files == nil {
			b.Files = map[string]string{}
		}
		b.Files[p] = rsha
		delete(b.Unmerged, p)
		changed = true
	}
	if len(b.Unmerged) == 0 {
		b.Unmerged = nil
	}
	return b, changed
}

// PushPlan is what a push will do for one area.
type PushPlan struct {
	// Record is the new snapshot to claim, parented on the effective one.
	Record Record
	// Claim is false when local already equals the effective snapshot, so no new
	// snapshot is needed (blobs may still need repairing).
	Claim bool
	// Uploads are the paths whose hosted head is not the snapshot's bytes.
	Uploads []string
}

// PlanPush decides a push of local over the effective snapshot, or refuses it.
// Order of refusals: behind (versions disagree), unmerged conflict, then any
// hosted change this machine has not applied — so a push can never publish a
// view that silently reverts someone else's work.
//
// While the hosted copy has no snapshot yet, its heads are the live files of a
// store that predates snapshots (nothing could be deleted there), so the first
// claim carries them forward — local bytes winning where both exist — rather than
// silently abandoning a file only another machine has. prune publishes this tree
// as the whole truth instead: a head the tree lacks is dropped, which is how an
// existing store's stale files are retired.
func PlanPush(area, pullCmd, by string, prune bool, base *hosted.AreaBase, effVersion int, eff Record, local, heads map[string]string, copyExists func(path string) bool) (PushPlan, error) {
	baseVersion := 0
	if base != nil {
		baseVersion = base.Version
	}
	if effVersion != baseVersion {
		return PushPlan{}, &ErrBehind{Area: area, Base: baseVersion, Effective: effVersion, PullCmd: pullCmd}
	}
	if base != nil && len(base.Unmerged) > 0 {
		e := &ErrUnmerged{Area: area, Copies: map[string]string{}}
		for p := range base.Unmerged {
			e.Paths = append(e.Paths, p)
		}
		sort.Strings(e.Paths)
		for _, p := range e.Paths {
			e.Copies[p] = CopyPath(area, p)
		}
		return PushPlan{}, e
	}
	if effVersion > 0 {
		var pending []string
		for _, e := range PlanPull(base, eff, effVersion, local, heads).Entries {
			switch e.Action {
			case Write, Delete, Conflict, Incomplete:
				pending = append(pending, e.Path)
			}
		}
		if len(pending) > 0 {
			return PushPlan{}, &ErrBehind{Area: area, Base: baseVersion, Effective: effVersion, Pending: pending, PullCmd: pullCmd}
		}
	}
	files := local
	if effVersion == 0 && !prune {
		files = make(map[string]string, len(heads)+len(local))
		for p, sha := range heads {
			if sha != "" {
				files[p] = sha
			}
		}
		for p, sha := range local {
			files[p] = sha
		}
	}
	rec, err := NewRecord(effVersion, by, files)
	if err != nil {
		return PushPlan{}, err
	}
	plan := PushPlan{Record: rec, Claim: !sameFiles(files, eff.Files)}
	if effVersion == 0 && prune && len(heads) > 0 {
		plan.Claim = true // an empty tree pruning every stale head still publishes
	}
	for p, sha := range local {
		if heads[p] != sha {
			plan.Uploads = append(plan.Uploads, p)
		}
	}
	sort.Strings(plan.Uploads)
	return plan, nil
}

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for p, s := range a {
		if b[p] != s {
			return false
		}
	}
	return true
}

// CopyPath is where a conflicting remote copy of an area's file is parked,
// relative to the data dir.
func CopyPath(area, p string) string { return subsync.AsidePath(subsync.ConflictDir, area, p) }
