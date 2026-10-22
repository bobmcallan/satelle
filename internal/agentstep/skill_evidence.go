package agentstep

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// The skill-evidence check (sty_23ceeca2) compares what a step produced — its
// output and its run trace — with the evidence each delivered skill declares in
// its frontmatter (agentartifact.Evidence). It is built in and always on; how
// strictly a gap blocks is the workflow's skill_evidence key. Nothing here names
// a provider's tool: commands are matched on their text, tools on whatever name
// the harness's own event carries.

// ToolCall is one tool call of a step's run, as its trace records it.
type ToolCall struct {
	Tool    string `json:"tool"`
	Command string `json:"command,omitempty"`
}

// ToolTrace is the in-loop session's tool calls since it entered a step, as the
// gate hooks saw them, and the harness that reported them. Harness is empty when
// no hook row was seen, which the check reads as "no trace channel".
type ToolTrace struct {
	Harness string
	Calls   []ToolCall
	// Docs names the documents attached since the step was entered, and
	// DocsScoped says that list is authoritative: a document from an earlier step
	// (an old plan) must not satisfy a later step's declared section. Unscoped
	// (no resolver wired) reads every attached document.
	Docs       []string
	DocsScoped bool
	// Stream is set when Calls came from a headless run's own event stream (a
	// tool_trace row RecordStepPerformance wrote) rather than from gate hooks; the
	// StreamCommands/StreamTools/StreamWhy fields then say what that stream could
	// show, as the run's own check judged it.
	Stream         bool
	StreamCommands bool
	StreamTools    bool
	StreamWhy      string
}

// ToolTraceFunc returns the in-loop trace for the story's CURRENT performing
// step: the calls recorded since the story entered status.
type ToolTraceFunc func(ctx context.Context, itemID, status string) ToolTrace

// SetToolTraceResolver wires the in-loop trace source. With none wired every
// command and tool item of an in-loop step is unverifiable.
func (g *Engine) SetToolTraceResolver(fn ToolTraceFunc) { g.toolTrace = fn }

// runTrace collects the tool calls of one dispatched run from its event stream.
type runTrace struct {
	mu    sync.Mutex
	calls []ToolCall
}

// handler wraps next so every tool event also lands in the trace. A tool call may
// report its command on a later event (ACP's tool_call_update), so any event that
// carries one is kept; matching treats the trace as a set.
func (t *runTrace) handler(next func(agentcli.Event)) func(agentcli.Event) {
	return func(ev agentcli.Event) {
		if ev.Kind == agentcli.EventToolStart || (ev.Kind == agentcli.EventToolEnd && ev.Command != "") {
			t.mu.Lock()
			t.calls = append(t.calls, ToolCall{Tool: ev.Tool, Command: ev.Command})
			t.mu.Unlock()
		}
		if next != nil {
			next(ev)
		}
	}
}

func (t *runTrace) snapshot() []ToolCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]ToolCall(nil), t.calls...)
}

// record is the trace in the shape RecordStepPerformance writes to the ledger,
// with the coverage the binding's stream had.
func (t *runTrace) record(binding config.AgentBinding) *verb.RunTrace {
	calls := t.snapshot()
	cov := dispatchedCoverage(binding, calls)
	out := &verb.RunTrace{
		Adapter:         agentcli.AdapterName(binding.CommandTemplate()),
		CommandsCovered: cov.commands, ToolsCovered: cov.tool != nil, Why: cov.why,
	}
	for _, c := range calls {
		out.Calls = append(out.Calls, verb.TraceCall{Tool: c.Tool, Command: c.Command})
	}
	return out
}

// traceCoverage says which kinds of evidence the run's trace can show at all.
type traceCoverage struct {
	commands bool
	tool     func(name string) bool
	why      string // why an uncovered item is unverifiable, naming the adapter
}

// dispatchedCoverage is what a dispatched run's event stream can show. A
// transport that emits no tool events shows nothing; one that emits them but
// whose events carry no command input (the command transport, or an ACP peer that
// omits rawInput) shows tools but not commands.
func dispatchedCoverage(binding config.AgentBinding, calls []ToolCall) traceCoverage {
	adapter := agentcli.AdapterName(binding.CommandTemplate())
	iface := binding.ResolvedInterface()
	if !agentcli.ReportsToolEvents(iface, binding.CommandTemplate()) {
		return traceCoverage{why: "adapter " + adapter + " reports no tool events"}
	}
	cov := traceCoverage{commands: true, tool: func(string) bool { return true }}
	if iface != agentcli.InterfaceStream {
		seen := false
		for _, c := range calls {
			if c.Command != "" {
				seen = true
				break
			}
		}
		if !seen {
			cov.commands = false
			cov.why = "adapter " + adapter + " reported tool calls without their command input"
		}
	}
	return cov
}

