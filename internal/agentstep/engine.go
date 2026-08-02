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
	"sync"
	"time"

	"github.com/bobmcallan/satelle/internal/logfile"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/structure"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/wfhook"
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
// resolve everything. A repo may still widen this in .satelle/workflows/agents.toml
// (transparently, the operator's choice); the default grant is read-only.
const defaultTools = "Read,Grep,Glob"

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
	// itemDocs resolves an item's attached documents (name/type/body) so isolated
	// agents judge attachments from the PAYLOAD — no disk path required
	// (sty_58fa970e). Nil-safe: an unwired resolver injects no docs.
	// Named itemDocs to avoid clashing with the docs DocGetter substrate field.
	itemDocs func(ctx context.Context, itemID string) []DocState
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
	// (.satelle/workflows/agents.toml [<name>] sections) for executor dispatch
	// (sty_fd427546). Nil keeps every step in-loop.
	namedAgents func(name string) (config.AgentBinding, bool)
	// resolveSecondary returns a fallback binding for rate-limit failover
	// (sty_5bf61f89). Nil disables secondary retry.
	resolveSecondary func(section string, b config.AgentBinding) (config.AgentBinding, string, bool)
	// newRunner builds the runner for a named binding's interface+command —
	// swappable in tests; defaults to agentcli.RunnerFromBinding
	// (epic:agent-dispatch-transport). iface is "command" (default) or "acp".
	newRunner func(iface, command string) (agentcli.Runner, error)
	// telemetry records a structured, queryable dispatch outcome (a reviewer/
	// executor retry, failure, or timeout) that only the binary observes — the
	// verb layer sees just the final result, not each attempt (sty_b73c3236). Nil
	// disables it (tests / no-ledger environments); best-effort like the other
	// engine-owned logging.
	telemetry TelemetryFunc
	// attachArtifact deposits a validated structured step artifact before the
	// transition status commits. Nil refuses a contracted dispatch rather than
	// silently dropping required output.
	attachArtifact func(context.Context, workitem.Item, string, string, string) (string, string, error)
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
		agentTimeout: defaultAgentTimeout, newRunner: agentcli.RunnerFromBinding,
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
// .satelle/workflows/agents.toml without touching the workflow. An empty value is ignored
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

// SetDocsResolver wires the resolver that lists an item's attachments so every
// isolated agent (reviewer, named executor, retrospective) receives plan/step
// summaries in the transition payload — the Bash-less read channel (sty_58fa970e).
// Nil-safe: an unwired resolver injects no docs.
func (g *Engine) SetDocsResolver(fn func(ctx context.Context, itemID string) []DocState) {
	g.itemDocs = fn
}

// docsPayloadCeiling bounds how many attachment body bytes ride in one payload
// so a long-lived story with many step summaries does not blow the prompt.
const docsPayloadCeiling = 128 << 10

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
	// Docs carries the item's attachments (plan, step summaries, …) so a
	// Bash-less reviewer can judge without reading any disk path (sty_58fa970e).
	// Bodies may be Truncated when the cumulative budget is spent.
	Docs []DocState `json:"docs,omitempty"`
}

