//go:build plannerbench

package plannerbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scheduleBindings() []studyBinding {
	return []studyBinding{
		{ID: "a", Command: "sh -c true", topology: topologyIsolated},
		{ID: "b", Command: "sh -c true", topology: topologyIsolated},
		{ID: "gone", Command: "definitely-not-a-real-binary-8c31 --x", topology: topologyIsolated},
		{ID: "inloop", topology: topologyInLoop},
	}
}

func TestWorkListSkipsUnavailableBindingsWithAReason(t *testing.T) {
	fixtures, err := loadFixtures(fixturesRoot())
	if err != nil {
		t.Fatal(err)
	}
	work, skipped := buildWorkList(scheduleBindings(), fixtures, 3, scheduleFilters{})
	if want := 2 * len(fixtures) * 3; len(work) != want {
		t.Fatalf("work list = %d items, want %d (2 available bindings)", len(work), want)
	}
	for _, id := range []string{"gone", "inloop"} {
		reason, ok := skipped[id]
		if !ok || strings.TrimSpace(reason) == "" {
			t.Errorf("%s must be skipped WITH a reason, not silently dropped: %q", id, reason)
		}
	}
	if _, ok := skipped["a"]; ok {
		t.Error("an available binding must not be skipped")
	}
}

func TestWorkListHonoursFilters(t *testing.T) {
	fixtures, err := loadFixtures(fixturesRoot())
	if err != nil {
		t.Fatal(err)
	}
	work, _ := buildWorkList(scheduleBindings(), fixtures, 2,
		scheduleFilters{Binding: "b", Fixture: fixtures[0].Name})
	if len(work) != 2 {
		t.Fatalf("filtered work list = %d items, want 2", len(work))
	}
	for _, item := range work {
		if item.binding.ID != "b" || item.fixture.Name != fixtures[0].Name {
			t.Fatalf("filter leaked: %+v", item)
		}
	}
}

func TestShuffleIsSeededAndInterleavesBindings(t *testing.T) {
	fixtures, err := loadFixtures(fixturesRoot())
	if err != nil {
		t.Fatal(err)
	}
	build := func() []workItem {
		work, _ := buildWorkList(scheduleBindings(), fixtures, 3, scheduleFilters{})
		return work
	}
	nested := build()
	first, second := build(), build()
	shuffleWork(first, 424242)
	shuffleWork(second, 424242)
	for i := range first {
		if first[i].binding.ID != second[i].binding.ID ||
			first[i].fixture.Name != second[i].fixture.Name || first[i].run != second[i].run {
			t.Fatal("the same seed must produce the same schedule — a study is reproducible from its seed")
		}
	}
	different := build()
	shuffleWork(different, 999)
	if sameOrder(first, different) {
		t.Fatal("different seeds produced an identical schedule")
	}
	// The nested order groups every sample of binding a before binding b; the
	// shuffled order must not, or run order stays confounded with binding.
	if sameOrder(nested, first) {
		t.Fatal("the shuffle left the nested per-binding order intact")
	}
	if !interleaves(first) {
		t.Fatal("the shuffled schedule never alternates bindings — order is still confounded with binding")
	}
}

func sameOrder(a, b []workItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].binding.ID != b[i].binding.ID || a[i].fixture.Name != b[i].fixture.Name ||
			a[i].run != b[i].run {
			return false
		}
	}
	return true
}

func interleaves(work []workItem) bool {
	for i := 1; i < len(work); i++ {
		if work[i].binding.ID != work[i-1].binding.ID {
			return true
		}
	}
	return false
}

