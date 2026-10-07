package cli

// Snapshot-based substrate sync (sty_fe5a8ed4): the CLI side. Everything that
// decides anything lives in internal/snapsync; this file only builds the two
// route adapters (config, documents) around the hosted client, runs the shared
// driver per area, and prints.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/subsync"
)

// configRecords stores snapshot records on the config route. Its manifest is the
// one the caller already fetched, so a run reads it once.
type configRecords struct {
	client   *hosted.Client
	project  string
	versions map[string]int // record path -> head version
}

func newConfigRecords(client *hosted.Client, project string, manifest []hosted.ConfigItem) *configRecords {
	r := &configRecords{client: client, project: project, versions: map[string]int{}}
	for _, it := range manifest {
		if _, ok := snapsync.AreaOfRecordPath(it.Path); ok {
			r.versions[it.Path] = it.Version
		}
	}
	return r
}

func (r *configRecords) Head(_ context.Context, area string) (int, error) {
	return r.versions[snapsync.RecordPath(area)], nil
}

func (r *configRecords) Read(ctx context.Context, area string, version int) ([]byte, bool, error) {
	body, _, err := r.client.ConfigFileContent(ctx, r.project, snapsync.RecordPath(area), version)
	if errors.Is(err, hosted.ErrConfigFileMissing) {
		return nil, false, nil
	}
	return body, err == nil, err
}

func (r *configRecords) Put(ctx context.Context, area string, body []byte) (int, error) {
	res, err := r.client.PushConfigFile(ctx, r.project, snapsync.RecordPath(area), body)
	if err != nil {
		return 0, err
	}
	r.versions[snapsync.RecordPath(area)] = res.Version
	return res.Version, nil
}

// configBlobs is the config route as the driver's file store.
type configBlobs struct {
	client   *hosted.Client
	project  string
	manifest []hosted.ConfigItem
}

func (b *configBlobs) Heads(context.Context) (map[string]string, error) {
	return headSHAByPath(b.manifest), nil
}

func (b *configBlobs) Get(ctx context.Context, path string) ([]byte, error) {
	body, _, err := b.client.ConfigFileContent(ctx, b.project, path, 0)
	if errors.Is(err, hosted.ErrConfigFileMissing) {
		return nil, snapsync.ErrBlobMissing
	}
	return body, err
}

func (b *configBlobs) Put(ctx context.Context, path string, content []byte) (bool, error) {
	res, err := b.client.PushConfigFile(ctx, b.project, path, content)
	return res.Created, err
}

// docBlobs is the documents route as the driver's file store.
type docBlobs struct {
	client  *hosted.Client
	project string
}

func (b *docBlobs) Heads(ctx context.Context) (map[string]string, error) {
	changes, err := b.client.ListDocumentChanges(ctx, b.project, "")
	if err != nil {
		return nil, err
	}
	return headSHAByPath(changes.Items), nil
}

func (b *docBlobs) Get(ctx context.Context, path string) ([]byte, error) {
	body, _, err := b.client.DocumentFileContent(ctx, b.project, path)
	if errors.Is(err, hosted.ErrDocumentFileMissing) {
		return nil, snapsync.ErrBlobMissing
	}
	return body, err
}

func (b *docBlobs) Put(ctx context.Context, path string, content []byte) (bool, error) {
	res, err := b.client.PushDocumentFile(ctx, b.project, path, content)
	if errors.Is(err, hosted.ErrDocumentConflict) {
		return false, fmt.Errorf("conflict — resolve on the server and retry")
	}
	return res.Created, err
}

// newSnapDriver builds the shared driver for one route.
func newSnapDriver(cfg config.Config, client *hosted.Client, out io.Writer, server, project, repoRoot, dataDir, pullCmd string, rec snapsync.Records, blobs snapsync.Blobs) *snapsync.Driver {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		abs = repoRoot
	}
	return &snapsync.Driver{
		Server: server, Project: project, RepoRoot: abs, DataDir: dataDir,
		By: client.Location(), Records: rec, Blobs: blobs, Out: out, PullCmd: pullCmd,
		Owns: func(area, p string) bool { return config.SyncAreaOf(cfg, repoRoot, p) == area },
	}
}

