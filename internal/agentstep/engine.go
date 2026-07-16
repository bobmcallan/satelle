// Package agentstep is satelle's isolated-agent dispatch engine — the
// quality-management spine that runs a fresh-context agent over a workflow step
// and folds its result back in. It dispatches three kinds of isolated agent, all
// briefed and run through one seam (invoke.go): a GATE REVIEWER judging a status
// transition and returning a verdict, a NAMED EXECUTOR performing a step's work,
// and the STEP SUMMARISER narrating a transition. A reviewer is just one agent
// kind, so the engine (type Engine) is not named for it.
//
// Reviewer path: the active workflow names a reviewer_skill per edge; the skill's
// markdown body rides as the agent's appended system prompt; the work item +
// requested transition go in on stdin; the agent prints one JSON object
// {decision, notes, reasoning}, parsed strictly into an accept/reject. Accept lets
// the caller enact; reject blocks and pushes the notes back to the executor.
// LLM gate and named-dispatch runs share Invoke (invoke.go) — one path that
// calls agentcli.Runner.Run (sty_ba860c8a).
//
// The edge is gated only when the workflow names a reviewer_skill AND that
// skill's rubric is installed in the substrate. A named-but-absent rubric (e.g.
// the canonical default referencing a skill not yet embedded) is treated as
// advisory, so gating switches on exactly when the rubrics ship — the gateless
// baseline keeps working until then.
package agentstep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// DocGetter is the read surface the engine needs over the authored-doc index
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

// Engine judges status transitions against the active workflow's reviewer skills.
// A skill is either an LLM reviewer (its body rides as an isolated agent's system
// prompt) or a functional check (its frontmatter names a deterministic `check:`
// command the gate runs — the command's exit code is the verdict).
type Engine struct {
	runner   agentcli.Runner
	docs     DocGetter
	repoRoot string
	model    string
	tools    string
	// reviewerEnv is the reviewer binding's resolved env (config.ResolveAgentEnvs),
	// layered onto the reviewer AND summariser subprocesses — both are isolated
	// children, so both carry the binding's env (sty_001558ce). A named executor
	// uses its OWN binding.Env instead.
	reviewerEnv  map[string]string
	checkTimeout time.Duration
	// check runs a functional-check command in dir and returns its combined
	// output. The command receives the SAME stdin transition payload an LLM
	// reviewer gets ({story, from, to, review_skill}), so a coded gate can judge
	// the story's tags/edge deterministically (sty_f804caaa). Swappable in
	// tests; defaults to a real `bash -c` exec.
	check func(ctx context.Context, dir, command, payload string) (string, error)
	// children resolves a parent's child stories (id + status) for a container
	// close gate's payload. Nil when unwired (no children injected).
	children func(ctx context.Context, parentID string) []ChildState
	// injectPrinciples is a cache of the reviewer binding's principle injection
	// (sty_46a40208). Order:2 feeds Invoke from reviewerBinding; this flag remains
	// so SetInjectPrinciples / tests keep working until order:3 retires the scalar.
	injectPrinciples bool
	// reviewerBinding is the agents.toml [reviewer] binding — the SINGLE resolution
	// shape for gate tools/model/env/principles/role (sty_ba860c8a AC3). Scalar
	// SetReviewer* mutators update this binding so existing tests keep working.
	reviewerBinding config.AgentBinding
	// constitution is the project constitution body (frontmatter stripped), injected
	// order-zero whenever principles ≠ none (design §5.3). Wired by the CLI via
	// SetConstitution — the engine must not import cli.
	constitution string
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
	// progress, when set, receives one-line status messages while a slow gate
	// runs ("running reviewer <skill>…"), so a legitimate multi-minute review is
	// visibly distinct from a hang (sty_6c88ca10). The CLI wires it to stderr;
	// nil (web/tests) disables emission.
	progress func(msg string)
	// agentTimeout bounds EACH nested agent invocation (a reviewer attempt or a
	// step summary) with a context deadline, so a wedged subprocess yields a
	// clear bounded failure instead of an open-ended block (sty_6c88ca10).
	// Zero/negative disables the bound (tests).
	agentTimeout time.Duration
	// namedAgents resolves a NAMED agent binding from the agents layer
	// (.satelle/agents.toml [<name>] sections) for executor dispatch
	// (sty_fd427546). Nil keeps every step in-loop.
	namedAgents func(name string) (config.AgentBinding, bool)
	// newRunner builds the runner for a named binding's command — swappable in
	// tests; defaults to agentcli.RunnerFromCommand.
	newRunner func(command string) (agentcli.Runner, error)
	// telemetry records a structured, queryable dispatch outcome (a reviewer/
	// executor retry, failure, or timeout) that only the binary observes — the
	// verb layer sees just the final result, not each attempt (sty_b73c3236). Nil
	// disables it (tests / no-ledger environments); best-effort like the other
	// engine-owned logging.
	telemetry TelemetryFunc
}

// TelemetryFunc records one typed telemetry/quality event for storyID. Callers
// pass the event's outcome/kind and its typed data (never env/secrets — the
// implementation validates). Implemented by verb.AppendTelemetry and wired via
// SetTelemetry; best-effort by contract, so it takes no error return.
type TelemetryFunc func(ctx context.Context, storyID, actor, kind string, data map[string]any)

// SetTelemetry wires the sink the engine uses to record a dispatch-level
// telemetry event (agent-retry/agent-failure/agent-timeout). Pass nil to
// disable (the default) — every call site nil-checks via telemetryEvent.
func (g *Engine) SetTelemetry(fn TelemetryFunc) { g.telemetry = fn }

// telemetryEvent records one telemetry event via the wired sink, if any.
func (g *Engine) telemetryEvent(ctx context.Context, storyID, actor, kind string, data map[string]any) {
	if g.telemetry != nil {
		g.telemetry(ctx, storyID, actor, kind, data)
	}
}

