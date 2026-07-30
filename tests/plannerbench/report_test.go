//go:build plannerbench

package plannerbench

import (
	"path/filepath"
	"strings"
	"testing"
)

// scoredSample builds a comparable sample with the given wall time and oracle
// coverage. Records built here are complete: an incomplete record is refused by
// buildReport, which is itself asserted below.
func scoredSample(dims sampleDims, wallMS int64, fraction float64) runRecord {
	record := newRunRecord(dims)
	record.Timing.WallMS = wallMS
	record.Timing.StartupMS = int64Ptr(wallMS / 10)
	record.Timing.TTFEMS = int64Ptr(wallMS / 5)
	record.Tools = toolEvidence{Available: true, Calls: 4, Source: "test"}
	record.Attempts = 1
	record.TransitionOK = true
	record.Policy = policyEvidence{ReadOnlyFaithful: true, MirrorsShipped: true}
	record.Accounting = isolatedAccounting(3, "test")
	record.Usage = usageEvidence{
		Available: true, TokensTotal: intPtr(1000), AttemptsTotal: 1, AttemptsWithUsage: 1,
		Provenance: "ledger-agent-attempt-sum",
	}
	record.Score = artifactScore{
		Scored: true, BodyProvenance: "attached-story-document",
		Deterministic: deterministicScore{
			OK: fraction == 1, Covered: int(fraction * 4), Total: 4, Fraction: fraction,
		},
	}
	record.Diagnostics = diagnosticEvidence{Class: classNone, Signal: "transition committed"}
	return record
}

// cellSamples produces minSamples comparable samples for one binding/fixture.
func cellSamples(dims sampleDims, n int, wallMS int64, fraction float64) []runRecord {
	var records []runRecord
	for run := 1; run <= n; run++ {
		d := dims
		d.Run, d.RunOrder = run, run
		records = append(records, scoredSample(d, wallMS, fraction))
	}
	return records
}

func transportStudy() study {
	return study{
		ID: "t", Seed: 1, Runs: 3, MinSamples: 3, P50GapPercent: 25,
		ContextBuckets: []contextBucket{{Name: "small", MaxBytes: 0}},
		Comparisons: []studyComparison{{
			ID: "transport", Free: "interface",
			Holds:   []string{"provider", "model", "effort_class", "tool_policy", "topology", "fixture"},
			Members: []string{"a-command", "a-acp"},
		}},
		Bindings: []studyBinding{{ID: "a-command"}, {ID: "a-acp"}},
	}
}

func find(t *testing.T, results []comparisonResult, id string) comparisonResult {
	t.Helper()
	for _, r := range results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("comparison %q not in %+v", id, results)
	return comparisonResult{}
}

func TestTransportComparisonHoldsProviderAndModelConstant(t *testing.T) {
	s := transportStudy()
	command := testDims("a-command", "fix", 1)
	acp := testDims("a-acp", "fix", 1)
	acp.Interface = "acp"
	records := append(cellSamples(command, 3, 1000, 1), cellSamples(acp, 3, 2000, 1)...)

	model := buildReport(s, records, nil)
	result := find(t, model.Comparisons, "transport")
	if result.Status != statusSupported {
		t.Fatalf("a legal command-vs-ACP pair must be supported: %+v", result)
	}
	if result.FasterSide != "a-command" {
		t.Fatalf("faster side = %q", result.FasterSide)
	}
	if result.P50GapPct == nil || *result.P50GapPct != 100 {
		t.Fatalf("p50 gap = %v, want 100%%", result.P50GapPct)
	}
	if !strings.Contains(result.Conclusion, "Transport comparison") {
		t.Fatalf("conclusion = %q", result.Conclusion)
	}
	// A transport conclusion must not read provider or model into its text: the
	// two sides share them, so naming them invites misattribution.
	for _, forbidden := range []string{command.Provider, command.Model} {
		if strings.Contains(result.Conclusion, forbidden) {
			t.Errorf("transport conclusion names %q: %q", forbidden, result.Conclusion)
		}
	}
}

