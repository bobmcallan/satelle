package wfdot

import (
	"fmt"
	"regexp"
	"strings"
)

// RefreshReport summarises a Refresh pass: what was applied and what still needs
// an operator-supplied --prompt mapping.
type RefreshReport struct {
	Applied []FormatFinding // changes that would be / were written
	Gaps    []FormatFinding // remaining format lag (e.g. promptless without mapping)
}

// Refresh rewrites a workflow body toward the canonical latest DOT form by a
// SURGICAL edit of the fenced ```dot block — never a lossy Spec→emitDOT re-emit.
// Topology, on= reviewers, comments, guardrails, and prose outside the touched
// attributes are preserved byte-for-byte.
//
// Auto-applied (deterministic, skill-neutral):
//   - edge reviewer_skill="…" → [agent=reviewer, prompt="@skill:…"]
//   - missing rankdir=LR
//   - missing graph [goal/vars] (TODO placeholders when absent)
//
// Operator-supplied: prompts maps node name → skill name for prompt-less
// performing nodes. Unmapped promptless performing nodes stay as Gaps.
//
// Idempotent: already-canonical input yields changed=false and empty Applied.
func Refresh(body string, prompts map[string]string) (newBody string, changed bool, report RefreshReport) {
	block := dotBlock(body)
	if block == "" {
		return body, false, report
	}
	newBlock, applied, gaps := refreshDOTBlock(block, prompts)
	report.Applied = applied
	report.Gaps = gaps
	if newBlock == block {
		return body, false, report
	}
	out, ok := replaceDotBlock(body, newBlock)
	if !ok {
		return body, false, report
	}
	return out, true, report
}

// refreshDOTBlock rewrites the interior of a digraph (no fences).
func refreshDOTBlock(block string, prompts map[string]string) (string, []FormatFinding, []FormatFinding) {
	var applied, gaps []FormatFinding
	stmts := dotStatements(block)
	// Rebuild from original block lines carefully: we rewrite statement text
	// but keep order. Simpler approach: walk the full block line-by-line and
	// rewrite edge attribute lists + node decls.
	lines := strings.Split(block, "\n")
	var out []string
	hasRankdir, hasGraphGoal, hasGraphVars := false, false, false
	// First pass: detect graph attrs
	for _, ln := range lines {
		low := strings.ToLower(ln)
		if strings.Contains(low, "rankdir") {
			hasRankdir = true
		}
		if strings.Contains(ln, "goal=") || strings.Contains(ln, "goal =") {
			hasGraphGoal = true
		}
		if strings.Contains(ln, "vars=") || strings.Contains(ln, "vars =") {
			hasGraphVars = true
		}
	}

	// Find digraph open line to insert missing graph attrs after it.
	insertedHeader := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		// Insert missing graph header after digraph { line once.
		if !insertedHeader && strings.HasPrefix(t, "digraph") && strings.Contains(t, "{") {
			out = append(out, ln)
			if !hasRankdir || !hasGraphGoal || !hasGraphVars {
				if !hasRankdir {
					out = append(out, "  rankdir=LR")
					applied = append(applied, FormatFinding{
						Kind: "missing_graph_attr", Where: "graph",
						Detail: "inserted rankdir=LR",
					})
				}
				if !hasGraphGoal || !hasGraphVars {
					// Single graph […] line with placeholders for missing fields.
					goal := `goal="TODO: workflow goal"`
					vars := `vars="TODO: story, repo_root"`
					// If one is already present on another line, still add a graph line for the missing one —
					// keep it simple: only insert a full graph line when NEITHER goal nor vars exists.
					if !hasGraphGoal && !hasGraphVars {
						out = append(out, fmt.Sprintf("  graph [%s, %s]", goal, vars))
						applied = append(applied, FormatFinding{
							Kind: "missing_graph_attr", Where: "graph",
							Detail: "inserted graph [goal=…, vars=…] placeholders",
						})
					} else if !hasGraphGoal {
						out = append(out, fmt.Sprintf("  graph [%s]", goal))
						applied = append(applied, FormatFinding{
							Kind: "missing_graph_attr", Where: "graph",
							Detail: "inserted graph [goal=…] placeholder",
						})
					} else if !hasGraphVars {
						out = append(out, fmt.Sprintf("  graph [%s]", vars))
						applied = append(applied, FormatFinding{
							Kind: "missing_graph_attr", Where: "graph",
							Detail: "inserted graph [vars=…] placeholder",
						})
					}
				}
			}
			insertedHeader = true
			continue
		}

		// Edge with legacy reviewer_skill=
		if strings.Contains(t, "->") && strings.Contains(t, "reviewer_skill") {
			rewritten, ok, finding := rewriteLegacyEdge(ln)
			if ok {
				out = append(out, rewritten)
				applied = append(applied, finding)
				continue
			}
		}

		// Performing node without prompt — apply --prompt mapping if present.
		if id, attrs := dotNodeDecl(t); id != "" {
			agent := attrs["agent"]
			prompt := attrs["prompt"]
			if agent != "" && agent != "reviewer" && !strings.HasPrefix(prompt, "@skill:") {
				if skill, ok := prompts[id]; ok && skill != "" {
					rewritten := addNodePrompt(ln, id, skill)
					out = append(out, rewritten)
					applied = append(applied, FormatFinding{
						Kind: "performing_prompt", Where: id,
						Detail: fmt.Sprintf("added prompt=\"@skill:%s\" to performing node %s", skill, id),
					})
					continue
				}
				// Gap: needs operator mapping
				gaps = append(gaps, FormatFinding{
					Kind: "promptless_performing", Where: id,
					Detail: fmt.Sprintf("performing node %s [agent=%s] still needs --prompt %s=<skill>", id, agent, id),
				})
			}
		}

		// Strip superseded model= (sty_a476a2f8) from any statement that carries it.
		if strings.Contains(t, "model=") {
			stripped, ok, finding := stripModelAttr(ln)
			if ok {
				out = append(out, stripped)
				applied = append(applied, finding)
				continue
			}
		}

		out = append(out, ln)
	}
	_ = stmts // reserved for future multi-line statement rewrites
	return strings.Join(out, "\n"), applied, gaps
}

