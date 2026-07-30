//go:build plannerbench

package plannerbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC4: isolated and in-loop are a SEPARATE topology comparison, accounted the
// same way on both sides, and never merged into a transport or provider result.

func topologyStudy() study {
	return study{
		ID: "topo", Seed: 1, Runs: 3, MinSamples: 3, P50GapPercent: 25,
		ContextBuckets: []contextBucket{{Name: "small", MaxBytes: 0}},
		Bindings:       []studyBinding{{ID: "isolated-a"}, {ID: "inloop-a"}},
		Comparisons: []studyComparison{{
			ID: "topology", Free: "topology",
			Holds:   []string{"provider", "model", "effort_class", "fixture"},
			Members: []string{"isolated-a", "inloop-a"},
		}},
	}
}

func inLoopDims(run int) sampleDims {
	dims := testDims("inloop-a", "fix", run)
	dims.Topology = topologyInLoop
	dims.Collection = collectionAttested
	dims.ToolPolicy = "driving-session-grant"
	return dims
}

func attestedAccounting() topologyAccounting {
	return topologyAccounting{
		Interventions: 2, InterventionKinds: []string{"clarification", "correction"},
		ConversationState: conversationCarried, PriorTurns: 14, CarriedContextBytes: 48000,
		UserVisibleProgress: 31, IntermediateVisible: true,
		ProgressProvenance: "operator transcript count",
	}
}

func TestIsolatedAccountingIsDeterministic(t *testing.T) {
	a := isolatedAccounting(7, "dispatch-event-log")
	if findings := a.validate(); len(findings) != 0 {
		t.Fatalf("isolated accounting must validate: %v", findings)
	}
	if a.Interventions != 0 || a.ConversationState != conversationFresh || a.PriorTurns != 0 {
		t.Fatalf("a dispatched child has no operator turns and no carried conversation: %+v", a)
	}
	if a.UserVisibleProgress != 7 || !a.IntermediateVisible {
		t.Fatalf("progress must come from the event stream: %+v", a)
	}
	silent := isolatedAccounting(0, "no dispatch event log")
	if silent.IntermediateVisible {
		t.Fatal("a transport that emitted no events showed the operator nothing")
	}
}

func TestAccountingValidationRejectsIncompleteAttestations(t *testing.T) {
	cases := map[string]func(*topologyAccounting){
		"conversation_state":  func(a *topologyAccounting) { a.ConversationState = "" },
		"intervention_kinds":  func(a *topologyAccounting) { a.InterventionKinds = nil },
		"progress_provenance": func(a *topologyAccounting) { a.ProgressProvenance = "" },
		"prior_turns":         func(a *topologyAccounting) { a.PriorTurns = 0 },
	}
	for field, breakIt := range cases {
		a := attestedAccounting()
		breakIt(&a)
		findings := a.validate()
		if len(findings) == 0 {
			t.Errorf("%s: an incomplete attestation must be refused", field)
			continue
		}
		if !strings.Contains(strings.Join(findings, "; "), field) {
			t.Errorf("%s: findings do not name it: %v", field, findings)
		}
	}
	undeclared := attestedAccounting()
	undeclared.InterventionKinds = []string{"vibes"}
	if findings := undeclared.validate(); len(findings) == 0 {
		t.Fatal("an undeclared intervention class must be refused")
	}
	inconsistent := attestedAccounting()
	inconsistent.InterventionKinds = []string{"none"}
	if findings := inconsistent.validate(); len(findings) == 0 {
		t.Fatal("counting interventions while classing them all none is inconsistent")
	}
}

func writeAttestation(t *testing.T, records []runRecord) string {
	t.Helper()
	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "inloop.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIngestRefusesAnAttestationMissingItsAccounting(t *testing.T) {
	s := topologyStudy()
	record := scoredSample(inLoopDims(1), 5000, 1)
	record.Accounting = topologyAccounting{} // omitted entirely
	path := writeAttestation(t, []runRecord{record})
	if _, err := ingestInLoopSamples(path, s); err == nil ||
		!strings.Contains(err.Error(), "accounting") {
		t.Fatalf("an attestation with no accounting must be refused: %v", err)
	}
}

func TestIngestRefusesAnAttestationMissingItsDimensions(t *testing.T) {
	s := topologyStudy()
	record := scoredSample(inLoopDims(1), 5000, 1)
	record.Accounting = attestedAccounting()
	record.Dims.Model = ""
	path := writeAttestation(t, []runRecord{record})
	if _, err := ingestInLoopSamples(path, s); err == nil ||
		!strings.Contains(err.Error(), "model") {
		t.Fatalf("an attestation with an unrecorded dimension must be refused: %v", err)
	}
}

