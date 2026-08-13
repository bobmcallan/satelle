// `satelle migrate` — one deterministic verb converging a repo to the current
// substrate-planes structure (sty_a3915840 / epic:substrate-planes). Composes
// existing mechanism: home-keyed runtime relocation, legacy residue removal,
// unedited-seed prune, gitignore managed-block convergence, then deployment
// validation. Dry-run by default; --yes applies.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	var yes, allowLive bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Converge this repo to the current satelle structure (dry-run default)",
		Long: `migrate composes the structural upgrade steps for substrate-planes:

  1. runtime relocation   — copy legacy .satelle/satelle.db (+ logs/backups/stories)
                            into ~/.satelle/<repo-key>/ (non-destructive)
  2. legacy residue       — remove in-repo runtime leftovers once home-keyed
                            (satelle.db*, logs/, backups/, stories/ — including
                            attachment residue recreated under .satelle/stories/).
                            Operator hand-made agents.*.bak under the repo is
                            NOT residue (sty_0445104b); keep backups outside
                            migrate's scan roots (e.g. next to the machine-wide
                            catalog, or a path you control)
  3. substrate prune      — remove unedited embedded-default seed copies
  4. gitignore converge   — rewrite the managed .gitignore block to the current form
  5. config converge      — append missing managed entries to non-empty
                            [gate] edit_exempt_paths / edit_exempt_globs
                            (operator additions kept; empty untouched)
  6. retired refs         — report (and under --yes, rewrite renames of) authored
                            [[wikilinks]] that cite embedded names the binary
                            retired ; removals-with-no-replacement
                            are report-only — never silently rewritten
  7. validate             — deployed-system check (same as end of satelle init)

Dry-run by default: prints the full plan and applies nothing. Pass --yes to apply.
Idempotent: a converged repo reports "already on current structure".

LIVE RUNTIME (sty_5308eb60): if the legacy DB holds a fresh engagement lease
or a satelle serve is responding on this repo's web port, migrate REFUSES to
relocate/remove unless --allow-live is set. Relocating under a live session
strands every write made after the VACUUM INTO snapshot. Prefer waiting for
the session to park/finish (satelle story seat) and stopping serve.

Edited/authored substrate is never touched. Runtime migrate leaves the legacy
DB in place until residue removal (only after a successful home-keyed copy).
See decision-substrate-planes-local-first and sty_a3915840 / sty_f115e6bf.
` + migrateSplitBrainHelp,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, cfgPath, err := config.Load("")
			if err != nil && err != config.ErrNotFound {
				return err
			}
			repoRoot := "."
			if cfgPath != "" {
				repoRoot = config.RepoRootFromConfigPath(cfgPath)
			} else if wd, e := os.Getwd(); e == nil {
				repoRoot = wd
			}
			dataDir := cfg.ResolveDataDir(repoRoot)
			a := &app.App{
				Config:     cfg,
				RepoRoot:   repoRoot,
				DataDir:    dataDir,
				RuntimeDir: cfg.ResolveRuntimeDir(repoRoot).Dir,
				DBPath:     cfg.ResolveDB(repoRoot),
			}
			return runMigrate(cmd.OutOrStdout(), a, yes, allowLive)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "apply the migration (default is dry-run)")
	cmd.Flags().BoolVar(&allowLive, "allow-live", false, "UNSAFE: relocate even when a live engagement lease or satelle serve is detected (strands post-snapshot writes)")
	register(cmd)
}

// migratePlan is the pure plan for one repo (no IO beyond what planning needs).
type migratePlan struct {
	RuntimeRelocate bool     // need runtime migrate (legacy DB present, home not yet)
	Residue         []string // dataDir-relative paths/globs to remove
	PruneSeeds      []string // dataDir-relative unedited seed paths
	Gitignore       bool     // managed block needs converge
	// ExemptManaged lists path prefixes still missing from [gate]
	// edit_exempt_paths (footprint + draft prefixes). An absent key
	// materialises the seed set; empty list is a deliberate opt-out.
	ExemptManaged []string
	// ExemptGlobsManaged lists managed [gate] edit_exempt_globs still lacking
	// (sty_fefc88cd / sty_e8e1879c). Absent key materialises the managed
	// dump names; empty list is a deliberate opt-out.
	ExemptGlobsManaged []string
	// HostedToSync lists leftover [hosted] project/workspace that [sync] still
	// lacks. Copied onto [sync] before the leftover table is dropped.
	// [hosted] server is machine-scope (sty_34037275) and is never copied here.
	HostedToSync []string
	// HostedTableDrop is true when a leftover [hosted] table is still in the
	// file (sty_5eb1bb8a). Dropped after any HostedToSync copy.
	HostedTableDrop bool
	// StaleAfter is true when [sync] stale_after is absent (sty_30696eeb).
	StaleAfter bool
	// WorkflowRetire lists DOT workflow files superseded by an authored,
	// PARSEABLE done.toml + step.toml (sty_9835070d). Actionable work: migrate
	// removes them.
	WorkflowRetire []string
	// WorkflowConvertPending lists DOT workflow files present while the route
	// source is absent or does not parse. A NOTICE, never work: migrate cannot
	// author a route, because deriving obligations from a graph is interpretation
	// that has to be authored and reviewed. It is deliberately outside empty() —
	// nothing here is actionable — but it is never silent either, because
	// "already on current structure" would then read as "nothing to do" about a
	// conversion that is outstanding.
	WorkflowConvertPending []string
	// StampRewrite lists work items whose `workflow:` tag still names the two
	// route-source FILES instead of the lifecycle (sty_81bb0dde). Populated from
	// the store rather than by planMigrate, which stays IO-free.
	StampRewrite []stampRewrite
	// StampCapped marks a stamp sweep that hit its page cap, so the report can
	// say another pass is owed instead of implying the repo is converged.
	StampCapped bool
	// RetiredRefs lists authored [[wikilinks]] to embedded names the binary has
	// retired . Renames are rewritten under --yes; removals are
	// report-only. Dry-run never writes.
	RetiredRefs []retiredRefHit
	// MachineStrays are leftover machine-scope keys in repo config files
	// (sty_21a7d16d). Re-homed under --yes; never silently ignored.
	MachineStrays []config.Stray
}

