// `satelle substrate` — effective process view and virtual-defaults tooling
// (sty_29e5a9a5 / epic:substrate-planes). Defaults resolve from the binary;
// on-disk files are overrides. list --effective shows provenance; edit
// materializes a default for day-one editing; prune removes unedited seeds.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/reviewscore"
	"github.com/bobmcallan/satelle/internal/substrate"
	"github.com/bobmcallan/satelle/internal/substratereview"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/wfroute"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func init() {
	root := &cobra.Command{
		Use:   "substrate",
		Short: "Effective process view and virtual-defaults tooling",
		Long: `Substrate is the authored process (workflows, skills, principles) layered
over the binary's sparse embedded defaults.

  satelle substrate list --effective   provenance default|edited|authored
  satelle substrate edit <kind> <name> materialize a default for editing
  satelle substrate prune              remove unedited seed copies (dry-run default)
  satelle substrate condense           the rules a resident document is condensed by
  satelle substrate review             judge every item against its history and the constitution
  satelle substrate review --apply <item>...  raise a substrate story for each cut you choose

See decision-substrate-planes-local-first.`,
	}

	var asJSON bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List effective substrate with provenance",
		Long: `List the substrate this repo actually runs, each item tagged with where it came
from: default (embedded), edited (a materialised copy you changed), or authored
(yours alone).

The provenance is the reason to run it. Overriding is per FILE, so an embedded
default stays live until a repo-authored file of the same name exists — this is
what shows you which half of the tree is really yours.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			// Always effective (the only meaningful list under virtual defaults).
			return runSubstrateList(cmd.OutOrStdout(), a.Store.DocIndex, asJSON)
		},
	}
	listCmd.Flags().Bool("effective", true, "show effective process (default; always on)")
	listCmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")

	editCmd := &cobra.Command{
		Use:   "edit [kind] [name]",
		Short: "Materialize an embedded default onto disk for editing",
		Long: `Copy an embedded default onto disk under .satelle/ so you can edit it.

The copy takes over from that moment: the file OVERRIDES the embedded default by
name, and it no longer tracks improvements shipped in later binaries. Materialise
what you actually intend to diverge on — satelle substrate prune removes copies
that were never edited.`,
		Args:        cobra.ExactArgs(2),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			return runSubstrateEdit(cmd.OutOrStdout(), a.DataDir, a.Store.DocIndex, args[0], args[1])
		},
	}

	var yes bool
	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove unedited seed copies of embedded defaults (dry-run unless --yes)",
		Long: `Remove on-disk copies of embedded defaults that were never edited, so the repo
tracks the shipped version again.

Dry-run unless --yes: it reports what it would remove first. Only byte-identical
copies go — anything you changed is authored substrate and is left alone, which
is why this cannot quietly discard your work.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			opts := ResolveBackupOpts(a.Config, a.RepoRoot)
			opts.BackupsDir = a.RuntimeDir
			// Resolve the seat off the app handle this command already opened —
			// currentSeat() would app.Open() a second handle on the same DB.
			storyID, state := pruneSeat(cmd.Context(), a)
			return runSubstratePrune(cmd.Context(), cmd.OutOrStdout(), cmd.InOrStdin(),
				a.DataDir, opts, yes, storyID, state)
		},
	}
	pruneCmd.Flags().BoolVar(&yes, "yes", false, "actually remove files (default is dry-run)")

	condenseCmd := &cobra.Command{
		Use:   "condense",
		Short: "Print the rules a resident document is condensed by (embedded defaults + repo override)",
		Long: `Print the effective condense rules as TOML: the binary's embedded defaults with
<data dir>/config/condense.toml laid over them, key by key.

These rules decide how a resident document is fitted when its context has a size
budget: the ladder, the filler dropped, the tokens never edited, where a clause
or a sentence ends. Read-only; the authored documents are never changed.`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			rules, rerr := condenseRules(a.DataDir)
			if rerr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "satelle: %s does not parse, showing the embedded rules: %v\n", config.CondenseOverrideRel, rerr)
			}
			return toml.NewEncoder(cmd.OutOrStdout()).Encode(rules)
		},
	}

	root.AddCommand(listCmd, editCmd, pruneCmd, condenseCmd, newSubstrateReviewCmd())
	register(root)
}

