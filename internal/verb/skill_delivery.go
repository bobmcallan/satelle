package verb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// The driving session is shown the skills delivered for an in-loop step, and a
// skill-evidence gap, on stderr: every harness reads the output of `story set`,
// which is why the delivery goes through it and not through a per-harness hook
// ([[satelle-agent-agnostic]]).

// recordInLoopDelivery records, on the ledger, each skill delivered to the
// driving session for an in-loop step (sty_23ceeca2), shows it the composed
// rubric once, and refuses the entry when a declared skill did not resolve and
// the workflow's skill_evidence is reject. A step that declares no skills
// delivers nothing and records nothing.
func recordInLoopDelivery(ctx context.Context, current workitem.Item, to, attempt string, res DispatchResult, run *performerRun, now time.Time) {
	if len(res.Skills) == 0 {
		return
	}
	var delivered []string
	for _, s := range res.Skills {
		if s.Delivered {
			delivered = append(delivered, fmt.Sprintf("%s (%.12s)", s.Name, s.Hash))
		}
	}
	body := fmt.Sprintf("delivered step %q skills to the driving session: %s", to, strings.Join(delivered, ", "))
	missing := UndeliveredSkills(res.Skills)
	if len(missing) > 0 {
		body += fmt.Sprintf("; NOT delivered: %s", strings.Join(missing, ", "))
	}
	payload, err := json.Marshal(struct {
		From    string          `json:"from"`
		To      string          `json:"to"`
		Attempt string          `json:"attempt,omitempty"`
		Kind    PerformerKind   `json:"kind"`
		Skills  []SkillDelivery `json:"skills"`
		// TraceCommands and TraceTools say the step's skills declare evidence only a
		// run trace can show, which is what tells a gate hook to record its calls.
		TraceCommands bool `json:"trace_commands,omitempty"`
		TraceTools    bool `json:"trace_tools,omitempty"`
	}{From: current.Status, To: to, Attempt: attempt, Kind: res.Kind, Skills: res.Skills,
		TraceCommands: res.TraceCommands, TraceTools: res.TraceTools})
	if err == nil {
		appendLedgerEntry(ctx, current.ID, ledger.KindSkillDelivery, "in-loop", body, payload, now)
	}
	if len(missing) > 0 {
		report := &EvidenceReport{Undelivered: missing, Strict: res.StrictSkills}
		reportSkillEvidence(current.ID, to, report)
		if res.StrictSkills {
			run.Err = fmt.Errorf("step %q refused: %s", to, report.Notes())
			return
		}
	}
	if strings.TrimSpace(res.InLoopRubric) != "" {
		fmt.Fprintf(os.Stderr, "satelle: %s — skills for step %q (delivered to this session; perform the step under them):\n\n%s\n",
			current.ID, to, strings.TrimSpace(res.InLoopRubric))
	}
}

// reportSkillEvidence puts a skill-evidence gap in front of the driver.
func reportSkillEvidence(id, step string, r *EvidenceReport) {
	if r == nil || len(r.Gaps()) == 0 {
		return
	}
	mode := SkillEvidenceRecord
	if r.Strict {
		mode = SkillEvidenceReject
	}
	fmt.Fprintf(os.Stderr, "satelle: %s — skill-evidence (%s) at step %q: %s\n", id, mode, step, r.Notes())
}

// TraceCall is one tool call of a step's run.
type TraceCall struct {
	Tool    string `json:"tool"`
	Command string `json:"command,omitempty"`
}

// RunTrace is a headless run's tool calls as its event stream reported them, with
// what that stream could show: whether commands and tools are visible at all, and
// Why not when they are not, naming the adapter. RecordStepPerformance writes it
// as one tool_trace row, so the step's exit gate reads the same calls the run's
// own check did.
type RunTrace struct {
	Adapter         string
	Calls           []TraceCall
	CommandsCovered bool
	ToolsCovered    bool
	Why             string
}

// ToolTracePayload is a tool_trace row's payload. A gate hook writes one row per
// allowed call (Harness, Tool, Command); a headless run writes its whole stream
// as one row (Source "stream", Calls, and the coverage the stream had).
type ToolTracePayload struct {
	Harness         string      `json:"harness"`
	Tool            string      `json:"tool,omitempty"`
	Command         string      `json:"command,omitempty"`
	Source          string      `json:"source,omitempty"`
	Calls           []TraceCall `json:"calls,omitempty"`
	CommandsCovered bool        `json:"commands_covered,omitempty"`
	ToolsCovered    bool        `json:"tools_covered,omitempty"`
	Why             string      `json:"why,omitempty"`
}

// ToolTraceStream marks a tool_trace row that holds a headless run's stream.
const ToolTraceStream = "stream"

// Bounds on a stream row, so a long run does not become a huge ledger row.
const (
	traceCallsMax   = 500
	traceCommandMax = 240
)

// recordRunTrace appends a headless run's stream as one tool_trace row.
func recordRunTrace(ctx context.Context, id, step string, t *RunTrace, now time.Time) {
	if t == nil {
		return
	}
	calls := t.Calls
	if len(calls) > traceCallsMax {
		calls = calls[:traceCallsMax]
	}
	trimmed := make([]TraceCall, 0, len(calls))
	for _, c := range calls {
		if len(c.Command) > traceCommandMax {
			c.Command = c.Command[:traceCommandMax]
		}
		trimmed = append(trimmed, c)
	}
	payload, err := json.Marshal(ToolTracePayload{
		Harness: t.Adapter, Source: ToolTraceStream, Calls: trimmed,
		CommandsCovered: t.CommandsCovered, ToolsCovered: t.ToolsCovered, Why: t.Why,
	})
	if err != nil {
		return
	}
	appendLedgerEntry(ctx, id, ledger.KindToolTrace, "executor",
		fmt.Sprintf("%d tool call(s) of the %s run performing step %q", len(trimmed), t.Adapter, step), payload, now)
}

