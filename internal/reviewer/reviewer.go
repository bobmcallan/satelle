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
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/structure"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// DocGetter is the read surface the gater needs over the authored-doc index
// (satisfied by *docindex.Store) — listing workflows (to resolve the one active
// for an item's category) and getting the reviewer skills / the baseline.
type DocGetter interface {
	Get(ctx context.Context, kind, name string) (docindex.Doc, error)
	List(ctx context.Context, kind string) ([]docindex.Doc, error)
}

// defaultTools is the reviewer's tool grant. It judges, never MUTATES the work
// tree (Write/Edit/NotebookEdit are denied by the harness ceiling), but it is
// granted scoped, read-only `satelle` CLI access so it can resolve the substrate
// it reasons about — skills and principles, including EMBEDDED defaults that are
// not files on disk and so are invisible to Read/Grep/Glob. Bash is scoped to the
// `satelle` binary by the Bash(satelle:*) specifier; the harness denylist keeps
// every mutating tool off, so this is read-only access, not a general shell.
const defaultTools = "Read,Grep,Glob,Bash(satelle:*)"

// baselineWorkflow is the workflow doc whose transitions carry the reviewer
// skills. The repo override or the embedded canonical resolves under this name.
const baselineWorkflow = "satelle-baseline-workflow"

// defaultCheckTimeout bounds a functional check (deploy/integration can be slow,
// but a hung command must not block a transition forever).
const defaultCheckTimeout = 20 * time.Minute

// alwaysReviewerTag marks a skill as an always-on SYSTEM reviewer: the gater
// discovers every skill whose frontmatter tags carry it and runs them on a
// gated transition AFTER the workflow-named reviewers (the system layer always
// runs last). Which skills carry the tag is authored substrate, not a binary
// branch — so the system layer is configured, not compiled.
const alwaysReviewerTag = "reviewer:always"

// Gater judges status transitions against the active workflow's reviewer skills.
// A skill is either an LLM reviewer (its body rides as an isolated agent's system
// prompt) or a functional check (its frontmatter names a deterministic `check:`
// command the gate runs — the command's exit code is the verdict).
type Gater struct {
	runner       agentcli.Runner
	docs         DocGetter
	repoRoot     string
	model        string
	tools        string
	checkTimeout time.Duration
	// check runs a functional-check command in dir and returns its combined
	// output. Swappable in tests; defaults to a real `sh -c` exec.
	check func(ctx context.Context, dir, command string) (string, error)
}

// New builds a Gater over the agent runner and doc index. model "" inherits the
// agent's default; the tool grant is read-only.
func New(runner agentcli.Runner, docs DocGetter, repoRoot, model string) *Gater {
	return &Gater{
		runner: runner, docs: docs, repoRoot: repoRoot, model: model, tools: defaultTools,
		checkTimeout: defaultCheckTimeout, check: execCheck,
	}
}

// SetReviewerTools sets the reviewer's tool grant from the actors layer (the
// resolved `reviewer` binding). It governs every isolated LLM reviewer this Gater
// runs. The default remains the read-only grant; a repo may widen or narrow it in
// .satelle/actors.toml without touching the workflow. An empty value is ignored
// so callers can pass through an unset binding safely.
func (g *Gater) SetReviewerTools(tools string) {
	if strings.TrimSpace(tools) != "" {
		g.tools = tools
	}
}

// SetRunner overrides the reviewer's agent-CLI runner — the actors layer's
// `reviewer` harness binding, resolved to a Runner. A nil runner is ignored,
// keeping the default configured at construction (the global `[agent] cli`).
func (g *Gater) SetRunner(r agentcli.Runner) {
	if r != nil {
		g.runner = r
	}
}

// execCheck runs command via `bash -c` in dir, returning combined stdout+stderr.
// bash (not sh) so a multi-line self-contained check embedded in a skill may use
// ordinary shell scripting.
func execCheck(ctx context.Context, dir, command string) (string, error) {
	c := exec.CommandContext(ctx, "bash", "-c", command)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}

