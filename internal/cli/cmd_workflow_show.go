// `satelle workflow show <name>` renders ONE workflow's effective governance:
// its identity, the shape of its status graph, and — the reason this command
// exists (sty_ede16f51) — its LIFECYCLE HOOKS with the binding each one will
// actually run.
//
// Before hooks were declarable, create review resolved a skill from frontmatter
// and ran it against an empty agent selector that silently became `[reviewer]`.
// Nothing displayed that choice. This command is where an operator reads it:
// operation, skill, logical agent, whether that agent resolves through a
// machine-wide profile or a local binding, its interface, model, effort and
// permission ceiling, and the files those values came from.
//
// Read-only, and deliberately tolerant: an unresolvable binding renders an
// explicit unresolved marker rather than failing the command, because the whole
// point is to diagnose a misconfiguration (`satelle agent validate` is the
// surface that refuses one).
package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/wfhook"
)

func workflowShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show one workflow's identity, graph shape, and lifecycle-hook allocations",
		Long: `show renders a single workflow's effective governance.

Beyond the status graph it prints the workflow's LIFECYCLE HOOKS — operations
that fire outside the graph (story creation today) — with the binding each will
run: skill, logical agent, the profile or local binding it resolves through,
interface, model, effort, permission ceiling, and source files.

Read-only. An unresolvable allocation is marked UNRESOLVED rather than failing
the command; satelle agent validate is the surface that refuses one.`,
		Args:        cobra.ExactArgs(1),
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := appFrom(cmd)
			if err != nil {
				return err
			}
			doc, err := a.Store.DocIndex.Get(context.Background(), "workflows", args[0])
			if err != nil {
				return fmt.Errorf("workflow %q: %w", args[0], err)
			}
			return renderWorkflowShow(cmd.OutOrStdout(), a, doc, skillResolver(a))
		},
	}
}

// renderWorkflowShow writes the whole view. Split from the cobra wiring — and
// taking the skill resolver as a parameter rather than reaching for the store —
// so a test can drive it against a doc + agents layer with no database.
func renderWorkflowShow(out io.Writer, a *app.App, doc docindex.Doc, resolves func(string) bool) error {
	dataDir := a.DataDir
	if dataDir == "" {
		dataDir = a.Config.ResolveDataDir(a.RepoRoot)
	}

	fmt.Fprintf(out, "WORKFLOW %s\n", doc.Name)
	fmt.Fprintf(out, "  scope:      %s\n", scopeLabel(doc))
	if applies := frontmatterLine(doc.Body, "applies_to"); applies != "" {
		fmt.Fprintf(out, "  applies_to: %s\n", applies)
	}
	fmt.Fprintf(out, "  source:     %s\n", workflowSourceFile(dataDir, doc))

	if wfgovern.IsRouteSource(doc.Name) {
		// A route SOURCE carries no lifecycle of its own — its half of the grammar
		// is what there is to show, and the route itself is `satelle story route`
		// (sty_9835070d).
		fmt.Fprintln(out, "  kind:       route source (half of a derived route; not a workflow)")
		switch doc.Name {
		case wfgovern.RouteSourceDone:
			if lists, err := wfdot.ParseDone(doc.Body); err == nil {
				var cats []string
				for _, l := range lists {
					cats = append(cats, l.Category)
				}
				fmt.Fprintf(out, "  declares:   done for %s\n", strings.Join(cats, ", "))
			} else {
				fmt.Fprintf(out, "  declares:   (does not parse: %v)\n", err)
			}
		case wfgovern.RouteSourceStep:
			if cat, err := wfdot.ParseSteps(doc.Body); err == nil {
				fmt.Fprintf(out, "  declares:   %d step(s), %d always-on gate(s)\n", len(cat.Steps), len(cat.Gates))
			} else {
				fmt.Fprintf(out, "  declares:   (does not parse: %v)\n", err)
			}
		}
	} else if spec, ok := wfdot.Parse(doc.Body); ok {
		gated := 0
		for _, tr := range spec.Transitions {
			if len(tr.Skills) > 0 || tr.Skill != "" {
				gated++
			}
		}
		fmt.Fprintf(out, "  graph:      %d states, %d transitions (%d gated)\n",
			len(spec.States), len(spec.Transitions), gated)
	} else {
		fmt.Fprintln(out, "  graph:      (no parseable dot block)")
	}

	// The hook section — the reason this command exists.
	hooks, problems := wfhook.Parse(doc.Body)
	fmt.Fprintln(out, "Lifecycle hooks (fire outside the status graph):")
	if len(hooks) == 0 {
		fmt.Fprintln(out, "  (none declared — creation stays deterministic-only)")
	}
	grants, agentsFile, catalogFile := hookGrantIndex(a, dataDir)
	for _, h := range hooks {
		renderHook(out, h, grants, agentsFile, catalogFile, resolves)
	}
	for _, p := range problems {
		fmt.Fprintf(out, "  PROBLEM %s\n", p)
	}
	return nil
}

