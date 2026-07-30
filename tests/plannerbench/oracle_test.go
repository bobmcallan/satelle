//go:build plannerbench

package plannerbench

import (
	"strings"
	"testing"
)

// AC8: the oracle must be independent of the transition validator. These tests
// pin the exact regression a review found — a plan that labels AC1..ACn scored
// as covered because the validator only looks for the label.

func loadSmallFixture(t *testing.T) fixture {
	t.Helper()
	f, err := loadFixture("testdata/fixtures/small_cli_flag")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestLabelOnlyPlanScoresZero(t *testing.T) {
	f := loadSmallFixture(t)
	// This body satisfies the TRANSITION VALIDATOR completely: every numbered
	// criterion has an AC<n> heading. It reaches no seam at all.
	body := `# Plan

## AC1
Will handle the first criterion carefully.

## AC2
Will handle the second criterion carefully.

## AC3
Will add tests.
`
	score := scoreArtifact(body, f, indexTree(f))
	if score.OK || score.Covered != 0 {
		t.Fatalf("a label-only plan must score zero, got %+v", score)
	}
	if !score.LabelOnly {
		t.Fatal("a plan that is all labels and no substance must be flagged label_only")
	}
	if score.LabelsFound < len(f.Acceptance) {
		t.Fatalf("labels found = %d; the oracle should still REPORT labels, just never score them",
			score.LabelsFound)
	}
	for _, c := range score.Criteria {
		if c.Covered {
			t.Errorf("criterion %d covered on a label alone: %+v", c.Ordinal, c)
		}
		if c.Evidence == "" {
			t.Errorf("criterion %d gives no reason for the miss", c.Ordinal)
		}
	}
}

func TestSubstanceWithoutLabelsScores(t *testing.T) {
	f := loadSmallFixture(t)
	// No "AC1"/"AC2"/"AC3" anywhere: substance only. The transition validator
	// would REJECT this body; the oracle scores it, which is the independence
	// the story asks for.
	body := `# Plan

## Flag parsing and the mutation seam

Add DryRun to Options in internal/cli/command.go, set by ParseFlags, and thread
it into Execute so internal/store/store.go Write is never reached. Proven by
TestExecuteWritesOnce extended with a dry-run case.

## Help text

Usage in internal/cli/command.go documents the flag, asserted in
internal/cli/command_test.go.

## Tests

internal/cli/command_test.go gains a table over dry-run and normal execution,
calling Reset between cases (TestExecuteWritesOnce).
`
	score := scoreArtifact(body, f, indexTree(f))
	if strings.Contains(body, "AC1") {
		t.Fatal("this fixture body must contain no acceptance labels")
	}
	if score.LabelsFound != 0 {
		t.Fatalf("labels found = %d in a label-free body", score.LabelsFound)
	}
	if !score.OK || score.Covered != len(f.Acceptance) {
		t.Fatalf("substance without labels must score: %+v", summarizeCriteria(score))
	}
	if score.LabelOnly {
		t.Fatal("a substantive plan must not be flagged label_only")
	}
}

func TestOracleRefusesSymbolsAndFilesTheTreeDoesNotHave(t *testing.T) {
	f := loadSmallFixture(t)
	body := `# Plan

## Parsing

Edit internal/cli/parser.go and change ParseArguments plus the DryRunner type so
the flag is handled, covered by internal/cli/parser_test.go.
`
	score := scoreArtifact(body, f, indexTree(f))
	if score.Covered != 0 {
		t.Fatalf("invented files and symbols must not score: %+v", summarizeCriteria(score))
	}
	first := score.Criteria[0]
	if len(first.FilesHit) != 0 || len(first.SymbolsHit) != 0 {
		t.Fatalf("criterion 1 recorded hits for things the tree lacks: %+v", first)
	}
	if len(first.FilesMissed) == 0 || len(first.SymbolsMissed) == 0 {
		t.Fatalf("criterion 1 must name what it missed: %+v", first)
	}
}

func TestOracleRequiresANamedTestNearTheSeam(t *testing.T) {
	f := loadSmallFixture(t)
	// Files and symbols reached, but no test named anywhere.
	body := `# Plan

## Parsing

Add DryRun to Options in internal/cli/command.go; ParseFlags sets it and Execute
skips internal/store/store.go Write.

## Help

Usage documents it.
`
	score := scoreArtifact(body, f, indexTree(f))
	if score.Criteria[0].Covered {
		t.Fatalf("a criterion with no named test must not be covered: %+v", score.Criteria[0])
	}
	if !strings.Contains(score.Criteria[0].Evidence, "named no test") {
		t.Fatalf("evidence should say the test is missing: %q", score.Criteria[0].Evidence)
	}
	if len(score.Criteria[0].FilesHit) == 0 || len(score.Criteria[0].SymbolsHit) == 0 {
		t.Fatalf("the file and symbol hits should still be recorded: %+v", score.Criteria[0])
	}
}

func TestOracleAttributesTestsPerSectionNotPerPlan(t *testing.T) {
	f := loadSmallFixture(t)
	// The test is named in a section that does NOT mention criterion 1's seam, so
	// it is not evidence for criterion 1.
	body := `# Plan

## Parsing

Add DryRun to Options in internal/cli/command.go; ParseFlags and Execute thread
it so store.go Write is skipped.

## Unrelated groundwork

We will also run TestExecuteWritesOnce at some point.
`
	score := scoreArtifact(body, f, indexTree(f))
	if score.Criteria[0].Covered {
		t.Fatalf("a test named away from the seam must not cover the criterion: %+v", score.Criteria[0])
	}
}

func TestPartialCoverageIsAFractionNotABoolean(t *testing.T) {
	f := loadSmallFixture(t)
	body := `# Plan

## Parsing and the store seam

Options gains DryRun in internal/cli/command.go; ParseFlags reads it and Execute
short-circuits before internal/store/store.go Write. Covered by
TestExecuteWritesOnce.
`
	score := scoreArtifact(body, f, indexTree(f))
	if score.Covered == 0 || score.Covered == score.Total {
		t.Fatalf("want partial coverage, got %d/%d: %+v", score.Covered, score.Total, summarizeCriteria(score))
	}
	want := float64(score.Covered) / float64(score.Total)
	if score.Fraction != want {
		t.Fatalf("fraction = %v, want %v", score.Fraction, want)
	}
	if score.OK {
		t.Fatal("partial coverage must not be OK")
	}
}

func TestRefusedRunIsStillScoredFromTheRecoveredBody(t *testing.T) {
	f := loadSmallFixture(t)
	raw := `2026-01-01 planner sty_x state plan ok — output: ` +
		`{"artifact":{"name":"plan","type":"plan","body":"# Plan\n\n## Parsing\n\nOptions and ParseFlags in internal/cli/command.go, proven by TestExecuteWritesOnce.\n"}}`
	body, ok := recoverArtifactBody(raw)
	if !ok {
		t.Fatal("a refused run's artifact envelope must be recoverable from the executor log")
	}
	score := scoreSample(sampleEnv{Study: study{}}, f, "binding-under-test", body, "executor-log-artifact-envelope")
	if !score.Scored {
		t.Fatalf("a recovered body must yield a scored sample: %+v", score)
	}
	if score.BodyProvenance != "executor-log-artifact-envelope" {
		t.Fatalf("provenance = %q", score.BodyProvenance)
	}
	if score.Deterministic.Covered == 0 {
		t.Fatalf("the recovered body reaches a seam and should score: %+v", summarizeCriteria(score.Deterministic))
	}
	if score.Judge.Available || score.Judge.Reason == "" {
		t.Fatalf("with no judge declared the judge score must be unavailable with a reason: %+v", score.Judge)
	}
}

func TestUnscorableSampleSaysWhy(t *testing.T) {
	f := loadSmallFixture(t)
	score := scoreSample(sampleEnv{Study: study{}}, f, "binding-under-test", "   ", "none")
	if score.Scored || score.Unscorable == "" {
		t.Fatalf("an empty body must be unscorable with a reason: %+v", score)
	}
}

func TestOracleNeverImportsTheTransitionValidator(t *testing.T) {
	// The independence AC8 asks for is structural: this package must not depend
	// on the package whose ValidateAll gates the transition.
	if importsAgentArtifact() {
		t.Fatal("plannerbench must not import internal/agentartifact — the oracle would echo the gate again")
	}
}

func summarizeCriteria(s deterministicScore) string {
	var sb strings.Builder
	for _, c := range s.Criteria {
		sb.WriteString("\n  ")
		sb.WriteString(c.Evidence)
	}
	return sb.String()
}

func TestJudgeIsNeverTheBindingUnderTest(t *testing.T) {
	f := loadSmallFixture(t)
	s := study{Judge: &judgeBinding{ID: "grader", Command: "sh -c true"}}
	// A judge that IS the binding under test is not a second opinion, so it is
	// refused before it can be invoked.
	self := scoreSample(sampleEnv{Study: s}, f, "grader", "# Plan\n\nSomething.\n", "attached-story-document")
	if self.Judge.Available {
		t.Fatalf("a binding must not grade its own answer: %+v", self.Judge)
	}
	if !strings.Contains(self.Judge.Reason, "binding under test") {
		t.Fatalf("the refusal must say why: %q", self.Judge.Reason)
	}
	// The deterministic oracle still ran, so the sample keeps a real score.
	if !self.Scored {
		t.Fatal("a refused judge must not cost the sample its deterministic score")
	}
}