// transitionPayload is the JSON delivered to the reviewer on stdin.
type transitionPayload struct {
	Story       workitem.Item `json:"story"`
	From        string        `json:"from"`
	To          string        `json:"to"`
	ReviewSkill string        `json:"review_skill"`
}

// reviewerCallToAction is appended to a reviewer's injected context. It tells the
// isolated reviewer it has read-only `satelle` CLI access and should resolve any
// principle or skill its rubric references but does not inline — including
// EMBEDDED defaults that are not files on disk — rather than assuming absence.
const reviewerCallToAction = "## You are an isolated satelle reviewer\n\n" +
	"You judge only — you CANNOT modify the repository. You DO have read-only " +
	"`satelle` CLI access: to resolve anything this rubric references but does not " +
	"inline, run e.g. `satelle doc get principles <name>`, `satelle doc get skills " +
	"<name>`, or `satelle doc list`. Do NOT conclude a skill or principle is missing " +
	"without checking via the CLI — an embedded default resolves even when no file " +
	"exists under .satelle/."

// reviewerSystemPrompt assembles the system prompt for an isolated reviewer: the
// always-resident principles (so it judges with the resident set the executor
// also sees), the read-only call-to-action, then the reviewer's own rubric.
func (g *Gater) reviewerSystemPrompt(ctx context.Context, rubric string) string {
	var b strings.Builder
	if resident := g.alwaysPrinciples(ctx); resident != "" {
		b.WriteString("# Always-resident principles (satelle)\n\n")
		b.WriteString(resident)
		b.WriteString("\n\n")
	}
	b.WriteString(reviewerCallToAction)
	b.WriteString("\n\n---\n\n")
	b.WriteString(rubric)
	return b.String()
}

// alwaysPrinciples returns the body of the single always-resident principle —
// the operating principle (config.OperatingPrinciple), frontmatter stripped — so
// a reviewer judges with the same one resident principle the executor sees
// (sty_53a4233c). Empty when it does not resolve; injection is additive and must
// never break a gate.
func (g *Gater) alwaysPrinciples(ctx context.Context) string {
	d, err := g.docs.Get(ctx, "principles", config.OperatingPrinciple)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stripFrontmatter(d.Body))
}

// stripFrontmatter drops a leading `---`…`---` YAML block, returning the markdown
// body. Returns body unchanged when there is no frontmatter.
func stripFrontmatter(body string) string {
	s := strings.TrimLeft(body, "\n")
	if !strings.HasPrefix(s, "---") {
		return body
	}
	rest := s[len("---"):]
	i := strings.Index(rest, "\n---")
	if i < 0 {
		return body
	}
	after := rest[i+len("\n---"):]
	if nl := strings.IndexByte(after, '\n'); nl >= 0 {
		return strings.TrimLeft(after[nl+1:], "\n")
	}
	return ""
}