// retiredRefHit is one authored citation of a retired embedded name.
type retiredRefHit struct {
	Rel    string // dataDir-relative path or constitution name
	Target string
	Entry  config.RetiredEntry
	Count  int
}

// stampRewrite is one item's stale workflow stamp and the tag set that replaces
// it. The whole tag set is carried so apply never re-derives it — the plan the
// operator read is the plan that runs.
type stampRewrite struct {
	ID   string
	Old  string
	Tags []string
}

// stampSweepLimit caps one pass of the stamp sweep. It is the store's own
// ceiling; a repo with more items than this rewrites what it found and says so,
// and a second `migrate --yes` finishes the job. Truncating in silence would
// report a converged repo that is not one.
const stampSweepLimit = 2000

// legacyStamps finds every work item stamped with the old file-pair spelling of
// the derived route and computes its replacement tag set. Order and every other
// tag are preserved: this rewrites ONE value, it does not re-canonicalise tags.
// The second return is true when the sweep hit its page cap.
//
// Read-only, and it opens its OWN short-lived handle rather than taking one from
// the App: migrate runs before the store is wired (and relocates the database
// out from under it mid-run), so a handle held across the plan would be pointing
// at the wrong file by the time apply runs. An absent database is not an error
// — a repo with no ledger has no stamps — and is deliberately not created here,
// so the dry-run path stays free of side effects.
func legacyStamps(ctx context.Context, dbPath string) ([]stampRewrite, bool) {
	if dbPath == "" || !fileExists(dbPath) {
		return nil, false
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, false
	}
	defer db.Close()
	items, err := db.Stories.List(ctx, workitem.ListFilter{
		Limit: stampSweepLimit, IncludeArchived: true,
	})
	if err != nil {
		return nil, false
	}
	var out []stampRewrite
	for _, it := range items {
		old := wfgovern.StampedWorkflowName(it)
		if old == "" || !wfgovern.IsFilePairStamp(old) {
			continue
		}
		tags := make([]string, len(it.Tags))
		copy(tags, it.Tags)
		for i, t := range tags {
			if strings.HasPrefix(t, wfgovern.WorkflowStampPrefix) {
				tags[i] = wfgovern.WorkflowStampPrefix + wfgovern.DerivedRouteName
			}
		}
		out = append(out, stampRewrite{ID: it.ID, Old: old, Tags: tags})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, len(items) >= stampSweepLimit
}

// applyStamps writes the planned rewrites to the database at dbPath — resolved
// AFTER any relocation, so the write lands in the authoritative ledger rather
// than the copy that is about to be removed.
func applyStamps(ctx context.Context, out io.Writer, dbPath string, plan []stampRewrite) error {
	db, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("migrate: open %s: %w", dbPath, err)
	}
	defer db.Close()
	now := time.Now()
	for _, s := range plan {
		tags := s.Tags
		if _, err := db.Stories.Update(ctx, s.ID, workitem.UpdateInput{Tags: &tags}, now); err != nil {
			return fmt.Errorf("migrate: restamp %s: %w", s.ID, err)
		}
		fmt.Fprintf(out, "  %s  %s%s → %s%s\n", s.ID,
			wfgovern.WorkflowStampPrefix, s.Old,
			wfgovern.WorkflowStampPrefix, wfgovern.DerivedRouteName)
	}
	return nil
}

// stampDB picks the database the stamp sweep reads at PLAN time: the home-keyed
// one when it exists, else the legacy in-repo one. Before relocation the legacy
// file is the only ledger there is, and planning against a home path that does
// not exist yet would report "no stamps" for a repo full of them.
func stampDB(cfg config.Config, repoRoot, dataDir string) string {
	if home := cfg.ResolveDB(repoRoot); fileExists(home) {
		return home
	}
	if legacy := filepath.Join(dataDir, config.DefaultDBName); fileExists(legacy) {
		return legacy
	}
	return ""
}

func planMigrate(cfg config.Config, repoRoot, dataDir string) migratePlan {
	p := migratePlan{}
	legacyDB := filepath.Join(dataDir, config.DefaultDBName)
	homeDB := filepath.Join(config.GlobalDir(), config.RepoKey(repoRoot), config.DefaultDBName)
	if fileExists(legacyDB) && !fileExists(homeDB) {
		p.RuntimeRelocate = true
	}
	// Residue is scheduled only when the home-keyed ledger exists or will after
	// relocate — never when removing would destroy the only DB copy.
	if fileExists(homeDB) || p.RuntimeRelocate {
		p.Residue = listLegacyResidue(dataDir)
	}
	p.PruneSeeds = listUneditedSeeds(dataDir)
	p.Gitignore = gitignoreNeedsConverge(repoRoot)
	p.ExemptManaged = editExemptManagedMissing(dataDir)
	p.ExemptGlobsManaged = editExemptGlobsManagedMissing(dataDir)
	p.HostedToSync = hostedBindingMissingOnSync(dataDir)
	p.HostedTableDrop = hasHostedTable(dataDir)
	p.StaleAfter = staleAfterMissing(dataDir)
	p.WorkflowRetire, p.WorkflowConvertPending = workflowConversionState(dataDir)
	p.RetiredRefs = planRetiredRefs(dataDir)
	p.MachineStrays = config.MachineScopeStrays(dataDir)
	return p
}

