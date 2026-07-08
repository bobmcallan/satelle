package agentstep

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

// invocation describes ONE isolated-agent call the engine assembles. The three
// call sites — the gate reviewer (runReviewer), the named executor
// (DispatchExecutor), and the step summariser (Summarise) — differ only in how
// they FILL this and how they handle the RESULT; the prompt assembly, payload
// marshalling, and grant/model wiring live here, once. Before this seam the three
// each built an agentcli.Request from copied prose; now buildRequest is the single
// source, so a change to how satelle briefs an isolated agent (e.g. the
// sty_47d31300 pull-context call-to-action) is authored in ONE place and reaches
// every dispatched agent.
type invocation struct {
	charter          string            // role charter; empty for the summariser (rubric-only prompt)
	rubric           string            // the skill body (the reviewer/executor/summariser rubric)
	injectPrinciples bool              // prepend the session-resident principles
	payload          any               // marshalled to JSON and delivered on stdin
	tools            string            // the tool grant ({tools})
	model            string            // optional model override ({model})
	settings         map[string]any    // optional, ${VAR}-resolved settings table ({settings}); nil drops the placeholder
	env              map[string]string // resolved env layered onto the subprocess (binding.Env / reviewerEnv)
	runner           agentcli.Runner   // the agent CLI to invoke (g.runner, or a binding's own runner)
	timeout          time.Duration     // per-invocation deadline; ≤0 disables the bound
}

// buildRequest composes an isolated agent's system prompt in ONE canonical order —
// the session-resident principles (when injected), the role charter, then the
// skill rubric, empty sections omitted — marshals the stdin payload, and fills the
// grant/model/dir. It is the single insertion point for anything satelle wants
// EVERY isolated agent to receive, so the reviewer, executor, and summariser can
// never drift in how they are briefed.
func (g *Engine) buildRequest(ctx context.Context, inv invocation) (agentcli.Request, error) {
	var b strings.Builder
	// Principle injection is an agents-layer option, default ON (sty_46a40208): a
	// repo may omit the resident principles for a binding via inject_principles.
	if inv.injectPrinciples {
		if resident := g.alwaysPrinciples(ctx); resident != "" {
			b.WriteString("# Always-resident principles (satelle)\n\n")
			b.WriteString(resident)
			b.WriteString("\n\n")
		}
	}
	if inv.charter != "" {
		b.WriteString(inv.charter)
		b.WriteString("\n\n")
	}
	// The pull-context call-to-action rides in EVERY isolated-agent prompt — reviewer,
	// executor, AND the charter-less summariser — so a clean-start dispatch always
	// knows how to reconstruct its context by id from the read-only CLI (sty_47d31300).
	// It lives HERE, in buildRequest, not in a charter: the summariser has no charter,
	// so a charter-only home would silently miss one of the three roles.
	b.WriteString(pullContextCallToAction)
	if inv.rubric != "" {
		b.WriteString("\n\n---\n\n")
		b.WriteString(inv.rubric)
	}
	payload, err := json.Marshal(inv.payload)
	if err != nil {
		return agentcli.Request{}, err
	}
	// A binding with no Settings emits no {settings} value at all — the placeholder
	// is dropped along with its flag (buildArgs), exactly like an empty {model} —
	// rather than marshalling to the literal string "null".
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

// runOnce executes a single bounded agent run against a caller-supplied runner and
// timeout — the generalisation of the old runAgent, so the gate reviewer (g.runner
// under agentTimeout), the summariser (same), and the named executor (its binding's
// own runner under checkTimeout) all run through one seam. timeout ≤0 disables the
// deadline (tests).
func (g *Engine) runOnce(ctx context.Context, runner agentcli.Runner, req agentcli.Request, timeout time.Duration) ([]byte, agentcli.UsageResult, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	// Time the run and unwrap any machine-readable usage envelope, so every gate/
	// dispatch records its token + wall-time cost (sty_a699ad14). A plain-text
	// harness unwraps to (stdout, zero-usage), so the cost fields are simply empty.
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

// isolatedAgentBriefing is the shared body of every isolated-agent charter: the
// facts true for a reviewer and an executor alike — the work item arrives on stdin,
// and the substrate is readable under `.satelle/`. The per-role charters
// (reviewerCharter, executorCharter) wrap it with their identity and constraint.
// This is the ONE place the sty_47d31300 pull-context instructions (fetch the
// story, its documents, and the ledger by id via read-only CLI) will be added so
// they reach every dispatched agent uniformly.
const isolatedAgentBriefing = "The work item arrives on stdin as JSON. The " +
	"substrate you reason about — skills, principles, workflows — lives as markdown " +
	"under `.satelle/`; read it directly to resolve anything this rubric references " +
	"but does not inline (including embedded defaults that are not files on disk)."

// pullContextCallToAction rides in EVERY isolated-agent prompt (via buildRequest),
// so a reviewer, executor, and summariser alike know how to reconstruct the full
// context of a clean-start dispatch. The stdin payload carries the work item and its
// id; the agent PULLS the rest itself, by id, via the read-only satelle CLI — satelle
// never pushes documents or the ledger into the payload. This works because every
// prior gated transition deposits a step-summary doc + ledger row (satelle-step-summary
// is mandatory), so the accumulated artifacts ARE the reconstructed context (sty_47d31300).
const pullContextCallToAction = "## Reconstruct your context (you start fresh)\n\n" +
	"You are dispatched with NO conversation history — the stdin payload carries the " +
	"work item (its `id`, title, body, acceptance criteria) and the transition. Pull " +
	"everything else yourself, by id, with the read-only satelle CLI:\n\n" +
	"- `satelle story get <id>` — the full current record.\n" +
	"- `satelle story docs <id>`, then `satelle story doc <id> <name>` — the attached " +
	"documents: the implementation `plan` and every prior step summary (each gated " +
	"transition deposits one), which together narrate the work so far.\n" +
	"- `satelle ledger list --story <id>` — the evidence ledger (transitions, review " +
	"verdicts, summaries).\n\n" +
	"If your grant excludes Bash (a read-only reviewer), the same attachments are on " +
	"disk under `.satelle/stories/<id>/` (tasks: `.satelle/tasks/<id>/`) — read them " +
	"with Read/Glob. Do not assume absence: fetch before concluding a document or a " +
	"prior step is missing."

// reviewerCharter is the charter for an isolated gate reviewer: read-only, judges
// the OUTCOME against the rubric and returns a verdict. It never implements.
func reviewerCharter() string {
	return "## You are an isolated satelle reviewer\n\n" +
		"You judge only — you CANNOT modify the repository, and your tool grant is " +
		"read-only (Read, Grep, Glob). " + isolatedAgentBriefing +
		" Judge the OUTCOME the story claims against this rubric and return your verdict."
}

// executorCharter is the charter for an isolated named executor performing a
// workflow step: it does the step's work but never advances status — the gates do.
func executorCharter(agent, step, workflow string) string {
	return fmt.Sprintf("## You are the isolated satelle executor agent %q "+
		"performing step %q of the workflow %q\n\n"+
		"Perform the step's work in this repository. Do NOT change the item's status "+
		"— the workflow's gates govern every advance. ", agent, step, workflow) +
		isolatedAgentBriefing
}
