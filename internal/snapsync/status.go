package snapsync

import (
	"context"
	"errors"
	"sort"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
)

// The states an area can be in relative to its hosted copy. They are the words
// `satelle sync status` prints and the words syncstate records.
const (
	StateInStep      = "in step"
	StateUnpushed    = "unpushed"
	StateBehind      = "behind"
	StateDiverged    = "diverged"
	StateUnmerged    = "unmerged"
	StateNeverSynced = "never synced"
	StateUnavailable = "unavailable"
)

// AreaStatus is one area's state against the hosted copy.
type AreaStatus struct {
	Area  string
	State string
	// Base is the snapshot this checkout last synced, Effective the hosted
	// effective snapshot; 0 means none.
	Base, Effective int
	// Pending lists the hosted changes this checkout has not applied (behind,
	// diverged); Unmerged the paths a pull left for merging.
	Pending, Unmerged []string
	// Err says why the state is StateUnavailable.
	Err error
}

// Status reports where one area stands against the hosted copy. It asks the
// push-planning path a push would run (planPush, force off), so an orphaned
// record is not reported as behind and a pull that would still change something
// is. local is the sha of each file as a push would send it (LocalShas).
//
// A hosted copy that cannot be read, or a missing login, is a StateUnavailable
// status rather than an error; any other failure is returned.
func (d *Driver) Status(ctx context.Context, area string, local map[string]string) (AreaStatus, error) {
	p, err := d.planPush(ctx, area, local, false)
	st := AreaStatus{Area: area, Effective: p.effVersion}
	if p.base != nil {
		st.Base = p.base.Version
	}
	var unavailable *UnavailableError
	var behind *ErrBehind
	var unmerged *ErrUnmerged
	switch {
	case err == nil:
	case errors.As(err, &unavailable), errors.Is(err, hosted.ErrLoginRequired):
		st.State, st.Err = StateUnavailable, err
		return st, nil
	case errors.As(err, &behind) && !behind.Diverged && len(local) == 0 && p.effVersion > 0:
		// Nothing local to send, so a push has nothing to be refused for: the pull's
		// own plan, read below, says whether the hosted copy differs.
	case errors.As(err, &behind):
		st.Pending = behind.Pending
		st.State = StateBehind
		if behind.Diverged {
			st.State = StateDiverged
		}
		return st, nil
	case errors.As(err, &unmerged):
		st.State, st.Unmerged = StateUnmerged, unmerged.Paths
		return st, nil
	default:
		return st, err
	}
	switch {
	case len(local) == 0:
		// Nothing local to send: a push has no file to upload and cannot publish
		// a deletion, so the area is never unpushed or never synced. What the
		// hosted copy holds is what differs, and a pull is the way to it. With a
		// snapshot that is the pull's own plan — what `sync reconcile` lists — so a
		// snapshot of files the binary already supplies, or one that no longer names
		// a stale head, leaves the area in step; with none, any head is a difference.
		if p.effVersion > 0 {
			for _, e := range PlanPull(p.base, p.eff, p.effVersion, local, p.heads, p.set).Differences() {
				st.Pending = append(st.Pending, e.Path)
			}
		} else {
			st.Pending = hostedPaths(p)
		}
		if len(st.Pending) > 0 {
			st.State = StateBehind
		} else {
			st.State = StateInStep
		}
	case p.base == nil && p.effVersion == 0:
		st.State = StateNeverSynced
	case p.plan.Claim || len(p.plan.Uploads) > 0 || !p.set.sameFilesExcept(local, baseFiles(p.base)):
		st.State = StateUnpushed
	default:
		st.State = StateInStep
	}
	return st, nil
}

// hostedPaths lists, sorted, every path the hosted copy holds for the area: live
// heads, the effective snapshot's files and the base's files. It is the pending set
// of an area with no snapshot, where there is no plan to read it from.
func hostedPaths(p plannedPush) []string {
	seen := map[string]struct{}{}
	for path, sha := range p.heads {
		if sha != "" {
			seen[path] = struct{}{}
		}
	}
	for path := range p.eff.Files {
		seen[path] = struct{}{}
	}
	for path := range baseFiles(p.base) {
		seen[path] = struct{}{}
	}
	var out []string
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func baseFiles(b *hosted.AreaBase) map[string]string {
	if b == nil {
		return nil
	}
	return b.Files
}

// LocalShas is the sha of each file of an area as a push would send it.
func LocalShas(cfg config.Config, repoRoot, area string) (map[string]string, error) {
	files, err := config.AreaFiles(cfg, repoRoot, area)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path] = SHA(f.Content)
	}
	return out, nil
}

// Drift is how an area's local files differ from the base this checkout last
// synced, read from disk alone: no server is contacted.
type Drift struct {
	// Synced is false when this checkout never synced the area.
	Synced  bool
	Version int
	// Changed lists the paths added, edited or deleted since the base.
	Changed []string
	// Unmerged lists the paths a pull parked for merging whose conflict copy is
	// still on disk.
	Unmerged []string
}

// OfflineDrift compares local with the area's recorded base. A conflict whose
// copy has been deleted counts as merged, exactly as when the driver loads the
// base, so the two never disagree. defaults is config.AreaDefaultShas for the
// area: a base file that is an unedited embedded default is not a deletion, since
// the local view never holds one.
func OfflineDrift(server, project, repoRoot, dataDir, area string, local map[string]string, defaults map[string]map[string]bool) (Drift, error) {
	b, ok, err := hosted.LoadAreaBase(server, project, repoRoot, area)
	if err != nil {
		return Drift{}, err
	}
	if ok {
		b, _ = Resolve(b, copyExistsIn(dataDir, area))
	}
	d := Drift{Synced: ok, Version: b.Version, Changed: NewDefaultSet(defaults, &b).changedPaths(b.Files, local)}
	for p := range b.Unmerged {
		d.Unmerged = append(d.Unmerged, p)
	}
	sort.Strings(d.Unmerged)
	return d, nil
}