func runSubstrateList(out io.Writer, idx *docindex.Store, asJSON bool) error {
	rows, err := substrate.List(context.Background(), idx)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	fmt.Fprintf(out, "%-12s %-40s %-10s %s\n", "KIND", "NAME", "PROV", "SOURCE")
	for _, r := range rows {
		fmt.Fprintf(out, "%-12s %-40s %-10s %s\n", r.Kind, r.Name, r.Provenance, r.Source)
	}
	fmt.Fprintf(out, "\n%d effective item(s)\n", len(rows))
	return nil
}

// isUneditedSeed delegates to substrate (shared with prune and provenance).
func isUneditedSeed(diskBody, embeddedBody string) bool {
	return substrate.IsUneditedSeed(diskBody, embeddedBody)
}

func runSubstrateEdit(out io.Writer, dataDir string, idx *docindex.Store, kind, name string) error {
	kind = strings.TrimSpace(kind)
	name = strings.TrimSpace(name)
	if kind == "" || name == "" {
		return fmt.Errorf("substrate edit: kind and name required")
	}
	rel := kind + "/" + name + config.EmbeddedExt(kind, name)
	path := filepath.Join(dataDir, filepath.FromSlash(rel))
	if fileExists(path) {
		fmt.Fprintln(out, path)
		return nil
	}
	// Resolve embedded body.
	var body string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == kind && d.Name == name {
			body = d.Body
			break
		}
	}
	if body == "" {
		// Try docindex Get (may be already virtual).
		if doc, err := idx.Get(context.Background(), kind, name); err == nil {
			body = doc.Body
		}
	}
	if body == "" {
		return fmt.Errorf("substrate edit: no embedded default %s/%s", kind, name)
	}
	stamped := applyEmbeddedStamp(body, substrate.SHA(body))
	if err := writeEmbedded(path, stamped); err != nil {
		return err
	}
	fmt.Fprintln(out, path)
	return nil
}

// recordPruneChange is the seam prune records removals through (injectable so
// the CLI test can observe the call without a live ledger).
var recordPruneChange = verb.RecordPathMutation

// pruneSeat reports the engaged story and the status it is engaged at, using the
// already-open app handle. Fail-open: no seat, or an unreadable one, means prune
// records nothing — pruning outside a story is legitimate.
func pruneSeat(ctx context.Context, a *app.App) (storyID, state string) {
	if a.Store == nil || a.Store.Leases == nil {
		return "", ""
	}
	wfs, err := a.Store.DocIndex.List(ctx, "workflows")
	if err != nil {
		return "", ""
	}
	items, err := a.Store.Stories.List(ctx, workitem.ListFilter{})
	if err != nil {
		return "", ""
	}
	leases, err := a.Store.Leases.List(ctx)
	if err != nil {
		return "", ""
	}
	live, _, err := evaluateSeat(leases, items, wfs, time.Now().UTC())
	if err != nil || len(live) == 0 {
		return "", ""
	}
	seat, _ := pickSessionSeat(live, nil, config.ResolveSession())
	return seat.ItemID, seat.StoryStatus
}

