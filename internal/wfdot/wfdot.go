// Package wfdot parses a workflow's fenced ```dot block into a neutral spec — the
// SINGLE DOT-to-spec path shared by the web diagram, the reviewer gater, and the
// commit/edit hooks (so the grammar is defined once, never copied). The model is
// node-centric: each DOT node is a step/state carrying an `actor`, each edge a
// transition, and the edge INTO a reviewer node (whose gate is prompt="@skill:NAME")
// carries that skill — so a story's status walks the nodes and entry to a reviewer
// node is the gated transition. See the satelle-actor-model principle.
package wfdot

import (
	"fmt"
	"sort"
	"strings"
)

// RequiredDoneGate is the mandatory close gate every workflow's path to a `done`
// terminal must carry — the spine the binary guarantees. A custom workflow that
// reaches `done` without it fails Validate: the gate cannot be dropped (see the
// satelle-done-is-last and satelle-actor-model principles).
const RequiredDoneGate = "satelle-story-done-review"

// Validate checks a parsed workflow Spec for structural soundness and the
// mandatory spine, returning human-readable problems (empty = valid):
//   - at least one state;
//   - every transition endpoint is a declared state (no dangling edge);
//   - at least one terminal state (a state with no outgoing edge);
//   - a state named "done", if present, is terminal;
//   - every path into "done" carries RequiredDoneGate (the spine gate).
func Validate(spec Spec) []string {
	if len(spec.States) == 0 {
		return []string{"workflow has no states"}
	}
	known := map[string]bool{}
	for _, s := range spec.States {
		known[s.Name] = true
	}
	hasOut := map[string]bool{}
	var problems []string
	for _, tr := range spec.Transitions {
		if !known[tr.From] {
			problems = append(problems, fmt.Sprintf("transition from unknown state %q", tr.From))
		}
		if !known[tr.To] {
			problems = append(problems, fmt.Sprintf("transition to unknown state %q", tr.To))
		}
		hasOut[tr.From] = true
	}
	terminal := 0
	for _, s := range spec.States {
		if !hasOut[s.Name] {
			terminal++
		}
	}
	if terminal == 0 {
		problems = append(problems, "workflow has no terminal state (every state has an outgoing edge)")
	}
	if known["done"] {
		if hasOut["done"] {
			problems = append(problems, `state "done" must be terminal (it has an outgoing edge)`)
		}
		into, gated := 0, 0
		for _, tr := range spec.Transitions {
			if tr.To == "done" {
				into++
				if tr.Skill == RequiredDoneGate {
					gated++
				}
			}
		}
		if into > 0 && gated == 0 {
			problems = append(problems, fmt.Sprintf(
				"the edge into \"done\" must be gated by the mandatory %s — the spine gate cannot be dropped", RequiredDoneGate))
		}
	}
	return problems
}

// Start returns the workflow's initial state — the first declared state with no
// incoming transition (the Mdiamond entry, e.g. "backlog"). Empty when every
// state has an incoming edge (no clear start). The engagement edge leaves Start.
func (s Spec) Start() string {
	hasIn := map[string]bool{}
	for _, tr := range s.Transitions {
		hasIn[tr.To] = true
	}
	for _, st := range s.States {
		if !hasIn[st.Name] {
			return st.Name
		}
	}
	return ""
}

// State is one workflow node. Terminal is true when no transition leaves it.
type State struct {
	Name     string
	Actor    string
	Terminal bool
	// Skill is the node's own `@skill:NAME` prompt — the executor rubric an
	// executor step performs, or the gate a reviewer node judges by (empty when
	// the node carries no prompt). Populated from the DOT grammar.
	Skill string
}

// doneReachable returns the set of states from which "done" is reachable
// (inclusive of "done"), by reverse traversal. Empty when there is no "done".
func (s Spec) doneReachable() map[string]bool {
	reach := map[string]bool{}
	hasDone := false
	for _, st := range s.States {
		if st.Name == "done" {
			hasDone = true
		}
	}
	if !hasDone {
		return reach
	}
	rev := map[string][]string{}
	for _, tr := range s.Transitions {
		rev[tr.To] = append(rev[tr.To], tr.From)
	}
	reach["done"] = true
	stack := []string{"done"}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, from := range rev[n] {
			if !reach[from] {
				reach[from] = true
				stack = append(stack, from)
			}
		}
	}
	return reach
}

