package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// workflowRowVM is one workflow in the Workflow panel list — the row the user
// filters and clicks to expand. Mirrors the stories/tasks row shape so the same
// filter + expand/collapse interactions apply (the tab is read-only).
type workflowRowVM struct {
	Name      string
	Headline  string
	Scope     string
	AppliesTo []string
	Updated   time.Time
}

// wfState is a workflow state node. Terminal is true when no transition leaves it.
type wfState struct {
	Name     string
	Agent    string
	Terminal bool
}

// wfTransition is a directed edge between states; Skill is its reviewer gate
// (empty = ungated/advisory).
type wfTransition struct {
	From  string
	To    string
	Skill string
}

// wfSpec is the parsed lifecycle of a workflow: its states and gated transitions.
type wfSpec struct {
	States      []wfState
	Transitions []wfTransition
}

// workflowDetailVM backs the inline expand: the parsed diagram + the applies_to
// binding + the raw definition (frontmatter stripped) for the read-only view.
type workflowDetailVM struct {
	Name      string
	Headline  string
	Scope     string
	AppliesTo []string
	Spec      wfSpec
	Diagram   template.HTML
	Body      string
}

// spineOrder orders the states into a readable top-to-bottom flow: it starts at
// the state nothing transitions into and walks forward, preferring GATED edges
// (the main path is gated; blocked/cancelled side-exits are ungated), then
// appends any states the walk did not reach (branches) in declared order.
func spineOrder(spec wfSpec) []string {
	type edge struct {
		to    string
		gated bool
	}
	adj := map[string][]edge{}
	indeg := map[string]int{}
	var names []string
	seen := map[string]bool{}
	for _, s := range spec.States {
		if !seen[s.Name] {
			seen[s.Name] = true
			names = append(names, s.Name)
			indeg[s.Name] = 0
		}
	}
	for _, tr := range spec.Transitions {
		if tr.From != tr.To {
			adj[tr.From] = append(adj[tr.From], edge{tr.To, tr.Skill != ""})
			indeg[tr.To]++
		}
	}
	if len(names) == 0 {
		return nil
	}
	start := names[0]
	for _, n := range names {
		if indeg[n] == 0 {
			start = n
			break
		}
	}
	visited := map[string]bool{}
	var order []string
	for cur := start; cur != "" && !visited[cur]; {
		visited[cur] = true
		order = append(order, cur)
		next := ""
		for _, e := range adj[cur] { // prefer a gated forward edge
			if e.gated && !visited[e.to] {
				next = e.to
				break
			}
		}
		if next == "" {
			for _, e := range adj[cur] {
				if !visited[e.to] {
					next = e.to
					break
				}
			}
		}
		cur = next
	}
	for _, n := range names {
		if !visited[n] {
			order = append(order, n)
		}
	}
	return order
}

// shortSkill abbreviates a reviewer skill for an edge label (the full name rides
// in a tooltip): satelle-story-intent-review → story-intent.
func shortSkill(s string) string {
	s = strings.TrimPrefix(s, "satelle-")
	s = strings.TrimSuffix(s, "-review")
	return s
}