// SkillEvidenceCheck is the name the skill-evidence check carries as a verdict's
// Skill (sty_23ceeca2). It is a built-in system check, not an authored skill.
const SkillEvidenceCheck = "skill-evidence"

// SkillEvidence strictness: whether a delivery or evidence gap refuses the
// transition or is only recorded. It is the workflow's `skill_evidence` key
// (the route source's [meta]); record is the default.
const (
	SkillEvidenceRecord = agentartifact.EvidenceModeRecord
	SkillEvidenceReject = agentartifact.EvidenceModeReject
)

// SkillDelivery is one step skill's delivery to the step's performer: the skill's
// name, the sha256 hex of the body the substrate index holds (the same hash
// `docindex` reports), and whether it was delivered at all. A skill that does not
// resolve is recorded Delivered=false with the reason, never skipped.
type SkillDelivery struct {
	Name      string `json:"name"`
	Hash      string `json:"hash,omitempty"`
	Delivered bool   `json:"delivered"`
	Reason    string `json:"reason,omitempty"`
}

// UndeliveredSkills names the skills a manifest records as not delivered.
func UndeliveredSkills(manifest []SkillDelivery) []string {
	var out []string
	for _, s := range manifest {
		if !s.Delivered {
			out = append(out, s.Name)
		}
	}
	return out
}

// Evidence item statuses.
const (
	EvidenceMet          = "met"
	EvidenceMissing      = "missing"
	EvidenceUnverifiable = "unverifiable"
)

// Evidence item kinds, one per evidence_* frontmatter key.
const (
	EvidenceSection = "section"
	EvidenceKey     = "key"
	EvidenceCommand = "command"
	EvidenceTool    = "tool"
)

// EvidenceItem is one declared evidence item compared with the step's output and
// run trace. Detail says why an item is unverifiable (naming the adapter).
type EvidenceItem struct {
	Skill  string `json:"skill"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// String renders the item the way the check names a gap:
// `<skill>: missing section "X"` or `<skill>: unverifiable command "go test" (…)`.
func (i EvidenceItem) String() string {
	s := fmt.Sprintf("%s: %s %s %q", i.Skill, i.Status, i.Kind, i.Name)
	if i.Detail != "" {
		s += " (" + i.Detail + ")"
	}
	return s
}

// EvidenceReport is the skill-evidence check's whole answer for one step: the
// skills it found undelivered and every declared item's status. Strict says the
// workflow's skill_evidence key is "reject": a gap then refuses the transition;
// otherwise the gap is only recorded.
type EvidenceReport struct {
	Undelivered []string       `json:"undelivered,omitempty"`
	Items       []EvidenceItem `json:"items,omitempty"`
	Strict      bool           `json:"strict,omitempty"`
}

// Empty reports whether the check had nothing to say: every skill delivered and
// none declared evidence.
func (r EvidenceReport) Empty() bool { return len(r.Undelivered) == 0 && len(r.Items) == 0 }

// Gaps lists each gap by name: an undelivered skill or a missing item. An
// unverifiable item is not a gap — the check could not observe it.
func (r EvidenceReport) Gaps() []string {
	var out []string
	for _, n := range r.Undelivered {
		out = append(out, fmt.Sprintf("skill %q was not delivered", n))
	}
	for _, i := range r.Items {
		if i.Status == EvidenceMissing {
			out = append(out, i.String())
		}
	}
	return out
}

// Unverifiable lists the items the check could not observe.
func (r EvidenceReport) Unverifiable() []string {
	var out []string
	for _, i := range r.Items {
		if i.Status == EvidenceUnverifiable {
			out = append(out, i.String())
		}
	}
	return out
}

// Blocks reports whether the report refuses the transition: a gap, under strict.
func (r EvidenceReport) Blocks() bool { return r.Strict && len(r.Gaps()) > 0 }

// Notes is the report in prose: each gap, then each unverifiable item, then the
// met count. It is the verdict's notes and the refusal's reason.
func (r EvidenceReport) Notes() string {
	var parts []string
	if gaps := r.Gaps(); len(gaps) > 0 {
		parts = append(parts, "gaps: "+strings.Join(gaps, "; "))
	}
	if un := r.Unverifiable(); len(un) > 0 {
		parts = append(parts, "not verifiable here: "+strings.Join(un, "; "))
	}
	met := 0
	for _, i := range r.Items {
		if i.Status == EvidenceMet {
			met++
		}
	}
	if met > 0 {
		parts = append(parts, fmt.Sprintf("%d declared item(s) met", met))
	}
	if len(parts) == 0 {
		return "every delivered skill's declared evidence is present"
	}
	return strings.Join(parts, ". ")
}

// Verdict renders the report as the system reviewer verdict the gate records. It
// accepts unless the report Blocks.
func (r EvidenceReport) Verdict(order int) ReviewerVerdict {
	return ReviewerVerdict{
		Skill: SkillEvidenceCheck, Order: order, Accept: !r.Blocks(), Notes: r.Notes(), System: true,
	}
}
