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
	"strconv"
	"strings"
	"sync"
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
	Expect Expect
	// Timeout is an OPTIONAL hard ceiling on this ONE run. ≤0 (the default,
	// sty_752c4ef2) means no wall-clock cap — IdleTimeout is what actually
	// bounds a progressing dispatch. An empty Timeout with expect=ExpectVerdict
	// still falls back to the engine's g.agentTimeout (also unset by default).
	Timeout time.Duration
	// IdleTimeout bounds how long this run may go with no REAL event (tool
	// start/end, message, usage — heartbeats excluded) before Watchdog judges
	// it stalled and cancels it (sty_752c4ef2). ≤0 falls back to the engine's
	// g.idleTimeout.
	IdleTimeout time.Duration
	// Runner overrides the runner built from Binding.Command. Reviewer path
	// passes g.runner (bootstrap-resolved); named dispatch builds from the binding.
	Runner agentcli.Runner
	// Attempts bounds verdict retries (0 → engine default). Perform ignores this.
	Attempts int
	// Sink, when set, receives live subprocess stdout (named dispatch live log).
	Sink io.Writer
	// OnEvent observes provider-neutral progressive events. It is composed with
	// the engine's interactive stderr progress consumer.
	OnEvent agentcli.EventHandler

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
	// SystemPromptBytes/PayloadBytes are the byte lengths of the system prompt
	// and stdin payload satelle sent for this invocation (sty_363eaf55) —
	// lengths only, never content.
	SystemPromptBytes int
	PayloadBytes      int
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
	effort     string // optional reasoning effort (sty_657f77b9)
	settings   map[string]any
	env        map[string]string
	scratch    string // this dispatch's scratch dir; "" → no scratch briefing (sty_e7aaf8b1)
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
	// The scratch briefing rides whenever this dispatch has a scratch dir,
	// charter or not — a charter-less perform dispatch still needs to know
	// where to put evidence (sty_e7aaf8b1 AC2).
	if brief := scratchBriefing(inv.scratch); brief != "" {
		b.WriteString(brief)
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
		Effort:       inv.effort,
		Settings:     settings,
		Env:          inv.env,
		Dir:          g.repoRoot,
	}, nil
}

// Invoke is the ONLY path that calls agentcli.Runner.Run for LLM gate/dispatch
// steps (sty_ba860c8a). It resolves prompt assembly from the binding, runs the
// agent, and for ExpectVerdict applies the retry + verdict parse loop.
// On a classified rate-limit/unavailable failure, retries once on the binding's
// secondary (per-binding secondary= or [defaults] secondary) when configured
// (sty_5bf61f89).
func (g *Engine) Invoke(ctx context.Context, req InvokeRequest) InvokeResult {
	res := g.invokePrimary(ctx, req)
	if res.Err == nil {
		return res
	}
	if !IsRateLimitOrUnavailable(res.Err, res.Stdout) {
		return res
	}
	if g.resolveSecondary == nil {
		return res
	}
	section := req.Section
	if section == "" {
		section = "agent"
	}
	sec, secName, ok := g.resolveSecondary(section, req.Binding)
	if !ok {
		return res
	}
	g.telemetryEvent(ctx, req.StoryID, req.Actor, "agent-secondary-failover", map[string]any{
		"primary": section, "secondary": secName, "outcome": "rate-limit",
		"primary_err": res.Err.Error(),
	})
	g.emitProgress("primary binding %q rate-limited/unavailable — retrying once on secondary %q…", section, secName)
	secReq := req
	secReq.Binding = sec
	secReq.Section = secName
	secReq.Runner = nil // rebuild runner from secondary binding
	return g.invokePrimary(ctx, secReq)
}