// workflowDiagram renders the parsed lifecycle as a dependency-free SVG flow
// diagram: states are nodes stacked top-to-bottom, transitions are directed
// edges curving down the right with their gate labelled. No mermaid — the SVG is
// generated here from parseWorkflow's output.
func workflowDiagram(spec wfSpec) template.HTML {
	order := spineOrder(spec)
	if len(order) == 0 {
		return ""
	}
	idx := map[string]int{}
	for i, n := range order {
		idx[n] = i
	}
	terminal := map[string]bool{}
	for _, s := range spec.States {
		terminal[s.Name] = s.Terminal
	}
	const (
		nodeW, nodeH = 150, 32
		gapY         = 58
		topPad       = 14
		leftX        = 14
	)
	height := topPad*2 + (len(order)-1)*gapY + nodeH
	width := leftX + nodeW + 260
	cy := func(i int) int { return topPad + i*gapY + nodeH/2 }
	rx := leftX + nodeW

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="wf-diagram" viewBox="0 0 %d %d" preserveAspectRatio="xMinYMin meet" role="img" aria-label="workflow flow diagram">`, width, height)
	b.WriteString(`<defs><marker id="wf-arrow" markerWidth="9" markerHeight="9" refX="7" refY="3" orient="auto"><path d="M0,0 L7,3 L0,6 Z" class="wf-arrowhead"/></marker></defs>`)
	// edges (under the nodes)
	for _, tr := range spec.Transitions {
		si, ok1 := idx[tr.From]
		ti, ok2 := idx[tr.To]
		if !ok1 || !ok2 || si == ti {
			continue
		}
		y1, y2 := cy(si), cy(ti)
		span := ti - si
		if span < 0 {
			span = -span
		}
		bulge := 26 + span*20
		cx := rx + bulge
		cls := "wf-edge-path"
		if tr.Skill == "" {
			cls += " ungated"
		}
		fmt.Fprintf(&b, `<path class="%s" d="M%d,%d C%d,%d %d,%d %d,%d" marker-end="url(#wf-arrow)"/>`,
			cls, rx, y1, cx, y1, cx, y2, rx+5, y2)
		if lbl := shortSkill(tr.Skill); lbl != "" {
			fmt.Fprintf(&b, `<text class="wf-edge-label" x="%d" y="%d"><title>%s</title>%s</text>`,
				cx+4, (y1+y2)/2, template.HTMLEscapeString(tr.Skill), template.HTMLEscapeString(lbl))
		}
	}
	// nodes
	for i, n := range order {
		y := topPad + i*gapY
		cls := "wf-dnode"
		if terminal[n] {
			cls += " terminal"
		}
		fmt.Fprintf(&b, `<g class="%s"><rect x="%d" y="%d" width="%d" height="%d" rx="7"/><text x="%d" y="%d">%s</text></g>`,
			cls, leftX, y, nodeW, nodeH, leftX+nodeW/2, y+nodeH/2+4, template.HTMLEscapeString(n))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// workflowRows builds the Workflow panel rows from the indexed workflow docs.
func workflowRows(docs []docindex.Doc) []workflowRowVM {
	out := make([]workflowRowVM, 0, len(docs))
	for _, d := range docs {
		out = append(out, workflowRowVM{
			Name:      d.Name,
			Headline:  d.Headline,
			Scope:     workflowScope(d),
			AppliesTo: frontmatterList(d.Body, "applies_to"),
			Updated:   d.ModTime,
		})
	}
	return out
}

// workflowFragment renders one workflow's diagram inline (the expand target).
func workflowFragment() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		doc, err := fetchOne[docindex.Doc](r.Context(), "doc-get", map[string]any{"kind": "workflows", "name": name})
		if err != nil || doc.Name == "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		spec := parseWorkflow(doc.Body)
		render(w, "workflowDetail", workflowDetailVM{
			Name:      doc.Name,
			Headline:  doc.Headline,
			Scope:     workflowScope(doc),
			AppliesTo: frontmatterList(doc.Body, "applies_to"),
			Spec:      spec,
			Diagram:   workflowDiagram(spec),
			Body:      strings.TrimSpace(stripDocFrontmatter(doc.Body)),
		})
	}
}

// parseWorkflow extracts the states and transitions from a workflow markdown
// body. States come from the `states:` YAML block (a bare name or an inline
// `{name:…, actor:…}` map); transitions are every `- {from:…, to:…}` line
// anywhere in the body (so the guardrails block is ignored). Terminal states are
// those no transition leaves. When there is no states block, states are derived
// from the transition endpoints.
func parseWorkflow(body string) wfSpec {
	if spec, ok := parseWorkflowDOT(body); ok {
		return spec
	}
	lines := strings.Split(body, "\n")
	var spec wfSpec

	for i, raw := range lines {
		if strings.TrimSpace(raw) != "states:" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" {
				continue
			}
			if !strings.HasPrefix(t, "- ") {
				break // end of the states block (e.g. `transitions:`)
			}
			spec.States = append(spec.States, parseState(strings.TrimSpace(t[2:])))
		}
		break
	}

	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if !strings.HasPrefix(t, "- {") || !strings.Contains(t, "from:") || !strings.Contains(t, "to:") {
			continue
		}
		spec.Transitions = append(spec.Transitions, wfTransition{
			From:  inlineField(t, "from"),
			To:    inlineField(t, "to"),
			Skill: inlineField(t, "reviewer_skill"),
		})
	}

	if len(spec.States) == 0 {
		seen := map[string]bool{}
		add := func(n string) {
			if n != "" && !seen[n] {
				seen[n] = true
				spec.States = append(spec.States, wfState{Name: n})
			}
		}
		for _, tr := range spec.Transitions {
			add(tr.From)
			add(tr.To)
		}
	}

	froms := map[string]bool{}
	for _, tr := range spec.Transitions {
		froms[tr.From] = true
	}
	for i := range spec.States {
		spec.States[i].Terminal = !froms[spec.States[i].Name]
	}
	return spec
}

// parseState parses one `states:` list item — a bare name or
// `{name:…, agent:…}` (the legacy `actor:` spelling is still accepted, with
// `agent` preferred when both are present — sty_536f9960).
func parseState(item string) wfState {
	if strings.HasPrefix(item, "{") {
		performer := inlineField(item, "agent")
		if performer == "" {
			performer = inlineField(item, "actor")
		}
		return wfState{Name: inlineField(item, "name"), Agent: performer}
	}
	return wfState{Name: strings.Trim(item, `"'`)}
}

