package web

import (
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/wfdot"
)

// workflowRowVM is one workflow in the Workflow panel list — the row the user
// filters and clicks to expand. Mirrors the stories/tasks row shape so the same
// filter + expand/collapse interactions apply (the tab is read-only).
type workflowRowVM struct {
	Name       string
	Headline   string
	Scope      string
	AppliesTo  []string
	Updated    time.Time
	Provenance string // default | edited | authored (sty_ba0eb5c6)
	Source     string
}

// bindingVM is one node's (or edge's) resolved agent/skill/model for the diagram.
type bindingVM struct {
	Agent      string
	Skill      string
	Model      string
	Overridden bool // true when DOT model= overrides the binding model
}

// wfState is a workflow state node. Terminal is true when no transition leaves it.
// Skill is the node's own @skill: prompt; On lists the target states an edge-less
// scoped reviewer node gates (its `on=` attribute) — the diagram renders such a
// node as an ANNOTATION on its targets, not as a free-floating state.
type wfState struct {
	Name     string
	Agent    string
	Terminal bool
	Skill    string
	On       []string
}

// wfTransition is a directed edge between states. Skill is the first reviewer
// gate (empty = ungated/advisory) for back-compat; Skills is the full CSV list
// in execution order (sty_1b7a0ca2). Parallel is the concurrency cap from
// parallel=true|N (0 = sequential).
type wfTransition struct {
	From     string
	To       string
	Skill    string   // first of Skills, or sole gate
	Skills   []string // full ordered list when multi-reviewer
	Parallel int      // 0 = sequential; >0 = concurrent cap
}

// gateSkills returns the ordered reviewer skills on the edge.
func (tr wfTransition) gateSkills() []string {
	if len(tr.Skills) > 0 {
		return tr.Skills
	}
	if tr.Skill != "" {
		return []string{tr.Skill}
	}
	return nil
}

// edgeGateLabel builds the diagram label and full tooltip for an edge gate
// (sty_1b7a0ca2). Multi-reviewer edges join short names; parallel edges prefix
// "∥". Title lists full skill names in order plus parallel=N when set.
func edgeGateLabel(tr wfTransition) (visible, title string) {
	skills := tr.gateSkills()
	if len(skills) == 0 {
		return "", ""
	}
	shorts := make([]string, len(skills))
	for i, s := range skills {
		shorts[i] = shortSkill(s)
	}
	visible = strings.Join(shorts, " · ")
	// Cap very long multi-labels so the DAG stays readable (tooltip has full list).
	if len(skills) > 3 {
		visible = fmt.Sprintf("%s · %s · +%d", shorts[0], shorts[1], len(skills)-2)
	}
	title = strings.Join(skills, ", ")
	if tr.Parallel > 0 {
		visible = "∥ " + visible
		title = title + fmt.Sprintf(" · parallel=%d", tr.Parallel)
	}
	return visible, title
}

// wfSpec is the parsed lifecycle of a workflow: its states and gated transitions.
type wfSpec struct {
	States      []wfState
	Transitions []wfTransition
}

// workflowDetailVM backs the inline expand: the parsed diagram + the applies_to
// binding + the raw definition (frontmatter stripped) for the read-only view.
type workflowDetailVM struct {
	Name       string
	Headline   string
	Scope      string
	AppliesTo  []string
	Spec       wfSpec
	Diagram    template.HTML
	Body       string
	Provenance string
	Source     string
	Bindings   map[string]bindingVM // state name or edge:from->to
}

// shortSkill abbreviates a reviewer skill for an edge label (the full name rides
// in a tooltip): satelle-story-intent-review → story-intent.
func shortSkill(s string) string {
	s = strings.TrimPrefix(s, "satelle-")
	s = strings.TrimSuffix(s, "-review")
	return s
}