// invokePrimary runs one binding without secondary failover.
func (g *Engine) invokePrimary(ctx context.Context, req InvokeRequest) InvokeResult {
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
		effort:     binding.Effort,
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
	if expect == ExpectPerform {
		// Mark isolated performers so PreToolUse can distinguish the child that
		// the workflow deliberately dispatched from the driving session trying
		// to work ahead while that transition is in flight. Copy before overlay
		// so binding-owned environment remains intact and reserved marker keys
		// cannot accidentally identify a different dispatch.
		env := make(map[string]string, len(inv.env)+3)
		for k, v := range inv.env {
			env[k] = v
		}
		env[config.DispatchAgentEnv] = section
		env[config.DispatchStepEnv] = req.Step
		env[config.DispatchItemEnv] = req.StoryID
		if id := config.SessionFromEnv(); id != "" {
			env[config.SessionEnv] = id
		}
		inv.env = env
	}

	// Every dispatch gets its own scratch directory (sty_e7aaf8b1 AC1): TMPDIR
	// and SATELLE_SCRATCH are RESERVED keys and win over any binding env of the
	// same name, so overlay them last.
	scratchDir, screrr := newScratch(g.repoRoot, req.StoryID)
	if screrr != nil {
		return InvokeResult{Err: screrr}
	}
	inv.scratch = scratchDir
	inv.env = overlayScratchEnv(inv.env, scratchDir)

	agentReq, err := g.buildRequest(ctx, inv)
	if err != nil {
		finishScratch(scratchDir, false)
		return InvokeResult{Err: err}
	}
	if req.Sink != nil {
		agentReq.Sink = req.Sink
	}
	var eventMu sync.Mutex
	var lastMessage time.Time
	// Throttled in-flight activity-detail refresh (sty_752c4ef2 AC5): only for
	// a genuine DISPATCH (ExpectPerform) with a story to attribute it to. Not
	// gate reviewers or the summariser — those are not the "in-flight dispatch"
	// AC6/AC7 render.
	var actCount int
	var actPid int
	var lastActivityPush time.Time
	trackActivity := expect == ExpectPerform && g.activityDetail != nil && req.StoryID != ""
	// Interactive ask-the-user questions the transport denied or auto-answered,
	// ledgered after the run (sty_32795645) — never from inside the callback.
	var asked []agentcli.Event
	agentReq.OnEvent = func(ev agentcli.Event) {
		eventMu.Lock()
		defer eventMu.Unlock()
		if req.OnEvent != nil {
			req.OnEvent(ev)
		}
		switch ev.Kind {
		case agentcli.EventStart:
			g.emitProgress("agent %s started", section)
			if pid, err := strconv.Atoi(ev.Meta[agentcli.EventMetaPid]); err == nil {
				actPid = pid
			}
		case agentcli.EventHeartbeat:
			g.emitProgress("agent %s still running…", section)
		case agentcli.EventToolStart:
			g.emitProgress("agent %s tool started: %s", section, progressLabel(ev.Tool))
		case agentcli.EventToolEnd:
			g.emitProgress("agent %s tool finished: %s", section, progressLabel(ev.Tool))
		case agentcli.EventMessage:
			// Token-level ACP chunks can be extremely noisy. Preserve every event
			// in normalized diagnostics, but rate-limit interactive prose.
			if time.Since(lastMessage) >= time.Second && strings.TrimSpace(ev.Text) != "" {
				g.emitProgress("agent %s: %s", section, progressLabel(ev.Text))
				lastMessage = time.Now()
			}
		case agentcli.EventFailed:
			g.emitProgress("agent %s failed: %s", section, progressLabel(ev.Error))
		case agentcli.EventInteractiveDenied:
			g.emitProgress("agent %s asked a question (%s): %s", section, ev.Meta[agentcli.EventMetaResponse], progressLabel(ev.Text))
			asked = append(asked, ev)
		}
		if trackActivity && isRealEvent(ev.Kind) {
			actCount++
			now := time.Now()
			if actCount == 1 || now.Sub(lastActivityPush) >= activityDetailThrottle {
				lastActivityPush = now
				g.activityDetail(req.StoryID, ActivityDetail{
					Agent: section, Model: agentReq.Model, Pid: actPid, EventLabel: eventLabel(ev),
					EventAt: now, EventCount: actCount,
				})
			}
		}
	}
	// Verdict parsing extracts decision JSON from anywhere in the blob
	// (parseDecision). Keep the full ACP stream so a decision emitted before
	// trailing chatter is not dropped by the answer-only segment rule
	// (sty_844b6ab1 AC6). Prose paths (summariser, perform) keep the zero
	// value CaptureAnswer so narration before tool fences is stripped.
	if expect == ExpectVerdict {
		agentReq.Capture = agentcli.CaptureFull
	}

	runner := req.Runner
	if runner == nil {
		r, rerr := g.newRunner(binding.ResolvedInterface(), binding.CommandTemplate())
		if rerr != nil {
			finishScratch(scratchDir, false)
			return InvokeResult{Err: fmt.Errorf("broken command for binding %q: %w", section, rerr)}
		}
		if r == nil {
			finishScratch(scratchDir, false)
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
	idle := req.IdleTimeout
	if idle <= 0 {
		idle = g.idleTimeout
	}
	busy, berr := g.busyTimeoutFor(section, binding)
	if berr != nil {
		finishScratch(scratchDir, false)
		return InvokeResult{Err: fmt.Errorf("binding %q: invalid busy_timeout: %w", section, berr)}
	}

	// A leftover sweep only ever runs for a PERFORM dispatch — reviewers are
	// read-only — and only when this repo configured a rule, so a repo with no
	// [dispatch.leftovers] pays no extra git cost (sty_e7aaf8b1 AC5/AC6).
	var leftoverBefore map[string]bool
	sweepConfigured := len(g.leftoverRule.Patterns) > 0 || strings.TrimSpace(g.leftoverRule.ContentRegex) != ""
	if expect == ExpectPerform && sweepConfigured {
		leftoverBefore, _ = untrackedSnapshot(g.repoRoot)
	}

	var res InvokeResult
	switch expect {
	case ExpectPerform:
		out, usage, runErr := g.runOnceBusy(ctx, runner, agentReq, timeout, idle, busy)
		if se := asStallError(runErr); se != nil {
			res = g.stallResult(ctx, req, cmdStr, se)
			res.Usage = usage
		} else {
			res = InvokeResult{Stdout: out, Usage: usage, Command: cmdStr, Err: runErr}
		}
	default: // ExpectVerdict
		res = g.invokeVerdict(ctx, req, runner, agentReq, cmdStr, timeout, idle, busy)
	}
	// Byte lengths of what satelle actually sent (sty_363eaf55) — lengths only,
	// never content. Stamped uniformly regardless of outcome (success, failure,
	// or stall) since the request was already dispatched by this point.
	res.SystemPromptBytes = len(agentReq.SystemPrompt)
	res.PayloadBytes = len(agentReq.Payload)
	eventMu.Lock()
	askedNow := append([]agentcli.Event(nil), asked...)
	eventMu.Unlock()
	g.ledgerInteractiveDenied(ctx, req, section, askedNow)

	var swept []string
	var sweepFailed bool
	if expect == ExpectPerform && leftoverBefore != nil {
		files, serr := SweepLeftovers(g.repoRoot, scratchDir, leftoverBefore, g.leftoverRule)
		if serr != nil {
			// SweepLeftovers may have already moved SOME matched files out of the
			// tree before hitting this error — files still reports every match it
			// attempted. Never delete scratch on this path: that would silently
			// destroy whatever it already moved, with nothing left to recover it
			// from (sty_e7aaf8b1 AC5).
			sweepFailed = true
			g.recordInvocation(ctx, req.StoryID, map[string]any{
				"phase": "leftovers_failed", "scratch_dir": scratchDir, "agent": section,
				"files": files, "error": serr.Error(),
			})
		} else if len(files) > 0 {
			swept = files
			g.ledgerLeftovers(ctx, req.StoryID, scratchDir, g.leftoverRule.ResolveAction(), files)
		}
	}
	keepScratch := res.Err != nil || sweepFailed || (len(swept) > 0 && g.leftoverRule.ResolveAction() == config.LeftoverActionMove)
	if res.Err != nil {
		g.recordInvocation(ctx, req.StoryID, map[string]any{
			"phase": "scratch_kept", "scratch_dir": scratchDir, "agent": section,
		})
	}
	finishScratch(scratchDir, keepScratch)
	return res
}

// ledgerInteractiveDenied records one "agent-interactive-denied" entry per
// ask-the-user question the transport denied or auto-answered, with the tool
// name and question text, so an Ask that would once have stalled the dispatch is
// visible (sty_32795645).
func (g *Engine) ledgerInteractiveDenied(ctx context.Context, req InvokeRequest, section string, asked []agentcli.Event) {
	for _, ev := range asked {
		g.ledgerInteractiveDeniedFor(ctx, req.StoryID, req.Actor, section, req.Step, ev)
	}
}

// ledgerInteractiveDeniedFor writes the row for one denied ask; shared by the
// one-shot and live-session paths. An empty actor defaults to "executor".
func (g *Engine) ledgerInteractiveDeniedFor(ctx context.Context, storyID, actor, section, step string, ev agentcli.Event) {
	if actor == "" {
		actor = "executor"
	}
	g.telemetryEvent(ctx, storyID, actor, "agent-interactive-denied", map[string]any{
		"agent": section, "step": step, "tool": ev.Tool, "question": ev.Text,
		"response": ev.Meta[agentcli.EventMetaResponse],
	})
}

// asStallError extracts a *StallError from err via errors.As, or nil.
func asStallError(err error) *StallError {
	var se *StallError
	if errors.As(err, &se) {
		return se
	}
	return nil
}

// stallResult builds the ledgered "agent-stalled" telemetry and the refusal
// InvokeResult for a stalled dispatch (AC2), shared by the verdict retry loop
// and the perform path so a Watchdog firing is recorded and phrased
// identically wherever it happens.
func (g *Engine) stallResult(ctx context.Context, req InvokeRequest, cmdStr string, se *StallError) InvokeResult {
	name := req.Skill
	if name == "" {
		name = req.Section
	}
	actor := req.Actor
	if actor == "" {
		actor = "executor"
	}
	g.telemetryEvent(ctx, req.StoryID, actor, "agent-stalled", map[string]any{
		"skill": name, "step": req.Step, "idle": se.Idle.String(),
		"last_event": se.LastEvent, "last_event_at": se.LastEventAt,
	})
	return InvokeResult{Command: cmdStr, Err: fmt.Errorf(
		"agent %q %w — the transition was NOT enacted", name, se)}
}

func progressLabel(s string) string {
	s = agentcli.SafeText(s)
	if s == "" {
		return "activity"
	}
	return s
}

// IsRateLimitOrUnavailable reports whether an isolated-agent failure should
// trigger secondary failover (sty_5bf61f89): rate limits, capacity, quota, and
// hard unavailability. Other errors stay fail-loud without fallback.
func IsRateLimitOrUnavailable(err error, stdout []byte) bool {
	var b strings.Builder
	if err != nil {
		b.WriteString(err.Error())
		b.WriteByte(' ')
	}
	b.Write(stdout)
	s := strings.ToLower(b.String())
	for _, needle := range []string{
		"rate limit", "rate-limit", "ratelimit", "too many requests", " 429", "status 429",
		"overloaded", "capacity", "resource_exhausted", "quota exceeded", "quota_exceeded",
		"temporarily unavailable", "service unavailable", " 503", "status 503",
		"usage limit", "tokens per min", "request limit",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// invokeVerdict runs the ExpectVerdict retry loop: parse JSON/prose decision,
// retry transient no-verdict, fail loud on timeout after attempts.
func (g *Engine) invokeVerdict(ctx context.Context, req InvokeRequest, runner agentcli.Runner, agentReq agentcli.Request, cmdStr string, timeout, idle, busy time.Duration) InvokeResult {
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
		out, usage, rerr := g.runOnceBusy(ctx, runner, agentReq, timeout, idle, busy)
		if rerr != nil {
			if se := asStallError(rerr); se != nil {
				return g.stallResult(ctx, req, cmdStr, se)
			}
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

// runOnce executes a single bounded agent run against a caller-supplied
// runner, an OPTIONAL hard deadline, and an OPTIONAL idle-stall bound
// (sty_752c4ef2). hard ≤0 disables the wall-clock deadline; idle ≤0 disables
// the Watchdog. Prefer Invoke for LLM steps; runOnce remains the primitive
// used by Invoke and by Summarise (until folded).
//
// The Watchdog sits ABOVE the runner: it wraps ctx (context.WithCancelCause)
// and req.OnEvent, so every transport — which spawns its child via
// exec.CommandContext(ctx, …) against this SAME context — is bounded with no
// transport-specific code. A run that ends because the watchdog fired surfaces
// as a *StallError (retrievable via errors.As or StallCause(ctx)), which the
// caller maps to the "stalled" outcome and refusal text instead of a bare
// context.Canceled.
func (g *Engine) runOnce(ctx context.Context, runner agentcli.Runner, req agentcli.Request, hard, idle time.Duration) ([]byte, agentcli.UsageResult, error) {
	return g.runOnceBusy(ctx, runner, req, hard, idle, 0)
}

// runOnceBusy is runOnce plus a busy cap (sty_db62a3b9): busy>0 (with idle>0)
// turns on the command transport's CPU-liveness probe, so a silent one-shot CLI
// whose process tree keeps burning CPU is not judged stalled until busy has
// elapsed since its last real event. Stream and ACP transports never emit the
// probe's EventProgress, so they are unaffected.
func (g *Engine) runOnceBusy(ctx context.Context, runner agentcli.Runner, req agentcli.Request, hard, idle, busy time.Duration) ([]byte, agentcli.UsageResult, error) {
	out, usage, err := g.runOnceBusyRaw(ctx, runner, req, hard, idle, busy)
	if !usage.Available && usage.UnavailableReason == "" {
		// An adapter that knows why names it; every other unreported run still
		// names the adapter (the runner's CLI) rather than a bare false
		// (sty_c8d45201).
		usage.UnavailableReason = runner.Name() + " adapter: transport reported no usage"
	}
	return out, usage, err
}

func (g *Engine) runOnceBusyRaw(ctx context.Context, runner agentcli.Runner, req agentcli.Request, hard, idle, busy time.Duration) ([]byte, agentcli.UsageResult, error) {
	if hard > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, hard)
		defer cancel()
	}
	if idle > 0 {
		wd := NewWatchdog(idle)
		if busy > 0 {
			wd.SetBusyCap(busy)
			req.LivenessInterval = watchdogTick(idle)
		}
		var stop func()
		ctx, stop = wd.Start(ctx)
		defer stop()
		parent := req.OnEvent
		req.OnEvent = func(ev agentcli.Event) {
			wd.TouchEvent(ev)
			if parent != nil {
				parent(ev)
			}
		}
	}
	mapStall := func(err error) error {
		if err == nil {
			return nil
		}
		if se := StallCause(ctx); se != nil {
			return se
		}
		return err
	}
	start := time.Now()
	// A transport that can report its own resolved-model usage (stream: modelUsage
	// arrives on an EventUsage, never inside the returned bytes) is preferred over
	// sniffing Run's output for a JSON envelope (sty_87b86044 AC2).
	if ur, ok := runner.(agentcli.UsageRunner); ok {
		text, usage, err := ur.RunUsage(ctx, req)
		usage.Duration = time.Since(start)
		return text, usage, mapStall(err)
	}
	raw, err := runner.Run(ctx, req)
	elapsed := time.Since(start)
	if err != nil {
		return raw, agentcli.UsageResult{Duration: elapsed}, mapStall(err)
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
	"reviewer reads what it judges. The payload `diff` object (when present) is " +
	"the engagement slice (`files`, `stat`, `patch`) — enumeration, not a verdict. " +
	"When `truncated: true` on a doc, the body is " +
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

// consultCharter is the charter for a named binding opened as a CONSULTANT by
// `satelle story chat --agent <binding>` — a reviewer interrogated about a
// rejection, a second opinion on a block. Consultation is not review
// ([[satelle-agent-consultation]]): the reply is context the caller may act on,
// the gate that judges the edge still runs cold and one-shot over the payload
// satelle builds, and the consultant never touches story state (sty_a0372443).
func consultCharter(binding, role, status string) string {
	return fmt.Sprintf("## You are the %q binding %q, CONSULTED on this story at status %q\n\n"+
		"You are consulting, NOT judging. Your reply is CONTEXT, not a verdict: the "+
		"reviewer that judges this edge still runs cold and one-shot over the payload "+
		"satelle builds, and its verdict alone advances the stage. Do NOT run "+
		"`satelle story set` and do NOT change the item's status. Stay centered on the "+
		"story's acceptance criteria — the conversation is context, never a criterion. ",
		role, binding, status) + isolatedAgentBriefing
}
