package agentstep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
)

// Expect selects the post-run contract for Invoke (sty_ba860c8a / design §4).
// ExpectVerdict runs the reviewer retry/parse loop; ExpectPerform is a single run.
type Expect int

const (
	// ExpectVerdict: isolated judge — output must parse to a gate decision.
	ExpectVerdict Expect = iota
	// ExpectPerform: performer — raw stdout, no verdict parse, no retry.
	ExpectPerform
)

// ExpectFromRole maps a resolved binding role to an Expect.
// role=reviewer → verdict; everything else → perform.
func ExpectFromRole(role string) Expect {
	if role == config.RoleReviewer {
		return ExpectVerdict
	}
	return ExpectPerform
}

// InvokeRequest describes one isolated-agent call through the shared Invoke seam.
// Call sites (runReviewer, DispatchExecutor, Retrospect) fill this after their
// own pre-flight; Invoke owns prompt assembly, Runner.Run, and (for verdict)
// retry/parse.
type InvokeRequest struct {
	Binding config.AgentBinding // resolved binding (tools/model/env/settings/role/principles)
	Section string              // agents.toml section name (for role inference / logging)
	Rubric  string              // skill body from the workflow node/edge
	Payload any                 // marshalled to JSON stdin
	Charter string              // optional override; empty → charter from role/expect
	// Expect selects the contract. Zero value is ExpectVerdict — callers that
	// perform must set ExpectPerform explicitly.
	Expect  Expect
	Timeout time.Duration // ≤0 → no per-run deadline (tests) or caller-supplied 0
	// Runner overrides the runner built from Binding.Command. Reviewer path
	// passes g.runner (bootstrap-resolved); named dispatch builds from the binding.
	Runner agentcli.Runner
	// Attempts bounds verdict retries (0 → engine default). Perform ignores this.
	Attempts int
	// Sink, when set, receives live subprocess stdout (named dispatch live log).
	Sink io.Writer

	// Telemetry / progress context for the verdict retry loop (design note:
	// Invoke needs labels beyond the minimal design sketch so retry telemetry
	// and progress lines move intact from runReviewer).
	StoryID string
	Step    string // to-status or step name
	Skill   string // rubric skill name for telemetry/logging
	Actor   string // ledger/telemetry actor (default "reviewer" for verdict)
}

// InvokeResult is the outcome of one Invoke call.
type InvokeResult struct {
	Stdout   []byte
	Usage    agentcli.UsageResult
	Command  string             // resolved harness command for ledger evidence
	Decision *verb.GateDecision // non-nil when ExpectVerdict and parse succeeded
	Err      error
}

// invocation is the internal prompt-assembly shape used by buildRequest.
// principles is the resolved selector (session|all|…|none); empty means none
// (no constitution/principles block).
type invocation struct {
	charter    string // role charter; empty for the summariser (rubric-only prompt)
	rubric     string // the skill body
	principles string // resolved principles selector; empty/none → no inject
	payload    any
	tools      string
	model      string
	settings   map[string]any
	env        map[string]string
}

// buildRequest composes an isolated agent's system prompt in ONE canonical order —
// constitution+principles (when selector ≠ none), role charter, pull-context
// call-to-action, skill rubric — marshals the stdin payload, and fills the
// grant/model/dir. It is the single insertion point for anything satelle wants
// EVERY isolated agent to receive.
func (g *Engine) buildRequest(ctx context.Context, inv invocation) (agentcli.Request, error) {
	var b strings.Builder
	// Principles selector (design §5): when not none, constitution rides order-zero
	// then the selected principles — SessionStart parity (cmd_hook renderAlwaysContent).
	if inv.principles != "" && inv.principles != config.PrinciplesNone {
		if c := strings.TrimSpace(g.constitution); c != "" {
			b.WriteString("# Project constitution\n\n")
			b.WriteString(c)
			b.WriteString("\n\n")
		}
		if resident := g.resolvePrinciples(ctx, inv.principles); resident != "" {
			b.WriteString("# Always-resident principles (satelle)\n\n")
			b.WriteString(resident)
			b.WriteString("\n\n")
		}
	}
	if inv.charter != "" {
		b.WriteString(inv.charter)
		b.WriteString("\n\n")
	}
	// The pull-context call-to-action rides in EVERY isolated-agent prompt.
	b.WriteString(pullContextCallToAction)
	if inv.rubric != "" {
		b.WriteString("\n\n---\n\n")
		b.WriteString(inv.rubric)
	}
	payload, err := json.Marshal(inv.payload)
	if err != nil {
		return agentcli.Request{}, err
	}
	var settings string
	if len(inv.settings) > 0 {
		sb, serr := json.Marshal(inv.settings)
		if serr != nil {
			return agentcli.Request{}, serr
		}
		settings = string(sb)
	}
	return agentcli.Request{
		SystemPrompt: b.String(),
		Payload:      string(payload),
		AllowedTools: inv.tools,
		Model:        inv.model,
		Settings:     settings,
		Env:          inv.env,
		Dir:          g.repoRoot,
	}, nil
}

