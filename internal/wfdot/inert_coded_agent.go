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

	// Nodes: scoped or otherwise, any non-empty agent= with a coded prompt skill.
	for _, st := range spec.States {
		if st.Agent == "" || st.Skill == "" {
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
	for _, tr := range spec.Transitions {
		if tr.Agent == "" || tr.Agent == "reviewer" {
			continue
		}
		skills := tr.Skills
		if len(skills) == 0 && tr.Skill != "" {
			skills = []string{tr.Skill}
		}
		var coded []string
		for _, s := range skills {
			if skillIsCoded(s) {
				coded = append(coded, s)
			}
		}
		if len(coded) == 0 {
			continue
		}
		where := tr.From + "->" + tr.To
		findings = append(findings, FormatFinding{
			Kind:  InertCodedCheckAgentKind,
			Where: where,
			Detail: fmt.Sprintf(
				"edge %s carries non-default agent=%s but skill(s) %v are coded checks; use agent=reviewer (parse bookkeeping only) or keep agent=reviewer — refresh rewrites non-default agent to reviewer",
				where, tr.Agent, coded,
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
		coded := false
		for _, s := range skills {
			if skillIsCoded(s) {
				coded = true
				break
			}
		}
		if !coded {
			return line, false, FormatFinding{}
		}
		// Rewrite agent=<non-default> → agent=reviewer.
		rewritten := agentAttrRE.ReplaceAllString(line, `agent=reviewer`)
		// tidy if the regex left spacing odd
		rewritten = regexp.MustCompile(`agent=reviewer\s*=`).ReplaceAllString(rewritten, "agent=reviewer")
		if rewritten == line {
			// Fallback: replace agent=<value> token more carefully
			rewritten = replaceAgentAttr(line, "reviewer")
		}
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

	// Node declaration.
	id, attrs := dotNodeDecl(t)
	if id == "" || attrs == nil {
		return line, false, FormatFinding{}
	}
	agent := attrs["agent"]
	if agent == "" {
		return line, false, FormatFinding{}
	}
	prompt := attrs["prompt"]
	if !strings.HasPrefix(prompt, "@skill:") {
		return line, false, FormatFinding{}
	}
	skill := strings.TrimPrefix(prompt, "@skill:")
	// prompt may be CSV for multi-skill nodes (rare); treat first token.
	if i := strings.Index(skill, ","); i >= 0 {
		skill = skill[:i]
	}
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

// agentAttrRE matches agent=value (quoted or bare) for rewrite-to-reviewer on edges.
var agentAttrRE = regexp.MustCompile(`(?i)agent\s*=\s*"[^"]*"|agent\s*=\s*'[^']*'|agent\s*=\s*[A-Za-z0-9_-]+`)

// stripAgentAttr removes agent=… from a node/edge attribute list.
func stripAgentAttr(line string) string {
	cleaned := agentAttrRE.ReplaceAllString(line, "")
	cleaned = regexp.MustCompile(`,\s*,`).ReplaceAllString(cleaned, ",")
	cleaned = regexp.MustCompile(`\[\s*,`).ReplaceAllString(cleaned, "[")
	cleaned = regexp.MustCompile(`,\s*\]`).ReplaceAllString(cleaned, "]")
	cleaned = regexp.MustCompile(`\s{2,}`).ReplaceAllString(cleaned, " ")
	// empty attr list → drop brackets? keep [] is odd; collapse empty []
	cleaned = regexp.MustCompile(`\[\s*\]`).ReplaceAllString(cleaned, "")
	return cleaned
}

// replaceAgentAttr sets agent=<name> on a line that already has agent=.
func replaceAgentAttr(line, name string) string {
	return agentAttrRE.ReplaceAllString(line, "agent="+name)
}