func runSubstratePrune(ctx context.Context, out io.Writer, in io.Reader, dataDir string, opts BackupOpts, yes bool, storyID, storyState string) error {
	plan := config.UneditedSeedPaths(dataDir) // rel paths to remove, tasks included
	if len(plan) == 0 {
		fmt.Fprintln(out, "prune: no unedited seed copies found")
		return nil
	}
	fmt.Fprintf(out, "prune would remove %d unedited seed(s):\n", len(plan))
	for _, rel := range plan {
		fmt.Fprintf(out, "  - %s\n", rel)
	}
	if !yes {
		fmt.Fprintln(out, "dry-run only — re-run with --yes to remove (edited/authored files are never touched)")
		return nil
	}
	var removed []string
	for _, rel := range plan {
		path := filepath.Join(dataDir, filepath.FromSlash(rel))
		body, _ := os.ReadFile(path)
		if _, err := backupExistingFile(dataDir, BackupKindPreMutation, rel, body, opts); err != nil {
			return fmt.Errorf("prune: backup %s: %w", rel, err)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("prune: remove %s: %w", rel, err)
		}
		removed = append(removed, path)
		fmt.Fprintf(out, "  removed %s\n", rel)
	}
	// These deletions are invisible to every re-derived evidence channel — git
	// cannot see an ignored path and the mtime walk cannot see a file that is
	// gone — so record them here or the engaged story has no evidence of the
	// slice (sty_30d3bd99). Best-effort: never lose the operator their prune.
	if storyID != "" && len(removed) > 0 {
		if err := recordPruneChange(ctx, storyID, removed, storyState, storyState, time.Now().UTC()); err != nil {
			fmt.Fprintf(out, "prune: warning: could not record the removals against %s: %v\n", storyID, err)
		}
	}
	fmt.Fprintf(out, "prune: removed %d seed(s); effective process is unchanged (virtual defaults)\n", len(plan))
	return nil
}

// substrateReviewSkill is the embedded rubric the review's judge runs under.
const substrateReviewSkill = "satelle-substrate-review"

// substrateJudge judges a review's input: one dispatch per run.
type substrateJudge func(ctx context.Context, in substratereview.JudgeInput) (substratereview.Judgement, error)

// newSubstrateJudge builds the judge `substrate review` runs a binding through.
// It is a seam so tests judge with a stub and never start a model; the default
// goes through newCaseJudge — the isolated reviewer path a real gate uses —
// under the satelle-substrate-review rubric, with the input as a document.
var newSubstrateJudge = func(cmd *cobra.Command, binding string) (substrateJudge, error) {
	a, err := appFrom(cmd)
	if err != nil {
		return nil, err
	}
	j, err := newCaseJudge(a, binding, a.Config.Validate.Injected)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, in substratereview.JudgeInput) (substratereview.Judgement, error) {
		b, err := json.MarshalIndent(in, "", "  ")
		if err != nil {
			return substratereview.Judgement{}, err
		}
		res, err := j.Judge(ctx, reviewscore.Case{
			ID: "substrate-review", Skill: substrateReviewSkill, StoryID: "substrate-review",
			From: "backlog", To: "plan",
			Payload: &reviewscore.ReplayPayload{
				Title:    "Review this repo's substrate",
				Body:     "Judge every item and every constitution rule in the substrate-review-input document under the satelle-substrate-review rubric.",
				Category: substratereview.ApplyCategory, NoPatch: true,
				Docs: []reviewscore.ReplayDoc{{Name: "substrate-review-input", Type: "document", Body: string(b)}},
			},
		})
		if err != nil {
			return substratereview.Judgement{}, err
		}
		if res.Verdict != reviewscore.VerdictAccept {
			return substratereview.Judgement{}, fmt.Errorf("the reviewer rejected the input: %s", strings.TrimSpace(res.Notes))
		}
		return substratereview.ParseJudgement(res.Notes)
	}, nil
}

