// Package reviewer runs an isolated, fresh-context reviewer over a requested
// status transition and returns its verdict — the quality-management spine.
// Mirrors satellites' request_review_dispatcher: the active workflow names a
// reviewer_skill per edge; the skill's markdown body rides as the agent's
// appended system prompt; the work item + requested transition go in on stdin;
// the agent prints one JSON object {decision, notes}, parsed strictly into an
// accept/reject. Accept lets the caller enact; reject blocks and pushes the
// notes back to the executor.
//
// The edge is gated only when the workflow names a reviewer_skill AND that
// skill's rubric is installed in the substrate. A named-but-absent rubric (e.g.
// the canonical default referencing a skill not yet embedded) is treated as
// advisory, so gating switches on exactly when the rubrics ship — the gateless
// baseline keeps working until then.
package reviewer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// DocGetter is the read surface the gater needs over the authored-doc index
// (satisfied by *docindex.Store) — listing workflows (to resolve the one active
// for an item's category) and getting the reviewer skills / the baseline.
type DocGetter interface {
	Get(ctx context.Context, kind, name string) (docindex.Doc, error)
	List(ctx context.Context, kind string) ([]docindex.Doc, error)
}

// defaultTools is the reviewer's read-only tool grant — it judges, never mutates.
const defaultTools = "Read,Grep,Glob"

// baselineWorkflow is the workflow doc whose transitions carry the reviewer
// skills. The repo override or the embedded canonical resolves under this name.
const baselineWorkflow = "satelle-baseline-workflow"

// Gater judges status transitions against the active workflow's reviewer skills,
// running each as an isolated agent subprocess.
type Gater struct {
	runner   agentcli.Runner
	docs     DocGetter
	repoRoot string
	model    string
	tools    string
}

// New builds a Gater over the agent runner and doc index. model "" inherits the
// agent's default; the tool grant is read-only.
func New(runner agentcli.Runner, docs DocGetter, repoRoot, model string) *Gater {
	return &Gater{runner: runner, docs: docs, repoRoot: repoRoot, model: model, tools: defaultTools}
}

// transitionPayload is the JSON delivered to the reviewer on stdin.
type transitionPayload struct {
	Story       workitem.Item `json:"story"`
	From        string        `json:"from"`
	To          string        `json:"to"`
	ReviewSkill string        `json:"review_skill"`
}

// Gate judges item's transition to toStatus. It returns Gated=false (enact
// directly) when no reviewer skill governs the edge; otherwise it runs the
// isolated reviewer and returns its accept/reject verdict.
func (g *Gater) Gate(ctx context.Context, item workitem.Item, toStatus string) (verb.GateDecision, error) {
	skill, err := g.reviewerSkill(ctx, item.Category, item.Status, toStatus)
	if err != nil {
		return verb.GateDecision{}, err
	}
	if skill == "" {
		return verb.GateDecision{Gated: false}, nil // ungated edge — advisory
	}
	body, err := g.skillBody(ctx, skill)
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			// Workflow names a reviewer skill whose rubric is not installed yet —
			// treat as advisory until the rubric ships (keeps fresh repos working).
			return verb.GateDecision{Gated: false}, nil
		}
		return verb.GateDecision{}, err
	}
	if g.runner == nil {
		return verb.GateDecision{Gated: true}, fmt.Errorf(
			"reviewer: transition %s→%s is gated by %q but no agent runner is configured", item.Status, toStatus, skill)
	}
	payload, err := json.Marshal(transitionPayload{Story: item, From: item.Status, To: toStatus, ReviewSkill: skill})
	if err != nil {
		return verb.GateDecision{}, err
	}
	out, err := g.runner.Run(ctx, agentcli.Request{
		SystemPrompt: body,
		Payload:      string(payload),
		AllowedTools: g.tools,
		Model:        g.model,
		Dir:          g.repoRoot,
	})
	if err != nil {
		return verb.GateDecision{Gated: true}, fmt.Errorf("reviewer: %s gate failed: %w", skill, err)
	}
	dec, err := parseDecision(out)
	if err != nil {
		return verb.GateDecision{Gated: true}, fmt.Errorf("reviewer: %s: %w", skill, err)
	}
	dec.Gated = true
	dec.Skill = skill
	return dec, nil
}

