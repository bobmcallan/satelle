package snapsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/subsync"
)

// Records is the hosted store of snapshot records: always the config route, for
// every area, because only that route reads a pinned version.
type Records interface {
	// Head is the highest version of the area's record, 0 when there is none.
	Head(ctx context.Context, area string) (int, error)
	// Read returns the record body at a pinned version; ok=false when that
	// version does not exist.
	Read(ctx context.Context, area string, version int) (body []byte, ok bool, err error)
	// Put appends a new record and returns the version it was given.
	Put(ctx context.Context, area string, body []byte) (int, error)
}

// ErrBlobMissing is returned by Blobs.Get when the hosted copy has no such path.
var ErrBlobMissing = errors.New("snapsync: hosted file not found")

// Blobs is the hosted store of the area's files: the config route for config
// areas, the documents route for documents.
type Blobs interface {
	// Heads is the sha256 of every hosted head on the route, by path.
	Heads(ctx context.Context) (map[string]string, error)
	// Get returns a path's head bytes.
	Get(ctx context.Context, path string) ([]byte, error)
	// Put uploads a path's bytes; created is false for an idempotent re-push.
	Put(ctx context.Context, path string, content []byte) (created bool, err error)
}

// UnavailableError means the snapshot store could not be read at all — a server
// without the route, say. Callers fall back to the unsnapshotted behaviour that
// predates this package, which is also exactly what a server with no snapshot
// gets.
type UnavailableError struct{ Err error }

func (e *UnavailableError) Error() string { return "snapshots unavailable: " + e.Err.Error() }
func (e *UnavailableError) Unwrap() error { return e.Err }

// Driver runs the claim → check → upload → save-base sequence for a push and the
// plan → stage → commit sequence for a pull. It is written once and shared by
// the config and documents routes, which differ only in their Blobs.
type Driver struct {
	// Server, Project and RepoRoot key this checkout's recorded base.
	Server, Project, RepoRoot string
	// DataDir is the .satelle data dir files are restored under.
	DataDir string
	// By is this checkout's location id, stamped into records.
	By      string
	Records Records
	Blobs   Blobs
	// Owns reports whether a server path belongs to the area.
	Owns func(area, path string) bool
	// PullCmd names the command that pulls this driver's route, for refusals.
	PullCmd string
	// Prune makes a first claim (no hosted snapshot yet) publish this tree as the
	// whole truth instead of carrying forward hosted files it lacks.
	Prune bool
	// Force publishes this tree as the next snapshot even when the hosted copy
	// is ahead of it or holds changes it has not applied. It never overrides an
	// unmerged conflict copy.
	Force bool
	// Materialize makes Stage return files already equal to the snapshot too (a
	// deploy rewrites the whole tree; a documents pull does not).
	Materialize bool
	Out         io.Writer

	heads map[string]string
}

// SHA is the hex sha256 snapsync records for a file's bytes.
func SHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// normSHA reduces the server's sha spelling (quotes, a sha256: prefix, case) to
// the hex form records use; anything else is returned empty so it never matches.
func normSHA(s string) string {
	s = strings.ToLower(strings.Trim(strings.TrimSpace(s), `"'`))
	s = strings.TrimPrefix(s, "sha256:")
	if len(s) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(s); err != nil {
		return ""
	}
	return s
}

func (d *Driver) printf(format string, a ...any) {
	if d.Out != nil {
		fmt.Fprintf(d.Out, format, a...)
	}
}

// Heads is the sha of every hosted head on the driver's route, whether or not
// any area owns it. It is read once per driver.
func (d *Driver) Heads(ctx context.Context) (map[string]string, error) {
	if d.heads != nil {
		return d.heads, nil
	}
	raw, err := d.Blobs.Heads(ctx)
	if err != nil {
		return nil, err
	}
	d.heads = make(map[string]string, len(raw))
	for p, s := range raw {
		d.heads[p] = normSHA(s)
	}
	return d.heads, nil
}