// inLoopCoverage is what the gate hooks of trace.Harness could see. The matchers
// are agentcli's per-harness table (HarnessHooks), not a copy: a shell reaches the
// commit hook on every known harness, an edit tool the gate hook, and a harness
// whose hook file cannot filter by tool (cursor) shows every call. Any other
// tool — a read, a search — reaches no hook on claude, grok or pi.
func inLoopCoverage(tr ToolTrace) traceCoverage {
	if tr.Stream {
		return traceCoverage{
			commands: tr.StreamCommands, why: tr.StreamWhy,
			tool: func(string) bool { return tr.StreamTools },
		}
	}
	if tr.Harness == "" || tr.Harness == agentcli.HarnessUnknown {
		return traceCoverage{why: "adapter " + agentcli.HarnessUnknown + ": no trace channel"}
	}
	spec := agentcli.HarnessHooks(tr.Harness)
	cov := traceCoverage{
		commands: spec.NoToolMatcher || spec.CommitMatcher != "",
		why:      "adapter " + tr.Harness + ": no trace channel for this tool",
	}
	cov.tool = func(name string) bool {
		if spec.NoToolMatcher {
			return true
		}
		for _, m := range []string{spec.GateMatcher, spec.CommitMatcher} {
			for _, t := range strings.Split(m, "|") {
				if t != "" && t == name {
					return true
				}
			}
		}
		return false
	}
	if !cov.commands {
		cov.why = "adapter " + tr.Harness + ": no trace channel"
	}
	return cov
}

// evidenceSource is what a step left behind for the check to read.
type evidenceSource struct {
	// Output is the text declared sections are looked for in: the performer's
	// output and attached artifact, or the documents an in-loop step attached.
	Output string
	// Final is the performer's final response, where declared keys are looked for.
	// HasFinal false (an in-loop step has none) makes a key unverifiable.
	Final    string
	HasFinal bool
	Calls    []ToolCall
	Cov      traceCoverage
}

// checkSkillEvidence compares each delivered skill's declared evidence with src
// and returns one item per declaration: met, missing, or unverifiable when the
// step's trace cannot show it.
func checkSkillEvidence(decls []skillEvidenceDecl, src evidenceSource) []verb.EvidenceItem {
	var items []verb.EvidenceItem
	add := func(skill, kind, name, status, detail string) {
		items = append(items, verb.EvidenceItem{Skill: skill, Kind: kind, Name: name, Status: status, Detail: detail})
	}
	var finalKeys map[string]bool
	if src.HasFinal {
		finalKeys = topLevelKeys(src.Final)
	}
	for _, d := range decls {
		for _, s := range d.Evidence.Sections {
			add(d.Skill, verb.EvidenceSection, s, presence(hasSection(src.Output, s)), "")
		}
		for _, k := range d.Evidence.Keys {
			switch {
			case !src.HasFinal:
				add(d.Skill, verb.EvidenceKey, k, verb.EvidenceUnverifiable, "an in-loop step has no structured final response")
			default:
				add(d.Skill, verb.EvidenceKey, k, presence(finalKeys[k]), "")
			}
		}
		for _, c := range d.Evidence.Commands {
			if !src.Cov.commands {
				add(d.Skill, verb.EvidenceCommand, c, verb.EvidenceUnverifiable, src.Cov.why)
				continue
			}
			add(d.Skill, verb.EvidenceCommand, c, presence(commandRan(src.Calls, c)), "")
		}
		for _, t := range d.Evidence.Tools {
			if src.Cov.tool == nil || !src.Cov.tool(t) {
				add(d.Skill, verb.EvidenceTool, t, verb.EvidenceUnverifiable, src.Cov.why)
				continue
			}
			add(d.Skill, verb.EvidenceTool, t, presence(toolRan(src.Calls, t)), "")
		}
	}
	return items
}

func presence(found bool) string {
	if found {
		return verb.EvidenceMet
	}
	return verb.EvidenceMissing
}

// hasSection reports whether text has a markdown heading whose text is name,
// compared case-insensitively and ignoring the heading level.
func hasSection(text, name string) bool {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		if strings.ToLower(strings.TrimSpace(strings.TrimLeft(line, "#"))) == want {
			return true
		}
	}
	return false
}

