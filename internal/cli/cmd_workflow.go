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
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/wfgovern"
)

func init() {
	wf := &cobra.Command{Use: "workflow", Short: "Inspect workflows (read-only)",
		Long: `Inspect the lifecycle this repo runs: which workflow governs a category, what a
route derives to, and whether the authored halves are valid.

Read-only — a lifecycle is authored markdown and TOML under .satelle/workflows,
so this never edits it. For one story's actual route and its verdicts, use
satelle story route.`}

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
			out := make([]workflowChoice, 0, len(docs))
			// A DERIVED route that GOVERNS heads the list and is the ACTIVE choice
			// (sty_9835070d). The predicate is the front door's, not a second copy of
			// it: an authored route beats every graph, the shipped route is order zero
			// and yields to an authored graph (sty_3795e7f6).
			rs, derived := wfgovern.RouteGoverns(docs, category)
			if derived {
				out = append(out, workflowChoice{
					Name:      wfgovern.DerivedRouteName,
					Headline:  "derived route — done.toml + step.toml",
					Scope:     "project",
					AppliesTo: wfgovern.RouteCategories(rs.Done),
					Active:    true,
				})
			}
			for i, d := range agentstep.OrderedWorkflows(wfgovern.LifecycleWorkflows(docs), category) {
				scope, applies := wfMeta(d.Body)
				out = append(out, workflowChoice{
					Name: d.Name, Headline: d.Headline, Scope: scope,
					AppliesTo: applies, Embedded: d.Embedded, Active: !derived && i == 0,
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

	wf.AddCommand(list, workflowShowCmd(), authoredCreateCmd("workflows"), authoredValidateCmd("workflows"))
	register(wf)
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