// Invoke is the ONLY path that calls agentcli.Runner.Run for LLM gate/dispatch
// steps (sty_ba860c8a). It resolves prompt assembly from the binding, runs the
// agent, and for ExpectVerdict applies the retry + verdict parse loop.
func (g *Engine) Invoke(ctx context.Context, req InvokeRequest) InvokeResult {
	binding := req.Binding
	section := req.Section
	if section == "" {
		section = "agent"
	}
	role := config.ResolvedRole(section, binding)
	expect := req.Expect
	// When the caller did not set ExpectPerform explicitly and role is reviewer,
	// default remains ExpectVerdict (zero value). For perform-role bindings the
	// caller must set ExpectPerform (DispatchExecutor/Retrospect do).
	_ = role

	charter := req.Charter
	if charter == "" {
		switch expect {
		case ExpectVerdict:
			charter = reviewerCharter()
		case ExpectPerform:
			// Perform charter is call-site specific (agent/step/workflow names);
			// empty is valid (e.g. if the caller already inlined context).
		}
	}

	principles := binding.ResolvedPrinciples()
	inv := invocation{
		charter:    charter,
		rubric:     req.Rubric,
		principles: principles,
		payload:    req.Payload,
		tools:      binding.Tools,
		model:      binding.Model,
		settings:   binding.Settings,
		env:        binding.Env,
	}
	// Fall back to engine reviewer caches when the binding leaves grant fields
	// empty (tests that only set g.tools/g.model still work until order:3 deletes
	// the scalar path entirely). Prefer binding values when set.
	if inv.tools == "" {
		inv.tools = g.tools
	}
	if inv.model == "" {
		inv.model = g.model
	}
	if len(inv.env) == 0 {
		inv.env = g.reviewerEnv
	}

	agentReq, err := g.buildRequest(ctx, inv)
	if err != nil {
		return InvokeResult{Err: err}
	}
	if req.Sink != nil {
		agentReq.Sink = req.Sink
	}

	runner := req.Runner
	if runner == nil {
		r, rerr := g.newRunner(binding.CommandTemplate())
		if rerr != nil {
			return InvokeResult{Err: fmt.Errorf("broken command for binding %q: %w", section, rerr)}
		}
		if r == nil {
			return InvokeResult{Err: fmt.Errorf(
				"binding %q is command=in-loop and cannot produce an isolated agent run", section)}
		}
		runner = r
	}
	cmdStr := runner.Command()

	timeout := req.Timeout
	if timeout <= 0 && expect == ExpectVerdict {
		timeout = g.agentTimeout
	}

	switch expect {
	case ExpectPerform:
		out, usage, runErr := g.runOnce(ctx, runner, agentReq, timeout)
		return InvokeResult{Stdout: out, Usage: usage, Command: cmdStr, Err: runErr}
	default: // ExpectVerdict
		return g.invokeVerdict(ctx, req, runner, agentReq, cmdStr, timeout)
	}
}