// Gate judges item's transition to toStatus against every reviewer governing the
// edge — the workflow-named reviewers (one, or an ordered list) followed by the
// always-on system reviewer layer. Each reviewer runs in order and ALL must
// accept; the first reject short-circuits and blocks the edge. It returns the
// per-reviewer verdicts in run order plus a top-level verdict mirroring the
// deciding reviewer (the first reject, or the last when all accept), so
// single-reviewer callers keep their contract. Gated=false (enact directly)
// when no reviewer governs the edge.
func (g *Gater) Gate(ctx context.Context, item workitem.Item, toStatus string) (verb.GateDecision, error) {
	skills, declared, err := g.reviewerSkills(ctx, item.Category, item.Status, toStatus)
	if err != nil {
		return verb.GateDecision{}, err
	}
	if !declared {
		// The active workflow does not declare this edge — it is not a legal move.
		// Refuse it (the caller blocks the transition), so a story cannot skip a
		// gate by jumping across an edge the workflow never declared.
		return verb.GateDecision{}, fmt.Errorf(
			"transition %s→%s is not a declared edge in the active workflow", item.Status, toStatus)
	}
	// Before a story is IMPLEMENTED, guard against engaging it into a workflow that
	// cannot complete. On the ENGAGEMENT edge, deterministically (no agent) resolve
	// every EXECUTOR-step skill on the path to done: an executor step whose rubric
	// is missing leaves that step unperformable (the wasted-work trap — e.g. a
	// removed commit-push). Reject engagement up front, naming the gap. This is the
	// fast, in-process complement to the LLM satelle-workflow-review, which judges
	// the workflow's full structure + actionability at create/update. Reviewer-gate
	// skills are NOT required here — a missing reviewer rubric degrades to advisory
	// by design, so fresh repos keep working.
	if dec, blocked, gerr := g.guardEngagementExecutorSkills(ctx, item, toStatus); gerr != nil {
		return verb.GateDecision{}, gerr
	} else if blocked {
		return dec, nil
	}
	// Append the always-on system reviewers AFTER the workflow-named ones — they
	// always run last. Skills already named on the edge are not duplicated, and a
	// reviewer that scopes itself with `on:` only joins on its target statuses.
	sys, err := g.systemReviewers(ctx, skills, toStatus)
	if err != nil {
		return verb.GateDecision{}, err
	}
	sysStart := len(skills)
	ordered := append(append([]string{}, skills...), sys...)

	var result verb.GateDecision
	for i, skill := range ordered {
		if skill == "" {
			continue
		}
		dec, rerr := g.runReviewer(ctx, item, toStatus, skill)
		if rerr != nil {
			return dec, rerr
		}
		if !dec.Gated {
			continue // declared but this reviewer's rubric is absent — advisory, skip it
		}
		result.Gated = true
		result.Skill = dec.Skill
		result.Accept = dec.Accept
		result.Notes = dec.Notes
		result.Reviewers = append(result.Reviewers, verb.ReviewerVerdict{
			Skill: skill, Order: i, Accept: dec.Accept, Notes: dec.Notes, System: i >= sysStart,
		})
		if !dec.Accept {
			return result, nil // a reject blocks the edge — do not run later reviewers
		}
	}
	return result, nil
}

// engagementSkillCheck is the synthetic reviewer name recorded when the
// deterministic engagement guard blocks because the active workflow's path to
// done has an executor step whose skill does not resolve.
const engagementSkillCheck = "satelle-workflow-skill-check"

// guardEngagementExecutorSkills is the fast, agent-free completability guard run
// on the ENGAGEMENT edge (leaving the workflow's start state for a non-cancel
// target). It resolves every EXECUTOR-step skill on the active workflow's path to
// done and returns blocked=true, naming any that do not resolve in the substrate
// (embedded ∪ project). It returns blocked=false to proceed: off the engagement
// edge, when the workflow is not parseable DOT, or when every executor skill
// resolves. A docs lookup error other than not-found is surfaced.
func (g *Gater) guardEngagementExecutorSkills(ctx context.Context, item workitem.Item, toStatus string) (verb.GateDecision, bool, error) {
	doc, err := g.activeWorkflow(ctx, item.Category)
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			return verb.GateDecision{}, false, nil
		}
		return verb.GateDecision{}, false, err
	}
	spec, ok := wfdot.Parse(doc.Body)
	if !ok || item.Status != spec.Start() || toStatus == "cancelled" {
		return verb.GateDecision{}, false, nil // not the engagement edge (or no DOT)
	}
	var missing []string
	for _, name := range spec.ExecutorPathToDoneSkills() {
		if _, gerr := g.docs.Get(ctx, "skills", name); gerr != nil {
			if errors.Is(gerr, docindex.ErrNotFound) {
				missing = append(missing, name)
				continue
			}
			return verb.GateDecision{}, false, gerr
		}
	}
	if len(missing) == 0 {
		return verb.GateDecision{}, false, nil
	}
	notes := fmt.Sprintf(
		"cannot engage: the active workflow's path to done has an executor step whose skill does not resolve in the substrate — %s. Author it under .satelle/skills (or embed it), or remove the step, before starting.",
		strings.Join(missing, ", "))
	dec := verb.GateDecision{Gated: true, Accept: false, Skill: engagementSkillCheck, Notes: notes}
	dec.Reviewers = append(dec.Reviewers, verb.ReviewerVerdict{
		Skill: engagementSkillCheck, Order: 0, Accept: false, Notes: notes, System: true,
	})
	return dec, true, nil
}