// stripModelAttr removes model="…" / model=… from a DOT statement line.
func stripModelAttr(line string) (string, bool, FormatFinding) {
	if !strings.Contains(line, "model=") {
		return line, false, FormatFinding{}
	}
	cleaned := modelAttrRE.ReplaceAllString(line, "")
	// tidy double commas / spaces left by removal
	cleaned = regexp.MustCompile(`,\s*,`).ReplaceAllString(cleaned, ",")
	cleaned = regexp.MustCompile(`\[\s*,`).ReplaceAllString(cleaned, "[")
	cleaned = regexp.MustCompile(`,\s*\]`).ReplaceAllString(cleaned, "]")
	cleaned = strings.ReplaceAll(cleaned, "  ", " ")
	if cleaned == line {
		return line, false, FormatFinding{}
	}
	where := "statement"
	if strings.Contains(line, "->") {
		ids := dotEdgeNodes(line)
		if len(ids) >= 2 {
			where = ids[0] + "->" + ids[len(ids)-1]
		}
	} else if id, _ := dotNodeDecl(line); id != "" {
		where = id
	}
	return cleaned, true, FormatFinding{
		Kind:   "legacy_model_attr",
		Where:  where,
		Detail: "stripped superseded model= attribute (agents.toml owns model; allocate with agent=<name>)",
	}
}

var modelAttrRE = regexp.MustCompile(`(?i),?\s*model\s*=\s*"[^"]*"\s*,?|,?\s*model\s*=\s*'[^']*'\s*,?|,?\s*model\s*=\s*[^\s,\]]+\s*,?`)