// TestDurableEvidenceRoundTripsIntoAReport is the reproducibility path AC7
// requires, exercised end to end: records written per sample, read back from
// disk, and rendered — with no clock and no tokens involved.
func TestDurableEvidenceRoundTripsIntoAReport(t *testing.T) {
	s := transportStudy()
	command := testDims("a-command", "fix", 1)
	acp := testDims("a-acp", "fix", 1)
	acp.Interface = "acp"
	records := append(cellSamples(command, 3, 1000, 1), cellSamples(acp, 3, 2000, 0.5)...)

	out := t.TempDir()
	for _, record := range records {
		if err := writeRunEvidence(out, record); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeAggregateEvidence(out, records); err != nil {
		t.Fatal(err)
	}
	reloaded, err := readRunEvidence(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != len(records) {
		t.Fatalf("round-tripped %d of %d records", len(reloaded), len(records))
	}
	fromMemory := renderReportMarkdown(buildReport(s, records, nil))
	fromDisk := renderReportMarkdown(buildReport(s, reloaded, nil))
	if fromMemory != fromDisk {
		t.Fatal("a report rendered from disk differs from one rendered in memory")
	}
	if err := writeReport(out, buildReport(s, reloaded, nil)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"report.md", "report.json", "results.json"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(out, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Transport comparison") {
		t.Fatalf("the written report carries no conclusion:\n%s", raw)
	}
}

func TestIncrementalEvidencePreservesCompletedSamples(t *testing.T) {
	out := t.TempDir()
	record := scoredSample(testDims("a-command", "fix", 1), 1000, 1)
	record.RawFinalResult = textRecord("raw final api_key=sk-secret /home/person", "fixture", "/home/person")
	record.Attachment = attachmentEvidence{OK: true, Body: "# Plan", SHA256: digest("# Plan")}
	if err := writeRunEvidence(out, record); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".json", ".raw.txt", ".artifact.md"} {
		if _, err := os.Stat(filepath.Join(out, "runs", record.RunID+suffix)); err != nil {
			t.Fatalf("durable sidecar %s: %v", suffix, err)
		}
	}
	// Redaction happens before persistence, and the hash is over what was stored.
	raw, err := os.ReadFile(filepath.Join(out, "runs", record.RunID+".raw.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-secret") || strings.Contains(string(raw), "/home/person") {
		t.Fatalf("persisted raw evidence was not redacted: %s", raw)
	}
	if digest(string(raw)) != record.RawFinalResult.SHA256 {
		t.Fatal("the content hash does not cover the bytes actually stored")
	}
	// A later interrupted sample cannot erase the completed one.
	later := scoredSample(testDims("a-acp", "fix", 1), 1, 0)
	later.InfrastructureFailure = true
	later.Diagnostics = diagnosticEvidence{Class: classTimeout, Signal: "test"}
	if err := writeRunEvidence(out, later); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "runs", record.RunID+".json")); err != nil {
		t.Fatalf("interruption discarded completed evidence: %v", err)
	}
}

func TestEvidenceProblemsSeparatesQualityFromDefects(t *testing.T) {
	dims := testDims("a-command", "fix", 1)
	// A low-scoring but comparable cell is DATA, not a problem.
	quality := cellSamples(dims, 3, 1000, 0.25)
	if problems := evidenceProblems(quality, 3); len(problems) != 0 {
		t.Fatalf("a low artifact score is benchmark data: %v", problems)
	}
	// An infrastructure failure is a defect AND shrinks the cell below minimum.
	broken := cellSamples(dims, 3, 1000, 1)
	broken[0].InfrastructureFailure = true
	broken[0].Diagnostics = diagnosticEvidence{Class: classSpawn, Signal: "not on PATH"}
	problems := evidenceProblems(broken, 3)
	if len(problems) < 2 {
		t.Fatalf("want both the failure and the under-sample: %v", problems)
	}
	if !strings.Contains(strings.Join(problems, ";"), "under-sampled") {
		t.Fatalf("the minimum sample count must be enforced: %v", problems)
	}
	// A refused-but-scored run keeps the cell whole.
	refused := cellSamples(dims, 3, 1000, 0.5)
	refused[0].TransitionOK = false
	refused[0].Diagnostics = diagnosticEvidence{Class: classMalformedOutput, Signal: "validator"}
	if problems := evidenceProblems(refused, 3); len(problems) != 0 {
		t.Fatalf("a refused-but-scored run must still count toward the cell: %v", problems)
	}
}
