package wfdot

import (
	"fmt"
	"regexp"
	"strings"
)

// InertCodedCheckAgentKind is the format-drift finding for an agent= attribute
// on a gate whose prompt skill is a coded ```check — the engine returns before
// gateBinding, so the attribute is never read (sty_4cebc624 /
// epic:coded-check-gate-honesty). Canonical form omits agent= on scoped coded-
// check nodes (see satelle-dot-standard).
const InertCodedCheckAgentKind = "inert_coded_check_agent"

// SkillIsCodedCheck is a predicate the CLI supplies by resolving skill bodies
// (structure.IsCodedCheck). wfdot stays stdlib-only: it never loads skills.
// nil means "no skill resolution" — InertCodedCheckAgentFindings returns nil.
type SkillIsCodedCheck func(skill string) bool

// InertCodedCheckAgentFindings reports inert agent= attributes on coded-check
// gates. The skill-body seam lives at the CLI edge (skillIsCodedCheck); this
// function only walks the graph. ok is false when the body has no parseable
// ```dot block (same contract as FormatDrift).
//
// Scope:
//   - Scoped / reviewer NODES with non-empty agent= whose prompt skill is coded
//     → finding (refresh strips agent=).
//   - Edges with a non-default agent= (not "reviewer") whose prompt skill(s)
//     include a coded check → finding (refresh rewrites agent=reviewer).
//   - Edges with agent=reviewer + coded check are NOT findings: the parser still
//     requires attrs["agent"] != "" to treat prompt="@skill:…" as a gate
//     (TestEdgeGateNodeConsistentForm); agent=reviewer is the documented edge
//     exception in satelle-dot-standard.
//
// A gate whose skill has no check fence is untouched (precise, not blanket).
func InertCodedCheckAgentFindings(body string, skillIsCoded SkillIsCodedCheck) (findings []FormatFinding, ok bool) {
	if skillIsCoded == nil {
		return nil, true
	}
	block := dotBlock(body)
	if block == "" {
		return nil, false
	}
	spec, parsed := Parse(body)
	if !parsed {
		return nil, false
	}

	// Nodes: scoped gates only (on= + skill). Skip performing tokens
	// (executor/planner/coder) — same skip as ScopedReviewersSplit — so a
	// coded-check skill mis-attached to a performing node is never stripped
	// into a non-performing state (AC4 behaviour-neutrality).
	for _, st := range spec.States {
		if st.Agent == "" || st.Skill == "" || len(st.On) == 0 {
			continue
		}
		switch st.Agent {
		case "executor", "planner", "coder":
			continue
		}
		if !skillIsCoded(st.Skill) {
			continue
		}
		findings = append(findings, FormatFinding{
			Kind:  InertCodedCheckAgentKind,
			Where: st.Name,
			Detail: fmt.Sprintf(
				"node %s carries agent=%s but skill %q is a coded check (engine returns before gateBinding); omit agent= — satelle workflow refresh strips it",
				st.Name, st.Agent, st.Skill,
			),
		})
	}

	// Edges: only non-default agent= is lag; agent=reviewer is parse bookkeeping.
	// Every skill on the edge must be coded (every-not-any): a mixed CSV still
	// dispatches the LLM path, so its agent= is live and must not be rewritten.
	for _, tr := range spec.Transitions {
		if tr.Agent == "" || tr.Agent == "reviewer" {
			continue
		}
		skills := tr.Skills
		if len(skills) == 0 && tr.Skill != "" {
			skills = []string{tr.Skill}
		}
		if len(skills) == 0 {
			continue
		}
		allCoded := true
		for _, s := range skills {
			if !skillIsCoded(s) {
				allCoded = false
				break
			}
		}
		if !allCoded {
			continue
		}
		where := tr.From + "->" + tr.To
		findings = append(findings, FormatFinding{
			Kind:  InertCodedCheckAgentKind,
			Where: where,
			Detail: fmt.Sprintf(
				"edge %s carries non-default agent=%s but all skill(s) %v are coded checks; use agent=reviewer (parse bookkeeping only) — refresh rewrites non-default agent to reviewer",
				where, tr.Agent, skills,
			),
		})
	}
	return findings, true
}

