// `satelle workflow list` surfaces the workflows that apply to a story category,
// in selection-priority order — the list satelle offers an agent starting a
// story. The head is the active/default workflow the engine enforces; a PROJECT
// (repo) workflow overrides the embedded SYSTEM default, and a category-specific
// workflow overrides a wildcard (applies_to ["*"]). Read-only.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/structure"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// baselineWorkflowName is the canonical order-zero default the engine falls back
// to by name. `satelle init` also seeds it onto disk as editable substrate
// (sty_bf153cbf reverses sty_3f9a6124's embedded-only stance) when no authored
// workflow already claims its category; the embedded copy still backstops the
// fallback either way. Kept in sync with the reviewer package's const.
const baselineWorkflowName = "satelle-baseline-workflow"

func init() {
	wf := &cobra.Command{Use: "workflow", Short: "Inspect workflows (read-only)"}

	var category string
	list := &cobra.Command{
		Use:   "list",
		Short: "List workflows applicable to a story category, in selection-priority order",
		Long: `list returns the workflows that apply to a story of the given category, ordered
by selection priority (highest first): a category-specific match beats a wildcard
(applies_to ["*"]), and a PROJECT (repo) workflow beats the embedded SYSTEM
default. The head of the list is the active workflow the reviewer enforces.`,
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			ctx := context.Background()
			docs, err := a.Store.DocIndex.List(ctx, "workflows")
			if err != nil {
				return err
			}
			// List enumerates only on-disk .satelle workflows (sty_94da9ac9). This is
			// a RESOLUTION query — "what governs this category" — so include the embedded
			// order-zero baseline as the fallback candidate (resolved by name via Get),
			// since it governs any category no project workflow covers. A repo file of
			// the same name already on disk wins and is not duplicated.
			if base, gerr := a.Store.DocIndex.Get(ctx, "workflows", baselineWorkflowName); gerr == nil {
				present := false
				for _, d := range docs {
					if d.Name == base.Name {
						present = true
						break
					}
				}
				if !present {
					docs = append(docs, base)
				}
			}
			ordered := agentstep.OrderedWorkflows(docs, category)
			out := make([]workflowChoice, 0, len(ordered))
			for i, d := range ordered {
				scope, applies := wfMeta(d.Body)
				out = append(out, workflowChoice{
					Name: d.Name, Headline: d.Headline, Scope: scope,
					AppliesTo: applies, Embedded: d.Embedded, Active: i == 0,
				})
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
	list.Flags().StringVar(&category, "category", "", "story category to match (empty = wildcard workflows only)")

	// format-drift is advisory (exit 0 when the workflow parses) — format lag
	// still runs; detection is for the operator / satelle-workflow-drift skill
	// (sty_2c0c8599), not a hard engage gate.
	// sty_4cebc624: also reports inert agent= on coded-check gates. Skill-body
	// resolution lives HERE (CLI edge), not in wfdot — FormatDrift stays pure
	// graph lint; InertCodedCheckAgentFindings takes a skillIsCoded predicate.
	formatDrift := &cobra.Command{
		Use:   "format-drift [name]",
		Short: "Report format drift vs the canonical latest workflow DOT form (advisory)",
		Long: `format-drift runs the deterministic format lint (wfdot.FormatDrift) against
one or every on-disk workflow, plus inert agent= on coded-check gates
(sty_4cebc624). Findings are FORMAT lag only (legacy reviewer_skill= edge gates,
prompt-less performing nodes, missing graph goal/vars/rankdir, inert coded-check
agent=) — distinct from binding-drift (workflow ↔ agents.toml). Skill bodies are
resolved at this CLI edge (wfdot stays stdlib-only). Repo-specific topology is
never reported. Exit 0 when the body parses; non-zero only if a named workflow
is missing. See the satelle-dot-standard principle and the satelle-workflow-drift skill.`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: needsStore(),
		RunE:        runWorkflowFormatDrift,
	}

	// refresh rewrites a named workflow toward the canonical format (sty_084f4879).
	// Default is dry-run (show diff); --apply writes after the operator confirms.
	var refreshApply bool
	var refreshPrompts []string
	refresh := &cobra.Command{
		Use:   "refresh <name>",
		Short: "Update a workflow DOT to the canonical latest format (consultative)",
		Long: `refresh rewrites a named workflow's fenced DOT toward the canonical latest
form (node-consistent edge gates, optional performing-node prompts via --prompt,
missing rankdir/graph attrs, strip inert agent= on coded-check gates). Topology,
on= reviewers, comments, and guardrails prose are preserved — only format lag is
patched. Never rewrites authored substrate without --apply (consultative).

Default is a DRY RUN: print a line diff and the applied/gap report; write nothing.
Pass --apply to write the file after you review the diff. Re-running on an
already-canonical workflow is a no-op. Performing-node rubrics are NEVER invented
— supply them with --prompt <node>=<skill> (repeatable).`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkflowRefresh(cmd, args[0], refreshApply, refreshPrompts)
		},
	}
	refresh.Flags().BoolVar(&refreshApply, "apply", false, "write the refreshed workflow after showing the diff (default: dry-run)")
	refresh.Flags().StringArrayVar(&refreshPrompts, "prompt", nil, "performing-node rubric mapping node=skill (repeatable)")

	wf.AddCommand(list, workflowShowCmd(), authoredCreateCmd("workflows"), authoredValidateCmd("workflows"), formatDrift, refresh)
	register(wf)
}

// runWorkflowFormatDrift implements `satelle workflow format-drift [name]`.
func runWorkflowFormatDrift(cmd *cobra.Command, args []string) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	ctx := context.Background()
	out := cmd.OutOrStdout()
	nameFilter := ""
	if len(args) > 0 {
		nameFilter = args[0]
	}

	var docs []struct {
		Name, Body string
	}
	if nameFilter != "" {
		d, gerr := a.Store.DocIndex.Get(ctx, "workflows", nameFilter)
		if gerr != nil {
			return fmt.Errorf("workflow %q: %w", nameFilter, gerr)
		}
		docs = append(docs, struct{ Name, Body string }{d.Name, d.Body})
	} else {
		listed, lerr := a.Store.DocIndex.List(ctx, "workflows")
		if lerr != nil {
			return lerr
		}
		for _, d := range listed {
			if d.Name == "README" {
				continue
			}
			docs = append(docs, struct{ Name, Body string }{d.Name, d.Body})
		}
	}

	isCoded := skillIsCodedCheck(a)
	totalFindings := 0
	for _, d := range docs {
		// FormatDriftWithSkills folds pure-graph lint + inert coded-check agent=
		// so a caller cannot get format-drift without the new finding (sty_4cebc624).
		fs, ok := wfdot.FormatDriftWithSkills(d.Body, isCoded)
		if !ok {
			fmt.Fprintf(out, "SKIP  workflows/%s — no parseable ```dot block\n", d.Name)
			continue
		}
		if len(fs) == 0 {
			fmt.Fprintf(out, "CLEAN workflows/%s — no format drift\n", d.Name)
			continue
		}
		fmt.Fprintf(out, "DRIFT workflows/%s — %d finding(s)\n", d.Name, len(fs))
		for _, f := range fs {
			fmt.Fprintf(out, "  [%s] %s — %s\n", f.Kind, f.Where, f.Detail)
		}
		totalFindings += len(fs)
	}
	fmt.Fprintf(out, "\nformat-drift: %d workflow(s), %d finding(s) (advisory)\n", len(docs), totalFindings)
	return nil
}