// ExecutorPathToDoneSkills returns the `@skill:` prompts of EXECUTOR nodes that
// lie on a path which can still reach "done", deduped and sorted. These are the
// rubrics an executor must read to PERFORM a step (e.g. commit-push). Unlike
// reviewer gates — which degrade to advisory when their rubric is absent — a
// missing executor skill leaves the step unperformable, so its absence is the
// genuine wasted-work trap to catch at engagement. Empty when there is no "done".
func (s Spec) ExecutorPathToDoneSkills() []string {
	reach := s.doneReachable()
	if len(reach) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, st := range s.States {
		if st.Actor == "executor" && st.Skill != "" && reach[st.Name] {
			set[st.Skill] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Transition is a directed edge; Skill is the reviewer gate admitting entry to
// the target node (empty = ungated).
type Transition struct {
	From  string
	To    string
	Skill string
}

// Spec is the parsed lifecycle: states and gated transitions.
type Spec struct {
	States      []State
	Transitions []Transition
}

// Parse extracts the Spec from a workflow body's fenced ```dot block. ok is false
// when the body carries no dot block, so callers fall back to the inline-YAML
// grammar.
func Parse(body string) (Spec, bool) {
	block := dotBlock(body)
	if block == "" {
		return Spec{}, false
	}
	type node struct {
		actor string
		skill string // resolved from prompt="@skill:NAME"
	}
	nodes := map[string]node{}
	var order []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			order = append(order, name)
			nodes[name] = node{}
		}
	}
	var spec Spec

	for _, stmt := range dotStatements(block) {
		t := strings.TrimSpace(stmt)
		if t == "" || dotReserved(t) {
			continue
		}
		if strings.Contains(t, "->") {
			ids := dotEdgeNodes(t)
			// An edge may carry its gate directly as a `reviewer_skill` attribute
			// (the edge-centric form, e.g. an intent gate on backlog->in_progress
			// where the target is an executor node, not a reviewer node).
			edgeSkill := ""
			if open := strings.Index(t, "["); open >= 0 {
				closeAt := strings.LastIndex(t, "]")
				if closeAt < open {
					closeAt = len(t)
				}
				edgeSkill = strings.TrimPrefix(parseDotAttrs(t[open+1 : closeAt])["reviewer_skill"], "@skill:")
			}
			for _, id := range ids {
				add(id)
			}
			for i := 0; i+1 < len(ids); i++ {
				spec.Transitions = append(spec.Transitions, Transition{From: ids[i], To: ids[i+1], Skill: edgeSkill})
			}
			continue
		}
		id, attrs := dotNodeDecl(t)
		if id == "" {
			continue
		}
		add(id)
		n := nodes[id]
		if a := attrs["actor"]; a != "" {
			n.actor = a
		}
		if p := attrs["prompt"]; strings.HasPrefix(p, "@skill:") {
			n.skill = strings.TrimPrefix(p, "@skill:")
		}
		nodes[id] = n
	}
	if len(order) == 0 {
		return Spec{}, false
	}

	for _, name := range order {
		spec.States = append(spec.States, State{Name: name, Actor: nodes[name].actor, Skill: nodes[name].skill})
	}
	// A transition into a reviewer node is gated by that node's skill — unless the
	// edge already carries an explicit reviewer_skill attribute, which wins.
	for i := range spec.Transitions {
		if spec.Transitions[i].Skill != "" {
			continue
		}
		if to := nodes[spec.Transitions[i].To]; to.actor == "reviewer" && to.skill != "" {
			spec.Transitions[i].Skill = to.skill
		}
	}
	froms := map[string]bool{}
	for _, tr := range spec.Transitions {
		froms[tr.From] = true
	}
	for i := range spec.States {
		spec.States[i].Terminal = !froms[spec.States[i].Name]
	}
	return spec, true
}

// dotBlock returns the contents of the first fenced ```dot code block in body.
func dotBlock(body string) string {
	lines := strings.Split(body, "\n")
	in := false
	var out []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if !in {
			if strings.HasPrefix(t, "```") {
				info := strings.TrimSpace(strings.TrimPrefix(t, "```"))
				if info == "dot" || strings.HasPrefix(info, "dot ") {
					in = true
				}
			}
			continue
		}
		if strings.HasPrefix(t, "```") {
			break
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// dotStatements splits a DOT graph body into statements, keeping bracketed
// attribute lists (which may span newlines) intact and treating graph braces as
// separators. A `//` line comment OUTSIDE a quoted string is stripped to the end
// of its line (so an edge like `a -> b // note` yields the clean `a -> b`); a
// `//` inside a quoted attribute value (e.g. a URL) is preserved. Byte iteration
// is safe: multi-byte runes only occur inside quoted strings, whose bytes are
// copied verbatim.
func dotStatements(block string) []string {
	var stmts []string
	var cur strings.Builder
	depth := 0
	inStr := false
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			stmts = append(stmts, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(block); i++ {
		c := block[i]
		if inStr {
			cur.WriteByte(c)
			if c == '"' {
				inStr = false
			}
			continue
		}
		// `//` line comment outside quotes — skip to end of line; the newline
		// still acts as a statement separator (or a space inside an attr list).
		if c == '/' && i+1 < len(block) && block[i+1] == '/' {
			for i < len(block) && block[i] != '\n' {
				i++
			}
			if depth == 0 {
				flush()
			} else {
				cur.WriteByte(' ')
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
			cur.WriteByte(c)
		case '[':
			depth++
			cur.WriteByte(c)
		case ']':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case '{', '}':
			if depth == 0 {
				flush()
			} else {
				cur.WriteByte(c)
			}
		case ';', '\n':
			if depth == 0 {
				flush()
			} else {
				cur.WriteByte(' ')
			}
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return stmts
}

// dotReserved reports whether a statement is a DOT keyword/graph-attr line that
// declares no workflow node.
func dotReserved(stmt string) bool {
	for _, kw := range []string{"digraph", "graph ", "graph[", "node ", "node[", "edge ", "edge[", "subgraph", "rankdir"} {
		if strings.HasPrefix(stmt, kw) {
			return true
		}
	}
	return false
}

// dotNodeDecl splits `id [attrs]` into the node id and its parsed attributes.
func dotNodeDecl(stmt string) (string, map[string]string) {
	open := strings.Index(stmt, "[")
	if open < 0 {
		return dotUnquote(strings.TrimSpace(stmt)), nil
	}
	id := dotUnquote(strings.TrimSpace(stmt[:open]))
	closeAt := strings.LastIndex(stmt, "]")
	if closeAt < open {
		closeAt = len(stmt)
	}
	return id, parseDotAttrs(stmt[open+1 : closeAt])
}

// dotEdgeNodes returns the node ids of an edge chain `a -> b -> c`, dropping any
// trailing attribute list.
func dotEdgeNodes(stmt string) []string {
	if br := strings.Index(stmt, "["); br >= 0 {
		stmt = stmt[:br]
	}
	var ids []string
	for _, p := range strings.Split(stmt, "->") {
		if id := dotUnquote(strings.TrimSpace(p)); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// parseDotAttrs parses `k=v, k="v"` pairs (commas inside quotes are literal).
func parseDotAttrs(s string) map[string]string {
	m := map[string]string{}
	var parts []string
	var cur strings.Builder
	inStr := false
	for _, r := range s {
		switch r {
		case '"':
			inStr = !inStr
			cur.WriteRune(r)
		case ',':
			if inStr {
				cur.WriteRune(r)
			} else {
				parts = append(parts, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	parts = append(parts, cur.String())
	for _, p := range parts {
		eq := strings.Index(p, "=")
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(p[:eq])
		v := dotUnquote(strings.TrimSpace(p[eq+1:]))
		if k != "" {
			m[k] = v
		}
	}
	return m
}

// dotUnquote trims surrounding double quotes from a DOT token.
func dotUnquote(s string) string {
	return strings.Trim(s, `"`)
}

// ToDOT normalizes a workflow body to the DOT standard — the conversion satelle
// runs at ingest (create/upload). A body that already carries a fenced ```dot
// block is returned unchanged (changed=false). A body in the inline-YAML grammar
// is parsed and re-emitted: its `states:`/`transitions:` block is replaced by an
// equivalent ```dot graph (edge-centric — each gated transition keeps its gate as
// a reviewer_skill attribute), and the frontmatter, prose, and any other YAML
// block (e.g. guardrails) are preserved. ToDOT is idempotent.
func ToDOT(body string) (string, bool) {
	if dotBlock(body) != "" {
		return body, false // already DOT
	}
	spec, ok := parseYAML(body)
	if !ok {
		return body, false
	}
	dot := "```dot\n" + emitDOT(spec, frontmatterName(body)) + "\n```"
	return replaceYAMLLifecycleBlock(body, dot)
}

// parseYAML parses the inline-YAML lifecycle grammar (a `states:` block plus
// `- {from, to[, reviewer_skill]}` transition lines) into a Spec. ok is false
// when the body declares no states and no transitions.
func parseYAML(body string) (Spec, bool) {
	lines := strings.Split(body, "\n")
	var spec Spec
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
				break
			}
			item := strings.TrimSpace(t[2:])
			if strings.HasPrefix(item, "{") {
				spec.States = append(spec.States, State{Name: inlineYAMLField(item, "name"), Actor: inlineYAMLField(item, "actor")})
			} else {
				spec.States = append(spec.States, State{Name: strings.Trim(item, `"'`)})
			}
		}
		break
	}
	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if !strings.HasPrefix(t, "- {") || !strings.Contains(t, "from:") || !strings.Contains(t, "to:") {
			continue
		}
		spec.Transitions = append(spec.Transitions, Transition{
			From:  inlineYAMLField(t, "from"),
			To:    inlineYAMLField(t, "to"),
			Skill: inlineYAMLField(t, "reviewer_skill"),
		})
	}
	if len(spec.States) == 0 && len(spec.Transitions) == 0 {
		return Spec{}, false
	}
	if len(spec.States) == 0 {
		seen := map[string]bool{}
		for _, tr := range spec.Transitions {
			for _, n := range []string{tr.From, tr.To} {
				if n != "" && !seen[n] {
					seen[n] = true
					spec.States = append(spec.States, State{Name: n})
				}
			}
		}
	}
	froms := map[string]bool{}
	for _, tr := range spec.Transitions {
		froms[tr.From] = true
	}
	for i := range spec.States {
		spec.States[i].Terminal = !froms[spec.States[i].Name]
	}
	return spec, true
}

// emitDOT renders a Spec as a DOT digraph body (edge-centric: a gated transition
// keeps its gate as a reviewer_skill attribute). Initial states (no incoming) get
// shape=Mdiamond and terminals shape=Msquare, matching the authored convention.
func emitDOT(spec Spec, name string) string {
	indeg := map[string]int{}
	for _, tr := range spec.Transitions {
		indeg[tr.To]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "digraph %s {\n  rankdir=LR\n\n", sanitizeID(name))
	for _, s := range spec.States {
		var attrs []string
		if indeg[s.Name] == 0 {
			attrs = append(attrs, "shape=Mdiamond")
		} else if s.Terminal {
			attrs = append(attrs, "shape=Msquare")
		}
		if s.Actor != "" {
			attrs = append(attrs, "actor="+s.Actor)
		}
		if len(attrs) > 0 {
			fmt.Fprintf(&b, "  %s [%s]\n", s.Name, strings.Join(attrs, ", "))
		} else {
			fmt.Fprintf(&b, "  %s\n", s.Name)
		}
	}
	b.WriteString("\n")
	for _, tr := range spec.Transitions {
		if tr.Skill != "" {
			fmt.Fprintf(&b, "  %s -> %s [reviewer_skill=%q]\n", tr.From, tr.To, tr.Skill)
		} else {
			fmt.Fprintf(&b, "  %s -> %s\n", tr.From, tr.To)
		}
	}
	b.WriteString("}")
	return b.String()
}

// replaceYAMLLifecycleBlock swaps the fenced code block containing `states:` (the
// inline-YAML lifecycle) for the given dot block, leaving every other block (e.g.
// the guardrails YAML) intact. changed=false when no such block is found.
func replaceYAMLLifecycleBlock(body, dot string) (string, bool) {
	lines := strings.Split(body, "\n")
	inFence, fenceStart, hasStates := false, -1, false
	start, end := -1, -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if !inFence {
			if strings.HasPrefix(t, "```") {
				inFence, fenceStart, hasStates = true, i, false
			}
			continue
		}
		if strings.HasPrefix(t, "states:") {
			hasStates = true
		}
		if strings.HasPrefix(t, "```") { // closing fence
			if hasStates {
				start, end = fenceStart, i
				break
			}
			inFence = false
		}
	}
	if start < 0 {
		return body, false
	}
	out := append([]string{}, lines[:start]...)
	out = append(out, strings.Split(dot, "\n")...)
	out = append(out, lines[end+1:]...)
	return strings.Join(out, "\n"), true
}

// frontmatterName returns the `name:` from a markdown frontmatter block, or "".
func frontmatterName(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for j := 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "---" {
			return ""
		}
		if strings.HasPrefix(t, "name:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "name:")), `"'`)
		}
	}
	return ""
}

// inlineYAMLField extracts key's value from a YAML inline-map (`{key: val, …}`),
// trimming quotes; the value runs to the next comma or closing brace.
func inlineYAMLField(line, key string) string {
	i := strings.Index(line, key+":")
	if i < 0 {
		return ""
	}
	rest := strings.TrimLeft(line[i+len(key)+1:], " ")
	if end := strings.IndexAny(rest, ",}"); end >= 0 {
		rest = rest[:end]
	}
	return strings.Trim(strings.TrimSpace(rest), `"'`)
}

// sanitizeID makes a safe DOT graph identifier from a workflow name.
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "workflow"
	}
	return b.String()
}