// stripInertCodedAgent rewrites one DOT statement line: for a node whose prompt
// skill is coded, drop agent=; for an edge with non-default agent= and a coded
// skill, rewrite agent to reviewer. Returns the line and whether it changed.
func stripInertCodedAgent(line string, skillIsCoded SkillIsCodedCheck) (string, bool, FormatFinding) {
	if skillIsCoded == nil {
		return line, false, FormatFinding{}
	}
	t := strings.TrimSpace(line)

	// Edge first (contains ->).
	if strings.Contains(t, "->") {
		attrs := edgeAttrs(t)
		if attrs == nil {
			return line, false, FormatFinding{}
		}
		agent := attrs["agent"]
		if agent == "" || agent == "reviewer" {
			return line, false, FormatFinding{}
		}
		prompt := attrs["prompt"]
		if !strings.HasPrefix(prompt, "@skill:") {
			return line, false, FormatFinding{}
		}
		skills := splitCSVSkills(prompt)
		if len(skills) == 0 {
			return line, false, FormatFinding{}
		}
		allCoded := true
		for _, s := range skills {
			if !skillIsCoded(s) {
				allCoded = false
				break
			}
		}
		if !allCoded {
			return line, false, FormatFinding{}
		}
		// Rewrite agent=<non-default> → agent=reviewer (preserves on_enter_agent=).
		rewritten := replaceAgentAttr(line, "reviewer")
		if rewritten == line {
			return line, false, FormatFinding{}
		}
		ids := dotEdgeNodes(t)
		where := "edge"
		if len(ids) >= 2 {
			where = ids[0] + "->" + ids[len(ids)-1]
		}
		return rewritten, true, FormatFinding{
			Kind:   InertCodedCheckAgentKind,
			Where:  where,
			Detail: fmt.Sprintf("rewrote non-default agent=%s to agent=reviewer on coded-check edge", agent),
		}
	}

	// Node declaration: only scoped on= gates (mirrors finding scope).
	id, attrs := dotNodeDecl(t)
	if id == "" || attrs == nil {
		return line, false, FormatFinding{}
	}
	agent := attrs["agent"]
	if agent == "" {
		return line, false, FormatFinding{}
	}
	switch agent {
	case "executor", "planner", "coder":
		return line, false, FormatFinding{}
	}
	if attrs["on"] == "" {
		return line, false, FormatFinding{}
	}
	prompt := attrs["prompt"]
	if !strings.HasPrefix(prompt, "@skill:") {
		return line, false, FormatFinding{}
	}
	// Skill is the full prompt suffix (parser stores it whole; no CSV split on nodes).
	skill := strings.TrimPrefix(prompt, "@skill:")
	skill = strings.TrimSpace(skill)
	if skill == "" || !skillIsCoded(skill) {
		return line, false, FormatFinding{}
	}
	stripped := stripAgentAttr(line)
	if stripped == line {
		return line, false, FormatFinding{}
	}
	return stripped, true, FormatFinding{
		Kind:   InertCodedCheckAgentKind,
		Where:  id,
		Detail: fmt.Sprintf("stripped inert agent=%s from coded-check node %s (skill %q)", agent, id, skill),
	}
}

// agentAttrRE matches the agent= attribute only — not on_enter_agent= — by
// requiring a non-identifier boundary before "agent" ([ , or whitespace).
var agentAttrRE = regexp.MustCompile(`(?i)(^|[\[,\s])agent\s*=\s*("[^"]*"|'[^']*'|[A-Za-z0-9_-]+)`)

// stripAgentAttr removes agent=… from a node/edge attribute list without
// touching on_enter_agent=. Tidies only around the removal site (no whole-line
// indent collapse).
func stripAgentAttr(line string) string {
	cleaned := agentAttrRE.ReplaceAllStringFunc(line, func(m string) string {
		// Keep the leading boundary (group 1) so "[agent=x," → "[" and ", agent=x" → ",".
		sub := agentAttrRE.FindStringSubmatch(m)
		if len(sub) >= 2 {
			return sub[1]
		}
		return ""
	})
	cleaned = regexp.MustCompile(`,\s*,`).ReplaceAllString(cleaned, ",")
	cleaned = regexp.MustCompile(`\[\s*,`).ReplaceAllString(cleaned, "[")
	cleaned = regexp.MustCompile(`,\s*\]`).ReplaceAllString(cleaned, "]")
	cleaned = regexp.MustCompile(`\[\s+`).ReplaceAllString(cleaned, "[")
	cleaned = regexp.MustCompile(`\[\s*\]`).ReplaceAllString(cleaned, "")
	return cleaned
}

// replaceAgentAttr sets agent=<name> on a line that already has agent= (not
// on_enter_agent=).
func replaceAgentAttr(line, name string) string {
	return agentAttrRE.ReplaceAllString(line, "${1}agent="+name)
}