// runReviewer runs ONE reviewer skill over item's transition and returns its
// verdict. A skill carrying a functional check runs deterministically; otherwise
// the skill body rides as an isolated LLM reviewer's system prompt. Gated=false
// when the skill's rubric is not installed (advisory — keeps fresh repos working).
func (g *Gater) runReviewer(ctx context.Context, item workitem.Item, toStatus, skill string) (verb.GateDecision, error) {
	body, err := g.skillBody(ctx, skill)
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			return verb.GateDecision{Gated: false}, nil
		}
		return verb.GateDecision{}, err
	}
	// Functional-check gate: when the skill carries a check — an embedded ```check
	// script block in its body, or a single-line `check:` in frontmatter — the
	// gate is deterministic. The check is SELF-CONTAINED in the skill (it never
	// references an external script); satelle runs it in the repo root, exit 0
	// accepts, non-zero rejects with the output tail as notes. No LLM (the command
	// IS the decision). This is the constitution's "skill + functional check" gate.
	if command := skillCheck(body); command != "" {
		return g.runCheck(ctx, skill, command), nil
	}
	if g.runner == nil {
		return verb.GateDecision{Gated: true, Skill: skill}, fmt.Errorf(
			"reviewer: transition %s→%s is gated by %q but no agent runner is configured", item.Status, toStatus, skill)
	}
	payload, err := json.Marshal(transitionPayload{Story: item, From: item.Status, To: toStatus, ReviewSkill: skill})
	if err != nil {
		return verb.GateDecision{}, err
	}
	out, err := g.runner.Run(ctx, agentcli.Request{
		SystemPrompt: g.reviewerSystemPrompt(ctx, body),
		Payload:      string(payload),
		AllowedTools: g.tools,
		Model:        g.model,
		Dir:          g.repoRoot,
	})
	if err != nil {
		return verb.GateDecision{Gated: true, Skill: skill}, fmt.Errorf("reviewer: %s gate failed: %w", skill, err)
	}
	dec, err := parseDecision(out)
	if err != nil {
		return verb.GateDecision{Gated: true, Skill: skill}, fmt.Errorf("reviewer: %s: %w", skill, err)
	}
	dec.Gated = true
	dec.Skill = skill
	return dec, nil
}

// systemReviewers returns the names of the always-on SYSTEM reviewers that join
// the transition into toStatus — skills whose frontmatter tags carry
// alwaysReviewerTag, sorted for a deterministic order, excluding any already
// named on the edge. A reviewer may SCOPE itself with an `on:` frontmatter list
// of target statuses: it then joins only on those edges, so a gate that governs
// just begin-work/close costs nothing on the edges between. An `on:`-less skill
// joins every edge (back-compat). A List failure degrades to no system layer —
// it is additive and must never break the workflow's own gating.
func (g *Gater) systemReviewers(ctx context.Context, exclude []string, toStatus string) ([]string, error) {
	docs, err := g.docs.List(ctx, "skills")
	if err != nil {
		return nil, nil
	}
	var out []string
	for _, d := range docs {
		if !containsStr(frontmatterList(d.Body, "tags"), alwaysReviewerTag) {
			continue
		}
		if containsStr(exclude, d.Name) {
			continue
		}
		if on := frontmatterList(d.Body, "on"); len(on) > 0 && !containsStr(on, toStatus) {
			continue // scoped reviewer — this edge's target is not one it governs
		}
		out = append(out, d.Name)
	}
	sort.Strings(out)
	return out, nil
}

