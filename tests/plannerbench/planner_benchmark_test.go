//go:build plannerbench

// Package plannerbench is a controlled, provider-neutral study of the isolated
// planner step. It answers three questions SEPARATELY — transport efficiency at
// a fixed provider and model, provider/model quality at a fixed interface, and
// isolated child execution versus the in-loop executor — and refuses to answer
// any of them from unmatched cells.
//
// The live matrix is opt-in: each sample spends model tokens. Everything else in
// this package (fixtures, dimensions, oracle, usage aggregation, failure
// classification, report rendering) is hermetic and runs on the build tag alone.
package plannerbench

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// workItem is one scheduled sample.
type workItem struct {
	binding studyBinding
	fixture fixture
	run     int
}

func TestLivePlannerStudy(t *testing.T) {
	if os.Getenv("SATELLE_PLANNER_BENCH") != "1" {
		t.Skip("set SATELLE_PLANNER_BENCH=1 (or run make planner-bench); this spends live model tokens")
	}
	bin := os.Getenv("SATELLE_BIN")
	if bin == "" {
		t.Fatal("SATELLE_BIN must name the built satelle binary")
	}
	s := mustLoadStudy(t)
	shipped, err := loadShippedGrant(shippedAgentsPath())
	if err != nil {
		t.Fatalf("read the shipped planner grant: %v", err)
	}
	bindings, err := resolveBindings(s, shipped.Grant)
	if err != nil {
		t.Fatalf("resolve study bindings: %v", err)
	}
	fixtures := mustLoadFixtures(t)
	plannerSkill := mustReadPlannerSkill(t)

	runs := s.Runs
	if raw := os.Getenv("SATELLE_PLANNER_BENCH_RUNS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			t.Fatalf("SATELLE_PLANNER_BENCH_RUNS=%q: want a positive integer", raw)
		}
		runs = n
	}
	minimum := s.MinSamples
	if raw := os.Getenv("SATELLE_PLANNER_BENCH_MIN_SAMPLES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			t.Fatalf("SATELLE_PLANNER_BENCH_MIN_SAMPLES=%q: want a positive integer", raw)
		}
		minimum = n
	}
	seed := s.Seed
	if raw := os.Getenv("SATELLE_PLANNER_BENCH_SEED"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			t.Fatalf("SATELLE_PLANNER_BENCH_SEED=%q: want an integer", raw)
		}
		seed = n
	}

	settings := map[string]string{
		"runs": strconv.Itoa(runs), "min_samples": strconv.Itoa(minimum),
		"seed":           strconv.FormatInt(seed, 10),
		"binding_filter": os.Getenv("SATELLE_PLANNER_BENCH_BINDING"),
		"fixture_filter": os.Getenv("SATELLE_PLANNER_BENCH_FIXTURE"),
		"inloop_file":    inLoopAttestationPath(),
	}
	env := sampleEnv{
		Bin: bin, PlannerSkill: plannerSkill, Study: s, Shipped: shipped, Settings: settings,
	}

	work, skipped := buildWorkList(bindings, fixtures, runs, scheduleFilters{
		Binding: os.Getenv("SATELLE_PLANNER_BENCH_BINDING"),
		Fixture: os.Getenv("SATELLE_PLANNER_BENCH_FIXTURE"),
	})
	for id, reason := range skipped {
		t.Logf("skipped binding %s: %s", id, reason)
	}
	if len(work) == 0 {
		t.Fatal("study filters and host availability selected no sample")
	}
	shuffleWork(work, seed)

	outDir := benchmarkOutDir()
	var records []runRecord
	for i, item := range work {
		runOrder := i + 1
		dims := item.binding.dims(s, item.fixture.Name, item.fixture.contextBytes, runOrder, item.run)
		name := fmt.Sprintf("%s/%s/%d", item.binding.ID, item.fixture.Name, item.run)
		t.Run(name, func(t *testing.T) {
			record := runSample(t.TempDir(), env, item.binding, item.fixture, dims)
			records = append(records, record)
			// Durable per-sample files land BEFORE the aggregate, so a later
			// interruption cannot erase a completed sample.
			if err := writeRunEvidence(outDir, record); err != nil {
				t.Fatalf("write durable run evidence: %v", err)
			}
			if err := writeAggregateEvidence(outDir, records); err != nil {
				t.Fatalf("write aggregate evidence: %v", err)
			}
			t.Logf("order=%d wall=%s startup=%s ttfe=%s oracle=%.2f usage=%v attempts=%d class=%s",
				runOrder, time.Duration(record.Timing.WallMS)*time.Millisecond,
				msOrNA(record.Timing.StartupMS), msOrNA(record.Timing.TTFEMS),
				record.Score.Deterministic.Fraction, record.Usage.Available,
				record.Attempts, record.Diagnostics.Class)
		})
	}

	// Operator-attested in-loop samples join the study for the topology
	// comparison only, and only if they carry the same dimensions and accounting.
	if path := inLoopAttestationPath(); path != "" {
		attested, err := ingestInLoopSamples(path, s)
		if err != nil {
			t.Fatalf("ingest in-loop attestation: %v", err)
		}
		t.Logf("ingested %d operator-attested in-loop samples", len(attested))
		records = append(records, attested...)
	} else {
		t.Log("no SATELLE_PLANNER_BENCH_INLOOP file: the topology comparison will report underpowered")
	}

	model := buildReport(s, records, skipped)
	if err := writeAggregateEvidence(outDir, records); err != nil {
		t.Fatalf("write aggregate evidence: %v", err)
	}
	if err := writeReport(outDir, model); err != nil {
		t.Fatalf("write report: %v", err)
	}
	t.Logf("report: %s", filepath.Join(outDir, "report.md"))
	t.Logf("recommendation: %s", model.Recommendation)
	for _, problem := range evidenceProblems(records, minimum) {
		t.Error(problem)
	}
}