// classifyOutcome names WHY a dispatched agent invocation failed, for the
// structured telemetry payload (AC2, sty_b73c3236): a bounded deadline is a
// timeout, a process killed out from under the caller (e.g. OOM, a session
// cancelling a concurrent claude subprocess) surfaces Go's "signal: killed" in
// the error text, and anything else is a generic error. A nil err (a parsed
// no-verdict output, not a run error) is reported as such by the caller instead.
func classifyOutcome(err error) string {
	if err == nil {
		return "no-verdict"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if strings.Contains(err.Error(), "signal: killed") {
		return "signal:killed"
	}
	return "error"
}

// New builds a Engine over the agent runner and doc index. model "" inherits the
// agent's default; the tool grant is read-only.
func New(runner agentcli.Runner, docs DocGetter, repoRoot, model string) *Engine {
	return &Engine{
		runner: runner, docs: docs, repoRoot: repoRoot, model: model, tools: defaultTools,
		checkTimeout: defaultCheckTimeout, check: execCheck, injectPrinciples: true,
		attempts: defaultReviewerAttempts, backoff: defaultReviewerBackoff,
		agentTimeout: defaultAgentTimeout, newRunner: agentcli.RunnerFromCommand,
	}
}

// defaultAgentTimeout bounds one nested agent invocation. A real review takes
// ~3-6 minutes; ten gives honest slack while turning a wedged subprocess into a
// bounded, legible failure instead of an indefinite block (sty_6c88ca10).
const defaultAgentTimeout = 10 * time.Minute

// SetProgress wires the sink for one-line gate progress messages (the CLI
// prints them to stderr). nil disables emission.
func (g *Engine) SetProgress(fn func(msg string)) { g.progress = fn }

// emitProgress sends one progress line to the wired sink, if any.
func (g *Engine) emitProgress(format string, a ...any) {
	if g.progress != nil {
		g.progress(fmt.Sprintf(format, a...))
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
func (g *Engine) SetLogDir(dir string, cfg logfile.Config) { g.logDir, g.logCfg = dir, cfg }

// logReviewerFailure appends one no-verdict failure record to
// <logDir>/reviewer.log via the shared rotating writer, so the actual cause —
// a rate-limited nested agent, or a reviewer that answered outside the JSON
// contract — is surfaced for review. The subprocess's FULL output is logged
// (newlines flattened), not a truncated tail: when the reviewer wrote real
// reasons without a parseable verdict, those words must be recoverable by the
// executor (sty_9485d47e). Rotation (daily + size + retention) bounds growth.
// Best-effort: a logging error never affects the gate.
func (g *Engine) logReviewerFailure(skill string, attempt, attempts int, rerr error, out []byte) {
	if g.logDir == "" {
		return
	}
	label := "reviewer subprocess error"
	full := ""
	if s := strings.TrimSpace(string(out)); s != "" {
		label = "no verdict in reviewer output"
		full = " — full output: " + strings.ReplaceAll(s, "\n", "\\n")
	}
	now := time.Now()
	line := fmt.Sprintf("%s\t%s\tattempt %d/%d\t%s: %v%s",
		now.UTC().Format(time.RFC3339), skill, attempt, attempts, label, rerr, full)
	_ = logfile.Append(now, filepath.Join(g.logDir, "reviewer.log"), g.logCfg, line)
}

// logProseFallback records that a reviewer's verdict was recovered from PROSE
// (no JSON decision object) — a normal decision, logged for observability so a
// reviewer drifting off the JSON contract is visible without failing the gate
// (sty_9485d47e). Best-effort.
func (g *Engine) logProseFallback(skill string, accept bool) {
	if g.logDir == "" {
		return
	}
	verdict := "reject"
	if accept {
		verdict = "accept"
	}
	now := time.Now()
	line := fmt.Sprintf("%s\t%s\tprose-verdict fallback: parsed %q from non-JSON reviewer output",
		now.UTC().Format(time.RFC3339), skill, verdict)
	_ = logfile.Append(now, filepath.Join(g.logDir, "reviewer.log"), g.logCfg, line)
}

// SetReviewerTools sets the reviewer's tool grant from the agents layer (the
// resolved `reviewer` binding). It governs every isolated LLM reviewer this Engine
// runs. The default remains the read-only grant; a repo may widen or narrow it in
// .satelle/agents.toml without touching the workflow. An empty value is ignored
// so callers can pass through an unset binding safely. Also mutates reviewerBinding.
func (g *Engine) SetReviewerTools(tools string) {
	if strings.TrimSpace(tools) != "" {
		g.tools = tools
		g.reviewerBinding.Tools = tools
	}
}

// SetReviewerBinding stores the full resolved [reviewer] binding as the single
// resolution source for Invoke (sty_ba860c8a). Scalar caches are synced from it.
func (g *Engine) SetReviewerBinding(b config.AgentBinding) {
	g.reviewerBinding = b
	if strings.TrimSpace(b.Tools) != "" {
		g.tools = b.Tools
	}
	if strings.TrimSpace(b.Model) != "" {
		g.model = b.Model
	}
	if len(b.Env) > 0 {
		g.reviewerEnv = b.Env
	}
	g.injectPrinciples = b.InjectsPrinciples()
}

// SetConstitution sets the project constitution body injected order-zero into
// isolated agent briefings whenever principles ≠ none (design §5.3).
func (g *Engine) SetConstitution(body string) { g.constitution = strings.TrimSpace(body) }

// SetChildrenResolver wires the resolver that lists a parent's child stories
// (id + status) so a container close gate judges the children-resolved rule from
// the payload satelle builds — not an on-disk story mirror. Nil-safe: an unwired
// resolver simply injects no children.
func (g *Engine) SetChildrenResolver(fn func(ctx context.Context, parentID string) []ChildState) {
	g.children = fn
}

// SetReviewerModel sets the reviewer's model from the agents layer (the resolved
// `reviewer` binding's `model`). It rides as `--model` to every isolated reviewer
// this Engine runs, so a repo can review on a different model (e.g. sonnet) without
// touching the executor. An empty value is ignored, keeping the agent CLI's
// default model (no `--model` flag emitted). Also mutates reviewerBinding.
func (g *Engine) SetReviewerModel(model string) {
	if strings.TrimSpace(model) != "" {
		g.model = model
		g.reviewerBinding.Model = model
	}
}

// SetReviewerEnv sets the reviewer binding's resolved env (config.ResolveAgentEnvs),
// applied to every isolated reviewer AND step-summariser subprocess this Engine
// runs — so a repo can point its reviews at an alternate model backend the same
// way a named executor does (sty_001558ce). Absent/empty leaves the child env at
// the inherited process env. Also mutates reviewerBinding.
func (g *Engine) SetReviewerEnv(env map[string]string) {
	g.reviewerEnv = env
	g.reviewerBinding.Env = env
}

// SetInjectPrinciples sets whether the resident principles ride in an isolated
// reviewer's system prompt, from the agents layer's resolved `reviewer` binding
// (sty_46a40208). Defaults ON; a repo disables it with inject_principles = false.
// Also mutates reviewerBinding (deprecated alias + principles selector).
func (g *Engine) SetInjectPrinciples(on bool) {
	g.injectPrinciples = on
	v := on
	g.reviewerBinding.InjectPrinciples = &v
	if on {
		g.reviewerBinding.Principles = config.PrinciplesSession
	} else {
		g.reviewerBinding.Principles = config.PrinciplesNone
	}
}

// SetRunner overrides the reviewer's agent-CLI runner — the agents layer's
// `reviewer` harness binding, resolved to a Runner. A nil runner is ignored,
// keeping the default configured at construction (the global `[agent] cli`).
func (g *Engine) SetRunner(r agentcli.Runner) {
	if r != nil {
		g.runner = r
	}
}

// execCheck runs command via `bash -c` in dir, returning combined stdout+stderr.
// bash (not sh) so a multi-line self-contained check embedded in a skill may use
// ordinary shell scripting.
func execCheck(ctx context.Context, dir, command, payload string) (string, error) {
	c := exec.CommandContext(ctx, "bash", "-c", command)
	c.Dir = dir
	c.Stdin = strings.NewReader(payload)
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

// alwaysPrinciples returns the bodies of the SESSION-resident (principles:session)
// principles — the SAME set the SessionStart injector gives the in-loop session
// (sty_46a40208), read via the SAME residency marker so the two never diverge —
// frontmatter stripped and joined in a stable (name-sorted) order, so an isolated
// reviewer judges with the session guardrails the executor also sees. The
// operating principle (config.OperatingPrinciple) is guaranteed
// even when it is embedded-only on a fresh repo, via Get's embedded fallback that
// List lacks. Empty when none resolve; injection is additive and must never break
// a gate.
func (g *Engine) alwaysPrinciples(ctx context.Context) string {
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
func (g *Engine) Gate(ctx context.Context, item workitem.Item, toStatus string) (verb.GateDecision, error) {
	// Broken substrate refuses to run (sty_d0d6bb67): a governing workflow that
	// fails its deterministic structure check must never gate work — refuse the
	// transition with the problems, instead of silently proceeding under a broken
	// definition.
	if err := g.guardWorkflowStructure(ctx, item); err != nil {
		return verb.GateDecision{}, err
	}
	skills, edgeModel, declared, err := g.reviewerSkills(ctx, item, item.Status, toStatus)
	if err != nil {
		return verb.GateDecision{}, err
	}
	if !declared {
		// The active workflow does not declare this edge — it is not a legal move.
		// Refuse it (the caller blocks the transition), so a story cannot skip a
		// gate by jumping across an edge the workflow never declared (sty_ebd3d666).
		// Prefer Successors so the expected next step is named when the DOT is known.
		msg := fmt.Sprintf("transition %s→%s is not a declared edge in the active workflow", item.Status, toStatus)
		if next := g.successorsOf(ctx, item, item.Status); len(next) > 0 {
			msg = fmt.Sprintf(
				"satelle: refusing transition %s→%s — not a declared edge; expected next step(s): %s",
				item.Status, toStatus, strings.Join(next, ", "))
		}
		return verb.GateDecision{}, fmt.Errorf("%s", msg)
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
	// Build ordered gate list with optional per-skill model overrides from the
	// edge (all edge skills share edgeModel) or scoped node model= (sty_19456622).
	var ordered []reviewerRef
	for _, sk := range skills {
		ordered = append(ordered, reviewerRef{skill: sk, model: edgeModel})
	}
	sysStart := len(ordered)
	ordered = append(ordered, sys...)

	var result verb.GateDecision
	for i, ref := range ordered {
		skill := ref.skill
		if skill == "" {
			continue
		}
		dec, rerr := g.runReviewer(ctx, item, toStatus, skill, ref.model)
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
		result.Reasoning = dec.Reasoning
		result.Command = dec.Command
		result.Context = dec.Context
		result.Model = dec.Model
		result.TokensIn, result.TokensOut, result.TokensTotal = dec.TokensIn, dec.TokensOut, dec.TokensTotal
		result.DurationMs = dec.DurationMs
		result.Reviewers = append(result.Reviewers, verb.ReviewerVerdict{
			Skill: skill, Order: i, Accept: dec.Accept, Notes: dec.Notes, Reasoning: dec.Reasoning, System: i >= sysStart,
			Command: dec.Command, Context: dec.Context, Model: dec.Model,
			TokensIn: dec.TokensIn, TokensOut: dec.TokensOut, TokensTotal: dec.TokensTotal, DurationMs: dec.DurationMs,
		})
		if !dec.Accept {
			return result, nil // a reject blocks the edge — do not run later reviewers
		}
	}
	return result, nil
}

// guardWorkflowStructure refuses a gate whose governing DEFINITION is broken
// (sty_d0d6bb67): the active workflow must pass its deterministic structure
// check (internal/structure) before it may gate a transition. The substrate
// executes as defined or refuses — a broken workflow never silently governs.
// Embedded canonical defaults are the binary's own bytes (validated by satelle's
// tests), so only authored (non-embedded) substrate is judged here; no resolvable
// workflow at all keeps the gateless path working.
func (g *Engine) guardWorkflowStructure(ctx context.Context, item workitem.Item) error {
	doc, err := g.activeWorkflowPreferring(ctx, workflowCategory(item), stampedWorkflowName(item))
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			return nil
		}
		return err
	}
	if doc.Embedded {
		return nil
	}
	// resolveSkill is nil DELIBERATELY: executor-skill actionability is the
	// engagement guard's territory (guardEngagementExecutorSkills — a recorded,
	// edge-scoped DECISION), not a hard structural error on every edge. This
	// guard judges only whether the workflow DEFINITION itself is well-formed.
	if problems := structure.Doc("workflows", doc.Name, doc.Body, nil); len(problems) > 0 {
		return fmt.Errorf(
			"gate refused: governing workflow %q fails structure validation: %s — fix the substrate (`satelle workflow validate %s`) before gated transitions can run",
			doc.Name, strings.Join(problems, "; "), doc.Name)
	}
	return nil
}

// SetNamedAgents wires the resolver for NAMED agent bindings from the agents
// layer (.satelle/agents.toml [<name>] sections) — the WHO of a workflow node's
// agent=<name> allocation (sty_fd427546). Nil keeps every step in-loop.
func (g *Engine) SetNamedAgents(fn func(name string) (config.AgentBinding, bool)) { g.namedAgents = fn }

// DispatchExecutor implements verb.ExecutorDispatcher: when the TARGET state of
// an accepted transition is allocated to a NAMED agent (agent=<name>, neither
// "executor" nor "reviewer"), OR carries on_enter_agent=<name> while its role
// agent is empty/executor/reviewer (one-shot entry perform — sty_5cabe26f), the
// binding's harness performs the step synchronously — prompt assembled from the
// item (title, body, acceptance criteria on stdin) plus the node's @skill rubric,
// tools/model/principles from the binding, nothing hardcoded (sty_fd427546). A
// missing binding or a failed run is an ERROR — the caller refuses the transition
// (broken definition never silently falls back in-loop, consistent with
// sty_d0d6bb67). agent=executor, agent-less, and reviewer states with no
// on_enter_agent dispatch nothing; a named binding whose harness is explicitly
// "in-loop" also stays with the orchestrator.
func (g *Engine) DispatchExecutor(ctx context.Context, item workitem.Item, toStatus string) (verb.DispatchResult, error) {
	doc, err := g.activeWorkflowPreferring(ctx, workflowCategory(item), stampedWorkflowName(item))
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			return verb.DispatchResult{}, nil
		}
		return verb.DispatchResult{}, err
	}
	spec, ok := wfdot.Parse(doc.Body)
	if !ok {
		return verb.DispatchResult{}, nil
	}
	var target *wfdot.State
	for i := range spec.States {
		if spec.States[i].Name == toStatus {
			target = &spec.States[i]
			break
		}
	}
	if target == nil {
		return verb.DispatchResult{}, nil
	}
	// Resolve WHO performs: a named agent= performer takes priority; otherwise
	// on_enter_agent is the one-shot entry dispatch (park nodes stay
	// agent=reviewer for engagement while still running triage once on entry).
	// Spine skill + surface-matched augmentations compose additively
	// (sty_8225d8a5); dispatchSkill is the first (primary) name for telemetry.
	dispatchAgent := target.Agent
	composed := spec.ExecutorSkillsFor(toStatus, item.Tags)
	dispatchSkill := firstStr(composed)
	if dispatchSkill == "" {
		dispatchSkill = target.Skill
	}
	if target.Agent == "" || target.Agent == "executor" || target.Agent == "reviewer" {
		if target.OnEnterAgent == "" {
			return verb.DispatchResult{}, nil
		}
		dispatchAgent, dispatchSkill = target.OnEnterAgent, target.OnEnterSkill
		composed = nil
		if dispatchSkill != "" {
			composed = []string{dispatchSkill}
		}
	}
	if g.namedAgents == nil {
		return verb.DispatchResult{}, fmt.Errorf(
			"workflow %q allocates state %q to named agent %q but no agents layer is wired", doc.Name, toStatus, dispatchAgent)
	}
	binding, found := g.namedAgents(dispatchAgent)
	if !found {
		return verb.DispatchResult{}, fmt.Errorf(
			"workflow %q allocates state %q to agent %q but .satelle/agents.toml defines no [%s] binding — define it, or reassign the step",
			doc.Name, toStatus, dispatchAgent, dispatchAgent)
	}
	// Per-node model= override on the target state (sty_19456622) — same mechanism
	// as gate edges: binding stays source of command/tools; only model varies.
	if target.Model != "" {
		binding.Model = target.Model
	}
	// Design §9 (a): when the resolved binding is role=reviewer, it is a judge
	// not a performer — do not dispatch as ExpectPerform (isNamedPerformer).
	if !isNamedPerformer(dispatchAgent, binding) {
		return verb.DispatchResult{}, nil
	}
	// Engagement lease (sty_8426b9c0) is acquired for the TARGET engaging state
	// BEFORE this dispatch runs (verb/workitem.go acquire-at-start). Edit/commit
	// gates read the lease, not committed FROM status — so a code-writing named
	// agent may edit during dispatch without the FROM state itself being
	// performing. The prior FROM-performing band-aid (sty_f5bd176f) is removed.
	runner, err := g.newRunner(binding.CommandTemplate())
	if err != nil {
		return verb.DispatchResult{}, fmt.Errorf("named agent %q: broken command in .satelle/agents.toml: %w", dispatchAgent, err)
	}
	if runner == nil {
		return verb.DispatchResult{}, nil // command "in-loop": the orchestrator performs the step
	}
	// A dispatched executor starts fresh and reconstructs its context by PULLING the
	// story, its documents, and the ledger — either via the read-only satelle CLI
	// (the pull-context call-to-action, sty_47d31300) or via disk reads under
	// .satelle/stories/<id>/ when the binding has a file-read tool but no shell
	// (sty_565a0202 grok coder). Without a context channel the agent is silently
	// context-starved. Refuse the dispatch with an actionable fix rather than run a
	// blind agent — the no-silent-fallback style the engine uses for a missing binding.
	if !grantsSatelleCLI(binding.Tools) {
		return verb.DispatchResult{}, fmt.Errorf(
			"named agent %q cannot perform step %q: its .satelle/agents.toml [%s] tools grant has no context channel (add `Bash(satelle:*)` for the satelle CLI, or `read_file` for disk reads under .satelle/stories/<id>/)",
			dispatchAgent, toStatus, dispatchAgent)
	}
	// Composed rubrics: spine skill first, then matching augmentations in order
	// (sty_8225d8a5). Absent skills stay advisory here — the engagement guard
	// already hard-blocks missing executor-path skills. LLM run goes through
	// shared Invoke (sty_ba860c8a).
	rubric, rerr := g.composeSkillBodies(ctx, composed)
	if rerr != nil {
		return verb.DispatchResult{}, rerr
	}
	timeout, terr := binding.TimeoutDuration(g.checkTimeout)
	if terr != nil {
		return verb.DispatchResult{}, fmt.Errorf("named agent %q: invalid timeout in .satelle/agents.toml [%s]: %w", dispatchAgent, dispatchAgent, terr)
	}
	sink, sinkPath, closeSink := g.dispatchSink(dispatchAgent, item.ID)
	if closeSink != nil {
		defer closeSink()
	}
	if sinkPath != "" {
		g.emitProgress("dispatching step %s to named agent %s (may take several minutes)… live output: %s", toStatus, dispatchAgent, sinkPath)
	} else {
		g.emitProgress("dispatching step %s to named agent %s (may take several minutes)…", toStatus, dispatchAgent)
	}
	invRes := g.Invoke(ctx, InvokeRequest{
		Binding: binding,
		Section: dispatchAgent,
		Rubric:  rubric,
		Payload: transitionPayload{Story: item, From: item.Status, To: toStatus, ReviewSkill: dispatchSkill},
		Charter: executorCharter(dispatchAgent, toStatus, doc.Name),
		Expect:  ExpectPerform,
		Timeout: timeout,
		Runner:  runner,
		Sink:    sink,
		StoryID: item.ID,
		Step:    toStatus,
		Skill:   dispatchSkill,
		Actor:   "executor",
	})
	res := verb.DispatchResult{
		Dispatched: true, Agent: dispatchAgent, Command: invRes.Command, Model: binding.Model, Skill: dispatchSkill,
		TokensIn: invRes.Usage.InputTokens, TokensOut: invRes.Usage.OutputTokens, TokensTotal: invRes.Usage.TotalTokens,
		DurationMs: invRes.Usage.Duration.Milliseconds(),
		Output:     string(invRes.Stdout),
	}
	g.logExecutorRun(dispatchAgent, item.ID, toStatus, invRes.Stdout, invRes.Err)
	if invRes.Err != nil {
		g.telemetryEvent(ctx, item.ID, "executor", "agent-failure", map[string]any{
			"agent": dispatchAgent, "step": toStatus, "outcome": classifyOutcome(invRes.Err),
			"tokens_total": res.TokensTotal, "duration_ms": res.DurationMs,
		})
		return res, fmt.Errorf("named agent %q failed performing step %q: %w", dispatchAgent, toStatus, invRes.Err)
	}
	return res, nil
}

// retrospectAgent / retrospectSkill name the post-story improvement step: an
// isolated named agent that reads a finished story and proposes improvements
// (sty_b53730e2). It is a dispatched EXECUTOR (it creates backlog proposals via
// the CLI), never a read-only reviewer — the constitution keeps reviewers judging
// only, so a step that MUTATES is a named agent.
const (
	retrospectAgent = "retrospective"
	retrospectSkill = "satelle-retrospective"
)

// Retrospect dispatches the retrospective agent over a finished story: it pulls
// the story + its plan/summary/ledger by id, then emits 1–3 improvement PROPOSALS
// as backlog stories (its Bash(satelle:*) grant). Invoked per-story by
// `satelle story retrospect` — kept opt-in rather than auto-on-done so its cost
// (visible via `satelle story cost`, sty_a699ad14) is measured before it is made
// always-on. Returns the dispatch result (with captured output + token/wall-time
// cost) so the verb layer can record an agent_invocation for the cost view.
func (g *Engine) Retrospect(ctx context.Context, item workitem.Item) (verb.DispatchResult, error) {
	if g.namedAgents == nil {
		return verb.DispatchResult{}, fmt.Errorf("no agents layer is wired — cannot dispatch the %q agent", retrospectAgent)
	}
	binding, found := g.namedAgents(retrospectAgent)
	if !found {
		return verb.DispatchResult{}, fmt.Errorf(
			"no [%s] binding in .satelle/agents.toml — define it (with Bash(satelle:*) so it can file proposals) to run the retrospective", retrospectAgent)
	}
	runner, err := g.newRunner(binding.CommandTemplate())
	if err != nil {
		return verb.DispatchResult{}, fmt.Errorf("%s agent: broken command: %w", retrospectAgent, err)
	}
	if runner == nil {
		return verb.DispatchResult{}, fmt.Errorf("%s agent harness is in-loop; set a real harness to dispatch it", retrospectAgent)
	}
	if !grantsSatelleCLI(binding.Tools) {
		return verb.DispatchResult{}, fmt.Errorf(
			"[%s] tools grant has no context channel (add `Bash(satelle:*)` for the satelle CLI, or `read_file` for disk reads) — it needs a channel to pull the story and file proposal stories", retrospectAgent)
	}
	rubric := ""
	if body, rerr := g.skillBody(ctx, retrospectSkill); rerr == nil {
		rubric = body
	} else if !errors.Is(rerr, docindex.ErrNotFound) {
		return verb.DispatchResult{}, rerr
	}
	g.emitProgress("running retrospective on %s (may take a few minutes)…", item.ID)
	invRes := g.Invoke(ctx, InvokeRequest{
		Binding: binding,
		Section: retrospectAgent,
		Rubric:  rubric,
		Payload: transitionPayload{Story: item, From: item.Status, To: item.Status, ReviewSkill: retrospectSkill},
		Charter: executorCharter(retrospectAgent, "retrospect", "post-story retrospective"),
		Expect:  ExpectPerform,
		Timeout: g.checkTimeout,
		Runner:  runner,
		StoryID: item.ID,
		Step:    "retrospect",
		Skill:   retrospectSkill,
		Actor:   "executor",
	})
	res := verb.DispatchResult{
		Dispatched: true, Agent: retrospectAgent, Command: invRes.Command, Model: binding.Model, Skill: retrospectSkill,
		TokensIn: invRes.Usage.InputTokens, TokensOut: invRes.Usage.OutputTokens, TokensTotal: invRes.Usage.TotalTokens,
		DurationMs: invRes.Usage.Duration.Milliseconds(),
		Output:     string(invRes.Stdout),
	}
	g.logExecutorRun(retrospectAgent, item.ID, "retrospect", invRes.Stdout, invRes.Err)
	if invRes.Err != nil {
		return res, fmt.Errorf("%s agent failed on %s: %w", retrospectAgent, item.ID, invRes.Err)
	}
	return res, nil
}

// setDecisionUsage copies an invocation's token/wall-time cost onto a gate
// decision so the verb layer can record it on the agent_invocation entry — the
// per-gate cost the observability view rolls up (sty_a699ad14). model is the
// effective model used for this run (binding/override), not only the engine cache.
func (g *Engine) setDecisionUsage(d *verb.GateDecision, u agentcli.UsageResult, model string) {
	d.TokensIn, d.TokensOut, d.TokensTotal = u.InputTokens, u.OutputTokens, u.TotalTokens
	d.DurationMs = u.Duration.Milliseconds()
	if model != "" {
		d.Model = model
	} else {
		d.Model = g.model
	}
}

// grantsSatelleCLI reports whether a binding's tool grant gives a dispatched
// agent a context channel — the pull-context contract (sty_47d31300). Two
// channels satisfy it (sty_565a0202):
//
//  1. satelle CLI via shell: `Bash(satelle…)`, broad `Bash`/`Bash(*)`, or `*`.
//  2. Disk reads of story documents under .satelle/stories/<id>/ via the
//     grok-native `read_file` tool (used when headless Grok cannot enable
//     run_terminal_command).
//
// A grant with neither channel is refused loudly. Reviewer bindings are
// Bash-less by design and never reach here — they run via runReviewer, not
// dispatch. Claude-only `Read` (without Bash) is intentionally not accepted:
// the Claude pull path is the satelle CLI, not a disk-first rubric.
func grantsSatelleCLI(tools string) bool {
	for _, t := range splitTrimList(tools) {
		if t == "*" || t == "Bash" || t == "Bash(*)" || strings.HasPrefix(t, "Bash(satelle") {
			return true
		}
		if t == "read_file" {
			return true
		}
	}
	return false
}

// isInLoopCommand reports whether a binding command is the in-loop preset
// (single-token "in-loop", case-insensitive) — cannot produce an isolated verdict.
// Empty command is NOT in-loop: tests and bootstrap leave command blank and wire
// g.runner directly.
func isInLoopCommand(cmd string) bool {
	fields := strings.Fields(strings.TrimSpace(cmd))
	return len(fields) == 1 && strings.EqualFold(fields[0], "in-loop")
}

// isNamedPerformer reports whether a workflow node agent=<name> should run as a
// performing dispatch (ExpectPerform). The built-in DSL tokens are filtered
// before this is called. A named binding whose resolved role is reviewer is a
// judge, not a performer (design §9 (a) / sty_e21cbc08).
func isNamedPerformer(section string, b config.AgentBinding) bool {
	if section == "" || section == "executor" || section == "reviewer" {
		return false
	}
	return config.ResolvedRole(section, b) != config.RoleReviewer
}

// dispatchSink opens a per-dispatch live log file under <data_dir>/logs/dispatch/
// so a named executor's subprocess streams its stdout/stderr somewhere an
// operator can `tail -f` WHILE it runs — observable before a hang or an
// approaching timeout SIGKILLs it (sty_0aa67b7f), unlike executor.log which is
// only written after the run completes. Returns a nil writer, empty path, and nil
// closer when no log dir is configured (most engine tests) or the file cannot be
// created, so streaming degrades to a no-op rather than failing the dispatch —
// best-effort, like the executor/reviewer logs. The caller must invoke the
// returned closer (when non-nil) once the dispatch finishes.
func (g *Engine) dispatchSink(agent, itemID string) (io.Writer, string, func()) {
	if g.logDir == "" {
		return nil, "", nil
	}
	dir := filepath.Join(g.logDir, "dispatch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", nil
	}
	path := filepath.Join(dir, fmt.Sprintf("dispatch-%s-%d-%s.log", agent, time.Now().UnixNano(), itemID))
	f, err := os.Create(path)
	if err != nil {
		return nil, "", nil
	}
	return f, path, func() { _ = f.Close() }
}

// logExecutorRun appends a named-agent run's output (or failure) to
// <data_dir>/logs/executor.log — the run log that makes an isolated executor's
// work reviewable (sty_fd427546). Best-effort, like the reviewer log.
func (g *Engine) logExecutorRun(agent, itemID, state string, out []byte, runErr error) {
	if g.logDir == "" {
		return
	}
	now := time.Now()
	status := "ok"
	if runErr != nil {
		status = "error: " + runErr.Error()
	}
	line := fmt.Sprintf("%s\t%s\t%s\tstate %s\t%s — output: %s",
		now.UTC().Format(time.RFC3339), agent, itemID, state, status,
		strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", "\\n"))
	_ = logfile.Append(now, filepath.Join(g.logDir, "executor.log"), g.logCfg, line)
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
func (g *Engine) guardEngagementExecutorSkills(ctx context.Context, item workitem.Item, toStatus string) (verb.GateDecision, bool, error) {
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
	// Tag-filtered: a missing surface-scoped augmentation blocks only stories
	// whose tags match it (sty_8225d8a5); structure validate still requires all.
	var missing []string
	for _, name := range spec.ExecutorPathToDoneSkillsFor(item.Tags) {
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

// retryWait backs off before a retry (attempt > 1) so transient contention can
// clear, aborting if the context is cancelled. Returns ctx.Err() on cancellation,
// else nil. The single source of the bounded-backoff step (sty_d71b0791), shared by
// the reviewer gate loop and the step summariser so both retry a transient the same
// way. attempt == 1 waits not at all.
func (g *Engine) retryWait(ctx context.Context, attempt int) error {
	if attempt <= 1 {
		return nil
	}
	wait := time.Duration(0)
	if g.backoff != nil {
		wait = g.backoff(attempt)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

// runReviewer runs ONE reviewer skill over item's transition and returns its
// verdict. A skill carrying a functional check runs deterministically; otherwise
// the skill body rides as an isolated LLM reviewer's system prompt. Gated=false
// when the skill's rubric is not installed (advisory — keeps fresh repos working).
// modelOverride is a DOT model= value for this gate (edge or scoped node); empty
// inherits the [reviewer] binding model (sty_19456622).
func (g *Engine) runReviewer(ctx context.Context, item workitem.Item, toStatus, skill, modelOverride string) (verb.GateDecision, error) {
	body, err := g.skillBody(ctx, skill)
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			return verb.GateDecision{Gated: false}, nil
		}
		return verb.GateDecision{}, err
	}
	// Broken substrate refuses to run (sty_d0d6bb67): a PRESENT reviewer skill
	// that fails its deterministic structure check must not judge the edge. An
	// ABSENT rubric stays advisory by design (fresh repos keep working); an
	// invalid one is a broken definition and refuses, naming the problems.
	if problems := structure.Doc("skills", skill, body, nil); len(problems) > 0 {
		return verb.GateDecision{}, fmt.Errorf(
			"gate refused: reviewer skill %q fails structure validation: %s — fix the substrate (`satelle skill validate %s`)",
			skill, strings.Join(problems, "; "), skill)
	}
	// Reviewer skill contract check (design §6.3): the body must specify the
	// verdict contract (at least decision + notes). reasoning is recommended.
	if problems := structure.ReviewerSkillContract(body); len(problems) > 0 {
		return verb.GateDecision{}, fmt.Errorf(
			"gate refused: reviewer skill %q does not specify the verdict contract: %s — the skill must document returning JSON {decision, notes} (reasoning recommended)",
			skill, strings.Join(problems, "; "))
	}
	tp := transitionPayload{Story: item, From: item.Status, To: toStatus, ReviewSkill: skill}
	if g.children != nil {
		tp.Children = g.children(ctx, item.ID)
	}
	payload, err := json.Marshal(tp)
	if err != nil {
		return verb.GateDecision{}, err
	}
	// Functional-check gate: when the skill carries a check — an embedded ```check
	// script block in its body, or a single-line `check:` in frontmatter — the
	// gate is deterministic. The check is SELF-CONTAINED in the skill (it never
	// references an external script); satelle runs it in the repo root with the
	// transition payload on stdin, exit 0 accepts, non-zero rejects with the
	// output tail as notes. No LLM (the command IS the decision). This is the
	// constitution's "skill + functional check" gate. Stays OUTSIDE Invoke (design §4.2).
	if command := skillCheck(body); command != "" {
		return g.runCheck(ctx, skill, command, string(payload)), nil
	}
	// LLM path: shared Invoke (sty_ba860c8a / sty_e21cbc08). Pre-flight (skill,
	// structure, functional-check, missing-rubric advisory) stays here.
	binding := g.reviewerBinding
	if binding.Tools == "" {
		binding.Tools = g.tools
	}
	if binding.Model == "" {
		binding.Model = g.model
	}
	// Per-gate model override (sty_19456622): DOT model= wins over binding/g.model
	// without mutating the engine-wide reviewer binding (other gates in the same
	// transition must not inherit a sibling gate's override).
	if modelOverride != "" {
		binding.Model = modelOverride
	}
	if len(binding.Env) == 0 {
		binding.Env = g.reviewerEnv
	}
	if binding.Principles == "" && binding.InjectPrinciples == nil {
		// Mirror engine injectPrinciples cache when binding never set principles.
		if g.injectPrinciples {
			binding.Principles = config.PrinciplesSession
		} else {
			binding.Principles = config.PrinciplesNone
		}
	}
	// Mechanism: a gate needs an isolated verdict. command=in-loop cannot produce
	// one — fail loud at gate time (design §6.4), not by policing tools/model.
	if isInLoopCommand(binding.CommandTemplate()) {
		return verb.GateDecision{Gated: true, Skill: skill}, fmt.Errorf(
			"gate refused: reviewer binding %q is command=in-loop and cannot produce an isolated verdict — set [reviewer] command to an isolated agent CLI (claude|grok|codex or a full template)", "reviewer")
	}
	// Role must resolve to reviewer for the gate binding (design §4.4 / §8).
	if config.ResolvedRole("reviewer", binding) != config.RoleReviewer {
		return verb.GateDecision{Gated: true, Skill: skill}, fmt.Errorf(
			"gate refused: [reviewer] binding has role=%q (want role=reviewer) — declare role = \"reviewer\" in agents.toml",
			config.ResolvedRole("reviewer", binding))
	}
	if g.runner == nil {
		return verb.GateDecision{Gated: true, Skill: skill}, fmt.Errorf(
			"reviewer: transition %s→%s is gated by %q but no agent runner is configured", item.Status, toStatus, skill)
	}
	res := g.Invoke(ctx, InvokeRequest{
		Binding:  binding,
		Section:  "reviewer",
		Rubric:   body,
		Payload:  tp,
		Charter:  reviewerCharter(),
		Expect:   ExpectVerdict,
		Timeout:  g.agentTimeout,
		Runner:   g.runner,
		Attempts: g.attempts,
		StoryID:  item.ID,
		Step:     toStatus,
		Skill:    skill,
		Actor:    "reviewer",
	})
	if res.Err != nil {
		return verb.GateDecision{Gated: true, Skill: skill}, res.Err
	}
	if res.Decision == nil {
		return verb.GateDecision{Gated: true, Skill: skill}, fmt.Errorf(
			"reviewer: %s produced no decision", skill)
	}
	return *res.Decision, nil
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
// toStatus (or "*"), excluding any already named on the edge. Each entry carries
// the skill and optional node model= override (sty_19456622). This replaces the
// old reviewer:always skill-tag scan: the scope is declared in the workflow DOT,
// not inferred from a skill's frontmatter tag, so the workflow is the SOLE gating
// authority (sty_ca9f675f). A workflow that is not parseable DOT (the inline-YAML
// grammar) has no scoped-node concept and contributes none. A resolution failure
// degrades to none — scoped reviewers are additive and must never break the
// workflow's own edge gating.
func (g *Engine) scopedReviewers(ctx context.Context, item workitem.Item, toStatus string, exclude []string) ([]reviewerRef, error) {
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
	var out []reviewerRef
	// item.Tags decide whether a surface-scoped node is ENQUEUED (sty_c6d093c8).
	// Skipped applies_to filters leave a telemetry artifact so a silent skip is
	// not identical to "no such surface gate" (sty_dcce86d5).
	enqueued, skipped := spec.ScopedReviewersSplit(toStatus, item.Tags)
	for _, s := range enqueued {
		if !containsStr(exclude, s.Skill) {
			out = append(out, reviewerRef{skill: s.Skill, model: s.Model})
		}
	}
	for _, s := range skipped {
		g.telemetryEvent(ctx, item.ID, "reviewer", "scoped-gate-skipped", map[string]any{
			"skill": s.Skill, "to": toStatus, "reason": "applies_to",
			"tags": item.Tags,
		})
	}
	return out, nil
}

// reviewerRef is one gate to run: skill name + optional DOT model= override.
type reviewerRef struct {
	skill, model string
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
//
// TODO(sty_ba860c8a): fold onto Invoke once a soft-fail/empty-retry expect mode
// exists without ballooning ExpectVerdict/ExpectPerform. Today it still uses
// buildRequest+runOnce directly (AC1 carve-out).
func (g *Engine) Summarise(ctx context.Context, item workitem.Item, from, to string) (verb.SummaryResult, error) {
	// The summariser runs ONLY when the active workflow DECLARES a step-summary
	// node (transparent opt-in via the DOT) — there is no hidden always-on
	// summariser (sty_9a139c78). A non-declaring workflow records nothing.
	declared, mandatory := g.stepSummaryDeclared(ctx, item)
	if !declared {
		return verb.SummaryResult{}, nil
	}
	// soft returns a zero result on a non-mandatory failure (best-effort) and the
	// error when the step node is mandatory, so the caller can surface the gap.
	soft := func(format string, a ...any) (verb.SummaryResult, error) {
		if mandatory {
			return verb.SummaryResult{}, fmt.Errorf(format, a...)
		}
		return verb.SummaryResult{}, nil
	}
	body, err := g.skillBody(ctx, summariserSkill)
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			return soft("step summary is mandatory but the %s skill is not installed", summariserSkill)
		}
		return verb.SummaryResult{}, err
	}
	if g.runner == nil {
		return soft("step summary is mandatory but no agent runner is configured")
	}
	// The summariser prompt is rubric-only (no charter, principles=none) so it
	// stays a plain narrator — buildRequest omits empty sections. Grant is read-only.
	req, err := g.buildRequest(ctx, invocation{
		rubric:     body,
		principles: config.PrinciplesNone,
		payload:    summaryPayload{Story: item, From: from, To: to},
		tools:      g.tools, // read-only (Read,Grep,Glob) — narrate, never mutate
		model:      g.model,
		env:        g.reviewerEnv,
	})
	if err != nil {
		return verb.SummaryResult{}, err
	}
	g.emitProgress("summarising step %s→%s (may take a minute)…", from, to)
	// Retry the SAME transient a reviewer retries (a rate-limited/killed/empty
	// subprocess under concurrent sessions — sty_d71b0791, sty_a1151fb0): a single
	// runOnce permanently LOST the summary on a transient kill, silently holing the
	// pull-context chain. Bounded by g.attempts with g.backoff; fail fast on a
	// deadline (a bound, not contention — retrying just re-blocks a full window).
	attempts := g.attempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if werr := g.retryWait(ctx, attempt); werr != nil {
			lastErr = werr
			break
		}
		out, usage, rerr := g.runOnce(ctx, g.runner, req, g.agentTimeout)
		if rerr != nil {
			if errors.Is(rerr, context.DeadlineExceeded) && ctx.Err() == nil {
				g.telemetryEvent(ctx, item.ID, "reviewer", "agent-timeout", map[string]any{
					"skill": summariserSkill, "step": to, "attempt": attempt, "attempts": attempts,
				})
				return soft("mandatory step summary timed out after %s", g.agentTimeout)
			}
			lastErr = rerr
			g.logReviewerFailure(summariserSkill, attempt, attempts, rerr, nil)
			g.telemetryEvent(ctx, item.ID, "reviewer", "agent-retry", map[string]any{
				"skill": summariserSkill, "step": to, "attempt": attempt, "attempts": attempts, "outcome": classifyOutcome(rerr),
			})
			continue // transient — retry
		}
		if s := strings.TrimSpace(string(out)); s != "" {
			// The summariser's own token/wall-time cost (sty_a699ad14, a documented
			// gap now closed): the verb layer folds this into an agent_invocation row
			// alongside the step_summary text, so `satelle story cost` sees it too.
			return verb.SummaryResult{
				Text: s, Command: g.runner.Command(), Context: summariserSkill, Model: g.model,
				TokensIn: usage.InputTokens, TokensOut: usage.OutputTokens, TokensTotal: usage.TotalTokens,
				DurationMs: usage.Duration.Milliseconds(),
			}, nil
		}
		lastErr = fmt.Errorf("empty summary output")
		g.logReviewerFailure(summariserSkill, attempt, attempts, lastErr, out)
		g.telemetryEvent(ctx, item.ID, "reviewer", "agent-retry", map[string]any{
			"skill": summariserSkill, "step": to, "attempt": attempt, "attempts": attempts, "outcome": "empty-output",
		})
	}
	g.telemetryEvent(ctx, item.ID, "reviewer", "agent-failure", map[string]any{
		"skill": summariserSkill, "step": to, "attempts": attempts, "outcome": classifyOutcome(lastErr),
	})
	return soft("mandatory step summary failed after %d attempts: %v", attempts, lastErr)
}

// MandatorySummary reports whether item's active workflow declares a MANDATORY
// step-summary node — used to gate the done-time missing-summary surfacing
// (sty_a1151fb0). Implements verb.StepSummariser.
func (g *Engine) MandatorySummary(ctx context.Context, item workitem.Item) bool {
	_, mandatory := g.stepSummaryDeclared(ctx, item)
	return mandatory
}

// stepSummaryDeclared reports whether the workflow active for category declares a
// step-summary node (wfdot StepSummary) and whether it is mandatory.
func (g *Engine) stepSummaryDeclared(ctx context.Context, item workitem.Item) (declared, mandatory bool) {
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
func (g *Engine) ReviewCreate(ctx context.Context, draft verb.CreateDraft) (verb.GateDecision, error) {
	// 1. Deterministic structural check FIRST — the one thing satelle always
	// enforces on creation. A structural failure pre-empts: the content reviewer
	// is never reached on a malformed draft.
	if problems := structure.Story(draft.Title, draft.Body, draft.AcceptanceCriteria, draft.Category); len(problems) > 0 {
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
	dec, err := g.runReviewer(ctx, draftItem, "backlog", skill, "")
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
func (g *Engine) createReviewSkillFor(ctx context.Context, category string) string {
	doc, err := g.activeWorkflow(ctx, category)
	if err != nil {
		return ""
	}
	return frontmatterScalar(doc.Body, createReviewKey)
}

// reviewerSkills resolves the ordered reviewer skills governing the (from→to)
// edge from the workflow active for the item's category, the edge's model=
// override (empty = inherit binding), and whether the edge is a DECLARED
// transition of that workflow. An absent workflow means no governance at all —
// every edge is allowed and ungated (declared=true, no skills), so fresh repos
// and the baseline keep working.
func (g *Engine) reviewerSkills(ctx context.Context, item workitem.Item, from, to string) (skills []string, model string, declared bool, err error) {
	doc, err := g.activeWorkflowPreferring(ctx, workflowCategory(item), stampedWorkflowName(item))
	if errors.Is(err, docindex.ErrNotFound) {
		return nil, "", true, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	skills, model, declared = reviewerSkillsFor(doc.Body, from, to)
	return skills, model, declared, nil
}

// successorsOf returns declared DOT successors of from for agent-facing refuse
// messages (sty_ebd3d666). Empty when no workflow/DOT resolves.
func (g *Engine) successorsOf(ctx context.Context, item workitem.Item, from string) []string {
	doc, err := g.activeWorkflowPreferring(ctx, workflowCategory(item), stampedWorkflowName(item))
	if err != nil {
		return nil
	}
	spec, ok := wfdot.Parse(doc.Body)
	if !ok {
		return nil
	}
	return spec.Successors(from)
}

// activeWorkflow returns the workflow doc governing an item of the given
// category. Selection matches the item's category against each indexed
// workflow's `applies_to` frontmatter: a workflow listing the category wins; a
// wildcard (`applies_to: ["*"]`) workflow is the next-best; the embedded
// baseline (resolved by name) is the final fallback. This is the
// configuration-over-code path — a repo adds a category-specific workflow as
// substrate and it takes effect with no binary change. A List error degrades to
// the baseline so gating never silently disappears.
func (g *Engine) activeWorkflow(ctx context.Context, category string) (docindex.Doc, error) {
	if workflows, err := g.docs.List(ctx, "workflows"); err == nil {
		if ordered := wfgovern.OrderedWorkflows(workflows, category); len(ordered) > 0 {
			return ordered[0], nil // the highest-priority applicable workflow
		}
	}
	return g.docs.Get(ctx, "workflows", baselineWorkflow)
}

// WorkflowStampPrefix re-exports the stamp tag prefix (owned by wfgovern).
const WorkflowStampPrefix = wfgovern.WorkflowStampPrefix

// workflowCategory / stampedWorkflowName thin-wrap wfgovern for in-package call sites.
func workflowCategory(item workitem.Item) string { return wfgovern.WorkflowCategory(item) }
func stampedWorkflowName(item workitem.Item) string {
	return wfgovern.StampedWorkflowName(item)
}

// OrderedWorkflows re-exports wfgovern.OrderedWorkflows for callers that still
// import agentstep (web, CLI). Prefer wfgovern directly for new code.
func OrderedWorkflows(workflows []docindex.Doc, category string) []docindex.Doc {
	return wfgovern.OrderedWorkflows(workflows, category)
}

// GoverningWorkflow re-exports wfgovern.GoverningWorkflow.
func GoverningWorkflow(workflows []docindex.Doc, item workitem.Item) (docindex.Doc, bool) {
	return wfgovern.GoverningWorkflow(workflows, item)
}

// activeWorkflowPreferring resolves the governing workflow, preferring the item's
// STAMPED workflow when present (deterministic after create); it falls back to
// category selection when un-stamped or the stamped workflow no longer resolves.
func (g *Engine) activeWorkflowPreferring(ctx context.Context, category, stamped string) (docindex.Doc, error) {
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
func (g *Engine) WorkflowNameFor(ctx context.Context, category string) string {
	doc, err := g.activeWorkflow(ctx, category)
	if err != nil {
		return ""
	}
	return doc.Name
}

// WorkflowStates returns the lifecycle states the named workflow declares — the
// nodes on its TRANSITIONS (an edge-less declared reviewer node like estimate/
// step is a gate declaration, not a lifecycle state) — and whether the workflow
// resolves at all. The restamp validation seam (sty_ed3386cf): a story may only
// be re-stamped onto a workflow that declares its current status. A resolved
// workflow whose lifecycle is not parseable DOT returns no states, so the caller
// skips the status check rather than stranding the story.
func (g *Engine) WorkflowStates(ctx context.Context, name string) ([]string, bool) {
	doc, err := g.docs.Get(ctx, "workflows", name)
	if err != nil {
		return nil, false
	}
	spec, ok := wfdot.Parse(doc.Body)
	if !ok {
		return nil, true
	}
	seen := map[string]bool{}
	var out []string
	for _, tr := range spec.Transitions {
		for _, s := range []string{tr.From, tr.To} {
			if s != "" && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out, true
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
		for _, c := range wfgovern.FrontmatterList(w.Body, "applies_to") {
			cats[c] = true
		}
	}
	for c := range cats {
		var repo []string
		for _, w := range workflows {
			if !w.Embedded && containsStr(wfgovern.FrontmatterList(w.Body, "applies_to"), c) {
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
			// A declared create_review binding must resolve too (sty_51ad783b):
			// an unresolved one silently degrades creation to deterministic-only,
			// which is exactly the misconfiguration to surface here.
			if cr := frontmatterScalar(w.Body, createReviewKey); cr != "" && !resolve(cr) {
				problems = append(problems, fmt.Sprintf(
					"workflow %s declares create_review %q which does not resolve in the substrate", w.Name, cr))
			}
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

// runCheck runs a skill's functional-check command and returns a deterministic
// verdict: exit 0 accepts, any non-zero (or a run error / timeout) rejects with
// the command's output tail as actionable notes.
func (g *Engine) runCheck(ctx context.Context, skill, command, payload string) verb.GateDecision {
	timeout := g.checkTimeout
	if timeout <= 0 {
		timeout = defaultCheckTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := g.check(cctx, g.repoRoot, command, payload)
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
func (g *Engine) skillBody(ctx context.Context, name string) (string, error) {
	doc, err := g.docs.Get(ctx, "skills", name)
	if err != nil {
		return "", err
	}
	return doc.Body, nil
}

// firstStr returns the first non-empty string in ss, or "".
func firstStr(ss []string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// composeSkillBodies loads and concatenates skill rubrics in order (spine first,
// then augmentations — sty_8225d8a5). Missing skills are skipped (engagement
// guard already hard-blocks them); other lookup errors surface.
func (g *Engine) composeSkillBodies(ctx context.Context, names []string) (string, error) {
	var parts []string
	for _, name := range names {
		if name == "" {
			continue
		}
		body, err := g.skillBody(ctx, name)
		if err != nil {
			if errors.Is(err, docindex.ErrNotFound) {
				continue
			}
			return "", err
		}
		if len(names) > 1 {
			parts = append(parts, "# Skill: "+name+"\n\n"+body)
		} else {
			parts = append(parts, body)
		}
	}
	return strings.Join(parts, "\n\n---\n\n"), nil
}

// reviewerSkillsFor scans a workflow body's transition lines for the (from→to)
// edge. It returns the edge's ordered reviewer skills (nil when the edge is
// declared but ungated), the edge model= override (empty when absent / YAML
// grammar), and whether the edge is DECLARED at all. The two cases are distinct:
// a declared ungated edge is advisory (enact directly), while an UNDECLARED edge
// is not a legal move in this workflow and must be refused — otherwise a story
// could skip a gate that rejected it by jumping to a later state across an edge
// the workflow never declared. The transition format is the inline-map shape the
// substrate uses, with either a single reviewer or a list:
//
//   - {from: backlog, to: in_progress, reviewer_skill: "satelle-story-intent-review"}
//   - {from: deployed, to: done, reviewer_skills: [satelle-story-done-review, satelle-estimate-actual]}
//
// reviewer_skills (the ordered list) takes precedence over reviewer_skill.
// DOT edges may carry model="…" (sty_19456622); YAML edges have no model field.
func reviewerSkillsFor(body, from, to string) (skills []string, model string, declared bool) {
	// DOT workflow: resolve the edge from the shared wfdot spec — entry to a
	// reviewer node is the gated transition, carrying that node's skill.
	if spec, ok := wfdot.Parse(body); ok {
		for _, tr := range spec.Transitions {
			if tr.From == from && tr.To == to {
				if len(tr.Skills) > 0 {
					return tr.Skills, tr.Model, true
				}
				return nil, tr.Model, true
			}
		}
		return nil, "", false
	}
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "- {") || !strings.Contains(l, "from:") || !strings.Contains(l, "to:") {
			continue
		}
		if inlineField(l, "from") == from && inlineField(l, "to") == to {
			if list := inlineListField(l, "reviewer_skills"); len(list) > 0 {
				return list, "", true
			}
			if s := inlineField(l, "reviewer_skill"); s != "" {
				return []string{s}, "", true
			}
			return nil, "", true
		}
	}
	return nil, "", false
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

// rawDecision is the reviewer's JSON contract: {decision, notes, reasoning}.
// reasoning is optional for back-compat with notes-only output (design §6.1).
type rawDecision struct {
	Decision  string `json:"decision"`
	Notes     string `json:"notes"`
	Reasoning string `json:"reasoning"`
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
			d := verb.GateDecision{Accept: true, Notes: rd.Notes, Reasoning: rd.Reasoning}
			found = &d
		case "reject":
			d := verb.GateDecision{Accept: false, Notes: rd.Notes, Reasoning: rd.Reasoning}
			found = &d
		}
	}
	if found != nil {
		return *found, nil
	}
	return verb.GateDecision{}, fmt.Errorf("no {\"decision\": \"accept\"|\"reject\"} object in reviewer output")
}

// proseVerdict matches an explicit prose decision statement — the word
// "verdict" or "decision" followed (allowing punctuation/markdown/an "is") by
// accept or reject, e.g. `Verdict: **reject**.` or `decision is accept`. The
// marker word is REQUIRED so rubric prose the reviewer echoes ("Reject when a
// criterion is unmet…") can never read as a verdict.
var proseVerdict = regexp.MustCompile(`(?i)\b(?:verdict|decision)\b(?:\s+is)?[^a-zA-Z0-9]{0,12}(accept|reject)`)

// parseProseDecision recovers a verdict from reviewer output that carries no
// parseable JSON decision object but states its conclusion in prose
// (sty_9485d47e). Every prose-verdict marker in the output must agree: one or
// more consistent markers yield that decision (with the full trimmed output as
// the notes — the reviewer's reasons ARE the prose); conflicting markers or none
// at all yield ok=false, leaving the caller's no-verdict handling in place.
func parseProseDecision(out []byte) (verb.GateDecision, bool) {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return verb.GateDecision{}, false
	}
	matches := proseVerdict.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return verb.GateDecision{}, false
	}
	verdict := strings.ToLower(matches[0][1])
	for _, m := range matches[1:] {
		if strings.ToLower(m[1]) != verdict {
			return verb.GateDecision{}, false // conflicting statements — genuinely ambiguous
		}
	}
	notes := text
	const maxNotes = 4000
	if len(notes) > maxNotes {
		notes = "…" + notes[len(notes)-maxNotes:]
	}
	return verb.GateDecision{Accept: verdict == "accept", Notes: notes}, true
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