// rewriteLegacyEdge converts reviewer_skill= on an edge line to node-consistent form.
func rewriteLegacyEdge(line string) (string, bool, FormatFinding) {
	attrs := edgeAttrs(line)
	if attrs == nil || attrs["reviewer_skill"] == "" {
		return line, false, FormatFinding{}
	}
	skills := splitCSVSkills(attrs["reviewer_skill"])
	if len(skills) == 0 {
		return line, false, FormatFinding{}
	}
	skillCSV := strings.Join(skills, ",")
	ids := dotEdgeNodes(line)
	where := "edge"
	if len(ids) >= 2 {
		where = ids[0] + "->" + ids[len(ids)-1]
	}

	// Replace the attribute list. Prefer rewriting the whole […] block.
	open := strings.Index(line, "[")
	closeAt := strings.LastIndex(line, "]")
	if open < 0 || closeAt < open {
		return line, false, FormatFinding{}
	}
	// Drop reviewer_skill; if agent/prompt already present keep them, else set node-consistent.
	// Rebuild attrs: start from existing minus reviewer_skill.
	var parts []string
	// Preserve non-gate attrs in original order by scanning the raw attr string.
	raw := line[open+1 : closeAt]
	// Remove reviewer_skill=… (quoted or unquoted) via regex.
	cleaned := reviewerSkillAttrRE.ReplaceAllString(raw, "")
	cleaned = strings.Trim(strings.TrimSpace(cleaned), ",")
	cleaned = regexp.MustCompile(`,\s*,`).ReplaceAllString(cleaned, ",")
	cleaned = strings.Trim(strings.TrimSpace(cleaned), ",")

	hasAgent := strings.Contains(cleaned, "agent=")
	hasPrompt := strings.Contains(cleaned, "prompt=")
	if !hasAgent && !hasPrompt {
		parts = append(parts, fmt.Sprintf(`agent=reviewer, prompt="@skill:%s"`, skillCSV))
	} else {
		// Already partially node-consistent — just drop legacy.
		if cleaned != "" {
			parts = append(parts, cleaned)
		} else {
			parts = append(parts, fmt.Sprintf(`agent=reviewer, prompt="@skill:%s"`, skillCSV))
		}
	}
	newAttrs := strings.Join(parts, ", ")
	// Clean double commas / spaces
	newAttrs = regexp.MustCompile(`\s*,\s*,\s*`).ReplaceAllString(newAttrs, ", ")
	newAttrs = strings.TrimSpace(newAttrs)
	rewritten := line[:open+1] + newAttrs + line[closeAt:]
	return rewritten, true, FormatFinding{
		Kind:   "legacy_edge_gate",
		Where:  where,
		Detail: fmt.Sprintf("rewrote reviewer_skill to [agent=reviewer, prompt=\"@skill:%s\"]", skillCSV),
	}
}

var reviewerSkillAttrRE = regexp.MustCompile(`(?i),?\s*reviewer_skill\s*=\s*"[^"]*"\s*,?|,?\s*reviewer_skill\s*=\s*'[^']*'\s*,?`)

// addNodePrompt inserts prompt="@skill:NAME" into a node declaration line.
func addNodePrompt(line, id, skill string) string {
	open := strings.Index(line, "[")
	closeAt := strings.LastIndex(line, "]")
	prompt := fmt.Sprintf(`prompt="@skill:%s"`, skill)
	if open >= 0 && closeAt > open {
		inner := strings.TrimSpace(line[open+1 : closeAt])
		if inner == "" {
			return line[:open+1] + prompt + line[closeAt:]
		}
		return line[:open+1] + inner + ", " + prompt + line[closeAt:]
	}
	// No attrs yet: id alone or id with trailing comment
	// Insert [prompt=…] after the node id.
	// Find id token end.
	trimmed := strings.TrimLeft(line, " \t")
	pad := line[:len(line)-len(trimmed)]
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return line
	}
	// Rest after id (comments etc.)
	rest := strings.TrimPrefix(trimmed, fields[0])
	return pad + fields[0] + " [" + prompt + "]" + rest
}

// replaceDotBlock swaps the first fenced ```dot … ``` block's interior for newBlock.
func replaceDotBlock(body, newBlock string) (string, bool) {
	lines := strings.Split(body, "\n")
	inFence, fenceStart := false, -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if !inFence {
			if strings.HasPrefix(t, "```dot") {
				inFence, fenceStart = true, i
			}
			continue
		}
		if strings.HasPrefix(t, "```") {
			// Replace lines fenceStart+1 .. i-1 with newBlock lines.
			var out []string
			out = append(out, lines[:fenceStart+1]...)
			out = append(out, strings.Split(newBlock, "\n")...)
			out = append(out, lines[i:]...)
			return strings.Join(out, "\n"), true
		}
	}
	return body, false
}