// pushOutcome is a snapshot push's blob counts, summed over areas.
type pushOutcome struct{ created, unchanged, notUploaded int }

// runSnapshotPush prepares every area's push first — so a refusal in any area
// uploads nothing anywhere — then runs them. handled=false means snapshots are
// unavailable on this server and the caller should use the plain upload.
func runSnapshotPush(ctx context.Context, d *snapsync.Driver, areaFiles map[string][]snapsync.PushFile, areas []string) (out pushOutcome, handled bool, err error) {
	var jobs []*snapsync.PushJob
	var refusals []string
	for _, area := range areas {
		job, perr := d.PreparePush(ctx, area, areaFiles[area])
		var unavailable *snapsync.UnavailableError
		var behind *snapsync.ErrBehind
		var unmerged *snapsync.ErrUnmerged
		switch {
		case perr == nil:
			jobs = append(jobs, job)
		case errors.As(perr, &unavailable):
			if d.Out != nil {
				fmt.Fprintf(d.Out, "%v — pushing without a snapshot.\n", perr)
			}
			return out, false, nil
		case errors.As(perr, &behind), errors.As(perr, &unmerged):
			refusals = append(refusals, perr.Error())
		default:
			return out, true, perr
		}
	}
	if len(refusals) > 0 {
		return out, true, errors.New(strings.Join(refusals, "\n"))
	}
	for _, job := range jobs {
		res, perr := d.Push(ctx, job)
		if perr != nil {
			return out, true, perr
		}
		out.created += res.Created
		out.unchanged += res.Unchanged
		out.notUploaded += res.NotUploaded
	}
	return out, true, nil
}

// pushConfigSnapshots is the config push when the hosted copy can hold snapshots.
func pushConfigSnapshots(cmd *cobra.Command, cfg config.Config, repoRoot, dataDir string, client *hosted.Client, server, project string, manifest []hosted.ConfigItem, files []config.ConfigFile) (pushOutcome, bool, error) {
	byArea := map[string][]snapsync.PushFile{}
	for _, f := range files {
		byArea[f.Area] = append(byArea[f.Area], snapsync.PushFile{Path: f.Path, Content: f.Content})
	}
	var areas []string
	for _, area := range config.ConfigAreas {
		scope, err := config.ScopeFor(cfg, area)
		if err != nil {
			return pushOutcome{}, true, err
		}
		if scope != config.LocalScope {
			areas = append(areas, area)
		}
	}
	d := newSnapDriver(cfg, client, cmd.OutOrStdout(), server, project, repoRoot, dataDir,
		"satelle sync rehydrate", newConfigRecords(client, project, manifest),
		&configBlobs{client: client, project: project, manifest: manifest})
	d.Prune = pruneRequested(cmd)
	return runSnapshotPush(cmd.Context(), d, byArea, areas)
}

// pushDocumentsSnapshot is the documents push when the hosted copy can hold
// snapshots. The records ride the config route, so it reads that manifest; a
// server without one gets the plain upload.
func pushDocumentsSnapshot(cmd *cobra.Command, cfg config.Config, repoRoot, dataDir string, client *hosted.Client, server, project string, files []config.ConfigFile) (pushOutcome, bool, error) {
	manifest, err := client.ConfigManifest(cmd.Context(), project)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return pushOutcome{}, true, err
		}
		return pushOutcome{}, false, nil
	}
	pf := make([]snapsync.PushFile, 0, len(files))
	for _, f := range files {
		pf = append(pf, snapsync.PushFile{Path: f.Path, Content: f.Content})
	}
	d := newSnapDriver(cfg, client, cmd.OutOrStdout(), server, project, repoRoot, dataDir,
		"satelle sync documents pull", newConfigRecords(client, project, manifest),
		&docBlobs{client: client, project: project})
	d.Prune = pruneRequested(cmd)
	return runSnapshotPush(cmd.Context(), d, map[string][]snapsync.PushFile{"documents": pf}, []string{"documents"})
}