// structureSkill is the required-structure reviewer that judges a draft work
// item at creation. Embedded by default; overridable under .satelle/skills.
const structureSkill = "satelle-story-review"

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
// persisted, DETERMINISTICALLY (internal/structure) — a clear goal and at least
// one numbered, testable acceptance criterion. No LLM, no agent CLI: the contract
// is code, so it is harness-independent and never flaky. Always Gated (the
// structure is the one thing satelle enforces on creation).
func (g *Gater) ReviewCreate(_ context.Context, draft verb.CreateDraft) (verb.GateDecision, error) {
	if problems := structure.Story(draft.Title, draft.Body, draft.AcceptanceCriteria); len(problems) > 0 {
		return verb.GateDecision{Gated: true, Accept: false, Skill: structureSkill, Notes: strings.Join(problems, "; ")}, nil
	}
	return verb.GateDecision{Gated: true, Accept: true, Skill: structureSkill}, nil
}

// reviewerSkills resolves the ordered reviewer skills governing the (from→to)
// edge from the workflow active for the item's category, and reports whether the
// edge is a DECLARED transition of that workflow. An absent workflow means no
// governance at all — every edge is allowed and ungated (declared=true, no
// skills), so fresh repos and the baseline keep working.
func (g *Gater) reviewerSkills(ctx context.Context, category, from, to string) (skills []string, declared bool, err error) {
	doc, err := g.activeWorkflow(ctx, category)
	if errors.Is(err, docindex.ErrNotFound) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	skills, declared = reviewerSkillsFor(doc.Body, from, to)
	return skills, declared, nil
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
		if ordered := OrderedWorkflows(workflows, category); len(ordered) > 0 {
			return ordered[0], nil // the highest-priority applicable workflow
		}
	}
	return g.docs.Get(ctx, "workflows", baselineWorkflow)
}

// OrderedWorkflows returns the workflows that APPLY to a story of the given
// category, ordered by selection priority (highest first) — the list satelle
// offers an agent starting a story, where the head is the active/default choice
// and the gater enforces. A workflow applies when its `applies_to` lists the
// category or the wildcard "*". Priority tiers, in order:
//
//  1. category-specific match on a PROJECT (repo) workflow,
//  2. category-specific match on a SYSTEM (embedded) workflow,
//  3. wildcard ("*") PROJECT workflow,
//  4. wildcard SYSTEM workflow.
//
// So a repo's project workflow overrides the embedded system default, and a
// category-specific workflow overrides a wildcard one. Within a tier, input
// order (name-sorted, as the doc index yields) is preserved.
func OrderedWorkflows(workflows []docindex.Doc, category string) []docindex.Doc {
	var specRepo, specSys, wildRepo, wildSys []docindex.Doc
	for _, w := range workflows {
		at := frontmatterList(w.Body, "applies_to")
		switch {
		case category != "" && containsStr(at, category):
			if w.Embedded {
				specSys = append(specSys, w)
			} else {
				specRepo = append(specRepo, w)
			}
		case containsStr(at, "*"):
			if w.Embedded {
				wildSys = append(wildSys, w)
			} else {
				wildRepo = append(wildRepo, w)
			}
		}
	}
	out := make([]docindex.Doc, 0, len(workflows))
	out = append(out, specRepo...)
	out = append(out, specSys...)
	out = append(out, wildRepo...)
	out = append(out, wildSys...)
	return out
}