// planRetiredRefs scans authored substrate for [[wikilinks]] to names in
// config.RetiredSubstrate. One hit per (file, target); Count is occurrences.
// Walk is recursive (matches auditWikilinks) so nested authored paths are not
// silently missed.
func planRetiredRefs(dataDir string) []retiredRefHit {
	var hits []retiredRefHit
	add := func(rel, body string) {
		body = codeSpanRe.ReplaceAllString(body, "")
		counts := map[string]int{}
		for _, m := range wikiLinkRe.FindAllStringSubmatch(body, -1) {
			target := strings.TrimSpace(m[1])
			if _, ok := config.LookupRetired(target); ok {
				counts[target]++
			}
		}
		for target, n := range counts {
			e, _ := config.LookupRetired(target)
			hits = append(hits, retiredRefHit{Rel: rel, Target: target, Entry: e, Count: n})
		}
	}
	for _, kind := range substrateKindsScanned {
		root := filepath.Join(dataDir, kind)
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			rel, rerr := filepath.Rel(dataDir, path)
			if rerr != nil {
				return nil
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			add(filepath.ToSlash(rel), string(b))
			return nil
		})
	}
	// Constitution at dataDir root if present.
	if b, err := os.ReadFile(filepath.Join(dataDir, config.DefaultConstitutionName)); err == nil {
		add(config.DefaultConstitutionName, string(b))
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Rel != hits[j].Rel {
			return hits[i].Rel < hits[j].Rel
		}
		return hits[i].Target < hits[j].Target
	})
	return hits
}

