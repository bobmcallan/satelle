// Structured refusals (sty_39e2d9df). When the graph is AUTHORED, an operator
// who hits "that edge is not declared" can open the workflow file and read why.
// When the graph is DERIVED there is no file to open, so the refusal itself has
// to carry what the graph used to show: the rule that fired, why it fired on this
// story, and where the story may legally go instead.
//
// Refusal lives in wfgovern because it is the one package BOTH refusal sites
// already import — internal/verb (the skipped-step fence) and internal/agentstep
// (the gate engine). It adds no dependency edge and stays free of the store.
package wfgovern

import (
	"fmt"
	"strings"
)

// Refusal rules. The rule names the DECISION the binary made, so an operator (or
// an agent reading stderr) can tell a structural refusal from a wrong-target one
// without parsing prose.
const (
	// RuleUndeclaredEdge: the governing workflow declares no from→to edge, refused
	// at the gate engine before any reviewer runs.
	RuleUndeclaredEdge = "undeclared-edge"
	// RuleSkippedStep: the same fence applied at the verb layer, where a status
	// change that jumps over a required step is refused before it reaches a gate.
	RuleSkippedStep = "skipped-step"
	// RuleParkResume: leaving a park state to anything but the recorded origin.
	RuleParkResume = "park-resume-origin"
	// RuleStructureGuard: the governing workflow itself fails structure validation,
	// so nothing may transition under it.
	RuleStructureGuard = "structure-guard"
)

// Refusal is a refused status transition, structured. Error() renders the single
// line the CLI already prints; the fields carry the same content for any caller
// that wants it typed (errors.As).
//
// Alternatives is where the story MAY go — the legal moves the refusal leaves
// open. Remedy is for a refusal that leaves none: what to fix so movement is
// possible again. A refusal with neither is a dead end and should not exist.
type Refusal struct {
	Rule         string   `json:"rule"`
	Item         string   `json:"item,omitempty"`
	Workflow     string   `json:"workflow,omitempty"`
	From         string   `json:"from"`
	To           string   `json:"to"`
	Why          string   `json:"why"`
	Alternatives []string `json:"alternatives,omitempty"`
	Remedy       string   `json:"remedy,omitempty"`
}

// Error renders the refusal as one operator-facing line:
//
//	satelle: refusing transition <from>→<to> on <item> — <rule>: <why>; expected next step(s): a, b
//
// The "refusing transition" prefix and the "expected next step(s)" label are the
// established wording; the rule token is the new, machine-greppable part.
func (r Refusal) Error() string {
	var b strings.Builder
	b.WriteString("satelle: refusing transition ")
	fmt.Fprintf(&b, "%s→%s", orUnknown(r.From), orUnknown(r.To))
	if r.Item != "" {
		b.WriteString(" on " + r.Item)
	}
	if r.Workflow != "" {
		b.WriteString(" in workflow " + r.Workflow)
	}
	b.WriteString(" — ")
	if r.Rule != "" {
		b.WriteString(r.Rule + ": ")
	}
	b.WriteString(strings.TrimSpace(r.Why))
	if len(r.Alternatives) > 0 {
		b.WriteString("; expected next step(s): " + strings.Join(r.Alternatives, ", "))
	}
	if r.Remedy != "" {
		b.WriteString("; " + strings.TrimSpace(r.Remedy))
	}
	return b.String()
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unset)"
	}
	return s
}
