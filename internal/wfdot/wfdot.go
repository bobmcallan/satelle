// Package wfdot parses a workflow's fenced ```dot block into a neutral spec — the
// SINGLE DOT-to-spec path shared by the web diagram, the reviewer gater, and the
// commit/edit hooks (so the grammar is defined once, never copied). The model is
// node-centric: each DOT node is a step/state carrying an `agent`, each edge a
// transition, and the edge INTO a reviewer node (whose gate is prompt="@skill:NAME")
// carries that skill — so a story's status walks the nodes and entry to a reviewer
// node is the gated transition. See the satelle-agent-model principle.
package wfdot

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultDoneGate is satelle's conventional close gate. It is no longer MANDATED
// by Validate (sty_9a139c78): the done gate is whatever the workflow's `done`
// node declares, transparently — a workflow may name it, name another, or omit it
// entirely ("if the user breaks the process, so be it"). The name remains the
// convention init seeds and the docs reference.
const DefaultDoneGate = "satelle-story-done-review"

// StepSummarySkill is the conventional step-review/summary skill. A workflow opts
// into per-transition step summaries by declaring a node whose gate is this skill
// (transparently, in the DOT), optionally marked mandatory=true. There is no
// hidden always-on summariser — the flow declares it (sty_9a139c78).
const StepSummarySkill = "satelle-step-summary"

// Validate checks a parsed workflow Spec for structural soundness, returning
// human-readable problems (empty = valid):
//   - at least one state;
//   - every transition endpoint is a declared state (no dangling edge);
//   - at least one terminal state (a state with no outgoing edge);
//   - a state named "done", if present, is terminal.
//
// The done gate is NOT mandated: it is whatever the workflow declares (sty_9a139c78).
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
	if known["done"] && hasOut["done"] {
		problems = append(problems, `state "done" must be terminal (it has an outgoing edge)`)
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
	Agent    string
	Terminal bool
	// Skill is the node's own `@skill:NAME` prompt — the executor rubric an
	// executor step performs, or the gate a reviewer node judges by (empty when
	// the node carries no prompt). Populated from the DOT grammar.
	Skill string
	// OnEnterAgent is an optional one-shot named performer dispatched on ENTRY
	// to this state (on_enter_agent=<name>), orthogonal to Agent. Lets a park
	// node stay agent=reviewer (non-engaging for edit/commit gates) while still
	// running a performing agent once on entry (sty_5cabe26f). Empty means no
	// entry dispatch. Does not affect IsPerforming / isEngaging.
	OnEnterAgent string
	// OnEnterSkill is the @skill rubric for OnEnterAgent
	// (on_enter_prompt="@skill:NAME"). Empty when no on_enter_prompt is set.
	OnEnterSkill string
	// Mandatory is the node's `mandatory=true` attribute. For a step-summary node
	// it means the step summary is required (a failure is surfaced, not swallowed);
	// for other nodes it is advisory metadata. Populated from the DOT grammar.
	Mandatory bool
	// On is the node's `on="s1,s2"` (or `on="*"`) attribute — the target states a
	// declared, edge-less reviewer node gates as a blocking gate. Empty for an
	// ordinary node. `*` means every transition. This is the declarative
	// replacement for the old reviewer:always tag layer: the DOT, not a skill tag,
	// is the sole authority for which always-on gates run. Populated from the grammar.
	On []string
	// Shape is the node's DOT shape attribute — the visual classification the
	// authored DOT uses to mark start/terminal states (Mdiamond=start,
	// Msquare=terminal). Populated from the DOT grammar.
	Shape string
	// Model is an optional per-node model override (model="…") for the
	// allocated agents.toml binding at dispatch/gate time (sty_19456622).
	// Empty means inherit the binding's model. The binding remains the source
	// of command template and tools; only {model} varies.
	Model string
}

// StepSummary reports whether the workflow declares a step-summary node (a node
// whose gate skill is StepSummarySkill) and whether it is marked mandatory. The
// summariser runs only when declared — there is no hidden always-on summariser
// (sty_9a139c78).
func (s Spec) StepSummary() (declared, mandatory bool) {
	for _, st := range s.States {
		if st.Skill == StepSummarySkill {
			return true, st.Mandatory
		}
	}
	return false, false
}

// ScopedReviewer is one edge-less always-on gate: its skill and optional per-node
// model override (sty_19456622). Model empty means inherit the [reviewer] binding.
type ScopedReviewer struct {
	Skill string
	Model string
}

