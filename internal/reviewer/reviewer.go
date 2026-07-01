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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/logfile"

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

// defaultTools is the reviewer's read-only tool grant. It judges, never MUTATES
// the work tree (Write/Edit/NotebookEdit are also denied by the harness ceiling).
// It needs NO shell: structural conformance is deterministic code (internal/
// structure), and the substrate it reasons about — skills, principles, workflows —
// is materialised as markdown under .satelle (satelle init), so Read/Grep/Glob
// resolve everything. A repo may still widen this in .satelle/agents.toml
// (transparently, the operator's choice); the default grant is read-only.
const defaultTools = "Read,Grep,Glob"

// baselineWorkflow is the workflow doc whose transitions carry the reviewer
// skills. The repo override or the embedded canonical resolves under this name.
const baselineWorkflow = "satelle-baseline-workflow"

// defaultCheckTimeout bounds a functional check (deploy/integration can be slow,
// but a hung command must not block a transition forever).
const defaultCheckTimeout = 20 * time.Minute

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
	// children resolves a parent's child stories (id + status) for a container
	// close gate's payload. Nil when unwired (no children injected).
	children func(ctx context.Context, parentID string) []ChildState
	// injectPrinciples toggles whether the session (principles:session) principles
	// ride in an isolated reviewer's system prompt — the agents-layer option
	// (sty_46a40208). Defaults ON (New sets it true); the reviewer binding's
	// inject_principles = false turns it off.
	injectPrinciples bool
	// attempts bounds how many times an LLM reviewer is retried when it produces NO
	// verdict — a TRANSIENT failure (a rate-limited/killed/empty subprocess under
	// concurrent sessions, sty_d71b0791), distinct from a genuine accept/reject which
	// returns on the first try. Defaults to defaultReviewerAttempts (New sets it).
	attempts int
	// backoff returns the wait before retry N (N ≥ 2) so transient contention can
	// clear. Swappable in tests (return 0 to avoid real waits); defaults to
	// defaultReviewerBackoff.
	backoff func(attempt int) time.Duration
	// logDir is <data_dir>/logs — where a transient reviewer failure (the failing
	// subprocess's own output) is appended to reviewer.log so API contention is
	// REVIEWABLE, not lost (sty_d71b0791). Empty disables logging (tests/unwired).
	// logCfg bounds that log's growth (daily + size + retention, sty_a67e6e8c).
	logDir string
	logCfg logfile.Config
}

// New builds a Gater over the agent runner and doc index. model "" inherits the
// agent's default; the tool grant is read-only.
func New(runner agentcli.Runner, docs DocGetter, repoRoot, model string) *Gater {
	return &Gater{
		runner: runner, docs: docs, repoRoot: repoRoot, model: model, tools: defaultTools,
		checkTimeout: defaultCheckTimeout, check: execCheck, injectPrinciples: true,
		attempts: defaultReviewerAttempts, backoff: defaultReviewerBackoff,
	}
}

// defaultReviewerAttempts is how many times an LLM reviewer is tried before a
// no-verdict transient failure is surfaced. A gated transition must be
// deterministic (advance or a clear error), and a nested reviewer subprocess can
// transiently return no verdict under concurrent load (sty_d71b0791), so it is
// retried a few times before giving up.
const defaultReviewerAttempts = 3

// defaultReviewerBackoff is the wait before retry N (called only for N ≥ 2), a
// short escalating pause so transient contention (e.g. many claude subprocesses
// across sessions) can clear before the next attempt.
func defaultReviewerBackoff(attempt int) time.Duration {
	if attempt <= 2 {
		return 2 * time.Second
	}
	return 5 * time.Second
}

// SetLogDir points the reviewer's transient-failure log at dir (the repo's
// <data_dir>/logs) and bounds it per cfg. When set, each transient reviewer
// failure — the failing subprocess's own output, e.g. a rate-limit message — is
// appended to reviewer.log so cross-session API contention is reviewable
// (sty_d71b0791), rotated daily + by size (sty_a67e6e8c). An empty dir disables
// logging.
func (g *Gater) SetLogDir(dir string, cfg logfile.Config) { g.logDir, g.logCfg = dir, cfg }