func TestIngestRefusesAnUndeclaredBinding(t *testing.T) {
	s := topologyStudy()
	record := scoredSample(inLoopDims(1), 5000, 1)
	record.Accounting = attestedAccounting()
	record.Dims.BindingID = "not-in-the-study"
	path := writeAttestation(t, []runRecord{record})
	if _, err := ingestInLoopSamples(path, s); err == nil ||
		!strings.Contains(err.Error(), "not-in-the-study") {
		t.Fatalf("an attestation naming an undeclared binding must be refused: %v", err)
	}
}

func TestIngestStampsCollectionAndTopologyItself(t *testing.T) {
	s := topologyStudy()
	var records []runRecord
	for run := 1; run <= 3; run++ {
		record := scoredSample(inLoopDims(run), 5000, 1)
		record.Accounting = attestedAccounting()
		// An attestation cannot talk itself into looking instrumented.
		record.Dims.Collection = collectionInstrumented
		record.Dims.Topology = topologyIsolated
		records = append(records, record)
	}
	ingested, err := ingestInLoopSamples(writeAttestation(t, records), s)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range ingested {
		if record.Dims.Topology != topologyInLoop || record.Dims.Collection != collectionAttested {
			t.Fatalf("ingest must stamp the topology and collection itself: %+v", record.Dims)
		}
		if record.RunID == "" {
			t.Fatal("every ingested record needs a run id")
		}
	}
}

func TestTopologyComparisonIsAlwaysQualifiedAndNeverRecommends(t *testing.T) {
	s := topologyStudy()
	isolated := cellSamples(testDims("isolated-a", "fix", 1), 3, 1000, 1)
	var attested []runRecord
	for run := 1; run <= 3; run++ {
		record := scoredSample(inLoopDims(run), 8000, 1)
		record.Accounting = attestedAccounting()
		attested = append(attested, record)
	}
	model := buildReport(s, append(isolated, attested...), nil)
	result := find(t, model.Comparisons, "topology")
	if result.Status != statusSupported {
		t.Fatalf("a matched topology pair should be readable: %+v", result)
	}
	if !result.Mixed {
		t.Fatal("attested-vs-instrumented must be flagged collection_mixed")
	}
	if !strings.Contains(result.Conclusion, "Collection methods DIFFER") {
		t.Fatalf("the conclusion must be qualified: %q", result.Conclusion)
	}
	if !strings.Contains(result.Conclusion, "never merged into a transport or provider conclusion") {
		t.Fatalf("the conclusion must say it is topology-only: %q", result.Conclusion)
	}
	if !strings.Contains(result.Conclusion, "Tool policy also differs") {
		t.Fatalf("a topology pair whose tool policies differ must say so: %q", result.Conclusion)
	}
	// An 8x gap on a collection-mixed comparison still recommends nothing.
	if model.Recommendation != "no binding change justified by this study" {
		t.Fatalf("a collection-mixed comparison must never justify a default change: %q", model.Recommendation)
	}
}

func TestTopologyNeverEntersATransportOrProviderConclusion(t *testing.T) {
	s := topologyStudy()
	s.Comparisons = append(s.Comparisons, studyComparison{
		ID: "transport", Free: "interface",
		Holds:   []string{"provider", "model", "effort_class", "tool_policy", "topology", "fixture"},
		Members: []string{"isolated-a", "inloop-a"},
	})
	isolated := cellSamples(testDims("isolated-a", "fix", 1), 3, 1000, 1)
	var attested []runRecord
	for run := 1; run <= 3; run++ {
		record := scoredSample(inLoopDims(run), 8000, 1)
		record.Accounting = attestedAccounting()
		attested = append(attested, record)
	}
	model := buildReport(s, append(isolated, attested...), nil)
	for _, r := range model.Comparisons {
		if r.ID != "transport" {
			continue
		}
		if r.Status == statusSupported {
			t.Fatalf("an in-loop sample must never satisfy a transport comparison: %+v", r)
		}
		if r.Conclusion != "" {
			t.Fatalf("transport comparison produced a conclusion from a topology pair: %q", r.Conclusion)
		}
	}
}

func TestMissingAttestationLeavesTopologyUnderpoweredNotFailed(t *testing.T) {
	s := topologyStudy()
	model := buildReport(s, cellSamples(testDims("isolated-a", "fix", 1), 3, 1000, 1), nil)
	result := find(t, model.Comparisons, "topology")
	if result.Status == statusSupported {
		t.Fatalf("with no in-loop samples the topology comparison cannot be supported: %+v", result)
	}
	if result.Conclusion != "" {
		t.Fatalf("no conclusion without both sides: %q", result.Conclusion)
	}
	// The study itself is still valid: a missing attestation is not a defect.
	if problems := evidenceProblems(cellSamples(testDims("isolated-a", "fix", 1), 3, 1000, 1), 3); len(problems) != 0 {
		t.Fatalf("a study without in-loop attestations must still pass: %v", problems)
	}
}