func TestTransportComparisonWithDifferentModelsIsConfounded(t *testing.T) {
	s := transportStudy()
	command := testDims("a-command", "fix", 1)
	acp := testDims("a-acp", "fix", 1)
	acp.Interface = "acp"
	acp.Model = "model-b" // the held dimension now differs
	records := append(cellSamples(command, 3, 1000, 1), cellSamples(acp, 3, 2000, 1)...)

	model := buildReport(s, records, nil)
	for _, r := range model.Comparisons {
		if r.ID != "transport" {
			continue
		}
		if r.Status == statusSupported {
			t.Fatalf("a pair differing on a held dimension must not be supported: %+v", r)
		}
		if r.Conclusion != "" {
			t.Fatalf("a confounded comparison must produce no conclusion: %q", r.Conclusion)
		}
	}
	confounded := false
	for _, r := range model.Comparisons {
		if r.ID == "transport" && r.Status == statusConfounded {
			confounded = true
			if !containsAny(r.Differing, "model") {
				t.Errorf("confounded verdict must name the differing dimension: %v", r.Differing)
			}
			if !strings.Contains(r.Reason, "model") {
				t.Errorf("reason must name it too: %q", r.Reason)
			}
		}
	}
	if !confounded {
		t.Fatalf("expected a confounded verdict, got %+v", model.Comparisons)
	}
}

func TestProviderComparisonEmitsNoTransportClaim(t *testing.T) {
	s := study{
		ID: "p", Seed: 1, Runs: 3, MinSamples: 3, P50GapPercent: 25,
		ContextBuckets: []contextBucket{{Name: "small", MaxBytes: 0}},
		Bindings:       []studyBinding{{ID: "prov-a"}, {ID: "prov-b"}},
		Comparisons: []studyComparison{{
			ID: "provider", Free: "provider",
			Holds:   []string{"interface", "effort_class", "context_size_bucket", "tool_policy", "topology", "fixture"},
			Members: []string{"prov-a", "prov-b"},
		}},
	}
	a := testDims("prov-a", "fix", 1)
	b := testDims("prov-b", "fix", 1)
	b.Provider = "provider-b"
	records := append(cellSamples(a, 3, 1000, 1.0), cellSamples(b, 3, 1500, 0.5)...)

	result := find(t, buildReport(s, records, nil).Comparisons, "provider")
	if result.Status != statusSupported {
		t.Fatalf("a matched provider pair must be supported: %+v", result)
	}
	// Naming interface as HELD constant is correct — it is the opposite of a
	// transport claim. What must never appear is transport ATTRIBUTION: the word
	// transport, an interface value named as a winner, or the transport
	// renderer's `interface "x" completed…` phrasing.
	lower := strings.ToLower(result.Conclusion)
	if strings.Contains(lower, "transport") {
		t.Errorf("provider conclusion claims a transport result: %q", result.Conclusion)
	}
	if strings.Contains(result.Conclusion, `interface "`) {
		t.Errorf("provider conclusion attributes an outcome to an interface: %q", result.Conclusion)
	}
	for _, value := range []string{`"command"`, `"acp"`} {
		if strings.Contains(result.Conclusion, value) {
			t.Errorf("provider conclusion names the interface value %s: %q", value, result.Conclusion)
		}
	}
	// And the transport renderer's own sentence must be structurally absent.
	if strings.Contains(result.Conclusion, "Transport comparison") {
		t.Errorf("provider conclusion reused the transport renderer: %q", result.Conclusion)
	}
	if result.BetterSide != "prov-a" {
		t.Fatalf("higher-quality side = %q, want the higher oracle score", result.BetterSide)
	}
}

