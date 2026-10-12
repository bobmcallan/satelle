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
			rs, derived, rerr := wfgovern.RouteGovernsErr(docs, category)
			if rerr != nil {
				return rerr // an unreadable authored dir is reported, never listed as the embedded route
			}
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

// diffOp is one line of a diff: ' ' shared, '-' only in old, '+' only in new.
type diffOp struct {
	kind byte
	line string
}

// maxDiffCells bounds the LCS table; two changed regions larger than this print
// as one replaced block rather than exhausting memory.
const maxDiffCells = 16_000_000

// printUnifiedDiff writes old against new as a unified diff with three lines of
// context. Lines keep their newline, so a file differing only by its last
// newline still shows a difference.
func printUnifiedDiff(w io.Writer, oldLabel, newLabel, oldS, newS string) {
	ops := diffLines(splitKeepNewline(oldS), splitKeepNewline(newS))
	fmt.Fprintf(w, "--- %s\n+++ %s\n", oldLabel, newLabel)
	const ctx = 3
	oldAt, newAt := make([]int, len(ops)+1), make([]int, len(ops)+1)
	oldAt[0], newAt[0] = 1, 1
	for i, op := range ops {
		oldAt[i+1], newAt[i+1] = oldAt[i], newAt[i]
		if op.kind != '+' {
			oldAt[i+1]++
		}
		if op.kind != '-' {
			newAt[i+1]++
		}
	}
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start, last := i-ctx, i
		if start < 0 {
			start = 0
		}
		j := i
		for ; j < len(ops) && j-last <= 2*ctx; j++ {
			if ops[j].kind != ' ' {
				last = j
			}
		}
		end := last + ctx + 1
		if end > len(ops) {
			end = len(ops)
		}
		oc, nc := oldAt[end]-oldAt[start], newAt[end]-newAt[start]
		oStart, nStart := oldAt[start], newAt[start]
		if oc == 0 {
			oStart--
		}
		if nc == 0 {
			nStart--
		}
		fmt.Fprintf(w, "@@ -%d,%d +%d,%d @@\n", oStart, oc, nStart, nc)
		for _, op := range ops[start:end] {
			fmt.Fprintf(w, "%c%s", op.kind, op.line)
			if !strings.HasSuffix(op.line, "\n") {
				fmt.Fprint(w, "\n\\ No newline at end of file\n")
			}
		}
		i = end
	}
}

// splitKeepNewline splits s into lines that keep their trailing newline; the
// last line has none when s does not end in one.
func splitKeepNewline(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffLines is a longest-common-subsequence diff of two line lists.
func diffLines(a, b []string) []diffOp {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	var ops []diffOp
	for _, l := range a[:p] {
		ops = append(ops, diffOp{' ', l})
	}
	ops = append(ops, lcsOps(a[p:len(a)-s], b[p:len(b)-s])...)
	for _, l := range a[len(a)-s:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

func lcsOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	var ops []diffOp
	if (n+1)*(m+1) > maxDiffCells {
		for _, l := range a {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range b {
			ops = append(ops, diffOp{'+', l})
		}
		return ops
	}
	// t[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
	t := make([]int32, (n+1)*(m+1))
	at := func(i, j int) *int32 { return &t[i*(m+1)+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				*at(i, j) = *at(i+1, j+1) + 1
			case *at(i+1, j) >= *at(i, j+1):
				*at(i, j) = *at(i+1, j)
			default:
				*at(i, j) = *at(i, j+1)
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case *at(i+1, j) >= *at(i, j+1):
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
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
