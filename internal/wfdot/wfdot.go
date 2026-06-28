// Package wfdot parses a workflow's fenced ```dot block into a neutral spec — the
// SINGLE DOT-to-spec path shared by the web diagram, the reviewer gater, and the
// commit/edit hooks (so the grammar is defined once, never copied). The model is
// node-centric: each DOT node is a step/state carrying an `actor`, each edge a
// transition, and the edge INTO a reviewer node (whose gate is prompt="@skill:NAME")
// carries that skill — so a story's status walks the nodes and entry to a reviewer
// node is the gated transition. See the satelle-recursive-actor-model principle.
package wfdot

import "strings"

// State is one workflow node. Terminal is true when no transition leaves it.
type State struct {
	Name     string
	Actor    string
	Terminal bool
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
			for _, id := range ids {
				add(id)
			}
			for i := 0; i+1 < len(ids); i++ {
				spec.Transitions = append(spec.Transitions, Transition{From: ids[i], To: ids[i+1]})
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
		spec.States = append(spec.States, State{Name: name, Actor: nodes[name].actor})
	}
	// A transition into a reviewer node is gated by that node's skill.
	for i := range spec.Transitions {
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
// separators.
func dotStatements(block string) []string {
	var stmts []string
	var cur strings.Builder
	depth := 0
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			stmts = append(stmts, s)
		}
		cur.Reset()
	}
	for _, r := range block {
		switch r {
		case '[':
			depth++
			cur.WriteRune(r)
		case ']':
			if depth > 0 {
				depth--
			}
			cur.WriteRune(r)
		case '{', '}':
			if depth == 0 {
				flush()
			} else {
				cur.WriteRune(r)
			}
		case ';', '\n':
			if depth == 0 {
				flush()
			} else {
				cur.WriteRune(' ')
			}
		default:
			cur.WriteRune(r)
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