// skillIsCodedCheck returns a predicate that resolves a skill body from the
// docindex and reports whether it is a functional-check skill
// (structure.IsCodedCheck). Widens the existence-only skillResolver seam
// (app.go) for format-drift / refresh without importing docindex into wfdot.
func skillIsCodedCheck(a *app.App) wfdot.SkillIsCodedCheck {
	if a == nil || a.Store == nil || a.Store.DocIndex == nil {
		return nil
	}
	idx := a.Store.DocIndex
	return func(skill string) bool {
		d, err := idx.Get(context.Background(), "skills", skill)
		if err != nil {
			return false
		}
		return structure.IsCodedCheck(d.Body)
	}
}

// runWorkflowRefresh implements `satelle workflow refresh <name> [--apply] [--prompt node=skill]`.
func runWorkflowRefresh(cmd *cobra.Command, name string, apply bool, promptFlags []string) error {
	a, err := appFrom(cmd)
	if err != nil {
		return err
	}
	ctx := context.Background()
	out := cmd.OutOrStdout()

	d, err := a.Store.DocIndex.Get(ctx, "workflows", name)
	if err != nil {
		return fmt.Errorf("workflow %q: %w", name, err)
	}
	prompts := map[string]string{}
	for _, p := range promptFlags {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" || v == "" {
			return fmt.Errorf("--prompt wants node=skill, got %q", p)
		}
		prompts[k] = v
	}

	newBody, changed, report := wfdot.Refresh(d.Body, prompts, skillIsCodedCheck(a))
	if !changed {
		fmt.Fprintf(out, "workflows/%s — already canonical, no changes\n", name)
		for _, g := range report.Gaps {
			fmt.Fprintf(out, "  GAP  [%s] %s — %s\n", g.Kind, g.Where, g.Detail)
		}
		return nil
	}

	fmt.Fprintf(out, "=== proposed refresh: workflows/%s ===\n", name)
	printLineDiff(out, d.Body, newBody)
	fmt.Fprintln(out, "\nApplied:")
	for _, f := range report.Applied {
		fmt.Fprintf(out, "  + [%s] %s — %s\n", f.Kind, f.Where, f.Detail)
	}
	if len(report.Gaps) > 0 {
		fmt.Fprintln(out, "Remaining gaps (pass --prompt node=skill):")
		for _, g := range report.Gaps {
			fmt.Fprintf(out, "  ? [%s] %s — %s\n", g.Kind, g.Where, g.Detail)
		}
	}

	if !apply {
		fmt.Fprintln(out, "\nDry-run only — re-run with --apply to write after you confirm the diff.")
		return nil
	}

	// Write in place (path from the indexed doc).
	path := d.Path
	if path == "" {
		path = filepath.Join(a.DataDir, "workflows", name+".md")
	}
	if err := os.WriteFile(path, []byte(newBody), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// Reindex so the store sees the new body.
	if _, err := a.Store.DocIndex.Sync(ctx, map[string]string{
		"workflows": filepath.Dir(path),
	}, time.Now()); err != nil {
		fmt.Fprintf(out, "wrote %s (reindex warning: %v — run satelle reindex)\n", path, err)
		return nil
	}
	fmt.Fprintf(out, "\nwrote %s\n", path)
	return nil
}