func TestProviderComparisonWithDifferentInterfacesIsRejected(t *testing.T) {
	s := study{
		ID: "p", Seed: 1, Runs: 3, MinSamples: 3, P50GapPercent: 25,
		ContextBuckets: []contextBucket{{Name: "small", MaxBytes: 0}},
		Bindings:       []studyBinding{{ID: "prov-a"}, {ID: "prov-b"}},
		Comparisons: []studyComparison{{
			ID: "provider", Free: "provider",
			Holds:   []string{"interface", "effort_class", "tool_policy", "topology", "fixture"},
			Members: []string{"prov-a", "prov-b"},
		}},
	}
	a := testDims("prov-a", "fix", 1)
	b := testDims("prov-b", "fix", 1)
	b.Provider, b.Interface = "provider-b", "acp"
	records := append(cellSamples(a, 3, 1000, 1), cellSamples(b, 3, 1000, 1)...)

	for _, r := range buildReport(s, records, nil).Comparisons {
		if r.ID == "provider" && r.Status == statusSupported {
			t.Fatalf("a provider pair on different interfaces must not be supported: %+v", r)
		}
	}
}

func TestUnderpoweredCellYieldsNoConclusion(t *testing.T) {
	s := transportStudy()
	command := testDims("a-command", "fix", 1)
	acp := testDims("a-acp", "fix", 1)
	acp.Interface = "acp"
	records := append(cellSamples(command, 3, 1000, 1), cellSamples(acp, 2, 2000, 1)...)

	result := find(t, buildReport(s, records, nil).Comparisons, "transport")
	if result.Status != statusUnderpowered {
		t.Fatalf("an n=2 side must be underpowered: %+v", result)
	}
	if result.Conclusion != "" {
		t.Fatalf("an underpowered comparison must produce no conclusion: %q", result.Conclusion)
	}
	if !strings.Contains(result.Reason, "n=2") || !strings.Contains(result.Reason, "n>=3") {
		t.Fatalf("reason should say what was short: %q", result.Reason)
	}
}

func TestRecommendationIsGatedOnASupportedGapThreshold(t *testing.T) {
	s := transportStudy()
	command := testDims("a-command", "fix", 1)
	acp := testDims("a-acp", "fix", 1)
	acp.Interface = "acp"

	// A gap below the threshold justifies nothing.
	narrow := buildReport(s, append(cellSamples(command, 3, 1000, 1), cellSamples(acp, 3, 1100, 1)...), nil)
	if narrow.Recommendation != "no binding change justified by this study" {
		t.Fatalf("a 10%% gap must not justify a change: %q", narrow.Recommendation)
	}
	// A gap above it does, and it names the free variable and the side.
	wide := buildReport(s, append(cellSamples(command, 3, 1000, 1), cellSamples(acp, 3, 2000, 1)...), nil)
	if !strings.Contains(wide.Recommendation, "a-command") ||
		!strings.Contains(wide.Recommendation, "interface") {
		t.Fatalf("recommendation = %q", wide.Recommendation)
	}
	// An underpowered study never recommends, whatever the gap.
	thin := buildReport(s, append(cellSamples(command, 3, 1000, 1), cellSamples(acp, 1, 9000, 1)...), nil)
	if thin.Recommendation != "no binding change justified by this study" {
		t.Fatalf("an underpowered comparison must not recommend: %q", thin.Recommendation)
	}
}

func TestReportRefusesToMixSchemaVersions(t *testing.T) {
	s := transportStudy()
	records := cellSamples(testDims("a-command", "fix", 1), 3, 1000, 1)
	stale := records[0]
	stale.SchemaVersion = 1
	stale.RunID = "stale"
	model := buildReport(s, append(records, stale), nil)
	if len(model.Problems) == 0 || !strings.Contains(strings.Join(model.Problems, ";"), "schema 1 refused") {
		t.Fatalf("a schema-1 record must be refused, not coerced: %v", model.Problems)
	}
	if model.Comparable != 3 {
		t.Fatalf("the refused record must not enter a cell: comparable = %d", model.Comparable)
	}
}