// logReviewerFailure appends one transient-failure record (the failing
// subprocess's output tail and error) to <logDir>/reviewer.log via the shared
// rotating writer, so the actual cause — typically a rate-limited nested agent
// under concurrent sessions — is surfaced for review. Best-effort: a logging
// error never affects the gate.
func (g *Gater) logReviewerFailure(skill string, attempt, attempts int, rerr error, out []byte) {
	if g.logDir == "" {
		return
	}
	now := time.Now()
	line := fmt.Sprintf("%s\t%s\tattempt %d/%d\ttransient reviewer failure: %v%s",
		now.UTC().Format(time.RFC3339), skill, attempt, attempts, rerr, outputTail(out))
	_ = logfile.Append(now, filepath.Join(g.logDir, "reviewer.log"), g.logCfg, line)
}

// SetReviewerTools sets the reviewer's tool grant from the agents layer (the
// resolved `reviewer` binding). It governs every isolated LLM reviewer this Gater
// runs. The default remains the read-only grant; a repo may widen or narrow it in
// .satelle/agents.toml without touching the workflow. An empty value is ignored
// so callers can pass through an unset binding safely.
func (g *Gater) SetReviewerTools(tools string) {
	if strings.TrimSpace(tools) != "" {
		g.tools = tools
	}
}

// SetChildrenResolver wires the resolver that lists a parent's child stories
// (id + status) so a container close gate judges the children-resolved rule from
// the payload satelle builds — not an on-disk story mirror. Nil-safe: an unwired
// resolver simply injects no children.
func (g *Gater) SetChildrenResolver(fn func(ctx context.Context, parentID string) []ChildState) {
	g.children = fn
}

// SetReviewerModel sets the reviewer's model from the agents layer (the resolved
// `reviewer` binding's `model`). It rides as `--model` to every isolated reviewer
// this Gater runs, so a repo can review on a different model (e.g. sonnet) without
// touching the executor. An empty value is ignored, keeping the agent CLI's
// default model (no `--model` flag emitted).
func (g *Gater) SetReviewerModel(model string) {
	if strings.TrimSpace(model) != "" {
		g.model = model
	}
}

// SetInjectPrinciples sets whether the resident principles ride in an isolated
// reviewer's system prompt, from the agents layer's resolved `reviewer` binding
// (sty_46a40208). Defaults ON; a repo disables it with inject_principles = false.
func (g *Gater) SetInjectPrinciples(on bool) { g.injectPrinciples = on }

// SetRunner overrides the reviewer's agent-CLI runner — the agents layer's
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
	// Children carries a container's child stories (id + status) so a parent/epic
	// close gate judges the children-resolved rule from the PAYLOAD — satelle does
	// the context selection — rather than reading any on-disk story mirror. Empty
	// for a non-container or when no resolver is wired.
	Children []ChildState `json:"children,omitempty"`
}

// ChildState is one child story's id and status, injected into a parent/epic
// close payload.
type ChildState struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// reviewerCallToAction is appended to a reviewer's injected context. It tells the
// isolated reviewer it has read-only `satelle` CLI access and should resolve any
// principle or skill its rubric references but does not inline — including
// EMBEDDED defaults that are not files on disk — rather than assuming absence.
const reviewerCallToAction = "## You are an isolated satelle reviewer\n\n" +
	"You judge only — you CANNOT modify the repository, and your tool grant is " +
	"read-only (Read, Grep, Glob). The substrate you reason about — skills, " +
	"principles, workflows — lives as markdown under `.satelle/`; read it directly " +
	"to resolve anything this rubric references but does not inline. Judge the " +
	"OUTCOME the story claims against this rubric and return your verdict."

// reviewerSystemPrompt assembles the system prompt for an isolated reviewer: the
// always-resident principles (so it judges with the resident set the executor
// also sees), the read-only call-to-action, then the reviewer's own rubric.
func (g *Gater) reviewerSystemPrompt(ctx context.Context, rubric string) string {
	var b strings.Builder
	// Principle injection is an agents-layer option, default ON (sty_46a40208): a
	// repo may omit the resident principles for the reviewer via inject_principles.
	if g.injectPrinciples {
		if resident := g.alwaysPrinciples(ctx); resident != "" {
			b.WriteString("# Always-resident principles (satelle)\n\n")
			b.WriteString(resident)
			b.WriteString("\n\n")
		}
	}
	b.WriteString(reviewerCallToAction)
	b.WriteString("\n\n---\n\n")
	b.WriteString(rubric)
	return b.String()
}