// printLineDiff writes a minimal line-oriented diff (no LCS): lines only in old
// as '-', only in new as '+', shared context omitted for brevity when long.
func printLineDiff(w io.Writer, oldS, newS string) {
	oldLines := strings.Split(oldS, "\n")
	newLines := strings.Split(newS, "\n")
	// Index multiset of new lines for a cheap presence check.
	newCount := map[string]int{}
	for _, ln := range newLines {
		newCount[ln]++
	}
	oldCount := map[string]int{}
	for _, ln := range oldLines {
		oldCount[ln]++
	}
	for _, ln := range oldLines {
		if newCount[ln] > 0 {
			newCount[ln]--
			continue
		}
		fmt.Fprintf(w, "- %s\n", ln)
	}
	for _, ln := range newLines {
		if oldCount[ln] > 0 {
			oldCount[ln]--
			continue
		}
		fmt.Fprintf(w, "+ %s\n", ln)
	}
}

// workflowChoice is one entry in the ordered list satelle offers the agent.
type workflowChoice struct {
	Name      string   `json:"name"`
	Headline  string   `json:"headline,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	AppliesTo []string `json:"applies_to,omitempty"`
	Embedded  bool     `json:"embedded"`
	Active    bool     `json:"active"` // the head — the workflow the engine enforces
}

// wfMeta parses a workflow's scope (scalar) and applies_to (list) from its
// frontmatter, reusing the package's frontmatter helper.
func wfMeta(body string) (scope string, appliesTo []string) {
	return frontmatterScope(body), frontmatterListValue(body, "applies_to")
}

// frontmatterScope returns the frontmatter `scope:` scalar, or "".
func frontmatterScope(body string) string {
	for _, ln := range strings.Split(frontmatter(body), "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "scope:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "scope:")), `"'`)
		}
	}
	return ""
}

// frontmatterListValue returns a list-valued frontmatter key (inline `[a, b]` or
// a block `- a` list), reusing frontmatterTags' parsing for the tags case.
func frontmatterListValue(body, key string) []string {
	if key == "tags" {
		return frontmatterTags(body)
	}
	lines := strings.Split(frontmatter(body), "\n")
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, key+":") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, key+":"))
		if strings.HasPrefix(rest, "[") {
			return splitTrimTags(strings.TrimSuffix(strings.TrimPrefix(rest, "["), "]"))
		}
		var out []string
		for j := i + 1; j < len(lines); j++ {
			l2 := strings.TrimSpace(lines[j])
			if l2 == "" {
				continue
			}
			if strings.HasPrefix(l2, "- ") {
				out = append(out, strings.Trim(strings.TrimSpace(l2[2:]), `"'`))
				continue
			}
			break
		}
		return out
	}
	return nil
}