// runCheck runs a skill's functional-check command and returns a deterministic
// verdict: exit 0 accepts, any non-zero (or a run error / timeout) rejects with
// the command's output tail as actionable notes.
func (g *Gater) runCheck(ctx context.Context, skill, command string) verb.GateDecision {
	timeout := g.checkTimeout
	if timeout <= 0 {
		timeout = defaultCheckTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := g.check(cctx, g.repoRoot, command)
	dec := verb.GateDecision{Gated: true, Skill: skill}
	if err != nil {
		dec.Accept = false
		dec.Notes = fmt.Sprintf("functional check failed (`%s`): %v\n%s", command, err, tailLines(out, 40))
		return dec
	}
	dec.Accept = true
	dec.Notes = "functional check passed: `" + command + "`"
	return dec
}

// skillCheck returns a functional-check skill's command — the SELF-CONTAINED
// check carried inside the skill artifact. It prefers an embedded fenced
// ```check script block in the body (a multi-line, self-contained script), and
// falls back to a single-line `check:` in frontmatter. Empty when the skill
// carries no check (an LLM reviewer). A reviewer never references an external
// file — see the satelle-reviewer-self-contained principle.
func skillCheck(body string) string {
	if block := bodyCheckBlock(body); block != "" {
		return block
	}
	return frontmatterScalar(body, "check")
}

// bodyCheckBlock extracts the contents of the first fenced code block whose info
// string is `check` (``` ```check ``` or ``` ```check sh ```) — the self-contained
// functional check embedded in a skill's body. Returns "" when none.
func bodyCheckBlock(body string) string {
	lines := strings.Split(body, "\n")
	in := false
	var out []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if !in {
			if strings.HasPrefix(t, "```") {
				info := strings.TrimSpace(strings.TrimPrefix(t, "```"))
				if info == "check" || strings.HasPrefix(info, "check ") {
					in = true
				}
			}
			continue
		}
		if strings.HasPrefix(t, "```") {
			break // closing fence
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// frontmatterScalar returns a single-line scalar value for key from a markdown
// frontmatter block (quotes trimmed), or "" when absent. Used to read a gate's
// `check:` command.
func frontmatterScalar(body, key string) string {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for j := 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "---" {
			return ""
		}
		if strings.HasPrefix(t, key+":") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, key+":")), `"'`)
		}
	}
	return ""
}

// tailLines returns the last n non-trailing-empty lines of s, so a long check log
// is summarised to its most relevant (final) output for the reject notes.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
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

// reviewerSkillsFor scans a workflow body's transition lines for the (from→to)
// edge. It returns the edge's ordered reviewer skills (nil when the edge is
// declared but ungated) and whether the edge is DECLARED at all. The two cases
// are distinct: a declared ungated edge is advisory (enact directly), while an
// UNDECLARED edge is not a legal move in this workflow and must be refused —
// otherwise a story could skip a gate that rejected it by jumping to a later
// state across an edge the workflow never declared. The transition format is the
// inline-map shape the substrate uses, with either a single reviewer or a list:
//
//   - {from: backlog, to: in_progress, reviewer_skill: "satelle-story-intent-review"}
//   - {from: deployed, to: done, reviewer_skills: [satelle-story-done-review, satelle-estimate-actual]}
//
// reviewer_skills (the ordered list) takes precedence over reviewer_skill.
func reviewerSkillsFor(body, from, to string) (skills []string, declared bool) {
	// DOT workflow: resolve the edge from the shared wfdot spec — entry to a
	// reviewer node is the gated transition, carrying that node's skill.
	if spec, ok := wfdot.Parse(body); ok {
		for _, tr := range spec.Transitions {
			if tr.From == from && tr.To == to {
				if tr.Skill != "" {
					return []string{tr.Skill}, true
				}
				return nil, true
			}
		}
		return nil, false
	}
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "- {") || !strings.Contains(l, "from:") || !strings.Contains(l, "to:") {
			continue
		}
		if inlineField(l, "from") == from && inlineField(l, "to") == to {
			if list := inlineListField(l, "reviewer_skills"); len(list) > 0 {
				return list, true
			}
			if s := inlineField(l, "reviewer_skill"); s != "" {
				return []string{s}, true
			}
			return nil, true
		}
	}
	return nil, false
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

// inlineListField extracts a bracketed list value (`key: [a, b, c]`) from an
// inline-map line, trimming whitespace and quotes per element. Returns nil when
// the key is absent or carries no bracketed list — so a single-valued field
// falls through to inlineField.
func inlineListField(line, key string) []string {
	i := strings.Index(line, key+":")
	if i < 0 {
		return nil
	}
	rest := line[i+len(key)+1:]
	open := strings.Index(rest, "[")
	if open < 0 {
		return nil
	}
	closeAt := strings.Index(rest[open:], "]")
	if closeAt < 0 {
		return nil
	}
	return splitTrimList(rest[open+1 : open+closeAt])
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