// structureSkill is the required-structure reviewer that judges a draft work
// item at creation. Embedded by default; overridable under .satelle/skills.
const structureSkill = "satelle-story-structure-review"

// summariserSkill recaps an enacted transition. Embedded by default; overridable.
const summariserSkill = "satelle-step-summary"

// summaryPayload is the JSON handed to the summariser on stdin.
type summaryPayload struct {
	Story workitem.Item `json:"story"`
	From  string        `json:"from"`
	To    string        `json:"to"`
}

// Summarise runs the read-only summariser over an enacted transition and returns
// its prose recap (empty when no summariser rubric is installed). The reviewer's
// read-only tool grant means it observes but cannot mutate the work tree.
func (g *Gater) Summarise(ctx context.Context, item workitem.Item, from, to string) (string, error) {
	body, err := g.skillBody(ctx, summariserSkill)
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			return "", nil // no summariser rubric installed — nothing to record
		}
		return "", err
	}
	if g.runner == nil {
		return "", nil
	}
	payload, err := json.Marshal(summaryPayload{Story: item, From: from, To: to})
	if err != nil {
		return "", err
	}
	out, err := g.runner.Run(ctx, agentcli.Request{
		SystemPrompt: body,
		Payload:      string(payload),
		AllowedTools: g.tools, // read-only (Read,Grep,Glob) — narrate, never mutate
		Model:        g.model,
		Dir:          g.repoRoot,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ReviewCreate judges a draft work item's required structure before it is
// persisted. Gated=false (advisory, persist) when the structure rubric is not
// installed; otherwise it runs the isolated reviewer and returns its verdict.
func (g *Gater) ReviewCreate(ctx context.Context, draft verb.CreateDraft) (verb.GateDecision, error) {
	body, err := g.skillBody(ctx, structureSkill)
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			return verb.GateDecision{Gated: false}, nil
		}
		return verb.GateDecision{}, err
	}
	if g.runner == nil {
		return verb.GateDecision{Gated: true}, fmt.Errorf("reviewer: create-gating is on but no agent runner is configured")
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return verb.GateDecision{}, err
	}
	out, err := g.runner.Run(ctx, agentcli.Request{
		SystemPrompt: body,
		Payload:      string(payload),
		AllowedTools: g.tools,
		Model:        g.model,
		Dir:          g.repoRoot,
	})
	if err != nil {
		return verb.GateDecision{Gated: true}, fmt.Errorf("reviewer: %s gate failed: %w", structureSkill, err)
	}
	dec, err := parseDecision(out)
	if err != nil {
		return verb.GateDecision{Gated: true}, fmt.Errorf("reviewer: %s: %w", structureSkill, err)
	}
	dec.Gated = true
	dec.Skill = structureSkill
	return dec, nil
}

