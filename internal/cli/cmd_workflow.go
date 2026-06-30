// `satelle workflow list` surfaces the workflows that apply to a story category,
// in selection-priority order — the list satelle offers an agent starting a
// story. The head is the active/default workflow the gater enforces; a PROJECT
// (repo) workflow overrides the embedded SYSTEM default, and a category-specific
// workflow overrides a wildcard (applies_to ["*"]). Read-only.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/reviewer"
)

// baselineWorkflowName is the canonical order-zero default the gater falls back to
// by name (it is embedded-only — never an editable repo file, sty_3f9a6124). Kept
// in sync with the reviewer package's const.
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
			ordered := reviewer.OrderedWorkflows(docs, category)
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

	wf.AddCommand(list, authoredCreateCmd("workflows"))
	register(wf)
}

// workflowChoice is one entry in the ordered list satelle offers the agent.
type workflowChoice struct {
	Name      string   `json:"name"`
	Headline  string   `json:"headline,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	AppliesTo []string `json:"applies_to,omitempty"`
	Embedded  bool     `json:"embedded"`
	Active    bool     `json:"active"` // the head — the workflow the gater enforces
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