// pruneFlagUsage is the one help text every command that takes --prune shares.
const pruneFlagUsage = "When the hosted copy has no snapshot yet, publish this tree as the whole truth: hosted files this tree lacks are dropped instead of carried forward."

// pruneRequested reads --prune from the command that is running. A command
// that does not define it (a test's bare command) reads as false.
func pruneRequested(cmd *cobra.Command) bool {
	f := cmd.Flags().Lookup("prune")
	return f != nil && f.Value.String() == "true"
}

// localShas is the sha of each file of an area as a push would send it.
func localShas(cfg config.Config, repoRoot, area string) (map[string]string, error) {
	files, err := config.AreaFiles(cfg, repoRoot, area)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path] = snapsync.SHA(f.Content)
	}
	return out, nil
}

// configDeploySnapshots holds the areas a deploy is driving from a snapshot.
type configDeploySnapshots struct {
	driver *snapsync.Driver
	jobs   []*snapsync.PullJob
	areas  map[string]bool
}

// owns reports whether a manifest path is governed by a snapshot, and so is
// restored only when that snapshot names it.
func (s *configDeploySnapshots) owns(cfg config.Config, repoRoot, path string) bool {
	if s == nil {
		return false
	}
	return s.areas[config.SyncAreaOf(cfg, repoRoot, path)]
}

// planConfigDeploySnapshots plans each config area the hosted copy holds a
// snapshot for. An area with no snapshot — or a server that cannot read them —
// is simply not governed, and deploys every head as it always did.
func planConfigDeploySnapshots(cmd *cobra.Command, cfg config.Config, repoRoot, dataDir string, client *hosted.Client, server, project string, manifest []hosted.ConfigItem) (*configDeploySnapshots, error) {
	has := map[string]bool{}
	for _, it := range manifest {
		if area, ok := snapsync.AreaOfRecordPath(it.Path); ok {
			has[area] = true
		}
	}
	s := &configDeploySnapshots{areas: map[string]bool{}}
	s.driver = newSnapDriver(cfg, client, cmd.OutOrStdout(), server, project, repoRoot, dataDir,
		"satelle sync rehydrate", newConfigRecords(client, project, manifest),
		&configBlobs{client: client, project: project, manifest: manifest})
	s.driver.Materialize = true
	for _, area := range config.ConfigAreas {
		if !has[area] {
			continue
		}
		local, err := localShas(cfg, repoRoot, area)
		if err != nil {
			// An unreadable local file (an agents layer that does not decode) must
			// not stop a deploy that exists to restore the tree.
			fmt.Fprintf(cmd.OutOrStdout(), "%s: cannot read the local files (%v) — deploying every head.\n", area, err)
			continue
		}
		job, err := s.driver.PlanPull(cmd.Context(), area, local)
		var unavailable *snapsync.UnavailableError
		if errors.As(err, &unavailable) {
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %v — deploying every head.\n", area, err)
			continue
		}
		if err != nil {
			return nil, err
		}
		if job == nil {
			continue
		}
		s.areas[area] = true
		s.jobs = append(s.jobs, job)
	}
	return s, nil
}

// stage runs every job's side effects and returns the files to restore.
func (s *configDeploySnapshots) stage(ctx context.Context) ([]subsync.File, error) {
	var files []subsync.File
	for _, job := range s.jobs {
		ws, err := s.driver.Stage(ctx, job)
		if err != nil {
			return nil, err
		}
		files = append(files, ws...)
	}
	return files, nil
}