func (d *Driver) areaHeads(ctx context.Context, area string) (map[string]string, error) {
	all, err := d.Heads(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for p, s := range all {
		if d.Owns(area, p) {
			out[p] = s
		}
	}
	return out, nil
}

func (d *Driver) read(ctx context.Context, area string) ReadFunc {
	return func(v int) (Record, bool, error) {
		body, ok, err := d.Records.Read(ctx, area, v)
		if err != nil {
			return Record{}, false, err
		}
		if !ok {
			return Record{}, false, nil
		}
		rec, derr := Decode(body)
		if derr != nil {
			return Record{}, false, nil
		}
		return rec, true, nil
	}
}

func (d *Driver) conflictCopyExists(area string) func(string) bool {
	return func(p string) bool {
		_, err := os.Stat(filepath.Join(d.DataDir, filepath.FromSlash(CopyPath(area, p))))
		return err == nil
	}
}

// loadBase loads this checkout's base for the area, clearing any unmerged marker
// whose conflict copy has been deleted (the operator saying "merged").
func (d *Driver) loadBase(area string) (*hosted.AreaBase, error) {
	b, ok, err := hosted.LoadAreaBase(d.Server, d.Project, d.RepoRoot, area)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	b, changed := Resolve(b, d.conflictCopyExists(area))
	if changed {
		if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, area, b); err != nil {
			return nil, err
		}
	}
	return &b, nil
}

// effective reads the record head and walks to the effective snapshot.
func (d *Driver) effective(ctx context.Context, area string, base *hosted.AreaBase) (int, Record, error) {
	head, err := d.Records.Head(ctx, area)
	if err != nil {
		return 0, Record{}, d.unavailable(err)
	}
	baseVersion := 0
	if base != nil {
		baseVersion = base.Version
	}
	v, rec, err := Effective(baseVersion, head, d.read(ctx, area))
	if err != nil {
		return 0, Record{}, d.unavailable(err)
	}
	if v == 0 && head > 0 {
		// Records exist but none can be read or decoded (a store without pinned
		// reads, say). A readable chain always has a first claim, so this is not
		// "no snapshot" — it is "cannot tell".
		return 0, Record{}, d.unavailable(fmt.Errorf("%d %s snapshot record(s) on the hosted copy are unreadable", head, area))
	}
	return v, rec, nil
}

func (d *Driver) unavailable(err error) error {
	if errors.Is(err, hosted.ErrLoginRequired) {
		return err
	}
	return &UnavailableError{Err: err}
}

// PushFile is one local file of the area as a push would send it.
type PushFile struct {
	Path    string
	Content []byte
}

// PushJob is a push that has passed every refusal and is ready to run.
type PushJob struct {
	Area       string
	files      map[string][]byte
	local      map[string]string
	effVersion int
	eff        Record
	plan       PushPlan
}

// Claims reports whether running the job will claim a new snapshot.
func (j *PushJob) Claims() bool { return j.plan.Claim }

// PushResult is what a push did.
type PushResult struct {
	Area    string
	Claimed bool
	Version int
	Parent  int
	// Created, Unchanged and NotUploaded count blobs: new heads, idempotent
	// re-pushes, and files whose hosted head already matched.
	Created, Unchanged, NotUploaded int
}

// PreparePush reads the hosted snapshot and decides the push without writing
// anything, so a caller can refuse every area before any of them uploads.
// It returns *ErrBehind or *ErrUnmerged when the push must not happen, and
// *UnavailableError when snapshots cannot be read at all.
func (d *Driver) PreparePush(ctx context.Context, area string, files []PushFile) (*PushJob, error) {
	base, err := d.loadBase(area)
	if err != nil {
		return nil, err
	}
	effVersion, eff, err := d.effective(ctx, area, base)
	if err != nil {
		return nil, err
	}
	heads, err := d.areaHeads(ctx, area)
	if err != nil {
		return nil, d.unavailable(err)
	}
	job := &PushJob{Area: area, files: map[string][]byte{}, local: map[string]string{}, effVersion: effVersion, eff: eff}
	for _, f := range files {
		job.files[f.Path] = f.Content
		job.local[f.Path] = SHA(f.Content)
	}
	job.plan, err = PlanPush(area, d.PullCmd, d.By, d.Prune, d.Force, base, effVersion, eff, job.local, heads, d.conflictCopyExists(area))
	if err != nil {
		return nil, err
	}
	return job, nil
}