// alwaysPrinciples returns the bodies of the SESSION-resident (principles:session)
// principles — the SAME set the SessionStart injector gives the in-loop session
// (sty_46a40208), read via the SAME residency marker so the two never diverge —
// frontmatter stripped and joined in a stable (name-sorted) order, so an isolated
// reviewer judges with the session guardrails the executor also sees. The
// operating principle (config.OperatingPrinciple) is guaranteed
// even when it is embedded-only on a fresh repo, via Get's embedded fallback that
// List lacks. Empty when none resolve; injection is additive and must never break
// a gate.
func (g *Gater) alwaysPrinciples(ctx context.Context) string {
	seen := map[string]bool{}
	var bodies []string
	add := func(d docindex.Doc) {
		if seen[d.Name] {
			return
		}
		seen[d.Name] = true
		if body := strings.TrimSpace(stripFrontmatter(d.Body)); body != "" {
			bodies = append(bodies, body)
		}
	}
	if docs, err := g.docs.List(ctx, "principles"); err == nil {
		sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
		for _, d := range docs {
			if hasSessionTag(d.Body) {
				add(d)
			}
		}
	}
	// Guarantee the operating principle even when it is embedded-only (not yet
	// materialised on disk) — Get carries the embedded fallback List does not.
	if !seen[config.OperatingPrinciple] {
		if d, err := g.docs.Get(ctx, "principles", config.OperatingPrinciple); err == nil {
			add(d)
		}
	}
	return strings.Join(bodies, "\n\n")
}

// hasSessionTag reports whether a doc's FRONTMATTER carries the principles:session
// residency marker — checked only within the leading `---`…`---` block so prose
// mentioning the tag never counts.
func hasSessionTag(body string) bool {
	s := strings.TrimLeft(body, "\n")
	if !strings.HasPrefix(s, "---") {
		return false
	}
	rest := s[len("---"):]
	i := strings.Index(rest, "\n---")
	if i < 0 {
		return false
	}
	return strings.Contains(rest[:i], "principles:session")
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
	skills, declared, err := g.reviewerSkills(ctx, item, item.Status, toStatus)
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
	// removed commit/push skill). Reject engagement up front, naming the gap. This is the
	// fast, in-process complement to the LLM satelle-workflow-review, which judges
	// the workflow's full structure + actionability at create/update. Reviewer-gate
	// skills are NOT required here — a missing reviewer rubric degrades to advisory
	// by design, so fresh repos keep working.
	if dec, blocked, gerr := g.guardEngagementExecutorSkills(ctx, item, toStatus); gerr != nil {
		return verb.GateDecision{}, gerr
	} else if blocked {
		return dec, nil
	}
	// Append the workflow's DECLARED scoped reviewers (edge-less reviewer nodes
	// whose on= includes this target, or "*") AFTER the edge-named ones — they run
	// last. Skills already named on the edge are not duplicated. The DOT is the sole
	// gating authority: there is no skill-tag scan that injects gates the workflow
	// never declared (the reviewer:always layer was removed — sty_ca9f675f).
	sys, err := g.scopedReviewers(ctx, item, toStatus, skills)
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
		result.Command = dec.Command
		result.Context = dec.Context
		result.Reviewers = append(result.Reviewers, verb.ReviewerVerdict{
			Skill: skill, Order: i, Accept: dec.Accept, Notes: dec.Notes, System: i >= sysStart,
			Command: dec.Command, Context: dec.Context,
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
	doc, err := g.activeWorkflowPreferring(ctx, workflowCategory(item), stampedWorkflowName(item))
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
	tp := transitionPayload{Story: item, From: item.Status, To: toStatus, ReviewSkill: skill}
	if g.children != nil {
		tp.Children = g.children(ctx, item.ID)
	}
	payload, err := json.Marshal(tp)
	if err != nil {
		return verb.GateDecision{}, err
	}
	req := agentcli.Request{
		SystemPrompt: g.reviewerSystemPrompt(ctx, body),
		Payload:      string(payload),
		AllowedTools: g.tools,
		Model:        g.model,
		Dir:          g.repoRoot,
	}
	// A gated transition must be DETERMINISTIC: the reviewer either returns a
	// verdict (accept/reject) or the transition fails with a CLEAR error — never a
	// silent one-shot non-advance. A nested reviewer subprocess can TRANSIENTLY
	// produce no verdict (a rate-limited/killed/empty run under concurrent sessions
	// across repos — sty_d71b0791), so retry that transient failure with bounded
	// backoff. A genuine accept/reject parses on the first try and returns at once;
	// only an agent error or no-verdict output is retried.
	attempts := g.attempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	var lastOut []byte
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			// Back off so transient contention can clear; abort if cancelled.
			wait := time.Duration(0)
			if g.backoff != nil {
				wait = g.backoff(attempt)
			}
			select {
			case <-ctx.Done():
				return verb.GateDecision{Gated: true, Skill: skill}, ctx.Err()
			case <-time.After(wait):
			}
		}
		out, rerr := g.runner.Run(ctx, req)
		if rerr != nil {
			lastErr, lastOut = rerr, nil
			g.logReviewerFailure(skill, attempt, attempts, rerr, nil) // surface the contention
			continue                                                  // transient agent failure — retry
		}
		dec, perr := parseDecision(out)
		if perr != nil {
			lastErr, lastOut = perr, out
			g.logReviewerFailure(skill, attempt, attempts, perr, out) // capture the subprocess output
			continue                                                  // no verdict in the output — transient, retry
		}
		dec.Gated = true
		dec.Skill = skill
		// Record HOW this isolated agent was invoked (sty_fb3e0873): the resolved
		// harness command and the injected-context source (the rubric/skill file).
		// Only the LLM path sets these — a functional check above invokes no agent.
		dec.Command = g.runner.Command()
		dec.Context = skill
		return dec, nil
	}
	// Every attempt failed to produce a verdict — surface a CLEAR, actionable error
	// (a transient reviewer failure, NOT a rejection), naming the retry count and a
	// tail of the last output so contention is distinguishable from a real gap.
	return verb.GateDecision{Gated: true, Skill: skill}, fmt.Errorf(
		"reviewer: %s produced no verdict after %d attempts (transient agent failure — e.g. a rate-limited or killed subprocess under concurrent sessions; retry, or reduce concurrent satelle sessions): %w%s",
		skill, attempts, lastErr, outputTail(lastOut))
}