// applyRetiredRefRewrites rewrites [[old]]→[[new]] for renames only, under --yes.
// Removals (empty Replacement) are never rewritten.
func applyRetiredRefRewrites(out io.Writer, dataDir string, hits []retiredRefHit) error {
	// Group by file so we rewrite once per file.
	byFile := map[string][]retiredRefHit{}
	for _, h := range hits {
		if h.Entry.Replacement == "" {
			fmt.Fprintf(out, "  leave %s: [[%s]] (no replacement — edit by hand)\n", h.Rel, h.Target)
			continue
		}
		byFile[h.Rel] = append(byFile[h.Rel], h)
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, rel := range files {
		path := filepath.Join(dataDir, rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		body := string(b)
		total := 0
		for _, h := range byFile[rel] {
			var n int
			body, n = rewriteWikilinkTarget(body, h.Target, h.Entry.Replacement)
			if n == 0 {
				continue
			}
			total += n
			fmt.Fprintf(out, "  rewrote %s: [[%s]] → [[%s]] (%d occurrence(s))\n",
				rel, h.Target, h.Entry.Replacement, n)
		}
		if total == 0 {
			continue
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

// rewriteWikilinkTarget replaces [[old…]] forms with [[new…]], preserving
// |label and #anchor suffixes. Code spans (backticks) are left untouched so
// prose quoting the old name is not rewritten.
func rewriteWikilinkTarget(body, old, newName string) (string, int) {
	n := 0
	// Mask code spans, rewrite, unmask — same span set as danglingIn detection.
	type span struct{ from, to int }
	var spans []span
	for _, loc := range codeSpanRe.FindAllStringIndex(body, -1) {
		spans = append(spans, span{loc[0], loc[1]})
	}
	inCode := func(i int) bool {
		for _, s := range spans {
			if i >= s.from && i < s.to {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	last := 0
	for _, loc := range wikiLinkRe.FindAllStringSubmatchIndex(body, -1) {
		// loc[0]:loc[1] full match; loc[2]:loc[3] target group
		if inCode(loc[0]) {
			continue
		}
		target := body[loc[2]:loc[3]]
		if strings.TrimSpace(target) != old {
			continue
		}
		b.WriteString(body[last:loc[0]])
		// Rebuild: [[newName + suffix after old target
		suffix := body[loc[3]:loc[1]] // from end of target through ]]
		b.WriteString("[[" + newName + suffix)
		last = loc[1]
		n++
	}
	b.WriteString(body[last:])
	if n == 0 {
		return body, 0
	}
	return b.String(), n
}

// applyMachineStrays re-homes leftover machine-scope keys into
// ~/.satelle/config.toml when the machine key is unset, then removes them from
// the repo file. An already-set machine value wins; the repo assignment is
// still dropped. Comments are preserved (RemoveKey).
func applyMachineStrays(out io.Writer, dataDir string, strays []config.Stray) error {
	gc, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	dirty := false
	byFile := map[string][]config.Stray{}
	for _, s := range strays {
		wrote, msg := rehomeStray(&gc, s)
		if wrote {
			dirty = true
		}
		fmt.Fprintf(out, "  %s\n", msg)
		byFile[s.File] = append(byFile[s.File], s)
	}
	if dirty {
		if err := config.SaveGlobal(gc); err != nil {
			return err
		}
	}
	for file, list := range byFile {
		path := filepath.Join(dataDir, file)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(b)
		for _, s := range list {
			section, key := straySectionKey(s.Key)
			content = config.RemoveKey(content, section, key)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "  removed stray key(s) from .satelle/%s\n", file)
	}
	return nil
}

func rehomeStray(gc *config.GlobalConfig, s config.Stray) (wrote bool, msg string) {
	switch {
	case s.Key == "web_port" || s.Key == "[service] port":
		if gc.Service.Port > 0 {
			return false, s.Key + ": machine port " + strconv.Itoa(gc.Service.Port) + " already set — kept (repo discarded)"
		}
		n, err := strconv.Atoi(strings.TrimSpace(s.RepoValue))
		if err != nil || n <= 0 {
			return false, s.Key + ": not a port — removed from repo only"
		}
		gc.Service.Port = n
		return true, s.Key + ": re-homed to ~/.satelle/config.toml [service] port = " + strconv.Itoa(n)
	case s.Key == "[server] endpoint" || s.Key == "[service] endpoint":
		if strings.TrimSpace(gc.Service.Endpoint) != "" {
			return false, s.Key + ": machine endpoint already set — kept (repo discarded)"
		}
		gc.Service.Endpoint = strings.TrimSpace(s.RepoValue)
		return true, s.Key + ": re-homed to ~/.satelle/config.toml [service] endpoint"
	case s.Key == "[service] addr":
		if strings.TrimSpace(gc.Service.Addr) != "" {
			return false, s.Key + ": machine addr already set — kept (repo discarded)"
		}
		gc.Service.Addr = strings.TrimSpace(s.RepoValue)
		return true, s.Key + ": re-homed to ~/.satelle/config.toml [service] addr"
	case s.Key == "[service] repo":
		if strings.TrimSpace(gc.Service.Repo) != "" {
			return false, s.Key + ": machine repo already set — kept (repo discarded)"
		}
		gc.Service.Repo = strings.TrimSpace(s.RepoValue)
		return true, s.Key + ": re-homed to ~/.satelle/config.toml [service] repo"
	case s.Key == "[sync] server" || s.Key == "[hosted] server":
		if strings.TrimSpace(gc.Hosted.ResolveServer()) != "" {
			return false, s.Key + ": machine hosted server already set — kept (repo discarded)"
		}
		gc.Hosted.Server = strings.TrimSpace(s.RepoValue)
		return true, s.Key + ": re-homed to ~/.satelle/config.toml [hosted] server"
	default:
		return false, s.Key + ": removed from repo (no machine mapping)"
	}
}

func straySectionKey(key string) (section, name string) {
	switch {
	case key == "web_port":
		return "", "web_port"
	case strings.HasPrefix(key, "[server] "):
		return "server", strings.TrimPrefix(key, "[server] ")
	case strings.HasPrefix(key, "[service] "):
		return "service", strings.TrimPrefix(key, "[service] ")
	case strings.HasPrefix(key, "[sync] "):
		return "sync", strings.TrimPrefix(key, "[sync] ")
	case strings.HasPrefix(key, "[hosted] "):
		return "hosted", strings.TrimPrefix(key, "[hosted] ")
	default:
		return "", key
	}
}

// workflowConversionState splits the on-disk DOT workflows into the ones a
// working route source supersedes (retire) and the ones still waiting for one
// (pending). Exactly one of the two is ever non-empty.
//
// The route source must PARSE before anything is retired: deleting the only
// remaining lifecycle because a half-authored done.toml happened to be on disk
// is the one failure this step must not have.
//
// The route halves are read by NAME, whatever their extension. A repo mid-
// conversion has done.md on disk and done.toml beside it; taking only one
// extension would either miss the converted half (and report a conversion that
// is done as outstanding) or feed markdown to the TOML parser. Reading both and
// letting the parse decide keeps this honest — an unparseable half is an
// unconverted repo, which is exactly what pending means.
func workflowConversionState(dataDir string) (retire, pending []string) {
	wfDir := filepath.Join(dataDir, "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		return nil, nil
	}
	var dots []string
	var doneBody, stepBody string
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		name := strings.TrimSuffix(e.Name(), ext)
		isMD := strings.EqualFold(ext, ".md")
		if e.IsDir() || strings.EqualFold(e.Name(), "README.md") ||
			!(isMD || strings.EqualFold(ext, ".toml")) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(wfDir, e.Name()))
		if err != nil {
			continue
		}
		switch name {
		case wfgovern.RouteSourceDone:
			doneBody = pickRouteHalf(doneBody, string(body), isMD)
			continue
		case wfgovern.RouteSourceStep:
			stepBody = pickRouteHalf(stepBody, string(body), isMD)
			continue
		}
		// Textual, because there is no DOT parser left to ask (sty_d953c5d8).
		// migrate is the one place that must still RECOGNISE a graph: it is what
		// tells an unconverted repo what it owes. A graph is always markdown — a
		// .toml file is never one.
		if isMD && strings.Contains(string(body), "```dot") {
			dots = append(dots, e.Name())
		}
	}
	if len(dots) == 0 {
		return nil, nil
	}
	sort.Strings(dots)
	if doneBody == "" || stepBody == "" {
		return nil, dots
	}
	if _, err := wfdot.ParseDone(doneBody); err != nil {
		return nil, dots
	}
	if _, err := wfdot.ParseSteps(stepBody); err != nil {
		return nil, dots
	}
	return dots, nil
}

// pickRouteHalf chooses between two files claiming the same route half. The
// TOML one wins: it is the converted form, and a repo that still has the
// markdown beside it has finished the conversion but not deleted the old file.
// Directory order is not stable, so this cannot be left to whichever is read
// last.
func pickRouteHalf(have, body string, isMD bool) string {
	if have != "" && isMD {
		return have
	}
	return body
}

// printConversionPending states an outstanding conversion wherever migrate
// reports, including the converged path — silence there would read as "nothing
// to do" about work that has not been done.
func printConversionPending(out io.Writer, pending []string) {
	if len(pending) == 0 {
		return
	}
	fmt.Fprintf(out, "\nworkflow conversion OUTSTANDING — %d DOT workflow(s) and no route source:\n", len(pending))
	for _, f := range pending {
		fmt.Fprintf(out, "  - workflows/%s\n", f)
	}
	fmt.Fprintln(out, "  until they are converted this repo REFUSES transitions rather than running them ungated.")
	fmt.Fprintln(out, "  read `satelle help workflow-convert` — it maps each node, edge gate and park/cancel sink")
	fmt.Fprintln(out, "  onto the two files, then author workflows/done.toml (a `[<category>]` table per category,")
	fmt.Fprintln(out, "  its obligations in order) and workflows/step.toml (a `[<obligation>]` table per step, a")
	fmt.Fprintln(out, "  `[[gate]]` per always-on gate), and re-run migrate to retire the graphs. migrate does not derive them:")
	fmt.Fprintln(out, "  turning a graph into obligations is interpretation, and it is authored and reviewed.")
}

func (p migratePlan) empty() bool {
	return !p.RuntimeRelocate && len(p.Residue) == 0 && len(p.PruneSeeds) == 0 &&
		!p.Gitignore && len(p.ExemptManaged) == 0 && len(p.ExemptGlobsManaged) == 0 && len(p.HostedToSync) == 0 && !p.HostedTableDrop && !p.StaleAfter && len(p.WorkflowRetire) == 0 &&
		len(p.StampRewrite) == 0 && len(p.RetiredRefs) == 0 && len(p.MachineStrays) == 0
}

func runMigrate(out io.Writer, a *app.App, yes, allowLive bool) error {
	cfg := a.Config
	repoRoot := a.RepoRoot
	dataDir := a.DataDir
	if dataDir == "" {
		dataDir = cfg.ResolveDataDir(repoRoot)
	}
	plan := planMigrate(cfg, repoRoot, dataDir)
	ctx := context.Background()
	plan.StampRewrite, plan.StampCapped = legacyStamps(ctx, stampDB(cfg, repoRoot, dataDir))

	if plan.empty() {
		fmt.Fprintln(out, "already on current structure")
		printConversionPending(out, plan.WorkflowConvertPending)
		return nil
	}

	// Live-runtime check before any plan print that implies apply is safe
	// (sty_5308eb60). Covers relocate AND residue: a live session writing the
	// legacy DB must not race residue removal even when home already exists.
	legacyDB := filepath.Join(dataDir, config.DefaultDBName)
	needLiveCheck := plan.RuntimeRelocate || len(plan.Residue) > 0
	var liveHolders []liveHolder
	if needLiveCheck {
		holders, err := ensureLiveOK(out, cfg, repoRoot, legacyDB, allowLive, false)
		if err != nil {
			return err
		}
		liveHolders = holders
	}

	// Report plan.
	fmt.Fprintln(out, "migrate plan:")
	if plan.RuntimeRelocate {
		home := filepath.Join(config.GlobalDir(), config.RepoKey(repoRoot))
		fmt.Fprintf(out, "  runtime relocate: %s → %s\n",
			filepath.Join(dataDir, config.DefaultDBName), home)
	} else {
		fmt.Fprintln(out, "  runtime relocate: (none)")
	}
	if needLiveCheck {
		printLivePlanLine(out, liveHolders)
	}
	if len(plan.Residue) == 0 {
		fmt.Fprintln(out, "  legacy residue:   (none)")
	} else {
		fmt.Fprintf(out, "  legacy residue:   %d path(s)\n", len(plan.Residue))
		for _, rel := range plan.Residue {
			fmt.Fprintf(out, "    - %s\n", rel)
		}
	}
	if len(plan.PruneSeeds) == 0 {
		fmt.Fprintln(out, "  substrate prune:  (none)")
	} else {
		fmt.Fprintf(out, "  substrate prune:  %d unedited seed(s)\n", len(plan.PruneSeeds))
		for _, rel := range plan.PruneSeeds {
			fmt.Fprintf(out, "    - %s\n", rel)
		}
	}
	if plan.Gitignore {
		fmt.Fprintln(out, "  gitignore:        converge managed block")
	} else {
		fmt.Fprintln(out, "  gitignore:        (already current)")
	}
	if len(plan.ExemptManaged) > 0 {
		fmt.Fprintf(out, "  config:           append %s to [gate] edit_exempt_paths\n",
			strings.Join(plan.ExemptManaged, ", "))
	} else {
		fmt.Fprintln(out, "  config:           (edit_exempt_paths current)")
	}
	if len(plan.ExemptGlobsManaged) > 0 {
		fmt.Fprintf(out, "  config:           append %s to [gate] edit_exempt_globs\n",
			strings.Join(plan.ExemptGlobsManaged, ", "))
	} else {
		fmt.Fprintln(out, "  config:           (edit_exempt_globs current)")
	}
	if len(plan.HostedToSync) > 0 {
		fmt.Fprintf(out, "  config:           copy [hosted] %s onto [sync]\n",
			strings.Join(plan.HostedToSync, ", "))
	} else {
		fmt.Fprintln(out, "  config:           ([sync] binding current)")
	}
	if plan.HostedTableDrop {
		fmt.Fprintln(out, "  config:           drop leftover [hosted] table")
	}
	if plan.StaleAfter {
		fmt.Fprintln(out, "  config:           seed [sync] stale_after = \"24h\"")
	}
	if len(plan.WorkflowRetire) == 0 {
		fmt.Fprintln(out, "  workflows:        (none superseded)")
	} else {
		fmt.Fprintf(out, "  workflows:        retire %d superseded DOT workflow(s) (done.toml + step.toml govern)\n",
			len(plan.WorkflowRetire))
		for _, f := range plan.WorkflowRetire {
			fmt.Fprintf(out, "    - workflows/%s\n", f)
		}
	}
	if len(plan.StampRewrite) == 0 {
		fmt.Fprintln(out, "  stamps:           (no file-pair workflow stamps)")
	} else {
		fmt.Fprintf(out, "  stamps:           rewrite %d item tag(s) → %s%s\n",
			len(plan.StampRewrite), wfgovern.WorkflowStampPrefix, wfgovern.DerivedRouteName)
		for _, s := range plan.StampRewrite {
			fmt.Fprintf(out, "    - %s  %s%s\n", s.ID, wfgovern.WorkflowStampPrefix, s.Old)
		}
		if plan.StampCapped {
			fmt.Fprintf(out, "    (sweep capped at %d item(s) — re-run migrate after this pass)\n", stampSweepLimit)
		}
	}
	if len(plan.MachineStrays) == 0 {
		fmt.Fprintln(out, "  machine keys:     (none)")
	} else {
		fmt.Fprintf(out, "  machine keys:     re-home %d stray(s)\n", len(plan.MachineStrays))
		for _, s := range plan.MachineStrays {
			fmt.Fprintf(out, "    - %s\n", s.Warning())
		}
	}
	if len(plan.RetiredRefs) == 0 {
		fmt.Fprintln(out, "  retired refs:     (none)")
	} else {
		fmt.Fprintf(out, "  retired refs:     %d citation(s) of retired embedded names\n", len(plan.RetiredRefs))
		for _, h := range plan.RetiredRefs {
			if h.Entry.Replacement != "" {
				fmt.Fprintf(out, "    - %s: [[%s]] → [[%s]] (%dx; --yes rewrites)\n",
					h.Rel, h.Target, h.Entry.Replacement, h.Count)
			} else {
				fmt.Fprintf(out, "    - %s: [[%s]] retired with no replacement (%dx; edit by hand)\n",
					h.Rel, h.Target, h.Count)
			}
		}
	}
	fmt.Fprintln(out, "  validate:         deployed system check")
	printConversionPending(out, plan.WorkflowConvertPending)

	if !yes {
		if len(liveHolders) > 0 {
			fmt.Fprintln(out, "\ndry-run only — would REFUSE apply (live runtime); re-run with --yes after idle, or --yes --allow-live (UNSAFE)")
		} else {
			fmt.Fprintln(out, "\ndry-run only — re-run with --yes to apply")
		}
		return nil
	}

	// Refuse live runtime on apply (unless --allow-live).
	if needLiveCheck {
		if _, err := ensureLiveOK(out, cfg, repoRoot, legacyDB, allowLive, true); err != nil {
			return err
		}
	}

	// Apply.
	if plan.RuntimeRelocate {
		fmt.Fprintln(out, "\n→ runtime relocate")
		if err := runRuntimeMigrate(out, a, false, allowLive, false); err != nil {
			return fmt.Errorf("migrate: runtime: %w", err)
		}
		// Home-keyed path is now authoritative — re-resolve so later steps
		// (prune backups, residue safety) never write under legacy dataDir
		// (code-ac-review finding, sty_a3915840 AC3/AC4).
		rt := cfg.ResolveRuntimeDir(repoRoot)
		a.RuntimeDir = rt.Dir
		a.DBPath = cfg.ResolveDB(repoRoot)
		// Refresh plan residue after relocate (home now has the DB).
		plan.Residue = listLegacyResidue(dataDir)
	}

	if len(plan.Residue) > 0 {
		// Safety: never remove residue if home-keyed DB is missing.
		homeDB := filepath.Join(config.GlobalDir(), config.RepoKey(repoRoot), config.DefaultDBName)
		if !fileExists(homeDB) {
			return fmt.Errorf("migrate: refusing residue removal — home-keyed database missing at %s", homeDB)
		}
		fmt.Fprintln(out, "\n→ remove legacy residue")
		for _, rel := range plan.Residue {
			path := filepath.Join(dataDir, filepath.FromSlash(rel))
			if err := removePath(path); err != nil {
				return fmt.Errorf("migrate: remove %s: %w", rel, err)
			}
			fmt.Fprintf(out, "  removed %s\n", rel)
		}
	}

	if len(plan.PruneSeeds) > 0 {
		fmt.Fprintln(out, "\n→ substrate prune")
		opts := ResolveBackupOpts(cfg, repoRoot)
		// Always re-resolve: even when RuntimeRelocate was false, the App may
		// have been constructed with a stale/empty RuntimeDir.
		opts.BackupsDir = cfg.ResolveRuntimeDir(repoRoot).Dir
		// No story id: migrate is repo maintenance, not a slice of authored work,
		// so its prune records nothing against an engaged story (sty_30d3bd99).
		if err := runSubstratePrune(context.Background(), out, strings.NewReader(""), dataDir, opts, true, "", ""); err != nil {
			return fmt.Errorf("migrate: prune: %w", err)
		}
		// Prune backups go under the home-keyed runtime dir — never re-create
		// .satelle/backups/ residue that would fail the next migrate plan.
	}

	if plan.Gitignore {
		fmt.Fprintln(out, "\n→ gitignore converge")
		changed, err := ensureGitignore(repoRoot)
		if err != nil {
			return fmt.Errorf("migrate: gitignore: %w", err)
		}
		if changed {
			fmt.Fprintln(out, "  updated .gitignore managed block")
		} else {
			fmt.Fprintln(out, "  .gitignore already current")
		}
	}

	if len(plan.WorkflowRetire) > 0 {
		// Removal only. The route source is authored substrate this step never
		// writes: baking a repo's lifecycle into the binary so migrate could
		// install it is the config-over-code violation the constitution exists to
		// prevent (sty_9835070d).
		fmt.Fprintln(out, "\n→ retire superseded DOT workflows")
		for _, f := range plan.WorkflowRetire {
			path := filepath.Join(dataDir, "workflows", f)
			if err := removePath(path); err != nil {
				return fmt.Errorf("migrate: remove workflows/%s: %w", f, err)
			}
			fmt.Fprintf(out, "  removed workflows/%s\n", f)
		}
	}

	if len(plan.RetiredRefs) > 0 {
		fmt.Fprintln(out, "\n→ retired embedded refs ")
		if err := applyRetiredRefRewrites(out, dataDir, plan.RetiredRefs); err != nil {
			return fmt.Errorf("migrate: retired refs: %w", err)
		}
	}

	if len(plan.StampRewrite) > 0 {
		// The stamp names a LIFECYCLE. Every item found here was governed by the
		// derived route — that is what the file-pair spelling meant — so they all
		// rewrite to the same name; there is nothing to re-resolve per item.
		//
		// AFTER relocation and residue removal on purpose: a.DBPath is the
		// post-relocate path by here, so the rewrite lands in the ledger the repo
		// keeps rather than the copy that was just deleted.
		fmt.Fprintln(out, "\n→ rewrite file-pair workflow stamps")
		if err := applyStamps(ctx, out, a.DBPath, plan.StampRewrite); err != nil {
			return err
		}
	}

	if len(plan.ExemptManaged) > 0 || len(plan.ExemptGlobsManaged) > 0 || len(plan.HostedToSync) > 0 || plan.HostedTableDrop || plan.StaleAfter {
		fmt.Fprintln(out, "\n→ config converge")
		if len(plan.ExemptManaged) > 0 {
			changed, err := ensureEditExemptManaged(dataDir)
			if err != nil {
				return fmt.Errorf("migrate: edit_exempt_paths: %w", err)
			}
			if changed {
				fmt.Fprintln(out, "  updated .satelle/satelle.toml [gate] edit_exempt_paths")
			} else {
				fmt.Fprintln(out, "  edit_exempt_paths already current")
			}
		}
		if len(plan.ExemptGlobsManaged) > 0 {
			changed, err := ensureGateListManaged(dataDir, "edit_exempt_globs", managedEditExemptGlobs, managedEditExemptGlobs)
			if err != nil {
				return fmt.Errorf("migrate: edit_exempt_globs: %w", err)
			}
			if changed {
				fmt.Fprintln(out, "  updated .satelle/satelle.toml [gate] edit_exempt_globs")
			} else {
				fmt.Fprintln(out, "  edit_exempt_globs already current")
			}
		}
		if len(plan.HostedToSync) > 0 || plan.HostedTableDrop {
			changed, err := healHostedBinding(dataDir)
			if err != nil {
				return fmt.Errorf("migrate: [sync] binding: %w", err)
			}
			if changed {
				fmt.Fprintln(out, "  folded leftover [hosted] into [sync] and dropped the table")
			} else {
				fmt.Fprintln(out, "  [sync] binding already current")
			}
		}
	}

	if changed, err := healStaleAfter(dataDir); err != nil {
		return fmt.Errorf("migrate: [sync] stale_after: %w", err)
	} else if changed {
		fmt.Fprintln(out, "  seeded [sync] stale_after = \"24h\"")
	}

	if len(plan.MachineStrays) > 0 {
		fmt.Fprintln(out, "\n→ re-home machine-scope keys")
		if err := applyMachineStrays(out, dataDir, plan.MachineStrays); err != nil {
			return fmt.Errorf("migrate: machine keys: %w", err)
		}
	}

	fmt.Fprintln(out, "\n→ validate")
	if err := validateDeployment(out, dataDir); err != nil {
		return fmt.Errorf("migrate: validation failed: %w", err)
	}
	fmt.Fprintln(out, "\nmigrate complete")
	return nil
}

// listLegacyResidue returns dataDir-relative paths that are obsolete once
// runtime is home-keyed: satelle.db (+wal/shm), logs/, backups/, stories/.
//
// Deliberately does NOT sweep agents.*.bak under the repo (sty_0445104b).
// Those files are operator hand-made recovery snapshots, not satelle runtime
// residue. The only satelle writer of agents.toml.bak is
// `satelle agent profiles restore --force`, and it targets GlobalAgentsPath
// under GlobalDir — outside migrate's scan roots. No historical satelle version
// wrote agents.*.bak under the repo tree, so there is no exact-name exception
// to keep.
func listLegacyResidue(dataDir string) []string {
	var out []string
	for _, name := range []string{
		config.DefaultDBName,
		config.DefaultDBName + "-wal",
		config.DefaultDBName + "-shm",
		"logs",
		"backups",
		"stories",
	} {
		p := filepath.Join(dataDir, name)
		if st, err := os.Stat(p); err == nil {
			if st.IsDir() {
				out = append(out, name+"/")
			} else {
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// hasLegacyResidue is true when any obsolete in-repo runtime path exists.
func hasLegacyResidue(dataDir string) bool {
	return len(listLegacyResidue(dataDir)) > 0
}

// listUneditedSeeds returns dataDir-relative paths of unedited embedded seeds.
func listUneditedSeeds(dataDir string) []string {
	var out []string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "tasks" {
			continue
		}
		rel := d.RelPath()
		path := filepath.Join(dataDir, filepath.FromSlash(rel))
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if isUneditedSeed(string(body), d.Body) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

func removePath(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

// editExemptManagedMissing returns the managed edit_exempt_paths entries absent
// from satelle.toml's non-empty [gate] edit_exempt_paths (sty_f115e6bf for
// .gitignore; sty_926cfcdc for the harness scaffolds).
func editExemptManagedMissing(dataDir string) []string {
	raw, err := os.ReadFile(filepath.Join(dataDir, config.ConfigName))
	if err != nil {
		return nil
	}
	return editExemptMissingManaged(string(raw))
}

func editExemptGlobsManagedMissing(dataDir string) []string {
	raw, err := os.ReadFile(filepath.Join(dataDir, config.ConfigName))
	if err != nil {
		return nil
	}
	return listMissingManaged(string(raw), "edit_exempt_globs", managedEditExemptGlobs, managedEditExemptGlobs)
}

// ensureEditExemptManaged converges [gate] edit_exempt_paths: an absent key
// materialises the seed set; a non-empty list gains missing converge entries
// (footprint + draft prefixes). An empty list is a deliberate opt-out.
func ensureEditExemptManaged(dataDir string) (bool, error) {
	return ensureGateListManaged(dataDir, "edit_exempt_paths", defaultEditExemptPaths(), managedEditExemptConverge())
}

// ensureGateListManaged applies listMissingManaged and writes the result.
// Empty list is a deliberate opt-out and is never rewritten.
func ensureGateListManaged(dataDir, key string, absentSet, convergeSet []string) (bool, error) {
	path := filepath.Join(dataDir, config.ConfigName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	content := string(raw)
	missing := listMissingManaged(content, key, absentSet, convergeSet)
	if len(missing) == 0 {
		return false, nil
	}
	items := config.ListStringValues(content, "gate", key)
	merged := append(append([]string{}, items...), missing...)
	quoted := make([]string, 0, len(merged))
	for _, p := range merged {
		quoted = append(quoted, `"`+p+`"`)
	}
	value := "[" + strings.Join(quoted, ", ") + "]"
	next := config.UpsertKey(content, "gate", key, value)
	if next == content {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func hostedBindingMissingOnSync(dataDir string) []string {
	path := filepath.Join(dataDir, config.ConfigName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(raw)
	var missing []string
	for _, k := range []string{"project", "workspace"} {
		if !config.HasKey(content, "hosted", k) {
			continue
		}
		if config.HasKey(content, "sync", k) {
			continue
		}
		missing = append(missing, k)
	}
	return missing
}

func hasHostedTable(dataDir string) bool {
	raw, err := os.ReadFile(filepath.Join(dataDir, config.ConfigName))
	if err != nil {
		return false
	}
	return config.HasSection(string(raw), "hosted")
}

// healHostedBinding copies leftover [hosted] project/workspace onto [sync]
// when [sync] does not already set them, re-homes leftover [hosted] server
// onto the machine file (never onto [sync] — sty_34037275), then drops the
// leftover [hosted] table.
func healHostedBinding(dataDir string) (bool, error) {
	path := filepath.Join(dataDir, config.ConfigName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		return false, err
	}
	if server := strings.TrimSpace(cfg.Hosted.Server); server != "" {
		gc, gerr := config.LoadGlobal()
		if gerr != nil {
			return false, gerr
		}
		if strings.TrimSpace(gc.Hosted.ResolveServer()) == "" {
			gc.Hosted.Server = server
			if serr := config.SaveGlobal(gc); serr != nil {
				return false, serr
			}
		}
	}
	content := string(raw)
	for _, k := range hostedBindingMissingOnSync(dataDir) {
		var val string
		switch k {
		case "project":
			val = strings.TrimSpace(cfg.Hosted.Project)
		case "workspace":
			val = strings.TrimSpace(cfg.Hosted.Workspace)
		}
		if val == "" {
			continue
		}
		content = config.UpsertKey(content, "sync", k, strconv.Quote(val))
	}
	if config.HasSection(content, "hosted") {
		content = config.RemoveSection(content, "hosted")
	}
	if content == string(raw) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func staleAfterMissing(dataDir string) bool {
	path := filepath.Join(dataDir, config.ConfigName)
	cfg, _, err := config.Load(path)
	if err != nil {
		return false
	}
	return strings.TrimSpace(cfg.Sync["stale_after"]) == ""
}

// healStaleAfter writes [sync] stale_after = "24h" when the key is absent
// (sty_30696eeb). An already-set value is not clobbered.
func healStaleAfter(dataDir string) (bool, error) {
	path := filepath.Join(dataDir, config.ConfigName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(cfg.Sync["stale_after"]) != "" {
		return false, nil
	}
	content := config.UpsertKey(string(raw), "sync", "stale_after", strconv.Quote("24h"))
	if content == string(raw) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