func TestIncompleteRecordIsRefusedFromEveryComparison(t *testing.T) {
	s := transportStudy()
	records := cellSamples(testDims("a-command", "fix", 1), 3, 1000, 1)
	records[1].Dims.Provider = "" // an unrecorded dimension
	model := buildReport(s, records, nil)
	if len(model.Problems) == 0 {
		t.Fatal("an incomplete record must be reported as a problem")
	}
	if model.Comparable != 2 {
		t.Fatalf("comparable = %d, want the 2 complete records", model.Comparable)
	}
}

func TestUnscoredSampleIsNotComparable(t *testing.T) {
	dims := testDims("a-command", "fix", 1)
	unscored := scoredSample(dims, 1000, 1)
	unscored.Score = artifactScore{Unscorable: "no body"}
	if unscored.comparable() {
		t.Fatal("a sample the oracle could not score must not enter a cell")
	}
	// A REFUSED but scored run IS comparable — refusal must not shrink a cell.
	refused := scoredSample(dims, 1000, 0.25)
	refused.TransitionOK = false
	refused.Diagnostics = diagnosticEvidence{Class: classMalformedOutput}
	if !refused.comparable() {
		t.Fatal("a refused-but-scored run must remain a comparable sample")
	}
	infra := scoredSample(dims, 1000, 1)
	infra.InfrastructureFailure = true
	if infra.comparable() {
		t.Fatal("an infrastructure failure must not be comparable")
	}
}

func TestSkippedBindingIsReportedWithItsReason(t *testing.T) {
	s := transportStudy()
	records := cellSamples(testDims("a-command", "fix", 1), 3, 1000, 1)
	skipped := map[string]string{"a-acp": "npx not on PATH"}
	model := buildReport(s, records, skipped)
	result := find(t, model.Comparisons, "transport")
	if result.Status != statusConfounded && result.Status != statusSkipped {
		t.Fatalf("one-sided data cannot be a conclusion: %+v", result)
	}
	if result.Conclusion != "" {
		t.Fatalf("a one-sided comparison must produce no conclusion: %q", result.Conclusion)
	}
	markdown := renderReportMarkdown(model)
	if !strings.Contains(markdown, "npx not on PATH") {
		t.Fatal("the report must state why a binding was not sampled")
	}
}

func TestReportRenderingIsDeterministicAndPure(t *testing.T) {
	s := transportStudy()
	command := testDims("a-command", "fix", 1)
	acp := testDims("a-acp", "fix", 1)
	acp.Interface = "acp"
	records := append(cellSamples(command, 3, 1000, 1), cellSamples(acp, 3, 2000, 0.75)...)
	first := renderReportMarkdown(buildReport(s, records, nil))
	for i := 0; i < 3; i++ {
		if again := renderReportMarkdown(buildReport(s, records, nil)); again != first {
			t.Fatal("report rendering is not deterministic over the same records")
		}
	}
	for _, want := range []string{
		"## Study", "## Cells", "## Supported conclusions", "## Underpowered",
		"## Confounded", "## Skipped", "## Binding-change recommendation", "## Honest limits",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("report is missing the %q section", want)
		}
	}
	if !strings.Contains(first, "p50") && !strings.Contains(first, "wall p50/p90") {
		t.Error("the cell table must report p50/p90")
	}
}

