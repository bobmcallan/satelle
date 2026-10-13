package cli

// Deliberate reconcile of diverged substrate (sty_b0873ec3): where a plain pull
// takes the three-way default for every path, `sync reconcile` shows each path
// that differs between this machine and the bound project's hosted copy and lets
// the developer choose a side per path or per area. Everything that decides what
// a choice does lives in internal/snapsync (Choose, Driver.ApplyChoices); this
// file parses the flags, previews, diffs, saves what a choice replaces and runs
// the same per-area pull a deploy does.

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/snapsync"
)

// reconcileOpts are the flags of one `sync reconcile` run.
type reconcileOpts struct {
	server        string
	areas         []string
	diff          string
	apply         bool
	takeLocal     []string
	takeHosted    []string
	takeLocalAll  []string
	takeHostedAll []string
}

func newSyncReconcileCmd() *cobra.Command {
	var o reconcileOpts
	cmd := &cobra.Command{
		Use:   "reconcile [area...]",
		Short: "Show what differs between this machine and the hosted copy, and choose a side per change",
		Long: `reconcile brings two machines whose substrate has diverged back in step
deliberately, with what differs on each side visible.

With no flag it only reads: for every opted-in area (or those named) it lists each
path whose local and hosted copies differ, how, and what a plain pull would do.
--diff shows the two copies; --apply carries out the pull with your choices. A path
nobody chose follows the plain pull (changed on both sides: kept here, hosted copy
parked as a conflict).

Whatever a choice replaces is saved first under the runtime backups/sync-replaced/
{local,hosted}/<area>/<path>; a choice that cannot be honoured is refused, by name,
before any file is written. It never pushes: run "satelle sync push" afterwards.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			o.areas = args
			return runSyncReconcile(cmd, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.server, "server", "", "Hosted server URL (overrides the configured machine hosted server).")
	f.StringVar(&o.diff, "diff", "", "Print a unified diff of the hosted copy of <area>/<path> against the local copy.")
	f.BoolVar(&o.apply, "apply", false, "Carry out the pull, applying the choices below.")
	f.StringArrayVar(&o.takeLocal, "take-local", nil, "Keep this machine's copy of <area>/<path> (repeatable; needs --apply).")
	f.StringArrayVar(&o.takeHosted, "take-hosted", nil, "Take the hosted copy of <area>/<path> (repeatable; needs --apply).")
	f.StringArrayVar(&o.takeLocalAll, "take-local-all", nil, "Keep this machine's copy of every differing path in <area> (repeatable; needs --apply).")
	f.StringArrayVar(&o.takeHostedAll, "take-hosted-all", nil, "Take the hosted copy of every differing path in <area> (repeatable; needs --apply).")
	return cmd
}

// reconcileApply is a reconcile's resolved choices, handed to the deploy and
// documents pull that carry it out.
type reconcileApply struct {
	areas   map[string]bool                     // config areas to pull
	choices map[string]map[string]snapsync.Side // area -> path -> side
	all     map[string]snapsync.Side            // area -> side for every differing path
	save    snapsync.SaveFunc
	// fastForward (a `sync pull`, sty_0fa4f732) restricts the apply to areas whose
	// plan only brings in what the hosted copy changed; an area whose plan is
	// anything else is left untouched and recorded in refused (area -> why).
	fastForward bool
	refused     map[string]string
}

// admit reports whether a planned job may be applied. Only a fast-forward pull
// can refuse one: the plan is made again at the point of writing, so an edit
// made since `sync status` classified the area turns it away here. That re-check
// narrows the window to subsync.Restore but does not close it; a file edited
// between this plan and the write is still replaced, which is why the pull
// applies a plan that holds no local change rather than trusting the earlier
// state alone.
func (r *reconcileApply) admit(job *snapsync.PullJob) bool {
	if r == nil || !r.fastForward || job.FastForward() {
		return true
	}
	r.refused[job.Area] = "its pull is not a fast-forward (a local change, a conflict or an incomplete entry)"
	return false
}

// arm gives a planned job its choices and the way to keep what they replace. A
// nil reconcile (a plain deploy or pull) leaves the job alone.
func (r *reconcileApply) arm(job *snapsync.PullJob) {
	if r == nil {
		return
	}
	job.Choices = map[string]snapsync.Side{}
	for p, s := range r.choices[job.Area] {
		job.Choices[p] = s
	}
	if s, ok := r.all[job.Area]; ok {
		job.Choices = snapsync.ChooseAll(job.Plan, job.Choices, s)
	}
	job.Save = r.save
}

// reconcileSaver keeps a replaced file through the substrate backup, under the
// runtime plane's backups/sync-replaced/<side>/<area>/<path>. It prints the
// backup policy's notice once.
func reconcileSaver(cfg config.Config, repoRoot, dataDir string, out io.Writer) snapsync.SaveFunc {
	opts := ResolveBackupOpts(cfg, repoRoot)
	opts.BackupsDir = cfg.ResolveRuntimeDir(repoRoot).Dir
	noticed := false
	return func(area, path string, from snapsync.Origin, body []byte) (string, error) {
		res, err := backupExistingFile(dataDir, BackupKindSyncReplaced, from.String()+"/"+area+"/"+path, body, opts)
		if err != nil {
			return "", err
		}
		if res.Notice != "" && !noticed {
			noticed = true
			fmt.Fprintln(out, res.Notice)
		}
		return res.LocalPath, nil
	}
}

func runSyncReconcile(cmd *cobra.Command, o reconcileOpts) error {
	if len(o.takeLocal)+len(o.takeHosted)+len(o.takeLocalAll)+len(o.takeHostedAll) > 0 && !o.apply {
		return errors.New("--take-local, --take-hosted, --take-local-all and --take-hosted-all take effect only with --apply")
	}
	if o.apply && o.diff != "" {
		return errors.New("--diff and --apply are separate runs")
	}
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		return err
	}
	areas, err := config.SubstrateAreas(cfg)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(areas) == 0 {
		fmt.Fprintln(out, "Nothing to reconcile — every config area and documents is local. Set [sync] <area> = personal (or [sync] all = personal) to opt in.")
		return nil
	}
	if areas, err = selectReconcileAreas(areas, o.areas); err != nil {
		return err
	}
	server := resolveServer(o.server)
	if server == "" {
		return fmt.Errorf("no hosted server configured — run \"satelle login\" or pass --server <url>")
	}
	project, err := resolveBoundProject(cfg, repoRoot)
	if err != nil {
		return err
	}
	client := newHostedClient(cmd.Context(), server, repoRoot)
	manifest, err := client.ConfigManifest(cmd.Context(), project)
	if err != nil {
		return fmt.Errorf("fetch manifest: %w", err)
	}
	drivers := &substrateDrivers{cfg: cfg, repoRoot: repoRoot, dataDir: dataDir, server: server, project: project, client: client, manifest: manifest}

	jobs := map[string]*snapsync.PullJob{}
	for _, area := range areas {
		d := drivers.forArea(area)
		local, err := pullLocalShas(cfg, repoRoot, server, project, d, area)
		if err != nil {
			return fmt.Errorf("%s: read the local files: %w", area, err)
		}
		job, err := d.PlanPull(cmd.Context(), area, local)
		if err != nil {
			return fmt.Errorf("%s: %w", area, err)
		}
		jobs[area] = job
	}

	switch {
	case o.diff != "":
		return reconcileDiff(cmd, cfg, repoRoot, drivers, areas, jobs, o.diff)
	case o.apply:
		return reconcileApplyRun(cmd, cfg, repoRoot, dataDir, server, project, drivers, areas, jobs, o)
	}
	return reconcilePreview(out, areas, jobs)
}

// selectReconcileAreas narrows the opted-in areas to the ones named.
func selectReconcileAreas(optedIn, named []string) ([]string, error) {
	if len(named) == 0 {
		return optedIn, nil
	}
	var out []string
	for _, n := range named {
		found := false
		for _, a := range optedIn {
			if a == n {
				found = true
				out = append(out, a)
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("area %q is not opted in for sync (opted in: %s)", n, strings.Join(optedIn, ", "))
		}
	}
	return out, nil
}

// reconcilePreview lists every path that differs, changing nothing.
func reconcilePreview(out io.Writer, areas []string, jobs map[string]*snapsync.PullJob) error {
	differing := 0
	for _, area := range areas {
		job := jobs[area]
		if job == nil {
			fmt.Fprintf(out, "%s: the hosted copy holds no snapshot yet — nothing to reconcile (satelle sync push publishes it)\n", area)
			continue
		}
		var rows []snapsync.Entry
		for _, e := range job.Plan.Entries {
			if e.Action != snapsync.InSync {
				rows = append(rows, e)
			}
		}
		if len(rows) == 0 {
			fmt.Fprintf(out, "%s: no differences (hosted snapshot %d)\n", area, job.Version)
			continue
		}
		differing += len(rows)
		fmt.Fprintf(out, "%s: %d path(s) differ — local base %s, hosted %s\n", area, len(rows), versionLabel(job.BaseVersion()), versionLabel(job.Version))
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  PATH\tCLASS\tPULL DEFAULT")
		for _, e := range rows {
			class, def := snapsync.Describe(e)
			fmt.Fprintf(w, "  %s\t%s\t%s\n", e.Path, class, def)
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	if differing > 0 {
		fmt.Fprintln(out, "\nSee one with: satelle sync reconcile --diff <area>/<path>")
		fmt.Fprintln(out, "Choose with:  satelle sync reconcile --apply [--take-local <area>/<path>] [--take-hosted <area>/<path>] [--take-local-all <area>] [--take-hosted-all <area>]")
	}
	return nil
}

// findReconcileEntry resolves an operator's <area>/<path> against the plans. The
// path as listed (relative to the data dir) is accepted too, when only one area
// has it. An in-step path is found too, so a choice naming one is refused by the
// plan rather than reported as unknown.
func findReconcileEntry(areas []string, jobs map[string]*snapsync.PullJob, arg string) (string, snapsync.Entry, error) {
	arg = strings.TrimPrefix(filepath.ToSlash(arg), "./")
	type hit struct {
		area  string
		entry snapsync.Entry
	}
	var qualified, plain []hit
	for _, area := range areas {
		job := jobs[area]
		if job == nil {
			continue
		}
		for _, e := range job.Plan.Entries {
			if area+"/"+e.Path == arg {
				qualified = append(qualified, hit{area, e})
			}
			if e.Path == arg {
				plain = append(plain, hit{area, e})
			}
		}
	}
	hits := qualified
	if len(hits) == 0 {
		hits = plain
	}
	switch len(hits) {
	case 0:
		return "", snapsync.Entry{}, fmt.Errorf("%s: not in any area being reconciled", arg)
	case 1:
		return hits[0].area, hits[0].entry, nil
	}
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		names = append(names, h.area)
	}
	return "", snapsync.Entry{}, fmt.Errorf("%s: in several areas (%s) — name it <area>/<path>", arg, strings.Join(names, ", "))
}

// reconcileDiff prints a unified diff of the hosted copy against the local one,
// the local side in the form a push would send it.
func reconcileDiff(cmd *cobra.Command, cfg config.Config, repoRoot string, drivers *substrateDrivers, areas []string, jobs map[string]*snapsync.PullJob, arg string) error {
	area, e, err := findReconcileEntry(areas, jobs, arg)
	if err != nil {
		return err
	}
	job := jobs[area]
	files, err := config.AreaFiles(cfg, repoRoot, area)
	if err != nil {
		return fmt.Errorf("%s: read the local files: %w", area, err)
	}
	var local string
	haveLocal := false
	for _, f := range files {
		if f.Path == e.Path {
			local, haveLocal = string(f.Content), true
		}
	}
	body, haveHosted, err := drivers.forArea(area).HostedBytes(cmd.Context(), e.Path)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", e.Path, err)
	}
	out := cmd.OutOrStdout()
	class, def := snapsync.Describe(e)
	fmt.Fprintf(out, "%s/%s — %s (a plain pull: %s)\n", area, e.Path, class, def)
	hostedLabel, localLabel := fmt.Sprintf("hosted (snapshot %d) %s", job.Version, e.Path), "local "+e.Path
	if !haveHosted {
		hostedLabel += " (absent on hosted)"
	} else if e.Remote != "" && snapsync.SHA(body) != e.Remote {
		fmt.Fprintf(out, "note: the hosted copy is not the snapshot's bytes yet (incomplete upload)\n")
	}
	if !haveLocal {
		localLabel += " (absent on local)"
	}
	if area == "settings" {
		fmt.Fprintln(out, "note: the local side is shown as a push sends it ([sync] project and [hosted] project withheld)")
	}
	printUnifiedDiff(out, hostedLabel, localLabel, string(body), local)
	return nil
}

// reconcileApplyRun resolves the choices against the plans, refuses any the plans
// refuse before touching a file, then carries out the pull per area.
func reconcileApplyRun(cmd *cobra.Command, cfg config.Config, repoRoot, dataDir, server, project string, drivers *substrateDrivers, areas []string, jobs map[string]*snapsync.PullJob, o reconcileOpts) error {
	out := cmd.OutOrStdout()
	rc := &reconcileApply{
		areas:   map[string]bool{},
		choices: map[string]map[string]snapsync.Side{},
		all:     map[string]snapsync.Side{},
		save:    reconcileSaver(cfg, repoRoot, dataDir, out),
	}
	var problems []string
	for _, c := range []struct {
		args []string
		side snapsync.Side
	}{{o.takeLocal, snapsync.TakeLocal}, {o.takeHosted, snapsync.TakeHosted}} {
		for _, arg := range c.args {
			area, e, err := findReconcileEntry(areas, jobs, arg)
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
			if rc.choices[area] == nil {
				rc.choices[area] = map[string]snapsync.Side{}
			}
			if prev, ok := rc.choices[area][e.Path]; ok && prev != c.side {
				problems = append(problems, fmt.Sprintf("%s: chosen both local and hosted", arg))
				continue
			}
			rc.choices[area][e.Path] = c.side
		}
	}
	for _, c := range []struct {
		args []string
		side snapsync.Side
	}{{o.takeLocalAll, snapsync.TakeLocal}, {o.takeHostedAll, snapsync.TakeHosted}} {
		for _, area := range c.args {
			if jobs[area] == nil {
				problems = append(problems, fmt.Sprintf("%s: not an area being reconciled with a hosted snapshot", area))
				continue
			}
			if prev, ok := rc.all[area]; ok && prev != c.side {
				problems = append(problems, fmt.Sprintf("%s: chosen both local and hosted for the whole area", area))
				continue
			}
			rc.all[area] = c.side
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("sync reconcile: %s", strings.Join(problems, "; "))
	}
	for _, area := range areas {
		if jobs[area] == nil {
			continue
		}
		job := jobs[area]
		rc.arm(job)
		if err := job.CheckChoices(); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}

	var applied []string
	for _, area := range areas {
		if jobs[area] == nil {
			fmt.Fprintf(out, "%s: the hosted copy holds no snapshot yet — nothing to reconcile\n", area)
			continue
		}
		applied = append(applied, area)
		if area != "documents" {
			rc.areas[area] = true
		}
	}
	docs := jobs["documents"] != nil && containsString(areas, "documents")
	if err := applySnapshotAreas(cmd, cfg, repoRoot, dataDir, server, project, o.server, rc, docs); err != nil {
		return err
	}

	// Where each area stands now, asked of the driver: kept local changes are
	// unpushed until "satelle sync push" publishes them.
	sort.Strings(applied)
	for _, area := range applied {
		st := areaStatus(cmd.Context(), cfg, repoRoot, drivers.forArea(area), area)
		fmt.Fprintf(out, "%s: %s — next: %s\n", area, st.State, nextCommand(st))
	}
	return nil
}

// applySnapshotAreas is the one area-restricted snapshot apply: it pulls the
// config areas rc names (a config deploy over their snapshot plans alone) and,
// when documents is set, the documents snapshot. A reconcile and a `sync pull`
// share it, so neither reaches the deploy's materialise-every-head path, and the
// agents ingest check and the local [sync] project binding stay the deploy's own.
// An area a fast-forward rc turned away is recorded in rc.refused and written to
// by nothing. serverArg is the --server flag as typed.
func applySnapshotAreas(cmd *cobra.Command, cfg config.Config, repoRoot, dataDir, server, project, serverArg string, rc *reconcileApply, documents bool) error {
	// Every area's saves come before any area stages: the documents choices are
	// applied (and what they replace kept) here, ahead of the config deploy, whose
	// own areas do the same before they stage. A save that fails in either leaves
	// the whole tree as it was.
	var docs *documentsPull
	if documents {
		client := newHostedClient(cmd.Context(), server, repoRoot)
		var handled bool
		var perr error
		if docs, handled, perr = planDocumentsPull(cmd, cfg, repoRoot, dataDir, client, server, project, rc); perr != nil {
			return perr
		}
		if (!handled || docs == nil) && rc.refused["documents"] == "" {
			return errors.New("documents: the hosted copy's snapshot could not be read")
		}
		if docs != nil {
			if err := docs.d.ApplyChoices(cmd.Context(), docs.job); err != nil {
				return err
			}
		}
	}
	if len(rc.areas) > 0 {
		if _, err := runSyncConfigDeployWith(cmd, serverArg, "", 0, rc); err != nil {
			return err
		}
	}
	if docs != nil {
		if err := docs.run(cmd, cfg, repoRoot, dataDir, server, project); err != nil {
			return err
		}
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