func newSubstrateReviewCmd() *cobra.Command {
	var asJSON bool
	var binding, out, from string
	var apply []string
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Judge every substrate item against this repo's story history and the constitution; --apply raises the cuts you choose",
		Long: `Report every principle, skill, workflow step and gate, and agent seat with
its origin and the evidence this repo's story history holds for it (rejections
and their reasons, amendments, parks, rework, flaky reruns, panel dissent, per
model), plus every constitution rule. One reviewer dispatch (--binding) gives
each item a disposition and each rule a finding; anything it omits reads "not
judged". Changes no file in the tree: the JSON report goes to --out or the OS
temp dir.

--apply <item> raises one category-substrate story per chosen item and edits
nothing; embedded and team items are refused. --from <report.json> applies a
saved report without judging again. See satelle help substrate.`,
		Args:        cobra.NoArgs,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			var rep substratereview.Report
			if from != "" {
				if len(apply) == 0 {
					return fmt.Errorf("--from applies a saved report: give --apply <item>")
				}
				if rep, err = substratereview.ReadReport(from); err != nil {
					return err
				}
			} else {
				if rep, err = runSubstrateReview(cmd, a, binding); err != nil {
					return err
				}
				path, err := substrateReportPath(a.RepoRoot, out)
				if err != nil {
					return err
				}
				if err := rep.WriteJSON(path); err != nil {
					return err
				}
				if asJSON {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					if err := enc.Encode(rep); err != nil {
						return err
					}
				} else {
					rep.Render(cmd.OutOrStdout())
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "report: %s\n", path)
			}
			if len(apply) == 0 {
				return nil
			}
			return applySubstrateCuts(cmd.Context(), cmd.OutOrStdout(), rep, apply)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON instead of markdown")
	cmd.Flags().StringVar(&binding, "binding", "reviewer", "agents.toml section that judges")
	cmd.Flags().StringVar(&out, "out", "", "JSON report path, outside the repo tree (default: a file under the OS temp dir)")
	cmd.Flags().StringArrayVar(&apply, "apply", nil, "item key from the report to raise a substrate story for (repeatable)")
	cmd.Flags().StringVar(&from, "from", "", "apply from this saved JSON report instead of judging again")
	return cmd
}

// substrateReportPath is where the JSON report goes: --out, which must lie
// outside the repo tree, else a timestamped file under the OS temp dir.
func substrateReportPath(repoRoot, out string) (string, error) {
	if out == "" {
		return filepath.Join(os.TempDir(), fmt.Sprintf("satelle-substrate-review-%s.json", time.Now().UTC().Format("20060102T150405Z"))), nil
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}
	if root, err := filepath.Abs(repoRoot); err == nil {
		if rel, err := filepath.Rel(root, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("--out %s is inside the repo tree: substrate review changes no file in the tree — write the report outside it", out)
		}
	}
	return abs, nil
}

// runSubstrateReview gathers the inventory, its evidence and the rules, makes
// the one judge dispatch, and joins the three into the report.
func runSubstrateReview(cmd *cobra.Command, a *app.App, binding string) (substratereview.Report, error) {
	in, bound, err := gatherSubstrateReview(cmd.Context(), a)
	if err != nil {
		return substratereview.Report{}, err
	}
	judge, err := newSubstrateJudge(cmd, binding)
	if err != nil {
		return substratereview.Report{}, err
	}
	j, err := judge(cmd.Context(), in)
	if err != nil {
		return substratereview.Report{}, fmt.Errorf("substrate review: the %s seat returned no judgement: %w", binding, err)
	}
	rep := substratereview.Build(in, j)
	rep.GeneratedAt, rep.Binding, rep.SeatModels = time.Now().UTC(), binding, bound
	return rep, nil
}

