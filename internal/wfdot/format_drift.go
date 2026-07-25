package wfdot

import (
	"fmt"
	"strings"
)

// FormatFinding is one deterministic FORMAT-drift observation against the
// canonical latest workflow DOT form (satelle-dot-standard / sty_ccf41efa).
// Distinct from binding-drift (workflow ↔ agents.toml).
type FormatFinding struct {
	// Kind classifies the lag: legacy_edge_gate | promptless_performing | missing_graph_attr.
	Kind string
	// Where names the node, edge (from->to), or "graph".
	Where string
	// Detail is a human-readable description including the canonical fix.
	Detail string
}

// FormatDrift reports format lag of a workflow body against the canonical latest
// form. It is DETERMINISTIC (source DOT scan + parsed Spec) — not prose guesswork.
// ok is false when the body has no parseable ```dot block (inline-YAML is itself
// legacy input; callers may treat that separately). Empty findings mean no format
// drift. Repo-specific topology (extra states, recovery edges, on= reviewers) is
// never reported as format drift.
func FormatDrift(body string) (findings []FormatFinding, ok bool) {
	block := dotBlock(body)
	if block == "" {
		return nil, false
	}
	spec, parsed := Parse(body)
	if !parsed {
		return nil, false
	}

	// 1. Legacy edge-gate syntax: reviewer_skill= still in the SOURCE.
	// Walk raw statements so we name the edge even when the parser accepts both.
	for _, stmt := range dotStatements(block) {
		t := strings.TrimSpace(stmt)
		if t == "" || !strings.Contains(t, "->") {
			continue
		}
		if !strings.Contains(t, "reviewer_skill") {
			continue
		}
		// Confirm it is an attribute key, not a comment word.
		attrs := edgeAttrs(t)
		if attrs["reviewer_skill"] == "" {
			continue
		}
		ids := dotEdgeNodes(t)
		where := "edge"
		if len(ids) >= 2 {
			where = ids[0] + "->" + ids[len(ids)-1]
		}
		skills := splitCSVSkills(attrs["reviewer_skill"])
		canonical := strings.Join(skills, ",")
		findings = append(findings, FormatFinding{
			Kind:   "legacy_edge_gate",
			Where:  where,
			Detail: fmt.Sprintf("edge uses legacy reviewer_skill=%q; canonical is [agent=reviewer, prompt=\"@skill:%s\"]", attrs["reviewer_skill"], canonical),
		})
	}

	// 1b. Legacy model= attribute (sty_a476a2f8): agents.toml owns model.
	for _, stmt := range dotStatements(block) {
		t := strings.TrimSpace(stmt)
		if t == "" || !strings.Contains(t, "model=") {
			continue
		}
		// attribute form model="…" or model=…
		if !strings.Contains(t, "model=\"") && !strings.Contains(t, "model='") && !strings.Contains(t, "model=") {
			continue
		}
		where := "graph"
		if strings.Contains(t, "->") {
			ids := dotEdgeNodes(t)
			if len(ids) >= 2 {
				where = ids[0] + "->" + ids[len(ids)-1]
			} else {
				where = "edge"
			}
		} else if id, _ := dotNodeDecl(t); id != "" {
			where = id
		}
		findings = append(findings, FormatFinding{
			Kind:   "legacy_model_attr",
			Where:  where,
			Detail: "model= is superseded — define an agents.toml binding with the desired model and allocate it with agent=<name>; satelle workflow refresh strips model=",
		})
	}

	// 2. Prompt-less performing nodes (agent set, not reviewer, no @skill prompt).
	// Always-on gate nodes with on= are reviewers-by-role even if mis-tagged; we
	// only flag performing roles: executor and named non-reviewer agents.
	for _, st := range spec.States {
		if st.Agent == "" || st.Agent == "reviewer" {
			continue
		}
		if len(st.On) > 0 {
			continue // scoped always-on reviewer declared with wrong agent — not format lag of performing convention
		}
		if st.Skill != "" {
			continue
		}
		// Terminal pure markers (done with agent= but no skill) can be shape-only;
		// still flag performing agents on non-terminal / work path.
		if st.Terminal && st.Agent == "executor" {
			// rare; still a lag if an executor is terminal without a rubric
		}
		findings = append(findings, FormatFinding{
			Kind:   "promptless_performing",
			Where:  st.Name,
			Detail: fmt.Sprintf("performing node %s [agent=%s] has no prompt=\"@skill:…\"; canonical performing nodes carry an explicit rubric", st.Name, st.Agent),
		})
	}

	// 3. Missing required graph attrs (goal, vars, rankdir) — documented shape,
	// not topology. Detect from the raw digraph / graph line and rankdir= line.
	hasGoal, hasVars, hasRankdir := false, false, false
	for _, stmt := range dotStatements(block) {
		t := strings.TrimSpace(stmt)
		low := strings.ToLower(t)
		if strings.HasPrefix(low, "graph ") || strings.Contains(low, "graph [") {
			if strings.Contains(t, "goal=") || strings.Contains(t, "goal =") {
				hasGoal = true
			}
			if strings.Contains(t, "vars=") || strings.Contains(t, "vars =") {
				hasVars = true
			}
		}
		if strings.HasPrefix(low, "rankdir") || strings.Contains(low, "rankdir=") {
			hasRankdir = true
		}
		// rankdir may appear inside a graph […] block too
		if strings.Contains(low, "rankdir=") {
			hasRankdir = true
		}
	}
	// Also scan the whole block for rankdir (may be its own statement).
	if strings.Contains(strings.ToLower(block), "rankdir") {
		hasRankdir = true
	}
	if !hasGoal {
		findings = append(findings, FormatFinding{
			Kind:   "missing_graph_attr",
			Where:  "graph",
			Detail: `graph is missing goal="…"; canonical form declares graph [goal="…", vars="…"]`,
		})
	}
	if !hasVars {
		findings = append(findings, FormatFinding{
			Kind:   "missing_graph_attr",
			Where:  "graph",
			Detail: `graph is missing vars="…"; canonical form declares graph [goal="…", vars="…"]`,
		})
	}
	if !hasRankdir {
		findings = append(findings, FormatFinding{
			Kind:   "missing_graph_attr",
			Where:  "graph",
			Detail: "graph is missing rankdir=LR; canonical form sets rankdir=LR",
		})
	}

	return findings, true
}

// edgeAttrs returns the attribute map of an edge statement, or nil.
func edgeAttrs(stmt string) map[string]string {
	open := strings.Index(stmt, "[")
	if open < 0 {
		return nil
	}
	closeAt := strings.LastIndex(stmt, "]")
	if closeAt < open {
		return nil
	}
	return parseDotAttrs(stmt[open+1 : closeAt])
}