// ChildState is one child story's id and status, injected into a parent/epic
// close payload.
type ChildState struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// DocState is one story attachment injected into the transition payload.
type DocState struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Body      string `json:"body,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// fillPayloadDocs attaches resolved docs under the cumulative body budget.
func (g *Engine) fillPayloadDocs(ctx context.Context, itemID string, tp *transitionPayload) {
	if g.itemDocs == nil || itemID == "" {
		return
	}
	all := g.itemDocs(ctx, itemID)
	if len(all) == 0 {
		return
	}
	var used int
	out := make([]DocState, 0, len(all))
	for _, d := range all {
		// type:change patches are disk retention for on-demand review; they must
		// not ride the cumulative payload ceiling or starve plan/step-summary
		// (sty_948ad5df). Pull via satelle story doc / story diff --recorded.
		if strings.EqualFold(d.Type, "change") {
			continue
		}
		// The route document is the OPERATOR's artifact (sty_39e2d9df): it grows by
		// one block per step, and injecting it into the gate that is about to write
		// the NEXT block is both circular and quadratic in tokens. A reviewer already
		// receives the plan and the step summaries. Pull via satelle story route.
		if strings.EqualFold(d.Type, verb.RouteDocName) {
			continue
		}
		if used >= docsPayloadCeiling {
			out = append(out, DocState{Name: d.Name, Type: d.Type, Truncated: true})
			continue
		}
		body := d.Body
		if used+len(body) > docsPayloadCeiling {
			// Prefer shipping a truncated marker over a partial body that could
			// mislead a judge into thinking the document is complete.
			out = append(out, DocState{Name: d.Name, Type: d.Type, Truncated: true})
			used = docsPayloadCeiling
			continue
		}
		out = append(out, d)
		used += len(body)
	}
	tp.Docs = out
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
	if err := g.guardWorkflowStructure(ctx, item, toStatus); err != nil {
		return verb.GateDecision{}, err
	}
	// Park resume-to-origin (sty_f75286dc): when ParkOrigin is set and the target
	// is that origin, the edge is declared and ungated — resume must not re-run
	// gates already passed to reach the origin. Wrong resume targets are refused
	// here (and by refuseSkippedStep) so park cannot wormhole around gates.
	if resume, refuse := g.parkResume(ctx, item, toStatus); refuse != nil {
		return verb.GateDecision{}, refuse
	} else if resume {
		return verb.GateDecision{Gated: false}, nil
	}
	skills, edgeAgent, parallelCap, declared, err := g.reviewerSkills(ctx, item, item.Status, toStatus)
	if err != nil {
		return verb.GateDecision{}, err
	}
	if !declared {
		// The active workflow does not declare this edge — it is not a legal move.
		// Refuse it (the caller blocks the transition), so a story cannot skip a
		// gate by jumping across an edge the workflow never declared (sty_ebd3d666).
		// Structured (sty_39e2d9df): with the graph derived there is no file to open,
		// so the refusal itself carries the rule, why it applied here, and where the
		// story may go instead.
		next := g.successorsOf(ctx, item, item.Status)
		ref := wfgovern.Refusal{
			Rule: wfgovern.RuleUndeclaredEdge, Item: item.ID,
			From: item.Status, To: toStatus, Alternatives: next,
		}
		if len(next) > 0 {
			ref.Why = fmt.Sprintf(
				"the route puts %s after %s; entry to %s is gated, and an undeclared edge would reach it with no reviewer",
				strings.Join(next, " or "), item.Status, toStatus)
		} else {
			ref.Why = fmt.Sprintf("the route declares no step after %s", item.Status)
			ref.Remedy = "fix the workflow's declaration of done, or move the story to a declared state"
		}
		return verb.GateDecision{}, ref
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
	// Build ordered gate list; edge skills share the edge's agent= binding
	// (sty_a476a2f8). Scoped nodes carry their own agent=.
	var ordered []reviewerRef
	for _, sk := range skills {
		ordered = append(ordered, reviewerRef{skill: sk, agent: edgeAgent})
	}
	sysStart := len(ordered)
	ordered = append(ordered, sys...)

	// Parallel opt-in (sty_4f0a15db): edge parallel=N|true runs reviewers
	// concurrently (no short-circuit). Absent/0 or a single reviewer keeps the
	// sequential first-reject loop byte-for-byte.
	if parallelCap > 0 && len(ordered) >= 2 {
		return g.runGateParallel(ctx, item, toStatus, ordered, sysStart, parallelCap)
	}

	var result verb.GateDecision
	for i, ref := range ordered {
		skill := ref.skill
		if skill == "" {
			continue
		}
		dec, rerr := g.runReviewer(ctx, item, toStatus, skill, ref.agent)
		if rerr != nil {
			return dec, rerr
		}
		if !dec.Gated {
			// Declared but this reviewer's rubric is absent — advisory, skip it.
			// Carry WHICH skill was skipped so the advance is recorded as ungated
			// rather than looking like an edge that never had a gate (sty_d59ec6a9).
			result.Unresolved = append(result.Unresolved, dec.Unresolved...)
			continue
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

// runGateParallel runs every reviewer in ordered concurrently with a bounded
// semaphore of size cap (sty_4f0a15db). Collects ALL verdicts (no short-circuit);
// order of result.Reviewers is the input index order. A reviewer ERROR (not a
// reject) returns the lowest-index error after all have finished.
func (g *Engine) runGateParallel(ctx context.Context, item workitem.Item, toStatus string, ordered []reviewerRef, sysStart, cap int) (verb.GateDecision, error) {
	if cap < 1 {
		cap = 1
	}
	if cap > len(ordered) {
		cap = len(ordered)
	}
	type slot struct {
		dec verb.GateDecision
		err error
	}
	results := make([]slot, len(ordered))
	sem := make(chan struct{}, cap)
	var wg sync.WaitGroup
	for i, ref := range ordered {
		if ref.skill == "" {
			continue
		}
		wg.Add(1)
		go func(i int, ref reviewerRef) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i].err = ctx.Err()
				return
			}
			dec, rerr := g.runReviewer(ctx, item, toStatus, ref.skill, ref.agent)
			results[i] = slot{dec: dec, err: rerr}
		}(i, ref)
	}
	wg.Wait()

	// Prefer lowest-index non-nil error (reviewer ERROR ≠ reject).
	for i := range results {
		if results[i].err != nil {
			return results[i].dec, results[i].err
		}
	}

	var result verb.GateDecision
	var firstReject *verb.GateDecision
	var lastGated *verb.GateDecision
	for i, ref := range ordered {
		if ref.skill == "" {
			continue
		}
		dec := results[i].dec
		if !dec.Gated {
			// Same advisory carry as the serial path — without it the parallel
			// edge silently loses the record of an ungated advance (sty_d59ec6a9).
			result.Unresolved = append(result.Unresolved, dec.Unresolved...)
			continue
		}
		result.Gated = true
		result.Reviewers = append(result.Reviewers, verb.ReviewerVerdict{
			Skill: ref.skill, Order: i, Accept: dec.Accept, Notes: dec.Notes, Reasoning: dec.Reasoning, System: i >= sysStart,
			Command: dec.Command, Context: dec.Context, Model: dec.Model,
			TokensIn: dec.TokensIn, TokensOut: dec.TokensOut, TokensTotal: dec.TokensTotal, DurationMs: dec.DurationMs,
		})
		d := dec
		lastGated = &d
		if !dec.Accept && firstReject == nil {
			firstReject = &d
		}
	}
	// Top-level fields: first reject if any, else last gated (mirrors serial path).
	pick := lastGated
	if firstReject != nil {
		pick = firstReject
		result.Accept = false
	} else if pick != nil {
		result.Accept = true
	}
	if pick != nil {
		result.Skill = pick.Skill
		result.Notes = pick.Notes
		result.Reasoning = pick.Reasoning
		result.Command = pick.Command
		result.Context = pick.Context
		result.Model = pick.Model
		result.TokensIn, result.TokensOut, result.TokensTotal = pick.TokensIn, pick.TokensOut, pick.TokensTotal
		result.DurationMs = pick.DurationMs
		if firstReject == nil {
			result.Accept = pick.Accept
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
func (g *Engine) guardWorkflowStructure(ctx context.Context, item workitem.Item, toStatus string) error {
	// A lifecycle is a DERIVED ROUTE, so the doc to judge is whichever half is
	// malformed (sty_d953c5d8). Judging the route source is the whole point of
	// this guard now — activeWorkflow deliberately does not return route halves,
	// so without this the guard would pass every broken route silently.
	if workflows, lerr := g.docs.List(ctx, "workflows"); lerr == nil {
		if _, governs := wfgovern.RouteGoverns(workflows, workflowCategory(item)); governs {
			for _, w := range workflows {
				if !wfgovern.IsRouteSource(w.Name) || w.Embedded {
					continue
				}
				if problems := structure.Doc("workflows", w.Name, w.Body, nil); len(problems) > 0 {
					return wfgovern.Refusal{
						Rule: wfgovern.RuleStructureGuard, Item: item.ID, Workflow: w.Name,
						From: item.Status, To: toStatus,
						Why: fmt.Sprintf("the governing workflow fails structure validation (%s), so no gate under it can be trusted to judge",
							strings.Join(problems, "; ")),
						Remedy: fmt.Sprintf("fix the substrate (`satelle workflow validate %s`) — no transition is legal until it passes", w.Name),
					}
				}
			}
			return nil
		}
	}
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
		// No alternatives on purpose: a workflow that fails structure validation
		// governs NO legal move, so the only honest answer is the remedy.
		return wfgovern.Refusal{
			Rule: wfgovern.RuleStructureGuard, Item: item.ID, Workflow: doc.Name,
			From: item.Status, To: toStatus,
			Why: fmt.Sprintf("the governing workflow fails structure validation (%s), so no gate under it can be trusted to judge",
				strings.Join(problems, "; ")),
			Remedy: fmt.Sprintf("fix the substrate (`satelle workflow validate %s`) — no transition is legal until it passes", doc.Name),
		}
	}
	return nil
}

// SetNamedAgents wires the resolver for NAMED agent bindings from the agents
// layer (.satelle/workflows/agents.toml [<name>] sections) — the WHO of a workflow node's
// agent=<name> allocation (sty_fd427546). Nil keeps every step in-loop.
func (g *Engine) SetNamedAgents(fn func(name string) (config.AgentBinding, bool)) { g.namedAgents = fn }

// SetArtifactAttacher wires the verb-owned typed document writer used by
// structured step output contracts.
func (g *Engine) SetArtifactAttacher(fn func(context.Context, workitem.Item, string, string, string) (string, string, error)) {
	g.attachArtifact = fn
}

// SetSecondaryResolver wires rate-limit failover (sty_5bf61f89). The resolver
// returns (binding, name, ok) for a primary section + binding.
func (g *Engine) SetSecondaryResolver(fn func(section string, b config.AgentBinding) (config.AgentBinding, string, bool)) {
	g.resolveSecondary = fn
}

// DispatchExecutor implements verb.ExecutorDispatcher: when the TARGET state of
// an accepted transition is allocated to a NAMED agent (agent=<name>, neither
// "executor" nor "reviewer"), the binding's harness performs the step
// synchronously — prompt assembled from the item (title, body, acceptance
// criteria on stdin) plus the node's @skill rubric, tools/model/principles from
// the binding, nothing hardcoded (sty_fd427546). A missing binding or a failed
// run is an ERROR — the caller refuses the transition (broken definition never
// silently falls back in-loop, consistent with sty_d0d6bb67). agent=executor,
// agent-less and reviewer states dispatch nothing; a named binding whose harness
// is explicitly "in-loop" also stays with the orchestrator.
//
// Flat dispatch (sty_05a5e203): this is the ONLY dispatch entering a state can
// cause, and it happens because the SPINE allocates the step — not because the
// state fires an agent of its own. Entry dispatch (on_enter_agent) is retired:
// steps never call steps, so an advisor is consulted by the orchestrator at a
// moment it chooses, and the route names which advisor that is.
func (g *Engine) DispatchExecutor(ctx context.Context, item workitem.Item, toStatus string) (verb.DispatchResult, error) {
	spec, wfName, err := g.activeSpec(ctx, item)
	if err != nil {
		if ungoverned(err) {
			return verb.DispatchResult{}, nil
		}
		return verb.DispatchResult{}, err
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
	// Resolve WHO performs: a named spine agent= performer, and nothing else.
	// Flat dispatch (sty_05a5e203): the orchestrator is the sole scheduler, so
	// entering a state never fires an agent of its own. An agent-less, executor or
	// reviewer state dispatches nothing — an ADVISOR named on the route is
	// consulted by the orchestrator, at a moment it chooses, and never by entry.
	// Spine skill + surface-matched augmentations compose additively
	// (sty_8225d8a5); dispatchSkill is the first (primary) name for telemetry.
	if target.Agent == "" || target.Agent == "executor" || target.Agent == "reviewer" {
		return verb.DispatchResult{}, nil
	}
	dispatchAgent := target.Agent
	composed := spec.ExecutorSkillsFor(toStatus, item.Tags)
	dispatchSkill := firstStr(composed)
	if dispatchSkill == "" {
		dispatchSkill = target.Skill
	}
	if g.namedAgents == nil {
		return verb.DispatchResult{}, fmt.Errorf(
			"workflow %q allocates state %q to named agent %q but no agents layer is wired", wfName, toStatus, dispatchAgent)
	}
	binding, found := g.namedAgents(dispatchAgent)
	if !found {
		return verb.DispatchResult{}, fmt.Errorf(
			"workflow %q allocates state %q to agent %q but .satelle/workflows/agents.toml defines no [%s] binding — define it, or reassign the step",
			wfName, toStatus, dispatchAgent, dispatchAgent)
	}
	// model= on nodes is superseded (sty_a476a2f8); agents.toml owns the model.
	// Design §9 (a): when the resolved binding is role=reviewer, it is a judge
	// not a performer — do not dispatch as ExpectPerform (isNamedPerformer).
	// Fail loud when a role=reviewer binding is allocated on a performing node.
	if config.ResolvedRole(dispatchAgent, binding) == config.RoleReviewer {
		return verb.DispatchResult{}, fmt.Errorf(
			"workflow %q allocates performing state %q to agent %q with role=reviewer — judges advance status only via gates; use a gated edge or scoped on= node",
			wfName, toStatus, dispatchAgent)
	}
	if !isNamedPerformer(dispatchAgent, binding) {
		return verb.DispatchResult{}, nil
	}
	var (
		outputContract agentartifact.Contract
		attemptPolicy  agentartifact.AttemptPolicy
	)
	if dispatchSkill != "" {
		skillBody, serr := g.skillBody(ctx, dispatchSkill)
		if serr != nil && !errors.Is(serr, docindex.ErrNotFound) {
			return verb.DispatchResult{}, serr
		}
		if serr == nil {
			var cerr error
			outputContract, cerr = agentartifact.ParseContract(skillBody)
			if cerr != nil {
				return verb.DispatchResult{}, fmt.Errorf("skill %q output contract: %w", dispatchSkill, cerr)
			}
			attemptPolicy, cerr = agentartifact.ParseAttemptPolicy(skillBody)
			if cerr != nil {
				return verb.DispatchResult{}, fmt.Errorf("skill %q attempt policy: %w", dispatchSkill, cerr)
			}
			if attemptPolicy.Active() && !outputContract.Active() {
				return verb.DispatchResult{}, fmt.Errorf(
					"skill %q declares an attempt policy without an output_* artifact contract", dispatchSkill)
			}
		}
	}
	// Engagement lease (sty_8426b9c0) is acquired for the TARGET engaging state
	// BEFORE this dispatch runs (verb/workitem.go acquire-at-start). Edit/commit
	// gates read the lease, not committed FROM status — so a code-writing named
	// agent may edit during dispatch without the FROM state itself being
	// performing. The prior FROM-performing band-aid (sty_f5bd176f) is removed.
	runner, err := g.newRunner(binding.ResolvedInterface(), binding.CommandTemplate())
	if err != nil {
		return verb.DispatchResult{}, fmt.Errorf("named agent %q: broken command in .satelle/workflows/agents.toml: %w", dispatchAgent, err)
	}
	if runner == nil {
		return verb.DispatchResult{}, nil // command "in-loop": the orchestrator performs the step
	}
	// A dispatched executor starts fresh and reconstructs its context by PULLING the
	// story, its documents, and the ledger — either via the read-only satelle CLI
	// (the pull-context call-to-action, sty_47d31300) or via disk reads under the
	// home-keyed runtime stories dir when the binding has a file-read tool but no
	// shell (sty_565a0202 grok coder; path: sty_58fa970e / sty_4660bbe1). Without a
	// context channel the agent is silently context-starved. Refuse the dispatch
	// with an actionable fix rather than run a blind agent — the no-silent-fallback
	// style the engine uses for a missing binding.
	if !config.GrantsContextChannel(binding.Tools) {
		return verb.DispatchResult{}, fmt.Errorf(
			"named agent %q cannot perform step %q: its .satelle/workflows/agents.toml [%s] tools grant has no context channel (add `Bash(satelle:*)` for the satelle CLI, or `read_file` for disk reads under ~/.satelle/<repo-key>/stories/<id>/)",
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
		return verb.DispatchResult{}, fmt.Errorf("named agent %q: invalid timeout in .satelle/workflows/agents.toml [%s]: %w", dispatchAgent, dispatchAgent, terr)
	}
	eventSink, sinkPath, closeSink := g.dispatchSink(dispatchAgent, item.ID)
	if closeSink != nil {
		defer closeSink()
	}
	rawSink, closeRaw := g.dispatchRawSink(sinkPath)
	if closeRaw != nil {
		defer closeRaw()
	}
	var eventSinkMu sync.Mutex
	onEvent := func(ev agentcli.Event) {
		if eventSink == nil {
			return
		}
		eventSinkMu.Lock()
		_, _ = io.WriteString(eventSink, agentcli.FormatEvent(ev))
		eventSinkMu.Unlock()
	}
	if sinkPath != "" {
		g.emitProgress("dispatching step %s to named agent %s (may take several minutes)… live output: %s", toStatus, dispatchAgent, sinkPath)
	} else {
		g.emitProgress("dispatching step %s to named agent %s (may take several minutes)…", toStatus, dispatchAgent)
	}
	execPayload := transitionPayload{Story: item, From: item.Status, To: toStatus, ReviewSkill: dispatchSkill}
	g.fillPayloadDocs(ctx, item.ID, &execPayload)
	charter := executorCharter(dispatchAgent, toStatus, wfName)
	var finalArtifact *agentartifact.Artifact
	var invRes InvokeResult
	if attemptPolicy.Active() {
		var attemptErr error
		invRes, finalArtifact, attemptErr = g.runArtifactAttempts(
			ctx, item, toStatus, dispatchSkill, dispatchAgent, rubric,
			execPayload, charter, binding, runner, timeout, rawSink, onEvent,
			outputContract, attemptPolicy)
		if attemptErr != nil {
			invRes.Err = attemptErr
		}
	} else {
		invRes = g.Invoke(ctx, InvokeRequest{
			Binding: binding,
			Section: dispatchAgent,
			Rubric:  rubric,
			Payload: execPayload,
			Charter: charter,
			Expect:  ExpectPerform,
			Timeout: timeout,
			Runner:  runner,
			Sink:    rawSink,
			OnEvent: onEvent,
			StoryID: item.ID,
			Step:    toStatus,
			Skill:   dispatchSkill,
			Actor:   "executor",
		})
	}
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
	if outputContract.Active() {
		if !attemptPolicy.Active() {
			artifact, derr := agentartifact.Decode(invRes.Stdout)
			if derr != nil {
				if !outputContract.Required && errors.Is(derr, agentartifact.ErrNoArtifact) {
					return res, nil
				}
				return res, fmt.Errorf("named agent %q structured output for step %q: %w", dispatchAgent, toStatus, derr)
			}
			artifact, verr := agentartifact.Validate(artifact, outputContract, item.AcceptanceCriteria)
			if verr != nil {
				return res, fmt.Errorf("named agent %q structured output for step %q: %w", dispatchAgent, toStatus, verr)
			}
			finalArtifact = &artifact
		}
		if finalArtifact == nil {
			return res, nil
		}
		if g.attachArtifact == nil {
			return res, fmt.Errorf("named agent %q produced contracted output for step %q but no artifact attachment writer is configured", dispatchAgent, toStatus)
		}
		name, typ, aerr := g.attachArtifact(ctx, item, finalArtifact.Name, finalArtifact.Type, finalArtifact.Body)
		if aerr != nil {
			return res, fmt.Errorf("named agent %q artifact attachment for step %q: %w", dispatchAgent, toStatus, aerr)
		}
		res.ArtifactName, res.ArtifactType = name, typ
	}
	return res, nil
}

// dispatchRawSink creates a sibling raw transport trace only when explicitly
// requested. Hidden reasoning is still filtered and obvious credentials are
// redacted by agentcli before bytes reach this writer.
func (g *Engine) dispatchRawSink(normalizedPath string) (io.Writer, func()) {
	if normalizedPath == "" {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SATELLE_AGENT_TRACE_RAW"))) {
	case "1", "true", "yes", "on":
	default:
		return nil, nil
	}
	path := strings.TrimSuffix(normalizedPath, ".log") + "-raw.log"
	f, err := os.Create(path)
	if err != nil {
		return nil, nil
	}
	return f, func() { _ = f.Close() }
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
			"no [%s] binding in .satelle/workflows/agents.toml — define it (with Bash(satelle:*) so it can file proposals) to run the retrospective", retrospectAgent)
	}
	runner, err := g.newRunner(binding.ResolvedInterface(), binding.CommandTemplate())
	if err != nil {
		return verb.DispatchResult{}, fmt.Errorf("%s agent: broken command: %w", retrospectAgent, err)
	}
	if runner == nil {
		return verb.DispatchResult{}, fmt.Errorf("%s agent harness is in-loop; set a real harness to dispatch it", retrospectAgent)
	}
	if !config.GrantsContextChannel(binding.Tools) {
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
	retroPayload := transitionPayload{Story: item, From: item.Status, To: item.Status, ReviewSkill: retrospectSkill}
	g.fillPayloadDocs(ctx, item.ID, &retroPayload)
	invRes := g.Invoke(ctx, InvokeRequest{
		Binding: binding,
		Section: retrospectAgent,
		Rubric:  rubric,
		Payload: retroPayload,
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

// dispatchSink opens a per-dispatch normalized event log under
// <data_dir>/logs/dispatch/ so an operator can `tail -f` provider-neutral,
// sanitized progress while it runs. Returns a nil writer, empty path, and nil
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
	spec, _, err := g.activeSpec(ctx, item)
	if err != nil {
		if ungoverned(err) {
			return verb.GateDecision{}, false, nil
		}
		return verb.GateDecision{}, false, err
	}
	if item.Status != spec.Start() || toStatus == "cancelled" {
		return verb.GateDecision{}, false, nil // not the engagement edge
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
// gateAgent is the agents.toml section that runs this gate (agent=<name>); empty
// or "reviewer" resolves [reviewer] (sty_a476a2f8).

// gateBinding resolves the agents.toml section that runs a gate (sty_a476a2f8).
// Empty or "reviewer" → g.reviewerBinding. Any other name requires namedAgents
// and role=reviewer (enforced by the caller).
func (g *Engine) gateBinding(section string) (config.AgentBinding, string, error) {
	section = strings.TrimSpace(section)
	if section == "" || section == "reviewer" {
		return g.reviewerBinding, "reviewer", nil
	}
	if g.namedAgents == nil {
		return config.AgentBinding{}, section, fmt.Errorf(
			"gate refused: agent=%s but no named-agent resolver is configured", section)
	}
	b, ok := g.namedAgents(section)
	if !ok {
		return config.AgentBinding{}, section, fmt.Errorf(
			"gate refused: agent=%s has no [%s] binding in agents.toml", section, section)
	}
	return b, section, nil
}

func (g *Engine) runReviewer(ctx context.Context, item workitem.Item, toStatus, skill, gateAgent string) (verb.GateDecision, error) {
	body, err := g.skillBody(ctx, skill)
	if err != nil {
		if errors.Is(err, docindex.ErrNotFound) {
			// Advisory degradation: the edge DECLARED this gate but its rubric is
			// not installed, so nothing judges the transition and it advances.
			// Fail-open is deliberate (a fresh repo must work before every gate is
			// authored) — but it must not be SILENT, so name the skill that was
			// skipped (sty_d59ec6a9).
			return verb.GateDecision{Gated: false, Skill: skill, Unresolved: []string{skill}}, nil
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
	g.fillPayloadDocs(ctx, item.ID, &tp)
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
	binding, section, berr := g.gateBinding(gateAgent)
	if berr != nil {
		return verb.GateDecision{Gated: true, Skill: skill}, berr
	}
	// Engine-wide caches fill only the default [reviewer] binding. Named
	// bindings must be self-contained in agents.toml (sty_a476a2f8).
	if section == "reviewer" {
		if binding.Tools == "" {
			binding.Tools = g.tools
		}
		if binding.Model == "" {
			binding.Model = g.model
		}
		if len(binding.Env) == 0 {
			binding.Env = g.reviewerEnv
		}
		if binding.Principles == "" && binding.InjectPrinciples == nil {
			if g.injectPrinciples {
				binding.Principles = config.PrinciplesSession
			} else {
				binding.Principles = config.PrinciplesNone
			}
		}
	}
	// Mechanism: a gate needs an isolated verdict. command=in-loop cannot produce
	// one — fail loud at gate time (design §6.4), not by policing tools/model.
	if config.IsInLoopCommand(binding.CommandTemplate()) {
		return verb.GateDecision{Gated: true, Skill: skill}, fmt.Errorf(
			"gate refused: reviewer binding %q is command=in-loop and cannot produce an isolated verdict — set [%s] command to an isolated agent CLI (claude|grok|codex or a full template)", section, section)
	}
	// Role must resolve to reviewer for the gate binding (design §4.4 / §8 / sty_a476a2f8).
	if config.ResolvedRole(section, binding) != config.RoleReviewer {
		return verb.GateDecision{Gated: true, Skill: skill}, fmt.Errorf(
			"gate refused: binding [%s] has role=%q (want role=reviewer) — a named performer never advances status; allocate a role=\"reviewer\" binding on gated edges",
			section, config.ResolvedRole(section, binding))
	}
	// Default [reviewer] uses the bootstrap runner (g.runner). A named
	// role=reviewer binding must run its OWN harness — leave Runner nil so
	// Invoke builds from the binding (sty_68dafd5f; runner must follow agent=).
	var gateRunner agentcli.Runner
	if section == "reviewer" {
		if g.runner == nil {
			return verb.GateDecision{Gated: true, Skill: skill}, fmt.Errorf(
				"reviewer: transition %s→%s is gated by %q but no agent runner is configured", item.Status, toStatus, skill)
		}
		gateRunner = g.runner
	}
	res := g.Invoke(ctx, InvokeRequest{
		Binding:  binding,
		Section:  section,
		Rubric:   body,
		Payload:  tp,
		Charter:  reviewerCharter(),
		Expect:   ExpectVerdict,
		Timeout:  g.agentTimeout,
		Runner:   gateRunner,
		Attempts: g.attempts,
		StoryID:  item.ID,
		Step:     toStatus,
		Skill:    skill,
		Actor:    section,
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
	spec, _, err := g.activeSpec(ctx, item)
	if err != nil {
		if ungoverned(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []reviewerRef
	// item.Tags decide whether a surface-scoped node is ENQUEUED (sty_c6d093c8).
	// Skipped applies_to filters leave a telemetry artifact so a silent skip is
	// not identical to "no such surface gate" (sty_dcce86d5).
	enqueued, skipped := spec.ScopedReviewersSplit(toStatus, item.Tags)
	for _, s := range enqueued {
		if !containsStr(exclude, s.Skill) {
			out = append(out, reviewerRef{skill: s.Skill, agent: s.Agent})
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

// reviewerRef is one gate to run: skill name + agents.toml binding section.
// agent empty means [reviewer] (sty_a476a2f8).
type reviewerRef struct {
	skill, agent string
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
// its prose recap (empty when no summariser rubric is installed). The binding
// named on the step node (agent=<name>, default [reviewer]) supplies the harness
// and model so a cheap summariser can narrate without burning the deep judgment
// model. Grant stays read-only (observes, never mutates).
//
// principles stays PrinciplesNone regardless of the binding's principles=
// (deliberate): the summariser is a rubric-only narrator; honouring session
// principles on every transition would inject the constitution into every recap
// and defeat a cheap high-frequency path (sty_8ee40f94).
//
// TODO(sty_ba860c8a): fold onto Invoke once a soft-fail/empty-retry expect mode
// exists without ballooning ExpectVerdict/ExpectPerform. Today it still uses
// buildRequest+runOnce directly (AC1 carve-out).
func (g *Engine) Summarise(ctx context.Context, item workitem.Item, from, to string) (verb.SummaryResult, error) {
	// The summariser runs ONLY when the active workflow DECLARES a step-summary
	// node (transparent opt-in via the DOT) — there is no hidden always-on
	// summariser (sty_9a139c78). A non-declaring workflow records nothing.
	agentSec, declared, mandatory := g.stepSummaryDeclared(ctx, item)
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
	binding, section, berr := g.gateBinding(agentSec)
	if berr != nil {
		return soft("step summary binding: %v", berr)
	}
	if config.ResolvedRole(section, binding) != config.RoleReviewer {
		return soft("step summary agent=%s has role=%q (want role=reviewer)",
			section, config.ResolvedRole(section, binding))
	}
	// Default [reviewer] reuses the engine bootstrap runner; a named binding
	// builds its own harness (same rule as gated edges — sty_68dafd5f).
	var runner agentcli.Runner
	tools, model, env := binding.Tools, binding.Model, binding.Env
	if section == "reviewer" {
		if g.runner == nil {
			return soft("step summary is mandatory but no agent runner is configured")
		}
		runner = g.runner
		if tools == "" {
			tools = g.tools
		}
		if model == "" {
			model = g.model
		}
		if len(env) == 0 {
			env = g.reviewerEnv
		}
	} else {
		r, rerr := g.newRunner(binding.ResolvedInterface(), binding.CommandTemplate())
		if rerr != nil {
			return soft("step summary agent=%s: %v", section, rerr)
		}
		runner = r
	}
	// The summariser prompt is rubric-only (no charter, principles=none) so it
	// stays a plain narrator — buildRequest omits empty sections. Grant is read-only.
	req, err := g.buildRequest(ctx, invocation{
		rubric:     body,
		principles: config.PrinciplesNone,
		payload:    summaryPayload{Story: item, From: from, To: to},
		tools:      tools,
		model:      model,
		effort:     binding.Effort,
		settings:   binding.Settings,
		env:        env,
	})
	if err != nil {
		return verb.SummaryResult{}, err
	}
	g.emitProgress("summarising step %s→%s via [%s] (may take a minute)…", from, to, section)
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
		out, usage, rerr := g.runOnce(ctx, runner, req, g.agentTimeout)
		if rerr != nil {
			if errors.Is(rerr, context.DeadlineExceeded) && ctx.Err() == nil {
				g.telemetryEvent(ctx, item.ID, section, "agent-timeout", map[string]any{
					"skill": summariserSkill, "step": to, "attempt": attempt, "attempts": attempts,
				})
				return soft("mandatory step summary timed out after %s", g.agentTimeout)
			}
			lastErr = rerr
			g.logReviewerFailure(summariserSkill, attempt, attempts, rerr, nil)
			g.telemetryEvent(ctx, item.ID, section, "agent-retry", map[string]any{
				"skill": summariserSkill, "step": to, "attempt": attempt, "attempts": attempts, "outcome": classifyOutcome(rerr),
			})
			continue // transient — retry
		}
		if s := strings.TrimSpace(string(out)); s != "" {
			// The summariser's own token/wall-time cost (sty_a699ad14, a documented
			// gap now closed): the verb layer folds this into an agent_invocation row
			// alongside the step_summary text, so `satelle story cost` sees it too.
			return verb.SummaryResult{
				Text: s, Command: runner.Command(), Context: summariserSkill, Model: model,
				TokensIn: usage.InputTokens, TokensOut: usage.OutputTokens, TokensTotal: usage.TotalTokens,
				DurationMs: usage.Duration.Milliseconds(),
			}, nil
		}
		lastErr = fmt.Errorf("empty summary output")
		g.logReviewerFailure(summariserSkill, attempt, attempts, lastErr, out)
		g.telemetryEvent(ctx, item.ID, section, "agent-retry", map[string]any{
			"skill": summariserSkill, "step": to, "attempt": attempt, "attempts": attempts, "outcome": "empty-output",
		})
	}
	g.telemetryEvent(ctx, item.ID, section, "agent-failure", map[string]any{
		"skill": summariserSkill, "step": to, "attempts": attempts, "outcome": classifyOutcome(lastErr),
	})
	return soft("mandatory step summary failed after %d attempts: %v", attempts, lastErr)
}

// MandatorySummary reports whether item's active workflow declares a MANDATORY
// step-summary node — used to gate the done-time missing-summary surfacing
// (sty_a1151fb0). Implements verb.StepSummariser.
func (g *Engine) MandatorySummary(ctx context.Context, item workitem.Item) bool {
	_, _, mandatory := g.stepSummaryDeclared(ctx, item)
	return mandatory
}

// stepSummaryDeclared reports whether the workflow active for category declares a
// step-summary node (wfdot StepSummary), its agent= section (empty → [reviewer]),
// and whether it is mandatory.
func (g *Engine) stepSummaryDeclared(ctx context.Context, item workitem.Item) (agent string, declared, mandatory bool) {
	spec, _, err := g.activeSpec(ctx, item)
	if err != nil {
		return "", false, false
	}
	return spec.StepSummaryBinding()
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
	// 2. Optional content/alignment review — the reviewer skill AND the logical
	// agent that runs it are DECLARED by the active workflow's create-review hook
	// (selected by the draft's category), not hardcoded. Absent a declaration (or
	// the skill does not resolve), creation stays deterministic-only.
	hook, declared := g.createReviewHook(ctx, draft.Category)
	if !declared || hook.Skill == "" {
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
	// The hook's DECLARED agent, not an empty selector: an omitted agent is
	// wfhook.DefaultAgent ("reviewer") — the same binding as before, but chosen by
	// the substrate and inspectable, rather than fallen into inside gateBinding.
	dec, err := g.runReviewer(ctx, draftItem, "backlog", hook.Skill, hook.Agent)
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

// createReviewHook resolves the create-review LIFECYCLE HOOK declared by the
// workflow active for the category — its `hooks:` entry or the `create_review:`
// shorthand. Not declared when no workflow governs the category or the workflow
// declares none, so creation stays deterministic-only (the binding is
// configuration, never a hardcoded filename).
//
// The hook carries both the skill and the logical agent; the engine does not
// choose either. Declaration DEFECTS (an unknown operation, a missing skill) are
// deliberately not judged here — that is validation's job, surfaced by
// `satelle agent validate` / `satelle workflow validate` before anything runs.
// The engine keeps only its mechanism-level refusals at dispatch (role, in-loop).
func (g *Engine) createReviewHook(ctx context.Context, category string) (wfhook.Hook, bool) {
	// A lifecycle hook is workflow FRONTMATTER, and a derived route's frontmatter
	// lives on its declaration of done — the half that says what this repo means
	// by finished, which is where a create gate belongs. Read it first, so a
	// converted repo keeps the gate its graphs used to declare (sty_9835070d).
	//
	// Only when the route GOVERNS the category, though. The doc index overlays the
	// shipped done.md wherever a repo has no file of that name, so reading it
	// unconditionally would let the default's create gate shadow the one an
	// authored workflow declares — the same precedence hole RouteGoverns closes
	// for the lifecycle itself (sty_3795e7f6).
	if workflows, err := g.docs.List(ctx, "workflows"); err == nil {
		if rs, ok := wfgovern.RouteGoverns(workflows, category); ok {
			if h, hooked := wfhook.For(rs.Done, wfhook.OpCreateReview); hooked {
				return h, true
			}
		}
	}
	doc, err := g.activeWorkflow(ctx, category)
	if err != nil {
		return wfhook.Hook{}, false
	}
	return wfhook.For(doc.Body, wfhook.OpCreateReview)
}

// reviewerSkills resolves the ordered reviewer skills governing the (from→to)
// edge from the workflow active for the item's category, the edge's model=
// override (empty = inherit binding), and whether the edge is a DECLARED
// transition of that workflow. An absent workflow means no governance at all —
// every edge is allowed and ungated (declared=true, no skills), so fresh repos
// and the baseline keep working.
func (g *Engine) reviewerSkills(ctx context.Context, item workitem.Item, from, to string) (skills []string, model string, parallel int, declared bool, err error) {
	spec, _, err := g.activeSpec(ctx, item)
	if ungoverned(err) {
		return nil, "", 0, true, nil
	}
	if err != nil {
		// A lifecycle EXISTS and does not resolve. Refusing here is the point:
		// falling through as "ungated but declared" would advance the story past
		// every gate the route declares (sty_9835070d).
		return nil, "", 0, false, err
	}
	skills, model, parallel, declared = specReviewerSkills(spec, from, to)
	return skills, model, parallel, declared, nil
}

// successorsOf returns declared DOT successors of from for agent-facing refuse
// messages (sty_ebd3d666). Empty when no workflow/DOT resolves.
func (g *Engine) successorsOf(ctx context.Context, item workitem.Item, from string) []string {
	spec, _, err := g.activeSpec(ctx, item)
	if err != nil {
		return nil
	}
	return spec.Successors(from)
}

// parkResume reports whether item→toStatus is a park resume-to-origin (resume
// true, ungated) or an illegal resume (refuse non-nil). When ParkOrigin is empty
// both are zero and the caller falls through to ordinary edge handling
// (sty_f75286dc).
func (g *Engine) parkResume(ctx context.Context, item workitem.Item, toStatus string) (resume bool, refuse error) {
	origin := strings.TrimSpace(item.ParkOrigin)
	if origin == "" {
		return false, nil
	}
	spec, _, err := g.activeSpec(ctx, item)
	if err != nil || !spec.IsParkState(item.Status) {
		return false, nil
	}
	if toStatus == origin {
		return true, nil
	}
	// Explicit non-performing exits (cancelled) stay on ordinary gate path.
	if spec.HasEdge(item.Status, toStatus) && !spec.IsPerformingState(toStatus) {
		return false, nil
	}
	// Performing targets other than origin, or undeclared exits: refuse so park
	// cannot wormhole around gates (e.g. park from in_progress → release).
	if spec.IsPerformingState(toStatus) || !spec.HasEdge(item.Status, toStatus) {
		var exits []string
		for _, to := range spec.Successors(item.Status) {
			if !spec.IsPerformingState(to) {
				exits = append(exits, to)
			}
		}
		return false, wfgovern.Refusal{
			Rule: wfgovern.RuleParkResume, Item: item.ID,
			From: item.Status, To: toStatus,
			Why: fmt.Sprintf(
				"a parked story resumes to the state it parked from (%q); resuming elsewhere would re-enter the route past gates it never passed",
				origin),
			Alternatives: append([]string{origin}, exits...),
		}
	}
	return false, nil
}

// activeWorkflow returns the authored WORKFLOW doc governing an item of the
// given category. Selection matches the item's category against each indexed
// workflow's `applies_to` frontmatter: a workflow listing the category wins, a
// wildcard (`applies_to: ["*"]`) workflow is the next-best. This is the
// configuration-over-code path — a repo adds a category-specific workflow as
// substrate and it takes effect with no binary change.
//
// It knows nothing about the derived route: the lifecycle front door is
// activeSpec / wfgovern.SpecFor, and the order-zero fallback is now the route
// the binary ships rather than a graph resolved by name (sty_3795e7f6). No
// applicable workflow is ErrNotFound, which every caller already treats as "no
// authored workflow governs this".
func (g *Engine) activeWorkflow(ctx context.Context, category string) (docindex.Doc, error) {
	workflows, err := g.docs.List(ctx, "workflows")
	if err != nil {
		return docindex.Doc{}, err
	}
	if ordered := wfgovern.OrderedWorkflows(wfgovern.LifecycleWorkflows(workflows), category); len(ordered) > 0 {
		return ordered[0], nil // the highest-priority applicable workflow
	}
	return docindex.Doc{}, docindex.ErrNotFound
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

// activeSpec resolves the LIFECYCLE governing item through the one front door
// (wfgovern.SpecFor): the derived route the substrate carries, and a named
// refusal when a workflows doc claims the category but declares no route. It
// replaces the resolve-then-parse pair every gating path used to repeat, so a
// repo's conversion needs no per-call-site change (sty_9835070d).
//
// The error is not collapsed. wfgovern.ErrNoWorkflow means nothing governs the
// item at all — a fresh repo, the case every caller below already treats as
// ungoverned. Any OTHER error means a lifecycle exists and does not resolve, and
// that must never read the same way: a route that fails to build would otherwise
// silently drop every gate it declares.
func (g *Engine) activeSpec(ctx context.Context, item workitem.Item) (wfdot.Spec, string, error) {
	workflows, err := g.docs.List(ctx, "workflows")
	if err != nil {
		return wfdot.Spec{}, "", err
	}
	spec, name, _, err := wfgovern.SpecFor(workflows, item)
	if err == nil {
		return spec, name, nil
	}
	// There is no second fallback to reach for: the order-zero lifecycle is the
	// route the binary ships, and the doc index overlays it into `workflows`
	// wherever the repo has no half of its own, so SpecFor has already considered
	// it (sty_3795e7f6). ErrNoWorkflow here means the substrate genuinely governs
	// nothing — a repo that deleted the shipped route.
	return wfdot.Spec{}, name, err
}

// ungoverned reports whether err from activeSpec means "nothing governs this
// item" — the fresh-repo case a caller may treat as no governance. A route that
// exists but does not build is NOT ungoverned and must not take this path.
func ungoverned(err error) bool {
	return errors.Is(err, wfgovern.ErrNoWorkflow) || errors.Is(err, docindex.ErrNotFound)
}

// WorkflowNameFor returns the name of the workflow that governs a story of the
// given category — the value stamped on the story at create. Empty when no
// workflow governs the category. Used by the create path to record the choice.
func (g *Engine) WorkflowNameFor(ctx context.Context, category string) string {
	// A story is stamped with what will GOVERN it. When a derived route claims
	// the category, that is the route — stamping an authored workflow the engine
	// will not consult would be a lie on every story created after a conversion
	// (sty_9835070d).
	if workflows, err := g.docs.List(ctx, "workflows"); err == nil {
		if _, ok := wfgovern.RouteGoverns(workflows, category); ok {
			return wfgovern.DerivedRouteName
		}
	}
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
// be re-stamped onto a workflow that declares its current status.
//
// A lifecycle is a DERIVED ROUTE now, and a route's states depend on the story's
// category — which a name alone does not carry. So a name that resolves returns
// no states, and the caller skips the status check rather than stranding the
// story: the same contract an unparseable lifecycle always had here
// (sty_d953c5d8).
func (g *Engine) WorkflowStates(ctx context.Context, name string) ([]string, bool) {
	if _, err := g.docs.Get(ctx, "workflows", name); err != nil {
		return nil, false
	}
	return nil, true
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

	// (2) Referenced skills that do not resolve — per workflow, so the same
	// definition serves the whole-set callers and the single-doc authoring paths
	// (sty_d59ec6a9). The two cannot drift.
	if resolve != nil {
		for _, w := range workflows {
			problems = append(problems, WorkflowSkillProblems(w, resolve)...)
		}
	}
	sort.Strings(problems)
	return problems
}

// WorkflowSkillProblems reports, for ONE workflow, every skill it names that
// does not resolve in the substrate — edge gates, node @skill: prompts, and
// declared lifecycle hooks — plus any hook declaration defect.
//
// This is the half of WorkflowConsistency that is meaningful per document. The
// ambiguity check is deliberately NOT here: it compares repo workflows against
// each other, so it is whole-set by nature and firing it on a single doc would
// misreport (sty_d59ec6a9 AC3).
//
// Callers: WorkflowConsistency (whole set, where these are FAILs), and the
// authoring paths `workflow validate <name>` / `workflow create` (where they are
// WARNs — a repo mid-authoring writes the workflow before it writes the gate
// skills, so blocking there would make the ordinary sequence impossible).
func WorkflowSkillProblems(w docindex.Doc, resolve func(skill string) bool) []string {
	if resolve == nil {
		return nil
	}
	var problems []string
	// A declared lifecycle hook's skill must resolve too (sty_51ad783b,
	// generalised in sty_ede16f51): an unresolved one silently degrades the
	// operation, which is exactly the misconfiguration to surface here.
	// Declaration defects (unknown operation, malformed entry) surface with
	// it, so one check covers the whole hook grammar.
	hooks, hookProblems := wfhook.Parse(w.Body)
	for _, p := range hookProblems {
		problems = append(problems, fmt.Sprintf("workflow %s %s", w.Name, p))
	}
	for _, h := range hooks {
		if !resolve(h.Skill) {
			problems = append(problems, fmt.Sprintf(
				"workflow %s declares %s %q which does not resolve in the substrate", w.Name, h.Operation, h.Skill))
		}
	}
	// A lifecycle is a derived route, so the skills a workflow-kind doc names come
	// from the route grammar rather than a graph (sty_d953c5d8).
	for _, s := range referencedWorkflowSkills(w.Body) {
		if !resolve(s) {
			// Message text is deliberately IDENTICAL to what the whole-set callers
			// have always printed — AC3 requires their output unchanged. The
			// authoring paths add the "will advance ungated" context around the
			// WARN they wrap it in, rather than editing this shared string.
			problems = append(problems, fmt.Sprintf(
				"workflow %s references skill %q which does not resolve in the substrate", w.Name, s))
		}
	}
	return problems
}

// referencedWorkflowSkills lists every skill one half of a derived route names —
// a step's executor rubrics and entry reviewers, an always-on gate, a park or
// cancel gate — deduped and sorted. A body that is not route grammar names
// nothing, which is the honest answer for a doc that carries no lifecycle
// (sty_d953c5d8).
func referencedWorkflowSkills(body string) []string {
	set := map[string]bool{}
	if lists, err := wfdot.ParseDone(body); err == nil {
		for _, l := range lists {
			for _, sk := range []string{l.ParkGate, l.CancelGate, l.ParkAdvisorSkill} {
				if sk != "" {
					set[sk] = true
				}
			}
		}
	}
	if cat, err := wfdot.ParseSteps(body); err == nil {
		// Catalogue-wide DELIBERATELY: this wants the UNION of every skill any
		// section names, so a skill referenced by any route resolves. Narrowing to
		// one category's selected steps (wfdot.SelectSteps) would report skills the
		// other route families use as unreferenced (sty_a7316b06).
		for _, st := range cat.Steps {
			for _, sk := range append(append([]string(nil), st.Skills...), st.Reviewers...) {
				if sk != "" {
					set[sk] = true
				}
			}
			if st.AdvisorSkill != "" {
				set[st.AdvisorSkill] = true
			}
		}
		for _, g := range cat.Gates {
			if g.Skill != "" {
				set[g.Skill] = true
			}
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
// check carried inside the skill artifact. Delegates to structure.CheckCommand,
// the single definition every caller shares (sty_4cebc624 / sty_6830e78e).
// Empty when the skill carries no check (an LLM reviewer).
func skillCheck(body string) string {
	return structure.CheckCommand(body)
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
// declared but ungated), the edge agent= binding section (empty → reviewer),
// the parallel concurrency cap (0 = sequential; sty_4f0a15db), and whether the
// edge is DECLARED at all.
func reviewerSkillsFor(body, from, to string) (skills []string, agent string, parallel int, declared bool) {
	// The inline `- {from:, to:}` grammar, which some fixtures and legacy bodies
	// still carry. A DERIVED route never reaches here — the front door hands its
	// callers a Spec, and specReviewerSkills answers off that (sty_d953c5d8).
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "- {") || !strings.Contains(l, "from:") || !strings.Contains(l, "to:") {
			continue
		}
		if inlineField(l, "from") == from && inlineField(l, "to") == to {
			if list := inlineListField(l, "reviewer_skills"); len(list) > 0 {
				return list, "", 0, true
			}
			if s := inlineField(l, "reviewer_skill"); s != "" {
				return []string{s}, "", 0, true
			}
			return nil, "", 0, true
		}
	}
	return nil, "", 0, false
}

// specReviewerSkills resolves an edge's gate off an already-built Spec. It is
// the front-door form: whichever representation produced the Spec — an authored
// DOT or a derived route — the edge answers the same way (sty_9835070d).
func specReviewerSkills(spec wfdot.Spec, from, to string) (skills []string, agent string, parallel int, declared bool) {
	for _, tr := range spec.Transitions {
		if tr.From == from && tr.To == to {
			if len(tr.Skills) > 0 {
				return tr.Skills, tr.Agent, tr.Parallel, true
			}
			return nil, tr.Agent, tr.Parallel, true
		}
	}
	return nil, "", 0, false
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