// renderHook prints one hook's full allocation. Every field the operator needs
// to answer "what will actually run, and where was it authored?" is on this
// block; nothing is left to be inferred from another command's output.
func renderHook(out io.Writer, h wfhook.Hook, grants map[string]agentvalidate.Grant, agentsFile, catalogFile string, resolves func(string) bool) {
	kind := "advisory"
	if h.Verdict {
		kind = "verdict"
	}
	fmt.Fprintf(out, "  HOOK %s (%s)\n", h.Operation, kind)
	if !wfhook.Known(h.Operation) {
		fmt.Fprintf(out, "    operation:  UNKNOWN — this binary knows: %s\n", strings.Join(wfhook.Operations(), ", "))
	}
	skillNote := ""
	if resolves != nil && !resolves(h.Skill) {
		skillNote = "  UNRESOLVED in the substrate"
	}
	fmt.Fprintf(out, "    skill:      %s%s\n", h.Skill, skillNote)
	fmt.Fprintf(out, "    agent:      %s\n", h.Describe())

	g, ok := grants[h.Agent]
	if !ok {
		fmt.Fprintf(out, "    binding:    UNRESOLVED — agents.toml declares no [%s]\n", h.Agent)
		fmt.Fprintf(out, "    sources:    %s\n", agentsFile)
		return
	}
	fmt.Fprintf(out, "    binding:    %s\n", bindingOrigin(g, h.Agent))
	fmt.Fprintf(out, "    interface:  %s (backend %s)\n", g.Interface, g.Backend)
	fmt.Fprintf(out, "    model:      %s\n", orNone(g.Model))
	fmt.Fprintf(out, "    effort:     %s\n", orNone(g.Effort))
	fmt.Fprintf(out, "    ceiling:    %s\n", ceilingLabel(g))
	fmt.Fprintf(out, "    sources:    %s\n", strings.Join(hookSourceFiles(g, agentsFile, catalogFile), ", "))
}

// bindingOrigin says whether the effective binding came from a machine-wide
// profile or is purely local, naming the profile when there is one. It reads the
// provenance the resolver already recorded rather than re-resolving anything.
func bindingOrigin(g agentvalidate.Grant, section string) string {
	profiles := map[string]bool{}
	for _, src := range g.Sources {
		for _, part := range strings.Split(src, "+") {
			if name, ok := strings.CutPrefix(part, "profile:"); ok {
				profiles[name] = true
			}
			if name, ok := strings.CutPrefix(part, "global-role:"); ok {
				profiles[name] = true
			}
		}
	}
	if len(profiles) == 0 {
		return fmt.Sprintf("local binding [%s]", section)
	}
	names := make([]string, 0, len(profiles))
	for n := range profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return fmt.Sprintf("local binding [%s] over profile %s", section, strings.Join(names, ", "))
}

// hookSourceFiles lists the files that contributed to the effective binding —
// always the repo agents layer, plus the machine-wide catalog when a profile
// supplied any field.
func hookSourceFiles(g agentvalidate.Grant, agentsFile, catalogFile string) []string {
	files := []string{agentsFile}
	for _, src := range g.Sources {
		if strings.Contains(src, "profile:") || strings.Contains(src, "global-role:") {
			return append(files, catalogFile)
		}
	}
	return files
}

// hookGrantIndex resolves the effective agents layer and indexes each binding's
// grant by section. A broken agents layer yields an empty index — every hook then
// renders UNRESOLVED, which is the diagnosis this command is for.
func hookGrantIndex(a *app.App, dataDir string) (map[string]agentvalidate.Grant, string, string) {
	agentsFile := filepath.Join(dataDir, config.AgentsConfigName)
	catalogFile := config.GlobalAgentsPath()
	index := map[string]agentvalidate.Grant{}

	repo, err := config.LoadAgents(dataDir)
	if err != nil {
		return index, agentsFile, catalogFile
	}
	global, err := config.LoadGlobalAgents()
	if err != nil {
		global = config.GlobalAgentsConfig{}
	}
	// Workflows are passed as nil: this is a DISPLAY of binding facts, not a
	// re-run of the allocation checks `agent validate` owns.
	for _, g := range agentvalidate.ValidateEffective(repo, global, a.Config.Vars, nil).Grants {
		index[g.Name] = g
	}
	return index, agentsFile, catalogFile
}

// ceilingLabel renders the permission ceiling in the terms the agent model uses.
func ceilingLabel(g agentvalidate.Grant) string {
	base := "read-write (no ceiling expressed)"
	if g.ReadOnly {
		base = "read-only"
	}
	if g.Backend == "in-loop" {
		base = "in-loop (full session grant — cannot produce an isolated verdict)"
	}
	if g.Tools != "" {
		base += fmt.Sprintf(", tools=%q", g.Tools)
	}
	return base
}

// workflowSourceFile names where the workflow body came from: the repo file when
// one exists on disk, else the embedded default.
func workflowSourceFile(dataDir string, doc docindex.Doc) string {
	path := filepath.Join(dataDir, "workflows", doc.Name+".md")
	if fileExists(path) {
		return path
	}
	return "embedded default (satelle binary)"
}

// scopeLabel reports the workflow's declared scope, defaulting to project.
func scopeLabel(doc docindex.Doc) string {
	if s := frontmatterLine(doc.Body, "scope"); s != "" {
		return s
	}
	return "project"
}

// frontmatterLine returns a top-level frontmatter key's raw value, or "".
func frontmatterLine(body, key string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return ""
		}
		if rest, ok := strings.CutPrefix(lines[i], key+":"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none — the CLI's default)"
	}
	return s
}