// outputTail returns a short, trimmed tail of a reviewer's last output for an
// error message — empty when there was none, so a runner error (no output) and a
// no-verdict output are both reported clearly.
func outputTail(out []byte) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return ""
	}
	const max = 300
	if len(s) > max {
		s = "…" + s[len(s)-max:]
	}
	return " — last output: " + s
}

// scopedReviewers returns the active workflow's DECLARED scoped reviewers for the
// transition into toStatus — edge-less reviewer nodes whose `on=` attribute lists
// toStatus (or "*"), excluding any already named on the edge. This replaces the
// old reviewer:always skill-tag scan: the scope is declared in the workflow DOT,
// not inferred from a skill's frontmatter tag, so the workflow is the SOLE gating
// authority (sty_ca9f675f). A workflow that is not parseable DOT (the inline-YAML
// grammar) has no scoped-node concept and contributes none. A resolution failure
// degrades to none — scoped reviewers are additive and must never break the
// workflow's own edge gating.
func (g *Gater) scopedReviewers(ctx context.Context, item workitem.Item, toStatus string, exclude []string) ([]string, error) {
	doc, err := g.activeWorkflowPreferring(ctx, workflowCategory(item), stampedWorkflowName(item))
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	spec, ok := wfdot.Parse(doc.Body)
	if !ok {
		return nil, nil
	}
	var out []string
	for _, s := range spec.ScopedReviewers(toStatus) {
		if !containsStr(exclude, s) {
			out = append(out, s)
		}
	}
	return out, nil
}

// structureSkill is the required-structure reviewer that judges a draft work
// item at creation. Embedded by default; overridable under .satelle/skills.
const structureSkill = "satelle-story-review"