// Push runs a prepared job: claim the snapshot, re-walk to confirm the claim
// won, record the base, then upload whichever blobs the hosted heads lack.
// Claiming first is what lets a lost race be refused before a single blob moves.
func (d *Driver) Push(ctx context.Context, job *PushJob) (PushResult, error) {
	res := PushResult{Area: job.Area, Version: job.effVersion, Parent: job.effVersion}
	if job.plan.Claim {
		v, err := d.Records.Put(ctx, job.Area, job.plan.Record.Encode())
		if err != nil {
			return res, fmt.Errorf("claim %s snapshot: %w", job.Area, err)
		}
		if _, ok, err := d.Records.Read(ctx, job.Area, v); err != nil {
			return res, fmt.Errorf("confirm %s snapshot: %w", job.Area, err)
		} else if !ok {
			// A server that took the record but cannot read a pinned version back
			// cannot arbitrate a race either; push as it always did. The next run
			// finds records it cannot read and stays on the plain upload.
			d.printf("%s: snapshot %d cannot be read back from the hosted copy — pushing without a snapshot.\n", job.Area, v)
			job.plan.Claim = false
			return d.upload(ctx, job, res)
		}
		won, _, err := Effective(job.effVersion, v, d.read(ctx, job.Area))
		if err != nil {
			return res, fmt.Errorf("confirm %s snapshot: %w", job.Area, err)
		}
		if won != v {
			// Another machine claimed the same parent first. Our record stays on
			// the hosted copy as a dead head every reader ignores.
			return res, &ErrBehind{Area: job.Area, Base: job.effVersion, Effective: won, PullCmd: d.PullCmd}
		}
		res.Claimed, res.Version = true, v
		// The base is what THIS tree holds, not the record: a first claim may carry
		// forward hosted files this tree lacks, and the next pull must fetch those
		// rather than read them as files this machine deleted.
		if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, job.Area, hosted.AreaBase{Version: v, Files: job.local}); err != nil {
			return res, err
		}
		if job.plan.Forced {
			d.printf("%s: snapshot %d (parent %d, forced over hosted %d; this machine had %d)\n", job.Area, v, job.effVersion, job.effVersion, job.plan.BaseVersion)
		} else {
			d.printf("%s: snapshot %d (parent %d)\n", job.Area, v, job.effVersion)
		}
	} else if job.effVersion > 0 {
		if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, job.Area, hosted.AreaBase{Version: job.effVersion, Files: job.local}); err != nil {
			return res, err
		}
		d.printf("%s: snapshot %d (unchanged)\n", job.Area, job.effVersion)
	}
	return d.upload(ctx, job, res)
}

// upload puts every blob whose hosted head is not the snapshot's bytes.
func (d *Driver) upload(ctx context.Context, job *PushJob, res PushResult) (PushResult, error) {
	for _, p := range job.plan.Uploads {
		created, err := d.Blobs.Put(ctx, p, job.files[p])
		if err != nil {
			return res, fmt.Errorf("push %s: %w", p, err)
		}
		if created {
			res.Created++
		} else {
			res.Unchanged++
		}
		if d.heads != nil {
			d.heads[p] = job.local[p]
		}
	}
	res.NotUploaded = len(job.local) - len(job.plan.Uploads)
	return res, nil
}

// PullJob is a planned pull of one area.
type PullJob struct {
	Area    string
	Version int
	Plan    PullPlan
	base    *hosted.AreaBase
	remote  Record
}

// PlanPull reads the hosted snapshot and plans the pull of one area against the
// local sha of each file. It returns a nil job when the hosted copy has no
// snapshot for the area (the caller falls back to restoring every head), and
// *UnavailableError when snapshots cannot be read.
func (d *Driver) PlanPull(ctx context.Context, area string, local map[string]string) (*PullJob, error) {
	base, err := d.loadBase(area)
	if err != nil {
		return nil, err
	}
	version, rec, err := d.effective(ctx, area, base)
	if err != nil {
		return nil, err
	}
	if version == 0 {
		return nil, nil
	}
	heads, err := d.areaHeads(ctx, area)
	if err != nil {
		return nil, d.unavailable(err)
	}
	return &PullJob{Area: area, Version: version, Plan: PlanPull(base, rec, version, local, heads), base: base, remote: rec}, nil
}