// gatherSubstrateReview resolves everything the judge receives from the repo:
// the effective documents and their origins, the derived routes, the agents
// layer, every story's ledger rows, the constitution and the session set.
func gatherSubstrateReview(ctx context.Context, a *app.App) (substratereview.JudgeInput, map[string]string, error) {
	idx := a.Store.DocIndex
	rows, err := substrate.List(ctx, idx)
	if err != nil {
		return substratereview.JudgeInput{}, nil, err
	}
	wfs, err := idx.List(ctx, "workflows")
	if err != nil {
		return substratereview.JudgeInput{}, nil, err
	}
	var routes []wfroute.Route
	if rs := wfgovern.RouteSourceOf(wfs); rs.Present() {
		for _, cat := range wfgovern.RouteCategories(rs.Done) {
			d, err := wfgovern.RouteSpecFor(rs, cat, nil)
			if err != nil {
				return substratereview.JudgeInput{}, nil, err
			}
			routes = append(routes, wfroute.Build(d.Spec, wfgovern.DerivedRouteName, nil, d.Advisors, d.Reworks))
		}
	}
	eff, err := requireAgents(a)
	if err != nil {
		return substratereview.JudgeInput{}, nil, err
	}
	names := []string{"executor", "reviewer"}
	for n := range eff.Agents.Agents {
		names = append(names, n)
	}
	sort.Strings(names[2:])
	bound := map[string]string{}
	var agents []substratereview.Agent
	for _, n := range names {
		b, _ := eff.Agents.RawBinding(n)
		var srcs []string
		for _, f := range eff.Provenance.Fields(n) {
			srcs = append(srcs, eff.Provenance.Source(n, f))
		}
		bound[n] = b.Model
		agents = append(agents, substratereview.Agent{Name: n, Model: b.Model, Source: strings.Join(srcs, "+")})
	}
	_, agentsTeam := config.ProjectTierAgentsPath(a.PlaneDir())
	items := substratereview.Inventory(substratereview.InventoryInput{
		Docs: rows, TierRoot: a.AuthoredDirs().Tier, Workflow: wfgovern.DerivedRouteName,
		Routes: routes, AgentsTeam: agentsTeam, Agents: agents,
	})

	entries := map[string][]ledger.Entry{}
	ids, err := a.Store.Ledger.StoryIDs(ctx)
	if err != nil {
		return substratereview.JudgeInput{}, nil, err
	}
	for _, id := range ids {
		es, err := a.Store.Ledger.ListByStory(ctx, id, "")
		if err != nil {
			return substratereview.JudgeInput{}, nil, err
		}
		entries[id] = es
	}
	hist := substratereview.Fold(entries, substratereview.EvidenceOptions{ParkStates: substratereview.ParkStates(routes), Bound: bound})

	var session []string
	for _, r := range rows {
		if r.Kind != "principles" {
			continue
		}
		if d, err := idx.Get(ctx, "principles", r.Name); err == nil && docHasTag(d.Body, sessionTag) {
			session = append(session, r.Name)
		}
	}
	constPath := a.PlaneConstitution()
	constitution, _ := os.ReadFile(constPath)

	in := substratereview.JudgeInput{
		Rules:             substratereview.Rules(string(constitution), session),
		ConstitutionPath:  constPath,
		OrchestratorModel: hist.Orchestrator,
	}
	for _, it := range items {
		in.Items = append(in.Items, substratereview.JudgeItem{Item: it, Evidence: hist.For(it.Selector, it.Seats)})
	}
	return in, bound, nil
}

// applySubstrateCuts raises one substrate story per chosen item through the
// same create verb `satelle story create` uses, refusing (and raising nothing
// for) an item the report does not hold or one this repo cannot cut. It edits
// no file. Any refusal makes the run fail after the rest are raised.
func applySubstrateCuts(ctx context.Context, out io.Writer, rep substratereview.Report, keys []string) error {
	var refused []string
	for _, key := range keys {
		it, ok := rep.Find(key)
		if !ok {
			refused = append(refused, fmt.Sprintf("%s: not an item of this report — `satelle substrate review` lists the item keys", key))
			continue
		}
		if why := substratereview.Refusal(it); why != "" {
			refused = append(refused, why)
			continue
		}
		title, body, criteria := substratereview.StoryFor(it)
		req, _ := json.Marshal(map[string]any{
			"title": title, "body": body, "acceptance_criteria": criteria,
			"category": substratereview.ApplyCategory, "tags": []string{substratereview.ApplyTag},
		})
		resp, err := verb.Dispatch(ctx, "story-create", req)
		if err != nil {
			return fmt.Errorf("apply %s: %w", key, err)
		}
		var created workitem.Item
		_ = json.Unmarshal(resp, &created)
		fmt.Fprintf(out, "raised %s for %s (%s, origin %s)\n", created.ID, key, it.Disposition, it.Origin)
	}
	for _, r := range refused {
		fmt.Fprintf(out, "refused %s\n", r)
	}
	if len(refused) > 0 {
		return fmt.Errorf("substrate review --apply: %d item(s) refused", len(refused))
	}
	return nil
}