// createReviewKey is the workflow-frontmatter key that DECLARES the opt-in
// content/alignment reviewer run after the structural check at creation
// (sty_b031b29f). The binding lives on the active workflow — configuration, not a
// hardcoded filename — so a repo wires create review by declaring it on the
// workflow that governs the story's category. Absent, creation is
// deterministic-only.
const createReviewKey = "create_review"

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
	// The summariser runs ONLY when the active workflow DECLARES a step-summary
	// node (transparent opt-in via the DOT) — there is no hidden always-on
	// summariser (sty_9a139c78). A non-declaring workflow records nothing.
	declared, mandatory := g.stepSummaryDeclared(ctx, item)
	if !declared {
		return "", nil
	}
	// soft returns "" on a non-mandatory failure (best-effort) and the error when
	// the step node is mandatory, so the caller can surface the gap.
	soft := func(format string, a ...any) (string, error) {
		if mandatory {
			return "", fmt.Errorf(format, a...)
		}
		return "", nil
	}
	body, err := g.skillBody(ctx, summariserSkill)
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			return soft("step summary is mandatory but the %s skill is not installed", summariserSkill)
		}
		return "", err
	}
	if g.runner == nil {
		return soft("step summary is mandatory but no agent runner is configured")
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
		return soft("mandatory step summary failed: %v", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// stepSummaryDeclared reports whether the workflow active for category declares a
// step-summary node (wfdot StepSummary) and whether it is mandatory.
func (g *Gater) stepSummaryDeclared(ctx context.Context, item workitem.Item) (declared, mandatory bool) {
	doc, err := g.activeWorkflowPreferring(ctx, workflowCategory(item), stampedWorkflowName(item))
	if err != nil {
		return false, false
	}
	spec, ok := wfdot.Parse(doc.Body)
	if !ok {
		return false, false
	}
	return spec.StepSummary()
}

// ReviewCreate judges a draft work item's required structure before it is
// persisted, DETERMINISTICALLY (internal/structure) — a clear goal and at least
// one numbered, testable acceptance criterion. No LLM, no agent CLI: the contract
// is code, so it is harness-independent and never flaky. Always Gated (the
// structure is the one thing satelle enforces on creation).
func (g *Gater) ReviewCreate(ctx context.Context, draft verb.CreateDraft) (verb.GateDecision, error) {
	// 1. Deterministic structural check FIRST — the one thing satelle always
	// enforces on creation. A structural failure pre-empts: the content reviewer
	// is never reached on a malformed draft.
	if problems := structure.Story(draft.Title, draft.Body, draft.AcceptanceCriteria); len(problems) > 0 {
		return verb.GateDecision{Gated: true, Accept: false, Skill: structureSkill, Notes: strings.Join(problems, "; ")}, nil
	}
	// 2. Optional content/alignment review — the reviewer skill is DECLARED by the
	// active workflow's `create_review` frontmatter (selected by the draft's
	// category), NOT a hardcoded filename. Absent a declaration (or the skill does
	// not resolve), creation stays deterministic-only.
	skill := g.createReviewSkillFor(ctx, draft.Category)
	if skill == "" {
		return verb.GateDecision{Gated: true, Accept: true, Skill: structureSkill}, nil
	}
	draftItem := workitem.Item{
		Title:              draft.Title,
		Body:               draft.Body,
		AcceptanceCriteria: draft.AcceptanceCriteria,
		Category:           draft.Category,
		Priority:           draft.Priority,
		Tags:               draft.Tags,
		Status:             "backlog",
	}
	dec, err := g.runReviewer(ctx, draftItem, "backlog", skill)
	if err != nil {
		return verb.GateDecision{}, err
	}
	if dec.Gated {
		return dec, nil // the declared content reviewer accepted or rejected
	}
	// The workflow declared a skill but it does not resolve — accept on structure
	// alone rather than blocking creation on a misconfigured binding.
	return verb.GateDecision{Gated: true, Accept: true, Skill: structureSkill}, nil
}

// createReviewSkillFor resolves the content/alignment create reviewer DECLARED by
// the workflow active for the category — its `create_review` frontmatter. Empty
// when no workflow governs the category or none is declared, so creation stays
// deterministic-only (the binding is configuration, never a hardcoded filename).
func (g *Gater) createReviewSkillFor(ctx context.Context, category string) string {
	doc, err := g.activeWorkflow(ctx, category)
	if err != nil {
		return ""
	}
	return frontmatterScalar(doc.Body, createReviewKey)
}

// reviewerSkills resolves the ordered reviewer skills governing the (from→to)
// edge from the workflow active for the item's category, and reports whether the
// edge is a DECLARED transition of that workflow. An absent workflow means no
// governance at all — every edge is allowed and ungated (declared=true, no
// skills), so fresh repos and the baseline keep working.
func (g *Gater) reviewerSkills(ctx context.Context, item workitem.Item, from, to string) (skills []string, declared bool, err error) {
	doc, err := g.activeWorkflowPreferring(ctx, workflowCategory(item), stampedWorkflowName(item))
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

// WorkflowStampPrefix is the tag prefix that STAMPS the governing workflow on a
// story at create (sty_3800ac23): `workflow:<name>`. Recorded once, so gating
// reads the chosen workflow rather than re-deriving it by category every time.
const WorkflowStampPrefix = "workflow:"

// workflowCategory returns the key used to resolve an item's governing workflow.
// A story resolves by its authored category; an EXECUTION resolves by its KIND
// ("execution"), so a task-execution workflow (applies_to:["execution"]) governs
// runs without depending on a per-item category, and an execution never falls
// through to the wildcard STORY workflow (sty_ef08ce2a). Tasks keep their
// category (a task header has no running lifecycle to gate).
func workflowCategory(item workitem.Item) string {
	if item.Kind == workitem.KindExecution {
		return string(workitem.KindExecution)
	}
	return item.Category
}

// stampedWorkflowName returns the workflow stamped on the item (its
// `workflow:<name>` tag), or "" when un-stamped (legacy/category-resolved).
func stampedWorkflowName(item workitem.Item) string {
	for _, t := range item.Tags {
		if strings.HasPrefix(t, WorkflowStampPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(t, WorkflowStampPrefix))
		}
	}
	return ""
}

// activeWorkflowPreferring resolves the governing workflow, preferring the item's
// STAMPED workflow when present (deterministic after create); it falls back to
// category selection when un-stamped or the stamped workflow no longer resolves.
func (g *Gater) activeWorkflowPreferring(ctx context.Context, category, stamped string) (docindex.Doc, error) {
	if stamped != "" {
		if doc, err := g.docs.Get(ctx, "workflows", stamped); err == nil {
			return doc, nil
		}
		// The stamped workflow is gone — fall back to category selection rather
		// than losing governance.
	}
	return g.activeWorkflow(ctx, category)
}

// WorkflowNameFor returns the name of the workflow that governs a story of the
// given category — the value stamped on the story at create. Empty when no
// workflow governs the category. Used by the create path to record the choice.
func (g *Gater) WorkflowNameFor(ctx context.Context, category string) string {
	doc, err := g.activeWorkflow(ctx, category)
	if err != nil {
		return ""
	}
	return doc.Name
}

// WorkflowConsistency reports cross-workflow inconsistencies an agent should
// advise the user about (sty_4c0c7246): (1) OVER-CONFIGURATION — two or more REPO
// workflows claim the same category (or the wildcard) at the same precedence, so
// the tiebreak is arbitrary; (2) a workflow that REFERENCES a skill (an edge gate
// or a node @skill: prompt) which does not resolve in the substrate. Empty when
// the workflow set is consistent. resolve may be nil to skip the skill check.
func WorkflowConsistency(workflows []docindex.Doc, resolve func(skill string) bool) []string {
	var problems []string

	// (1) Ambiguous applies_to among REPO workflows (the embedded defaults are the
	// single canonical source, so a tie there is not the user's misconfiguration).
	cats := map[string]bool{}
	for _, w := range workflows {
		for _, c := range frontmatterList(w.Body, "applies_to") {
			cats[c] = true
		}
	}
	for c := range cats {
		var repo []string
		for _, w := range workflows {
			if !w.Embedded && containsStr(frontmatterList(w.Body, "applies_to"), c) {
				repo = append(repo, w.Name)
			}
		}
		if len(repo) >= 2 {
			sort.Strings(repo)
			label := c
			if c == "*" {
				label = "* (wildcard)"
			}
			problems = append(problems, fmt.Sprintf(
				"category %s: workflows %s apply at the same precedence — give them distinct applies_to or remove the duplicate", label, strings.Join(repo, ", ")))
		}
	}

	// (2) Referenced skills that do not resolve.
	if resolve != nil {
		for _, w := range workflows {
			spec, ok := wfdot.Parse(w.Body)
			if !ok {
				continue
			}
			for _, s := range referencedWorkflowSkills(spec) {
				if !resolve(s) {
					problems = append(problems, fmt.Sprintf(
						"workflow %s references skill %q which does not resolve in the substrate", w.Name, s))
				}
			}
		}
	}
	sort.Strings(problems)
	return problems
}

// referencedWorkflowSkills lists every skill a workflow names — node @skill:
// prompts and edge gates — deduped.
func referencedWorkflowSkills(spec wfdot.Spec) []string {
	set := map[string]bool{}
	for _, s := range spec.States {
		if s.Skill != "" {
			set[s.Skill] = true
		}
	}
	for _, tr := range spec.Transitions {
		if tr.Skill != "" {
			set[tr.Skill] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
				if len(tr.Skills) > 0 {
					return tr.Skills, true
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