// ScopedReviewers returns the DECLARED, edge-less reviewer nodes that gate the
// transition into toStatus — a reviewer node whose `on=` list includes toStatus
// or the wildcard "*". These are the workflow-declared always-on gates, replacing
// the old reviewer:always skill-tag scan so the DOT is the sole gating authority.
// The step-summary node is excluded: it is a post-transition summariser (run via
// Summarise), not a blocking gate. Sorted by skill for a deterministic order.
func (s Spec) ScopedReviewers(toStatus string) []ScopedReviewer {
	var out []ScopedReviewer
	for _, st := range s.States {
		if st.Agent != "reviewer" || st.Skill == "" || len(st.On) == 0 {
			continue
		}
		if st.Skill == StepSummarySkill {
			continue
		}
		if containsStr(st.On, "*") || containsStr(st.On, toStatus) {
			out = append(out, ScopedReviewer{Skill: st.Skill, Model: st.Model})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Skill < out[j].Skill })
	return out
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

// IsPerforming reports whether a node PERFORMS work — any node carrying a
// non-reviewer agent (the in-loop `executor` OR a named isolated agent a node
// allocates a step to, e.g. planner/coder/commit-push). A reviewer node judges,
// it does not perform; an agent-less node (a terminal marker) performs nothing.
// This is the dispatch lock-guard's predicate (internal/agentstep uses
// IsPerformingState to verify a named agent's FROM state is genuinely engaged) —
// NOT the edit-gate's engaged check, which has its own independent shape-derived
// predicate (NonTerminalEngagingStates, sty_f3d5d4b8). The two are deliberately
// separate: PerformingStates answers "which nodes dispatch work";
// NonTerminalEngagingStates answers "which statuses count as in-flight for the
// edit gate" (non-start, non-terminal).
func (st State) IsPerforming() bool {
	return st.Agent != "" && st.Agent != "reviewer"
}

// PerformingStates returns the names of every performing node (see IsPerforming),
// in declaration order — the workflow's engaged states.
func (s Spec) PerformingStates() []string {
	var out []string
	for _, st := range s.States {
		if st.IsPerforming() {
			out = append(out, st.Name)
		}
	}
	return out
}

// NonTerminalEngagingStates returns all states that are neither the start state
// (shape=Mdiamond) nor a terminal state (shape=Msquare), nor a cancel/exception
// sink (agent=reviewer with no outgoing edges). This reads the DOT shape markers
// directly — no hardcoded state names — so the hook's engagement check is
// configuration-over-code (sty_f3d5d4b8).
func (s Spec) NonTerminalEngagingStates() []string {
	var out []string
	for _, st := range s.States {
		if !s.isEngaging(st) {
			continue
		}
		out = append(out, st.Name)
	}
	return out
}

// isEngaging reports whether a state is a non-terminal engaging state: it is
// neither the start state (shape=Mdiamond), nor a terminal state (shape=Msquare),
// nor any agent=reviewer role state. Reviewer-role states are never engaged work
// (edit/commit gates): that covers cancel sinks AND park states that keep an
// outgoing resume edge (authored as agent=reviewer so a parked story is not
// engaged — config-over-code, no state-name literals).
func (s Spec) isEngaging(st State) bool {
	// Start marker: shape=Mdiamond
	if st.Shape == "Mdiamond" {
		return false
	}
	// Terminal marker: shape=Msquare
	if st.Shape == "Msquare" {
		return false
	}
	// Reviewer role: not engaged (cancel sinks, park/resume nodes, edge-less gates).
	if st.Agent == "reviewer" {
		return false
	}
	return true
}

// IsPerformingState reports whether the named state exists and performs work.
// An unknown name is not performing (false). Used by the dispatch lock-guard to
// verify a named agent's FROM state is genuinely engaged.
func (s Spec) IsPerformingState(name string) bool {
	for _, st := range s.States {
		if st.Name == name {
			return st.IsPerforming()
		}
	}
	return false
}

// ExecutorPathToDoneSkills returns the `@skill:` prompts of PERFORMING nodes that
// lie on a path which can still reach "done", deduped and sorted. These are the
// rubrics that perform a step. Unlike reviewer gates — which degrade to advisory
// when their rubric is absent — a missing performer skill leaves the step
// unperformable, so its absence is the genuine wasted-work trap to catch at
// engagement. Empty when no "done".
func (s Spec) ExecutorPathToDoneSkills() []string {
	reach := s.doneReachable()
	if len(reach) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, st := range s.States {
		if st.IsPerforming() && st.Skill != "" && reach[st.Name] {
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

// Transition is a directed edge. Skills are the reviewer gates admitting entry to
// the target node, in order (empty = ungated); Skill mirrors the first for
// single-reviewer back-compat. An edge declares its gate(s) either edge-centric
// (`reviewer_skill="a,b"`) or in the NODE-CONSISTENT form
// (`agent=reviewer, prompt="@skill:a"`) — the same vocabulary a reviewer node
// uses (sty_be67919a); reviewer_skill wins when both are present.
type Transition struct {
	From   string
	To     string
	Skill  string
	Skills []string
	// Model is an optional per-edge model override (model="…") applied to every
	// reviewer skill on this edge at gate time (sty_19456622). Empty inherits
	// the [reviewer] binding model. One model per edge (CSV skills share it).
	Model string
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
		agent        string
		skill        string   // resolved from prompt="@skill:NAME"
		onEnterAgent string   // on_enter_agent=<name> one-shot performer on entry
		onEnterSkill string   // on_enter_prompt="@skill:NAME"
		mandatory    bool     // mandatory=true attribute
		on           []string // on="s1,s2" / on="*" scope (declared always-on gate)
		shape        string   // DOT shape attribute (Mdiamond=start, Msquare=terminal)
		model        string   // model="…" per-node override (sty_19456622)
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
			// An edge may carry its gate directly (e.g. an intent gate on
			// backlog->in_progress where the target is an executor node). Two
			// equivalent forms are accepted (sty_be67919a): the edge-centric
			// `reviewer_skill="NAME"`, and the NODE-CONSISTENT form
			// `agent=reviewer, prompt="@skill:NAME"` — the same vocabulary a
			// reviewer node uses — so every step reads the same way. reviewer_skill
			// wins when both are present.
			var edgeSkills []string
			var edgeModel string
			if open := strings.Index(t, "["); open >= 0 {
				closeAt := strings.LastIndex(t, "]")
				if closeAt < open {
					closeAt = len(t)
				}
				attrs := parseDotAttrs(t[open+1 : closeAt])
				edgeSkills = splitCSVSkills(attrs["reviewer_skill"])
				if len(edgeSkills) == 0 && attrs["agent"] == "reviewer" && strings.HasPrefix(attrs["prompt"], "@skill:") {
					edgeSkills = splitCSVSkills(attrs["prompt"])
				}
				edgeModel = attrs["model"]
			}
			for _, id := range ids {
				add(id)
			}
			for i := 0; i+1 < len(ids); i++ {
				spec.Transitions = append(spec.Transitions, Transition{
					From: ids[i], To: ids[i+1], Skill: first(edgeSkills), Skills: edgeSkills, Model: edgeModel,
				})
			}
			continue
		}
		id, attrs := dotNodeDecl(t)
		if id == "" {
			continue
		}
		add(id)
		n := nodes[id]
		if a := attrs["agent"]; a != "" {
			n.agent = a
		}
		if p := attrs["prompt"]; strings.HasPrefix(p, "@skill:") {
			n.skill = strings.TrimPrefix(p, "@skill:")
		}
		if ea := attrs["on_enter_agent"]; ea != "" {
			n.onEnterAgent = ea
		}
		if ep := attrs["on_enter_prompt"]; strings.HasPrefix(ep, "@skill:") {
			n.onEnterSkill = strings.TrimPrefix(ep, "@skill:")
		}
		if strings.EqualFold(attrs["mandatory"], "true") {
			n.mandatory = true
		}
		if on := splitCSV(attrs["on"]); len(on) > 0 {
			n.on = on
		}
		if s := attrs["shape"]; s != "" {
			n.shape = s
		}
		if m := attrs["model"]; m != "" {
			n.model = m
		}
		nodes[id] = n
	}
	if len(order) == 0 {
		return Spec{}, false
	}

	for _, name := range order {
		n := nodes[name]
		spec.States = append(spec.States, State{
			Name: name, Agent: n.agent, Skill: n.skill,
			OnEnterAgent: n.onEnterAgent, OnEnterSkill: n.onEnterSkill,
			Mandatory: n.mandatory, On: n.on, Shape: n.shape, Model: n.model,
		})
	}
	// A transition into a reviewer node is gated by that node's skill — unless the
	// edge already carries an explicit reviewer_skill attribute, which wins.
	for i := range spec.Transitions {
		if len(spec.Transitions[i].Skills) > 0 {
			continue
		}
		if to := nodes[spec.Transitions[i].To]; to.agent == "reviewer" && to.skill != "" {
			spec.Transitions[i].Skill = to.skill
			spec.Transitions[i].Skills = []string{to.skill}
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

// splitCSV splits a comma-separated attribute value into trimmed, non-empty
// tokens (e.g. on="in_progress, done" → ["in_progress","done"]). Returns nil when
// empty.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// splitCSVSkills splits a comma-separated reviewer_skill value into skill names,
// stripping any per-token `@skill:` prefix (e.g. reviewer_skill="a,@skill:b").
func splitCSVSkills(s string) []string {
	out := splitCSV(s)
	for i, v := range out {
		out[i] = strings.TrimPrefix(v, "@skill:")
	}
	return out
}

// first returns the first element of ss, or "" when empty.
func first(ss []string) string {
	if len(ss) > 0 {
		return ss[0]
	}
	return ""
}

// containsStr reports whether ss contains v.
func containsStr(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// ToDOT normalizes a workflow body to the DOT standard — the conversion satelle
// runs at ingest (create/upload). A body that already carries a fenced ```dot
// block is returned unchanged (changed=false). A body in the inline-YAML grammar
// is parsed and re-emitted: its `states:`/`transitions:` block is replaced by an
// equivalent ```dot graph in the CANONICAL node-consistent form (gated edges as
// [agent=reviewer, prompt="@skill:NAME"]; nodes with a Skill emit prompt="@skill:…"),
// and the frontmatter, prose, and any other YAML block (e.g. guardrails) are
// preserved. ToDOT is idempotent. See the satelle-dot-standard principle.
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
				spec.States = append(spec.States, State{Name: inlineYAMLField(item, "name"), Agent: inlineYAMLField(item, "agent")})
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
		sk := inlineYAMLField(t, "reviewer_skill")
		var skills []string
		if sk != "" {
			skills = []string{sk}
		}
		spec.Transitions = append(spec.Transitions, Transition{
			From:   inlineYAMLField(t, "from"),
			To:     inlineYAMLField(t, "to"),
			Skill:  sk,
			Skills: skills,
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

// emitDOT renders a Spec as a DOT digraph body in the CANONICAL node-consistent
// form: a gated edge is [agent=reviewer, prompt="@skill:NAME"] (CSV skills join
// as prompt="@skill:a,b"); a node with State.Skill emits prompt="@skill:…".
// Initial states (no incoming) get shape=Mdiamond and terminals shape=Msquare.
// The legacy reviewer_skill= edge attribute is never written — it remains a
// parse-only back-compat input. See the satelle-dot-standard principle.
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
		if s.Agent != "" {
			attrs = append(attrs, "agent="+s.Agent)
		}
		if s.Skill != "" {
			attrs = append(attrs, fmt.Sprintf("prompt=\"@skill:%s\"", s.Skill))
		}
		if s.OnEnterAgent != "" {
			attrs = append(attrs, "on_enter_agent="+s.OnEnterAgent)
		}
		if s.OnEnterSkill != "" {
			attrs = append(attrs, fmt.Sprintf("on_enter_prompt=\"@skill:%s\"", s.OnEnterSkill))
		}
		if s.Model != "" {
			attrs = append(attrs, fmt.Sprintf("model=%q", s.Model))
		}
		if len(attrs) > 0 {
			fmt.Fprintf(&b, "  %s [%s]\n", s.Name, strings.Join(attrs, ", "))
		} else {
			fmt.Fprintf(&b, "  %s\n", s.Name)
		}
	}
	b.WriteString("\n")
	for _, tr := range spec.Transitions {
		skills := tr.Skills
		if len(skills) == 0 && tr.Skill != "" {
			skills = []string{tr.Skill}
		}
		if len(skills) > 0 {
			if tr.Model != "" {
				fmt.Fprintf(&b, "  %s -> %s [agent=reviewer, prompt=\"@skill:%s\", model=%q]\n",
					tr.From, tr.To, strings.Join(skills, ","), tr.Model)
			} else {
				fmt.Fprintf(&b, "  %s -> %s [agent=reviewer, prompt=\"@skill:%s\"]\n", tr.From, tr.To, strings.Join(skills, ","))
			}
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