// invokeVerdict runs the ExpectVerdict retry loop: parse JSON/prose decision,
// retry transient no-verdict, fail loud on timeout after attempts.
func (g *Engine) invokeVerdict(ctx context.Context, req InvokeRequest, runner agentcli.Runner, agentReq agentcli.Request, cmdStr string, timeout time.Duration) InvokeResult {
	skill := req.Skill
	if skill == "" {
		skill = "reviewer"
	}
	actor := req.Actor
	if actor == "" {
		actor = "reviewer"
	}
	attempts := req.Attempts
	if attempts < 1 {
		attempts = g.attempts
	}
	if attempts < 1 {
		attempts = 1
	}
	storyID := req.StoryID
	step := req.Step

	var lastErr error
	var lastOut []byte
	for attempt := 1; attempt <= attempts; attempt++ {
		if werr := g.retryWait(ctx, attempt); werr != nil {
			return InvokeResult{Command: cmdStr, Err: werr}
		}
		g.emitProgress("running reviewer %s (attempt %d/%d, may take several minutes)…", skill, attempt, attempts)
		out, usage, rerr := g.runOnce(ctx, runner, agentReq, timeout)
		if rerr != nil {
			if errors.Is(rerr, context.DeadlineExceeded) && ctx.Err() == nil {
				g.logReviewerFailure(skill, attempt, attempts, rerr, nil)
				g.telemetryEvent(ctx, storyID, actor, "agent-timeout", map[string]any{
					"skill": skill, "step": step, "attempt": attempt, "attempts": attempts,
				})
				return InvokeResult{Command: cmdStr, Err: fmt.Errorf(
					"reviewer: %s timed out after %s — the gate did not complete and the transition was NOT enacted; retry when the agent backend is responsive", skill, timeout)}
			}
			lastErr, lastOut = rerr, nil
			g.logReviewerFailure(skill, attempt, attempts, rerr, nil)
			g.telemetryEvent(ctx, storyID, actor, "agent-retry", map[string]any{
				"skill": skill, "step": step, "attempt": attempt, "attempts": attempts, "outcome": classifyOutcome(rerr),
			})
			continue
		}
		dec, perr := parseDecision(out)
		if perr != nil {
			if pd, ok := parseProseDecision(out); ok {
				g.logProseFallback(skill, pd.Accept)
				pd.Gated = true
				pd.Skill = skill
				pd.Command = cmdStr
				pd.Context = skill
				g.setDecisionUsage(&pd, usage, agentReq.Model)
				return InvokeResult{Stdout: out, Usage: usage, Command: cmdStr, Decision: &pd}
			}
			lastErr, lastOut = perr, out
			g.logReviewerFailure(skill, attempt, attempts, perr, out)
			g.telemetryEvent(ctx, storyID, actor, "agent-retry", map[string]any{
				"skill": skill, "step": step, "attempt": attempt, "attempts": attempts, "outcome": "no-verdict",
			})
			continue
		}
		dec.Gated = true
		dec.Skill = skill
		dec.Command = cmdStr
		dec.Context = skill
		g.setDecisionUsage(&dec, usage, agentReq.Model)
		return InvokeResult{Stdout: out, Usage: usage, Command: cmdStr, Decision: &dec}
	}
	where := ""
	if len(bytes.TrimSpace(lastOut)) > 0 && g.logDir != "" {
		where = " — full reviewer output logged to " + filepath.Join(g.logDir, "reviewer.log")
	}
	g.telemetryEvent(ctx, storyID, actor, "agent-failure", map[string]any{
		"skill": skill, "step": step, "attempts": attempts, "outcome": classifyOutcome(lastErr),
	})
	return InvokeResult{Command: cmdStr, Err: fmt.Errorf(
		"reviewer: %s produced no verdict after %d attempts (empty/ambiguous reviewer output or a transient agent failure — e.g. a rate-limited or killed subprocess under concurrent sessions; retry, or reduce concurrent satelle sessions): %w%s%s",
		skill, attempts, lastErr, outputTail(lastOut), where)}
}

// runOnce executes a single bounded agent run against a caller-supplied runner and
// timeout. timeout ≤0 disables the deadline (tests). Prefer Invoke for LLM steps;
// runOnce remains the primitive used by Invoke and by Summarise (until folded).
func (g *Engine) runOnce(ctx context.Context, runner agentcli.Runner, req agentcli.Request, timeout time.Duration) ([]byte, agentcli.UsageResult, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	start := time.Now()
	raw, err := runner.Run(ctx, req)
	elapsed := time.Since(start)
	if err != nil {
		return raw, agentcli.UsageResult{Duration: elapsed}, err
	}
	text, usage := agentcli.UnwrapUsage(raw)
	usage.Duration = elapsed
	return text, usage, nil
}