// reviewerSkill resolves the reviewer_skill governing the (from→to) edge from
// the workflow active for the item's category. An absent workflow means no
// gating.
func (g *Gater) reviewerSkill(ctx context.Context, category, from, to string) (string, error) {
	doc, err := g.activeWorkflow(ctx, category)
	if errors.Is(err, docindex.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return reviewerSkillFor(doc.Body, from, to), nil
}

// activeWorkflow returns the workflow doc governing an item of the given
// category. Selection matches the item's category against each indexed
// workflow's `applies_to` frontmatter: a workflow listing the category wins; a
// wildcard (`applies_to: ["*"]`) workflow is the next-best; the embedded
// baseline (resolved by name) is the final fallback. This is the
// configuration-over-code path — a repo adds a category-specific workflow as
// substrate and it takes effect with no binary change. A List error degrades to
// the baseline so gating never silently disappears.
func (g *Gater) activeWorkflow(ctx context.Context, category string) (docindex.Doc, error) {
	if workflows, err := g.docs.List(ctx, "workflows"); err == nil {
		var wildcard *docindex.Doc
		for i := range workflows {
			at := frontmatterList(workflows[i].Body, "applies_to")
			if category != "" && containsStr(at, category) {
				return workflows[i], nil // a specific category match wins
			}
			if wildcard == nil && containsStr(at, "*") {
				w := workflows[i]
				wildcard = &w
			}
		}
		if wildcard != nil {
			return *wildcard, nil
		}
	}
	return g.docs.Get(ctx, "workflows", baselineWorkflow)
}

// frontmatterList parses a list-valued key from a markdown frontmatter block,
// handling both the inline flow form (`applies_to: ["*", "web"]`) and the block
// list form (`applies_to:` then `- web` lines). Returns nil when absent.
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
		if strings.HasPrefix(rest, "[") { // inline flow form
			rest = strings.TrimSuffix(strings.TrimPrefix(rest, "["), "]")
			return splitTrimList(rest)
		}
		var out []string // block list form
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

// splitTrimList splits a comma-separated inline list, trimming whitespace and
// surrounding quotes, dropping empties.
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

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// skillBody returns the reviewer skill's markdown body from the substrate.
func (g *Gater) skillBody(ctx context.Context, name string) (string, error) {
	doc, err := g.docs.Get(ctx, "skills", name)
	if err != nil {
		return "", err
	}
	return doc.Body, nil
}

// reviewerSkillFor scans a workflow body's transition lines for the (from→to)
// edge and returns its reviewer_skill (empty if the edge is ungated or absent).
// The transition format is the fixed inline-map shape the substrate uses:
//
//   - {from: backlog, to: in_progress, reviewer_skill: "satelle-intent-plan-review"}
func reviewerSkillFor(body, from, to string) string {
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "- {") || !strings.Contains(l, "from:") || !strings.Contains(l, "to:") {
			continue
		}
		if inlineField(l, "from") == from && inlineField(l, "to") == to {
			return inlineField(l, "reviewer_skill")
		}
	}
	return ""
}

// inlineField extracts key's value from an inline-map line, trimming quotes. The
// value runs to the next comma or closing brace.
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

// rawDecision is the reviewer's JSON contract: {decision: accept|reject, notes}.
type rawDecision struct {
	Decision string `json:"decision"`
	Notes    string `json:"notes"`
}

// parseDecision finds the reviewer's verdict in the agent's stdout — lenient on
// surrounding prose, extra wrapping braces, and example objects, but strict on
// shape. It scans every balanced {…} candidate and returns the LAST that yields a
// decision in {accept, reject}: a model reasons then concludes, so its final
// verdict wins over any format example it echoed earlier.
func parseDecision(out []byte) (verb.GateDecision, error) {
	var found *verb.GateDecision
	for _, obj := range jsonObjectCandidates(out) {
		var rd rawDecision
		if err := json.Unmarshal(obj, &rd); err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(rd.Decision)) {
		case "accept":
			d := verb.GateDecision{Accept: true, Notes: rd.Notes}
			found = &d
		case "reject":
			d := verb.GateDecision{Accept: false, Notes: rd.Notes}
			found = &d
		}
	}
	if found != nil {
		return *found, nil
	}
	return verb.GateDecision{}, fmt.Errorf("no {\"decision\": \"accept\"|\"reject\"} object in reviewer output")
}

// jsonObjectCandidates returns every balanced {…} substring, trying each '{'
// start so wrapping braces (e.g. {{…}}), prose, or a code-fenced example don't
// defeat extraction. Brace counting is string-aware so a '{' inside the notes
// text does not unbalance it.
func jsonObjectCandidates(b []byte) [][]byte {
	var out [][]byte
	for i := 0; i < len(b); i++ {
		if b[i] == '{' {
			if end := balancedEnd(b, i); end > i {
				out = append(out, b[i:end+1])
			}
		}
	}
	return out
}

// balancedEnd returns the index of the '}' that closes the '{' at i, ignoring
// braces inside JSON strings, or -1 if unbalanced.
func balancedEnd(b []byte, i int) int {
	depth, inStr, esc := 0, false, false
	for j := i; j < len(b); j++ {
		c := b[j]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}