// workflowDiagram renders the parsed lifecycle as a dependency-free, read-only
// SVG the user can actually FOLLOW (sty_677c604c): a layered left-to-right DAG.
// States are ranked by longest path from the start, so the main spine reads as
// one row; gate labels anchor ON their own edges; recovery (backward) edges and
// the cancel fan (many edges into one terminal exit) are de-emphasised
// (`wf-edge-alt`, toggleable in JS); an edge-less scoped reviewer node
// (`on="…"`) renders as an ANNOTATION under each state it gates, never as a
// free-floating node. Interactivity (pan/zoom, hover highlight, the alt-edge
// toggle) is progressive enhancement in app.js over the same data-state /
// data-from/data-to identifiers as before (sty_19b2107a).
func workflowDiagram(spec wfSpec, bindings map[string]bindingVM) template.HTML {
	if bindings == nil {
		bindings = map[string]bindingVM{}
	}
	// Split ANNOTATION nodes out of the flow: an edge-less node with on= is a
	// declaration (scoped reviewer gate, step-summary opt-in, OR executor
	// augmentation — sty_8225d8a5), not a lifecycle state. Without this, an
	// edge-less executor augmentation would render as a phantom main-flow node.
	// Scoped (on=…) nodes annotate their targets; other edge-less reviewers
	// become a footnote line.
	incident := map[string]bool{}
	for _, tr := range spec.Transitions {
		incident[tr.From], incident[tr.To] = true, true
	}
	annByTarget := map[string][]wfState{}
	var footers []wfState
	flow := wfSpec{Transitions: spec.Transitions}
	for _, s := range spec.States {
		if !incident[s.Name] && len(s.On) > 0 {
			// Annotation: scoped reviewer OR executor augmentation (any agent).
			for _, target := range s.On {
				annByTarget[target] = append(annByTarget[target], s)
			}
			continue
		}
		if s.Agent == "reviewer" && !incident[s.Name] {
			footers = append(footers, s)
			continue
		}
		flow.States = append(flow.States, s)
	}

	terminal := map[string]bool{}
	agentOf := map[string]string{}
	var names []string
	known := map[string]bool{}
	for _, s := range flow.States {
		terminal[s.Name] = s.Terminal
		agentOf[s.Name] = s.Agent
		if !known[s.Name] {
			known[s.Name] = true
			names = append(names, s.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}

	// Topological order via DFS from the start (the state nothing transitions
	// into), detecting true BACK edges (cycles — e.g. a recovery edge) as edges
	// into a node still on the stack. Deterministic: edges iterate in declared
	// order, unreached states are visited in declared order.
	indeg := map[string]int{}
	for _, tr := range flow.Transitions {
		if tr.From != tr.To && known[tr.From] && known[tr.To] {
			indeg[tr.To]++
		}
	}
	start := names[0]
	for _, n := range names {
		if indeg[n] == 0 {
			start = n
			break
		}
	}
	state := map[string]int{} // 0 unvisited, 1 on-stack, 2 done
	backEdge := map[string]bool{}
	ekey := func(from, to string) string { return from + "\x00" + to }
	var post []string
	var visit func(n string)
	visit = func(n string) {
		state[n] = 1
		for _, tr := range flow.Transitions {
			if tr.From != n || tr.From == tr.To || !known[tr.To] {
				continue
			}
			switch state[tr.To] {
			case 0:
				visit(tr.To)
			case 1:
				backEdge[ekey(tr.From, tr.To)] = true
			}
		}
		state[n] = 2
		post = append(post, n)
	}
	visit(start)
	for _, n := range names {
		if state[n] == 0 {
			visit(n)
		}
	}
	order := make([]string, 0, len(post)) // reverse postorder = topological
	for i := len(post) - 1; i >= 0; i-- {
		order = append(order, post[i])
	}

	// Rank = longest path from the start over forward (non-back) edges, so the
	// main spine reads as one row of increasing columns; row = arrival order
	// within a rank.
	rank := map[string]int{}
	for _, n := range order {
		for _, tr := range flow.Transitions {
			if tr.From != n || tr.From == tr.To || !known[tr.To] || backEdge[ekey(tr.From, tr.To)] {
				continue
			}
			if r := rank[n] + 1; r > rank[tr.To] {
				rank[tr.To] = r
			}
		}
	}
	// The cancel fan: every edge into a terminal state with several inbound edges
	// is an EXIT, not the spine — de-emphasise it, and keep its target off the
	// spine row.
	inCount := map[string]int{}
	for _, tr := range flow.Transitions {
		if tr.From != tr.To && known[tr.From] && known[tr.To] {
			inCount[tr.To]++
		}
	}
	fanExit := func(n string) bool { return terminal[n] && inCount[n] >= 3 }

	// Rows within a rank: follow the predecessors' rows (the spine stays on row
	// 0), pushing fan exits below; topo position breaks the remaining ties.
	row := map[string]int{}
	maxRank, maxRow := 0, 0
	for _, n := range order {
		if rank[n] > maxRank {
			maxRank = rank[n]
		}
	}
	topoPos := map[string]int{}
	for i, n := range order {
		topoPos[n] = i
	}
	for r := 0; r <= maxRank; r++ {
		var members []string
		for _, n := range order {
			if rank[n] == r {
				members = append(members, n)
			}
		}
		desired := func(n string) int {
			d := 1 << 20
			for _, tr := range flow.Transitions {
				if tr.To != n || tr.From == tr.To || !known[tr.From] || backEdge[ekey(tr.From, tr.To)] {
					continue
				}
				if pr, ok := row[tr.From]; ok && pr < d {
					d = pr
				}
			}
			return d
		}
		sort.SliceStable(members, func(i, j int) bool {
			a, b := members[i], members[j]
			if fanExit(a) != fanExit(b) {
				return !fanExit(a) // spine candidates before fan exits
			}
			da, db := desired(a), desired(b)
			if da != db {
				return da < db
			}
			return topoPos[a] < topoPos[b]
		})
		for i, n := range members {
			row[n] = i
			if i > maxRow {
				maxRow = i
			}
		}
	}

	const (
		nodeW, nodeH = 148, 48 // taller for agent/model sub-label (sty_ba0eb5c6)
		gapX, gapY   = 84, 72
		pad          = 16
	)
	topGutter := pad
	if len(backEdge) > 0 {
		topGutter += 34 // a lane above the grid for recovery arcs
	}
	nx := func(n string) int { return pad + rank[n]*(nodeW+gapX) }
	ny := func(n string) int { return topGutter + row[n]*(nodeH+gapY) }
	gridBottom := topGutter + (maxRow+1)*nodeH + maxRow*gapY
	width := pad*2 + (maxRank+1)*nodeW + maxRank*gapX

	esc := template.HTMLEscapeString
	type labelBox struct{ x1, x2, y int }
	var placed []labelBox
	overlaps := func(x1, x2, y int) bool {
		for _, p := range placed {
			dy := y - p.y
			if dy < 0 {
				dy = -dy
			}
			if x1 < p.x2 && x2 > p.x1 && dy < 14 {
				return true
			}
		}
		return false
	}
	maxY := gridBottom
	label := func(b *strings.Builder, tr wfTransition, lx, ly int, extra string) {
		lbl, tip := edgeGateLabel(tr)
		if lbl == "" {
			return
		}
		lw := len(lbl) * 6
		lx -= lw / 2
		for overlaps(lx, lx+lw, ly) {
			ly += 15 // deterministic anti-collision nudge, source-order stable
		}
		placed = append(placed, labelBox{lx, lx + lw, ly})
		if ly+10 > maxY {
			maxY = ly + 10
		}
		cls := "wf-edge-label" + extra
		if tr.Parallel > 0 {
			cls += " parallel"
		}
		parAttr := ""
		if tr.Parallel > 0 {
			parAttr = fmt.Sprintf(` data-parallel="%d"`, tr.Parallel)
		}
		fmt.Fprintf(b, `<text class="%s" data-from="%s" data-to="%s"%s x="%d" y="%d"><title>%s</title>%s</text>`,
			cls, esc(tr.From), esc(tr.To), parAttr, lx, ly, esc(tip), esc(lbl))
	}

	var edgeB strings.Builder
	altLane, backLane := 0, 0
	for _, tr := range flow.Transitions {
		if !known[tr.From] || !known[tr.To] || tr.From == tr.To {
			continue
		}
		back := backEdge[ekey(tr.From, tr.To)]
		fan := !back && terminal[tr.To] && inCount[tr.To] >= 3
		cls := "wf-edge-path"
		if len(tr.gateSkills()) == 0 {
			cls += " ungated"
		}
		if tr.Parallel > 0 {
			cls += " parallel"
		}
		switch {
		case back: // recovery — arc over the top, muted + toggleable
			cls += " back wf-edge-alt"
			x1, y1 := nx(tr.From)+nodeW/2, ny(tr.From)
			x2, y2 := nx(tr.To)+nodeW/2, ny(tr.To)
			laneY := pad + backLane*12
			backLane++
			fmt.Fprintf(&edgeB, `<path class="%s" data-from="%s" data-to="%s" d="M%d,%d C%d,%d %d,%d %d,%d" marker-end="url(#wf-arrow)"/>`,
				cls, esc(tr.From), esc(tr.To), x1, y1, x1, laneY, x2, laneY, x2, y2-2)
			label(&edgeB, tr, (x1+x2)/2, laneY+12, " wf-edge-alt")
		case fan: // exit fan (e.g. cancel) — sag below the grid, muted + toggleable
			cls += " wf-edge-alt"
			x1, y1 := nx(tr.From)+nodeW/2, ny(tr.From)+nodeH
			x2, y2 := nx(tr.To)+nodeW/2, ny(tr.To)+nodeH
			sag := gridBottom + 26 + altLane*14
			altLane++
			if sag+16 > maxY {
				maxY = sag + 16
			}
			fmt.Fprintf(&edgeB, `<path class="%s" data-from="%s" data-to="%s" d="M%d,%d C%d,%d %d,%d %d,%d" marker-end="url(#wf-arrow)"/>`,
				cls, esc(tr.From), esc(tr.To), x1, y1, x1, sag, x2, sag, x2, y2+2)
			label(&edgeB, tr, (x1+x2)/2, sag-5, " wf-edge-alt")
		case rank[tr.To] == rank[tr.From]+1 && row[tr.To] == row[tr.From]: // spine — straight
			x1, x2 := nx(tr.From)+nodeW, nx(tr.To)
			y := ny(tr.From) + nodeH/2
			fmt.Fprintf(&edgeB, `<path class="%s" data-from="%s" data-to="%s" d="M%d,%d L%d,%d" marker-end="url(#wf-arrow)"/>`,
				cls, esc(tr.From), esc(tr.To), x1, y, x2-2, y)
			label(&edgeB, tr, (x1+x2)/2, y-8, "")
		default: // forward skip / row change — gentle curve
			x1, y1 := nx(tr.From)+nodeW, ny(tr.From)+nodeH/2
			x2, y2 := nx(tr.To), ny(tr.To)+nodeH/2
			mx := (x1 + x2) / 2
			fmt.Fprintf(&edgeB, `<path class="%s" data-from="%s" data-to="%s" d="M%d,%d C%d,%d %d,%d %d,%d" marker-end="url(#wf-arrow)"/>`,
				cls, esc(tr.From), esc(tr.To), x1, y1, mx, y1, mx, y2, x2-2, y2)
			label(&edgeB, tr, mx, (y1+y2)/2-6, "")
		}
	}

	var b strings.Builder
	b.WriteString(`<defs><marker id="wf-arrow" markerWidth="9" markerHeight="9" refX="7" refY="3" orient="auto"><path d="M0,0 L7,3 L0,6 Z" class="wf-arrowhead"/></marker></defs>`)
	b.WriteString(edgeB.String()) // edges under the nodes
	// nodes — each is focusable (tabindex) and carries a stable data-state so hover,
	// keyboard focus, and click can be wired in vanilla JS (progressive enhancement).
	for _, n := range order {
		x, y := nx(n), ny(n)
		cls := "wf-dnode"
		if terminal[n] {
			cls += " terminal"
		}
		// Binding sub-label: agent + short model (sty_ba0eb5c6 AC1). Prefer
		// process-view allocation; fall back to DOT agent= alone.
		bind := bindings[n]
		ag := bind.Agent
		if ag == "" {
			ag = agentOf[n]
		}
		nameY := y + nodeH/2 + 4
		agentBadge := ""
		titleParts := []string{n}
		if ag != "" {
			nameY = y + nodeH/2 - 4
			badge := "@" + ag
			if bind.Model != "" {
				badge += " · " + shortModel(bind.Model)
				if bind.Overridden {
					badge += " *"
				}
			}
			agentBadge = fmt.Sprintf(`<text class="wf-agent-badge" x="%d" y="%d">%s</text>`, x+nodeW/2, y+nodeH-8, esc(badge))
			titleParts = append(titleParts, "agent="+ag)
			if bind.Skill != "" {
				titleParts = append(titleParts, "skill="+bind.Skill)
			}
			if bind.Model != "" {
				titleParts = append(titleParts, "model="+bind.Model)
			}
		}
		tip := esc(strings.Join(titleParts, " · "))
		// Keep <rect> immediately after <g …> so nodePos tests (and any CSS
		// :first-child selectors) still match; title follows the rect.
		fmt.Fprintf(&b, `<g class="%s" data-state="%s" tabindex="0" role="button" aria-label="state %s — highlight its transitions"><rect x="%d" y="%d" width="%d" height="%d" rx="7"/><title>%s</title><text x="%d" y="%d">%s</text>%s</g>`,
			cls, esc(n), esc(n), x, y, nodeW, nodeH, tip, x+nodeW/2, nameY, esc(n), agentBadge)
		// Scoped (on=…) reviewers annotate the states they gate.
		for k, ann := range annByTarget[n] {
			ay := y + nodeH + 13 + k*13
			if ay+8 > maxY {
				maxY = ay + 8
			}
			fmt.Fprintf(&b, `<text class="wf-annot" data-state="%s" x="%d" y="%d"><title>%s gates entry to %s</title>⊙ %s</text>`,
				esc(n), x+nodeW/2, ay, esc(ann.Skill), esc(n), esc(shortSkill(ann.Skill)))
		}
	}
	// Edge-less, un-scoped reviewer declarations (e.g. the step-summary opt-in)
	// become one footnote line.
	if len(footers) > 0 {
		var parts []string
		for _, f := range footers {
			parts = append(parts, f.Name+": "+shortSkill(f.Skill))
		}
		fy := maxY + 20
		maxY = fy
		fmt.Fprintf(&b, `<text class="wf-foot" x="%d" y="%d">every transition — %s</text>`, pad, fy, esc(strings.Join(parts, " · ")))
	}

	height := maxY + pad
	var svg strings.Builder
	fmt.Fprintf(&svg, `<svg class="wf-diagram" viewBox="0 0 %d %d" data-vb="0 0 %d %d" preserveAspectRatio="xMinYMin meet" role="img" aria-label="workflow flow diagram">`,
		width, height, width, height)
	svg.WriteString(b.String())
	svg.WriteString(`</svg>`)
	return template.HTML(svg.String())
}

// shortModel trims a model id for the diagram badge (last path segment / short tail).
func shortModel(m string) string {
	m = strings.TrimSpace(m)
	if m == "" {
		return ""
	}
	if i := strings.LastIndex(m, "/"); i >= 0 && i+1 < len(m) {
		m = m[i+1:]
	}
	if len(m) > 18 {
		return m[:18]
	}
	return m
}

// workflowRows builds the Workflow panel rows from the indexed workflow docs.
// prov maps "workflows\x00name" → provenance; src maps the same key → source.
func workflowRows(docs []docindex.Doc, prov, src map[string]string) []workflowRowVM {
	out := make([]workflowRowVM, 0, len(docs))
	for _, d := range docs {
		key := "workflows\x00" + d.Name
		out = append(out, workflowRowVM{
			Name:       d.Name,
			Headline:   d.Headline,
			Scope:      workflowScope(d),
			AppliesTo:  frontmatterList(d.Body, "applies_to"),
			Updated:    d.ModTime,
			Provenance: prov[key],
			Source:     src[key],
		})
	}
	return out
}

// parseWorkflow extracts the states and transitions from a workflow markdown
// body. States come from the `states:` YAML block (a bare name or an inline
// `{name:…, agent:…}` map); transitions are every `- {from:…, to:…}` line
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
		sk := inlineField(t, "reviewer_skill")
		tr := wfTransition{From: inlineField(t, "from"), To: inlineField(t, "to"), Skill: sk}
		if sk != "" {
			tr.Skills = []string{sk}
		}
		spec.Transitions = append(spec.Transitions, tr)
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

// parseState parses one `states:` list item — a bare name or `{name:…, agent:…}`.
func parseState(item string) wfState {
	if strings.HasPrefix(item, "{") {
		return wfState{Name: inlineField(item, "name"), Agent: inlineField(item, "agent")}
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
		spec.States = append(spec.States, wfState{Name: st.Name, Agent: st.Agent, Terminal: st.Terminal, Skill: st.Skill, On: st.On})
	}
	for _, tr := range s.Transitions {
		skills := tr.Skills
		if len(skills) == 0 && tr.Skill != "" {
			skills = []string{tr.Skill}
		}
		first := tr.Skill
		if first == "" && len(skills) > 0 {
			first = skills[0]
		}
		spec.Transitions = append(spec.Transitions, wfTransition{
			From: tr.From, To: tr.To, Skill: first, Skills: skills, Parallel: tr.Parallel,
		})
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