// parseWorkflowDOT parses a fenced ```dot block via the shared wfdot package and
// adapts it to the web diagram's wfSpec. ok is false when there is no dot block,
// so parseWorkflow falls back to the inline-YAML grammar.
func parseWorkflowDOT(body string) (wfSpec, bool) {
	s, ok := wfdot.Parse(body)
	if !ok {
		return wfSpec{}, false
	}
	spec := wfSpec{}
	for _, st := range s.States {
		spec.States = append(spec.States, wfState{Name: st.Name, Agent: st.Agent, Terminal: st.Terminal})
	}
	for _, tr := range s.Transitions {
		spec.Transitions = append(spec.Transitions, wfTransition{From: tr.From, To: tr.To, Skill: tr.Skill})
	}
	return spec, true
}

// inlineField extracts key's value from a YAML inline-map line, trimming quotes;
// the value runs to the next comma or closing brace. (Same shape the reviewer
// uses to read transition lines.)
func inlineField(line, key string) string {
	i := strings.Index(line, key+":")
	if i < 0 {
		return ""
	}
	rest := strings.TrimLeft(line[i+len(key)+1:], " ")
	if end := strings.IndexAny(rest, ",}"); end >= 0 {
		rest = rest[:end]
	}
	return strings.Trim(strings.TrimSpace(rest), `"`)
}

// frontmatterList parses a list-valued frontmatter key (inline `[a, b]` or a
// block `- a` list), returning nil when absent.
func frontmatterList(body, key string) []string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	end := -1
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			end = j
			break
		}
	}
	if end < 0 {
		return nil
	}
	for i := 1; i < end; i++ {
		t := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(t, key+":") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, key+":"))
		if strings.HasPrefix(rest, "[") {
			rest = strings.TrimSuffix(strings.TrimPrefix(rest, "["), "]")
			return splitTrimList(rest)
		}
		var out []string
		for j := i + 1; j < end; j++ {
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

func splitTrimList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		v := strings.Trim(strings.TrimSpace(p), `"'`)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// workflowScope returns a workflow doc's frontmatter scope, defaulting an
// embedded canonical default to "system" when it declares none.
func workflowScope(d docindex.Doc) string {
	for _, ln := range strings.Split(d.Body, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "scope:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "scope:")), `"'`)
		}
	}
	if d.Embedded {
		return "system"
	}
	return ""
}

// stripDocFrontmatter returns body with any leading YAML frontmatter removed.
func stripDocFrontmatter(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return body
	}
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			return strings.TrimLeft(strings.Join(lines[j+1:], "\n"), "\n")
		}
	}
	return body
}