// resolvePrinciples returns principle bodies for the given selector (design §5.1).
// session = principles:session-tagged (+ operating principle guarantee);
// system = embedded docs; project = non-embedded; all = every body; none = empty;
// comma-list = union (name-sorted, deduped).
func (g *Engine) resolvePrinciples(ctx context.Context, selector string) string {
	selector = strings.TrimSpace(selector)
	if selector == "" || selector == config.PrinciplesNone {
		return ""
	}
	// Back-compat: session path is alwaysPrinciples (operating-principle guarantee).
	if selector == config.PrinciplesSession {
		return g.alwaysPrinciples(ctx)
	}

	want := map[string]bool{}
	for _, p := range strings.Split(selector, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			want[p] = true
		}
	}
	if want[config.PrinciplesSession] && len(want) == 1 {
		return g.alwaysPrinciples(ctx)
	}

	// Collect by classification.
	type pick struct {
		name string
		body string
	}
	var picked []pick
	seen := map[string]bool{}
	add := func(d docindex.Doc) {
		if seen[d.Name] {
			return
		}
		seen[d.Name] = true
		if body := strings.TrimSpace(stripFrontmatter(d.Body)); body != "" {
			picked = append(picked, pick{d.Name, body})
		}
	}
	match := func(d docindex.Doc) bool {
		if want[config.PrinciplesAll] {
			return true
		}
		if want[config.PrinciplesSystem] && d.Embedded {
			return true
		}
		if want[config.PrinciplesProject] && !d.Embedded {
			return true
		}
		if want[config.PrinciplesSession] && hasSessionTag(d.Body) {
			return true
		}
		return false
	}
	if docs, err := g.docs.List(ctx, "principles"); err == nil {
		sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
		for _, d := range docs {
			if match(d) {
				add(d)
			}
		}
	}
	// Operating principle guarantee when session/all is in the selector union.
	if want[config.PrinciplesSession] || want[config.PrinciplesAll] {
		if !seen[config.OperatingPrinciple] {
			if d, err := g.docs.Get(ctx, "principles", config.OperatingPrinciple); err == nil {
				if want[config.PrinciplesAll] || want[config.PrinciplesSession] ||
					(want[config.PrinciplesSystem] && d.Embedded) ||
					(want[config.PrinciplesProject] && !d.Embedded) {
					add(d)
				}
			}
		}
	}
	if len(picked) == 0 {
		return ""
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].name < picked[j].name })
	var bodies []string
	for _, p := range picked {
		bodies = append(bodies, p.body)
	}
	return strings.Join(bodies, "\n\n")
}

// isolatedAgentBriefing is the shared body of every isolated-agent charter.
const isolatedAgentBriefing = "The work item arrives on stdin as JSON. The " +
	"substrate you reason about — skills, principles, workflows — lives as markdown " +
	"under `.satelle/`; read it directly to resolve anything this rubric references " +
	"but does not inline (including embedded defaults that are not files on disk)."

// pullContextCallToAction rides in EVERY isolated-agent prompt (via buildRequest).
// Attachment READ CHANNEL (sty_58fa970e / sty_4660bbe1): the transition payload's
// `docs` array carries every attachment (name, type, body) so Bash-less reviewers
// can judge without any disk path. Prefer that. Shell-granted agents may also
// pull more via the satelle CLI. NEVER name the obsolete in-repo .satelle/stories/
// path — writing/reading there recreates pre-relocation residue.
const pullContextCallToAction = "## Reconstruct your context (you start fresh)\n\n" +
	"You are dispatched with NO conversation history — the stdin payload carries the " +
	"work item (its `id`, title, body, acceptance criteria), the transition, and a " +
	"`docs` array of every attached document (`name`, `type`, `body`). Prefer the " +
	"payload `docs` for the plan and step summaries — that is how a Bash-less " +
	"reviewer reads what it judges. When `truncated: true` on a doc, the body is " +
	"omitted; pull the full text with the CLI when your grant includes shell:\n\n" +
	"- `satelle story get <id>` — the full current record.\n" +
	"- `satelle story docs <id>`, then `satelle story doc <id> <name>` — attachments " +
	"beyond (or fuller than) the payload.\n" +
	"- `satelle ledger list --story <id>` — the evidence ledger (transitions, review " +
	"verdicts, summaries).\n\n" +
	"Do **not** look under in-repo `.satelle/stories/` — that path is obsolete " +
	"post-relocation and must not be recreated. Do not assume absence: check the " +
	"payload `docs` (and CLI when available) before concluding a document is missing."

// reviewerCharter is the charter for an isolated gate reviewer.
func reviewerCharter() string {
	return "## You are an isolated satelle reviewer\n\n" +
		"You judge only — you CANNOT modify the repository, and your tool grant is " +
		"read-only (Read, Grep, Glob). " + isolatedAgentBriefing +
		" Judge the OUTCOME the story claims against this rubric and return your verdict."
}

// executorCharter is the charter for an isolated named executor performing a step.
func executorCharter(agent, step, workflow string) string {
	return fmt.Sprintf("## You are the isolated satelle executor agent %q "+
		"performing step %q of the workflow %q\n\n"+
		"Perform the step's work in this repository. Do NOT change the item's status "+
		"— the workflow's gates govern every advance. ", agent, step, workflow) +
		isolatedAgentBriefing
}