// Stage carries out everything a pull does outside the restore itself — parks
// conflicting remote copies, moves stale local copies out of the tree, removes
// files the hosted copy deleted — and returns the files the caller must restore.
// The caller restores them (through whatever per-area handling it needs), then
// calls Commit.
func (d *Driver) Stage(ctx context.Context, job *PullJob) ([]subsync.File, error) {
	var writes []subsync.File
	var deletes []string
	for i := range job.Plan.Entries {
		e := &job.Plan.Entries[i]
		switch e.Action {
		case Write, Conflict:
			body, err := d.Blobs.Get(ctx, e.Path)
			if err != nil && !errors.Is(err, ErrBlobMissing) {
				return nil, fmt.Errorf("fetch %s: %w", e.Path, err)
			}
			if err != nil || SHA(body) != e.Remote {
				job.demote(e)
				continue
			}
			if e.Action == Write {
				writes = append(writes, subsync.File{Path: e.Path, Content: body})
				continue
			}
			if err := subsync.WriteAside(d.DataDir, CopyPath(job.Area, e.Path), body); err != nil {
				return nil, err
			}
		case InSync:
			if !d.Materialize {
				continue
			}
			// A deploy re-materialises the whole snapshot, so a file already equal
			// is written again — byte-identical, and still through the caller's
			// per-file handling (the agents merge, the settings binding). If the
			// hosted head cannot supply it, the equal local file is left alone.
			body, err := d.Blobs.Get(ctx, e.Path)
			if err != nil && !errors.Is(err, ErrBlobMissing) {
				return nil, fmt.Errorf("fetch %s: %w", e.Path, err)
			}
			if err == nil && SHA(body) == e.Remote {
				writes = append(writes, subsync.File{Path: e.Path, Content: body})
			}
		case Delete:
			deletes = append(deletes, e.Path)
		case RemovedStale:
			cur, err := os.ReadFile(filepath.Join(d.DataDir, filepath.FromSlash(e.Path)))
			if err != nil {
				return nil, fmt.Errorf("back up %s: %w", e.Path, err)
			}
			if err := subsync.WriteAside(d.DataDir, subsync.AsidePath(subsync.RemovedDir, job.Area, e.Path), cur); err != nil {
				return nil, err
			}
			deletes = append(deletes, e.Path)
		}
	}
	if _, err := subsync.Remove(d.DataDir, deletes); err != nil {
		return nil, err
	}
	d.report(job)
	return writes, nil
}

// demote turns a planned Write or Conflict into Incomplete when the hosted copy
// cannot supply the snapshot's bytes, and undoes the base changes that entry had
// already made.
func (j *PullJob) demote(e *Entry) {
	e.Action = Incomplete
	delete(j.Plan.Base.Unmerged, e.Path)
	if len(j.Plan.Base.Unmerged) == 0 {
		j.Plan.Base.Unmerged = nil
	}
	if j.base != nil {
		if old, ok := j.base.Files[e.Path]; ok {
			j.Plan.Base.Files[e.Path] = old
			return
		}
	}
	delete(j.Plan.Base.Files, e.Path)
}

func (d *Driver) report(job *PullJob) {
	for _, e := range job.Plan.Entries {
		switch e.Action {
		case Delete:
			d.printf("  removed (deleted on hosted copy): %s\n", e.Path)
		case Conflict:
			d.printf("  conflict (changed on both sides): %s — remote copy at %s; merge, delete the copy, then push\n", e.Path, CopyPath(job.Area, e.Path))
		case StillUnmerged:
			d.printf("  still unmerged: %s — merge it and delete %s\n", e.Path, CopyPath(job.Area, e.Path))
		case Incomplete:
			d.printf("  incomplete (hosted copy of %s is not the snapshot's yet): left unchanged\n", e.Path)
		case RemovedStale:
			d.printf("  removed (deleted on hosted copy; backup at %s): %s\n", subsync.AsidePath(subsync.RemovedDir, job.Area, e.Path), e.Path)
		case LocalOnly:
			d.printf("  local only — push to publish: %s\n", e.Path)
		case DeletedRemotelyKept:
			d.printf("  deleted on hosted copy but changed here — kept, push to republish: %s\n", e.Path)
		}
	}
}

// Commit records the base once the caller has restored the staged writes.
// failed names writes the restore could not complete; those keep their old base
// so the next pull tries again.
func (d *Driver) Commit(job *PullJob, failed map[string]bool) error {
	for i := range job.Plan.Entries {
		e := &job.Plan.Entries[i]
		if e.Action == Write && failed[e.Path] {
			e.Action = Incomplete
			if job.base != nil {
				if old, ok := job.base.Files[e.Path]; ok {
					job.Plan.Base.Files[e.Path] = old
					continue
				}
			}
			delete(job.Plan.Base.Files, e.Path)
		}
	}
	if err := hosted.SaveAreaBase(d.Server, d.Project, d.RepoRoot, job.Area, job.Plan.Base); err != nil {
		return err
	}
	d.printf("%s: synced to snapshot %d\n", job.Area, job.Version)
	return nil
}

// Changed reports how many paths the pull wrote, removed or parked, so a caller
// can tell "up to date" from a pull that did something.
func (j *PullJob) Changed() int {
	n := 0
	for _, e := range j.Plan.Entries {
		switch e.Action {
		case Write, Delete, Conflict, RemovedStale:
			n++
		}
	}
	return n
}

// Paths returns the sorted paths whose entry has the action.
func (j *PullJob) Paths(a Action) []string {
	var out []string
	for _, e := range j.Plan.Entries {
		if e.Action == a {
			out = append(out, e.Path)
		}
	}
	sort.Strings(out)
	return out
}
