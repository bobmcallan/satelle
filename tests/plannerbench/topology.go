//go:build plannerbench

package plannerbench

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Topology is a first-class variable, not a property of a provider. The earlier
// study risked attributing an in-loop session's richer progressive interaction
// to the PROVIDER that happened to run in-loop. Isolated and in-loop are
// therefore compared only as a topology comparison, and they are collected
// differently: an isolated sample is instrumented by this harness, while an
// in-loop sample CANNOT be spawned by a test — the in-loop executor IS the
// driving session (config.IsInLoopCommand). Faking one by dispatching a child
// would measure a child, so in-loop samples are ingested as operator
// attestations and labelled as such forever.

// interventionClass values for one operator turn during an in-loop step.
var interventionClasses = map[string]bool{
	"clarification": true, "correction": true, "retry": true, "none": true,
}

// topologyAccounting is the accounting AC4 requires on EVERY sample, isolated or
// in-loop. Without it a topology comparison would compare wall-clock across two
// situations that differ in whether a human was steering.
type topologyAccounting struct {
	// Interventions counts operator turns during the step, with each turn
	// classified. An isolated dispatch has zero by construction.
	Interventions     int      `json:"interventions"`
	InterventionKinds []string `json:"intervention_kinds"`
	// ConversationState is "carried" or "fresh". A dispatched child reads only
	// its stdin payload, so it is always fresh.
	ConversationState   string `json:"conversation_state"`
	PriorTurns          int    `json:"prior_turns"`
	CarriedContextBytes int    `json:"carried_context_bytes"`
	// UserVisibleProgress counts the progress events an operator could actually
	// see while the step ran, and whether any intermediate output was visible.
	UserVisibleProgress int    `json:"user_visible_progress_events"`
	IntermediateVisible bool   `json:"intermediate_output_visible"`
	ProgressProvenance  string `json:"progress_provenance"`
}

const (
	conversationFresh   = "fresh"
	conversationCarried = "carried"
)

// isolatedAccounting is the deterministic accounting for a dispatched child: no
// operator turns, no carried conversation, and a progress count read from the
// dispatch event stream.
func isolatedAccounting(progressEvents int, provenance string) topologyAccounting {
	return topologyAccounting{
		Interventions: 0, InterventionKinds: []string{"none"},
		ConversationState: conversationFresh, PriorTurns: 0, CarriedContextBytes: 0,
		UserVisibleProgress: progressEvents, IntermediateVisible: progressEvents > 0,
		ProgressProvenance: provenance,
	}
}

func (a topologyAccounting) validate() []string {
	var findings []string
	switch a.ConversationState {
	case conversationFresh, conversationCarried:
	default:
		findings = append(findings, fmt.Sprintf(
			"conversation_state %q: want %q or %q", a.ConversationState, conversationFresh, conversationCarried))
	}
	if len(a.InterventionKinds) == 0 {
		findings = append(findings, "intervention_kinds not recorded (use [\"none\"] for an unattended run)")
	}
	for _, kind := range a.InterventionKinds {
		if !interventionClasses[kind] {
			findings = append(findings, fmt.Sprintf("intervention kind %q is not a declared class", kind))
		}
	}
	if a.Interventions < 0 {
		findings = append(findings, "interventions must be >= 0")
	}
	if a.Interventions > 0 && onlyNone(a.InterventionKinds) {
		findings = append(findings, "interventions counted but every kind is \"none\"")
	}
	if strings.TrimSpace(a.ProgressProvenance) == "" {
		findings = append(findings, "progress_provenance not recorded")
	}
	if a.ConversationState == conversationCarried && a.PriorTurns <= 0 {
		findings = append(findings, "conversation_state is carried but prior_turns is 0")
	}
	return findings
}

func onlyNone(kinds []string) bool {
	for _, k := range kinds {
		if k != "none" {
			return false
		}
	}
	return true
}

// ingestInLoopSamples reads operator-attested in-loop records. Every record must
// carry the same dimension set and the same accounting as an instrumented
// sample; a record that omits either is REFUSED rather than admitted with
// blanks, because the whole point of the topology comparison is that the two
// sides are accounted the same way.
func ingestInLoopSamples(path string, s study) ([]runRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read in-loop attestation %s: %w", path, err)
	}
	var records []runRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("decode in-loop attestation %s: %w", path, err)
	}
	known := map[string]bool{}
	for _, id := range s.bindingIDs() {
		known[id] = true
	}
	for i := range records {
		records[i].SchemaVersion = evidenceSchemaVersion
		records[i].Dims.Topology = topologyInLoop
		records[i].Dims.Collection = collectionAttested
		records[i].Dims.StudyID = s.ID
		records[i].Dims.HarnessVersion = s.harnessVersion()
		if !known[records[i].Dims.BindingID] {
			return nil, fmt.Errorf("in-loop record %d names binding %q which the study does not declare",
				i, records[i].Dims.BindingID)
		}
		if findings := records[i].Dims.validate(); len(findings) > 0 {
			return nil, fmt.Errorf("in-loop record %d (%s): %s",
				i, records[i].Dims.BindingID, strings.Join(findings, "; "))
		}
		if findings := records[i].Accounting.validate(); len(findings) > 0 {
			return nil, fmt.Errorf("in-loop record %d (%s) accounting: %s",
				i, records[i].Dims.BindingID, strings.Join(findings, "; "))
		}
		if records[i].RunID == "" {
			records[i].RunID = fmt.Sprintf("%s__%s__attested-%03d",
				safeName(records[i].Dims.BindingID), safeName(records[i].Dims.Fixture), i+1)
		}
	}
	return records, nil
}

// inLoopAttestationPath names the operator-attested in-loop file, or "" when the
// operator supplied none. A missing file is not an error: the topology
// comparison reports itself underpowered and the study still passes.
func inLoopAttestationPath() string {
	return strings.TrimSpace(os.Getenv("SATELLE_PLANNER_BENCH_INLOOP"))
}