// commit records each area's base after the restore succeeded.
func (s *configDeploySnapshots) commit(res subsync.Result) error {
	failed := map[string]bool{}
	for _, f := range res.Failed {
		failed[f.Path] = true
	}
	for _, job := range s.jobs {
		if err := s.driver.Commit(job, failed); err != nil {
			return err
		}
	}
	return nil
}

// pullDocumentsSnapshot is the documents pull when the hosted copy holds a
// documents snapshot. handled=false sends the caller to the cursor-driven pull.
func pullDocumentsSnapshot(cmd *cobra.Command, cfg config.Config, repoRoot, dataDir string, client *hosted.Client, server, project string) (bool, error) {
	manifest, err := client.ConfigManifest(cmd.Context(), project)
	if err != nil {
		if errors.Is(err, hosted.ErrLoginRequired) {
			return true, err
		}
		return false, nil
	}
	out := cmd.OutOrStdout()
	d := newSnapDriver(cfg, client, out, server, project, repoRoot, dataDir,
		"satelle sync documents pull", newConfigRecords(client, project, manifest),
		&docBlobs{client: client, project: project})
	local, err := localShas(cfg, repoRoot, "documents")
	if err != nil {
		return true, err
	}
	job, err := d.PlanPull(cmd.Context(), "documents", local)
	var unavailable *snapsync.UnavailableError
	if errors.As(err, &unavailable) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if job == nil {
		return false, nil
	}
	writes, err := d.Stage(cmd.Context(), job)
	if err != nil {
		return true, err
	}
	res, err := subsync.Restore(dataDir, writes)
	if err != nil {
		return true, fmt.Errorf("restore documents: %w", err)
	}
	for _, f := range res.Failed {
		fmt.Fprintf(out, "  could not write %s: %v\n", f.Path, f.Err)
	}
	failed := map[string]bool{}
	for _, f := range res.Failed {
		failed[f.Path] = true
	}
	if err := d.Commit(job, failed); err != nil {
		return true, err
	}
	// Hosted heads Restore would refuse (a partition poisoned with backups/
	// entries, sty_84f14ace) are not part of any snapshot; they are still reported,
	// so a pull that skipped something never reads as "up to date".
	skipped := 0
	if heads, herr := d.Heads(cmd.Context()); herr == nil {
		for p := range heads {
			if subsync.ExcludedLocal(p) {
				skipped++
			}
		}
	}
	tail := ""
	if skipped > 0 {
		tail = fmt.Sprintf(", %d skipped (local-only path)", skipped)
	}
	switch {
	case job.Changed() == 0 && len(res.Failed) == 0 && skipped == 0:
		fmt.Fprintf(out, "Documents up to date on %s.\n", server)
	case res.Written == 0:
		fmt.Fprintf(out, "Documents pull on %s%s.\n", server, tail)
	default:
		fmt.Fprintf(out, "Pulled %d document(s) from project %q personal collection on %s into %s%s.\n", res.Written, project, server, dataDir, tail)
	}
	return true, nil
}

// lastSyncedNotes renders the recorded base of each non-local area for
// `sync scopes`: area -> "snapshot N", and the unmerged paths per area.
func lastSyncedNotes(cfg config.Config, repoRoot string) (map[string]string, map[string][]string) {
	versions := map[string]string{}
	unmerged := map[string][]string{}
	server := resolveServer("")
	if server == "" {
		return versions, unmerged
	}
	project, err := resolveBoundProject(cfg, repoRoot)
	if err != nil {
		return versions, unmerged
	}
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		abs = repoRoot
	}
	for _, area := range append(append([]string{}, config.ConfigAreas...), "documents") {
		b, ok, err := hosted.LoadAreaBase(server, project, abs, area)
		if err != nil || !ok {
			continue
		}
		versions[area] = fmt.Sprintf("last synced snapshot %d", b.Version)
		for p := range b.Unmerged {
			unmerged[area] = append(unmerged[area], p)
		}
		sort.Strings(unmerged[area])
	}
	return versions, unmerged
}