// TestRegenerateReportFromDurableEvidence rebuilds the report from the durable
// per-run files without spending a token. It is the reproducibility path AC7
// requires: the same records must always render the same report.
func TestRegenerateReportFromDurableEvidence(t *testing.T) {
	if os.Getenv("SATELLE_PLANNER_REPORT") != "1" {
		t.Skip("set SATELLE_PLANNER_REPORT=1 (or run make planner-report) to regenerate from out/runs")
	}
	s := mustLoadStudy(t)
	outDir := benchmarkOutDir()
	records, err := readRunEvidence(outDir)
	if err != nil {
		t.Fatalf("read durable run evidence under %s: %v", outDir, err)
	}
	if len(records) == 0 {
		t.Fatalf("no durable run records under %s/runs", outDir)
	}
	model := buildReport(s, records, nil)
	first := renderReportMarkdown(model)
	if second := renderReportMarkdown(buildReport(s, records, nil)); first != second {
		t.Fatal("report rendering is not deterministic over the same records")
	}
	if err := writeReport(outDir, model); err != nil {
		t.Fatalf("write report: %v", err)
	}
	t.Logf("regenerated %s from %d durable records", filepath.Join(outDir, "report.md"), len(records))
	t.Logf("recommendation: %s", model.Recommendation)
}

func mustLoadStudy(t *testing.T) study {
	t.Helper()
	path := os.Getenv("SATELLE_PLANNER_BENCH_STUDY")
	if path == "" {
		path = "study.json"
	}
	s, err := loadStudy(path)
	if err != nil {
		t.Fatalf("load study %s: %v", path, err)
	}
	return s
}

func mustLoadFixtures(t *testing.T) []fixture {
	t.Helper()
	fixtures, err := loadFixtures(fixturesRoot())
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	return fixtures
}

func mustReadPlannerSkill(t *testing.T) string {
	t.Helper()
	path := os.Getenv("SATELLE_PLANNER_SKILL")
	if path == "" {
		path = filepath.Join("..", "..", ".satelle", "skills", "plan.md")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read production planner skill %s: %v", path, err)
	}
	return string(raw)
}

func fixturesRoot() string {
	if p := os.Getenv("SATELLE_PLANNER_BENCH_FIXTURES"); p != "" {
		return p
	}
	return filepath.Join("testdata", fixturesDir)
}

func benchmarkOutDir() string {
	if out := os.Getenv("SATELLE_PLANNER_BENCH_OUT"); out != "" {
		return out
	}
	return "out"
}

func msOrNA(v *int64) string {
	if v == nil {
		return "n/a"
	}
	return (time.Duration(*v) * time.Millisecond).String()
}