func TestUnknownFreeVariableOrHoldIsRefusedAtStudyLoad(t *testing.T) {
	base := study{
		ID: "s", Runs: 3, MinSamples: 3,
		ContextBuckets: []contextBucket{{Name: "large", MaxBytes: 0}},
		Bindings:       []studyBinding{{ID: "a"}, {ID: "b"}},
	}
	bad := base
	bad.Comparisons = []studyComparison{{ID: "c", Free: "vibes", Members: []string{"a", "b"}}}
	if err := bad.validate(); err == nil || !strings.Contains(err.Error(), "free_variable") {
		t.Fatalf("an unknown free variable must be refused: %v", err)
	}
	bad = base
	bad.Comparisons = []studyComparison{{ID: "c", Free: "model", Holds: []string{"mood"}, Members: []string{"a", "b"}}}
	if err := bad.validate(); err == nil || !strings.Contains(err.Error(), "mood") {
		t.Fatalf("an unknown held dimension must be refused: %v", err)
	}
	bad = base
	bad.Comparisons = []studyComparison{{ID: "c", Free: "model", Holds: []string{"model"}, Members: []string{"a", "b"}}}
	if err := bad.validate(); err == nil || !strings.Contains(err.Error(), "free_variable") {
		t.Fatalf("holding the free variable constant must be refused: %v", err)
	}
	bad = base
	bad.MinSamples = 2
	if err := bad.validate(); err == nil || !strings.Contains(err.Error(), "min_samples") {
		t.Fatalf("min_samples below 3 must be refused (AC6): %v", err)
	}
}

func containsAny(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// TestDefaultStudyComparisonsBehaveAsDocumented pins the claims the report's
// honest-limits section makes about the SHIPPED study: the model and transport
// comparisons are answerable, and the cross-provider comparison is confounded by
// construction because two providers' native tool vocabularies are not the same
// grant. If someone later "fixes" that by relabelling a divergent grant, this
// fails.
func TestDefaultStudyComparisonsBehaveAsDocumented(t *testing.T) {
	s, err := loadStudy("study.json")
	if err != nil {
		t.Fatal(err)
	}
	shipped, err := loadShippedGrant(filepath.Join("..", "..", ".satelle", "agents.toml"))
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := resolveBindings(s, shipped.Grant)
	if err != nil {
		t.Fatal(err)
	}
	// Three comparable samples per binding on one fixture, all otherwise equal.
	var records []runRecord
	for i, b := range bindings {
		for run := 1; run <= s.MinSamples; run++ {
			dims := b.dims(s, "small_cli_flag", 4096, run, run)
			record := scoredSample(dims, int64(1000+200*i), 1)
			record.Policy.MirrorsShipped = b.grant == shipped.Grant
			if b.topology == topologyInLoop {
				record.Accounting = attestedAccounting()
			}
			records = append(records, record)
		}
	}
	model := buildReport(s, records, nil)
	if len(model.Problems) != 0 {
		t.Fatalf("the default study over complete records must report no problems: %v", model.Problems)
	}
	byID := map[string]comparisonResult{}
	for _, r := range model.Comparisons {
		// Keep the most decisive verdict per comparison id.
		if existing, ok := byID[r.ID]; ok && existing.Status == statusSupported {
			continue
		}
		byID[r.ID] = r
	}
	for _, id := range []string{"transport-codex-o4mini", "model-claude-command"} {
		if got := byID[id].Status; got != statusSupported {
			t.Errorf("%s: status = %q (%s), want supported — this comparison is matched by construction",
				id, got, byID[id].Reason)
		}
	}
	provider := byID["provider-acp-high"]
	if provider.Status == statusSupported {
		t.Errorf("provider-acp-high must NOT be supported: two providers' native grants are different tool policies")
	}
	if provider.Status == statusConfounded && !containsAny(provider.Differing, "tool_policy") {
		t.Errorf("the confounded verdict must name tool_policy: %v", provider.Differing)
	}
	topology := byID["topology-claude-opus"]
	if topology.Status == statusSupported && !topology.Mixed {
		t.Errorf("a topology comparison over attested in-loop samples must be collection-mixed: %+v", topology)
	}
	// And whatever the timings, a study whose only wide gaps are confounded or
	// collection-mixed recommends nothing.
	if strings.Contains(model.Recommendation, "provider-acp-high") ||
		strings.Contains(model.Recommendation, "topology-claude-opus") {
		t.Errorf("a confounded or collection-mixed comparison must never justify a change: %q",
			model.Recommendation)
	}
}