// topLevelKeys returns the keys of the last top-level JSON object in text — the
// performer's final answer is the last object it printed.
func topLevelKeys(text string) map[string]bool {
	var last map[string]any
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			continue
		}
		last = m
		i += int(dec.InputOffset()) - 1
	}
	keys := map[string]bool{}
	for k := range last {
		keys[k] = true
	}
	return keys
}

// commandRan reports whether some shell command in calls runs prefix as one of
// its commands. A command line is split on the shell's separators so `cd x && go
// test ./...` runs `go test`, and a leading VAR=value assignment is ignored.
func commandRan(calls []ToolCall, prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	for _, c := range calls {
		for _, seg := range commandSegments(c.Command) {
			if seg == prefix || strings.HasPrefix(seg, prefix+" ") {
				return true
			}
		}
	}
	return false
}

func commandSegments(line string) []string {
	if line == "" {
		return nil
	}
	r := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "(", "\n", ")", "\n")
	var out []string
	for _, seg := range strings.Split(r.Replace(line), "\n") {
		fields := strings.Fields(seg)
		for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "-") {
			fields = fields[1:]
		}
		if len(fields) > 0 {
			out = append(out, strings.Join(fields, " "))
		}
	}
	return out
}

func toolRan(calls []ToolCall, tool string) bool {
	for _, c := range calls {
		if strings.EqualFold(c.Tool, tool) {
			return true
		}
	}
	return false
}

// dispatchedEvidence is the check for a dispatched performer's run: the manifest
// it was handed, its output and attached artifact, and the tool events its
// transport reported. Nil when nothing was delivered undelivered and nothing
// declares evidence, so a step with no declarations leaves no extra row.
func (g *Engine) dispatchedEvidence(ctx context.Context, c composedSkills, binding config.AgentBinding, stdout string, artifactBody string, trace *runTrace, mode string) *verb.EvidenceReport {
	calls := trace.snapshot()
	src := evidenceSource{
		Output: stdout + "\n" + artifactBody, Final: stdout, HasFinal: true,
		Calls: calls, Cov: dispatchedCoverage(binding, calls),
	}
	return g.evidenceReport(c, src, mode)
}

// evidenceReport assembles the report, or nil when there is nothing to report.
func (g *Engine) evidenceReport(c composedSkills, src evidenceSource, mode string) *verb.EvidenceReport {
	r := &verb.EvidenceReport{
		Undelivered: c.Undelivered(),
		Items:       checkSkillEvidence(c.evidence, src),
		Strict:      mode == verb.SkillEvidenceReject,
	}
	if r.Empty() {
		return nil
	}
	return r
}

// inLoopEvidence is the check run when a story leaves a step the driving session
// performed: the skills the step delivers, the documents attached to the story
// since the step was entered, and the hook-recorded trace since then.
func (g *Engine) inLoopEvidence(ctx context.Context, item workitem.Item, names []string) (*verb.EvidenceReport, error) {
	c, err := g.composeSkills(ctx, names, false)
	if err != nil {
		return nil, err
	}
	tr := g.stepTrace(ctx, item.ID, item.Status)
	src := evidenceSource{Output: g.attachedText(ctx, item.ID, tr), Calls: tr.Calls, Cov: inLoopCoverage(tr)}
	return g.evidenceReport(c, src, g.skillEvidenceMode(ctx)), nil
}

// stepTrace is the trace resolver's answer for the story's current step, or the
// zero trace when none is wired.
func (g *Engine) stepTrace(ctx context.Context, itemID, status string) ToolTrace {
	if g.toolTrace == nil {
		return ToolTrace{}
	}
	return g.toolTrace(ctx, itemID, status)
}

// attachedText is the text of the documents attached to the story since the step
// was entered (every document when the trace is not scoped): a document from an
// earlier step, an old plan, never satisfies a later step's declared section.
func (g *Engine) attachedText(ctx context.Context, itemID string, tr ToolTrace) string {
	if g.itemDocs == nil {
		return ""
	}
	attached := map[string]bool{}
	for _, n := range tr.Docs {
		attached[n] = true
	}
	var out strings.Builder
	for _, d := range g.itemDocs(ctx, itemID) {
		if d.Binary || (tr.DocsScoped && !attached[d.Name]) {
			continue
		}
		out.WriteString(d.Body)
		out.WriteString("\n")
	}
	return out.String()
}
