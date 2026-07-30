//go:build plannerbench

package plannerbench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunRecordSchemaAndRedaction(t *testing.T) {
	record := newRunRecord("command", "fixture", 1)
	record.Binding = bindingEvidence{Name: "command", AgentsTOMLSHA: digest("binding")}
	record.StartedAt = time.Now().UTC()
	record.FinishedAt = record.StartedAt.Add(time.Second)
	record.RawFinalResult = textRecord(
		"result api_key=sk-example-secret-value /home/person/repo",
		"fixture", "/home/person")
	record.Attachment = attachmentEvidence{OK: true, Body: "# Plan", SHA256: digest("# Plan")}
	record.ArtifactScore = scorePlan("## AC1\nproof", []string{"first"})
	record.Usage = usageEvidence{Available: false, Provenance: "transport-unreported"}

	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{
		`"schema_version":1`, `"binding"`, `"environment"`,
		`"raw_final_result"`, `"attachment"`, `"artifact_score"`,
		`"content_hashes"`, `"usage"`, `"started_at"`, `"finished_at"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("record missing %s: %s", required, text)
		}
	}
	if strings.Contains(text, "sk-example") || strings.Contains(text, "/home/person") {
		t.Fatalf("raw result was not redacted: %s", text)
	}
}

func TestArtifactScorePerCriterionOrdinalCoverageRegression(t *testing.T) {
	score := scorePlan("## AC10\nunrelated\n\n## AC2\ncovered", []string{"first", "second"})
	if score.OK || len(score.Findings) != 2 {
		t.Fatalf("score = %#v", score)
	}
	if score.Findings[0].OK || !strings.Contains(strings.Join(score.Findings[0].Reasons, " "), "criterion 1") {
		t.Fatalf("AC1 ambiguity lacks explicit evidence: %#v", score.Findings[0])
	}
	if !score.Findings[1].OK {
		t.Fatalf("AC2 should be explicit success: %#v", score.Findings[1])
	}

	all := scorePlan("## Acceptance criterion 1\nproof\n\n## AC2\nproof", []string{"first", "second"})
	if !all.OK {
		t.Fatalf("structurally valid alternate heading should score: %#v", all)
	}
}

func TestUsageUnavailableIsNotNumericZero(t *testing.T) {
	unavailable := parseUsageEvidence("TOTAL 0 tokens", `{"usage_available":false}`)
	raw, err := json.Marshal(unavailable)
	if err != nil {
		t.Fatal(err)
	}
	if unavailable.Available || strings.Contains(string(raw), "tokens_total") {
		t.Fatalf("unavailable usage is ambiguous: %s", raw)
	}

	reportedZero := parseUsageEvidence("TOTAL 0 tokens", `{"usage_available":true,"tokens_total":0}`)
	if !reportedZero.Available || reportedZero.TokensTotal == nil || *reportedZero.TokensTotal != 0 {
		t.Fatalf("reported zero lost provenance: %#v", reportedZero)
	}
	reported := parseUsageEvidence("TOTAL 123 tokens", "")
	if !reported.Available || reported.TokensTotal == nil || *reported.TokensTotal != 123 {
		t.Fatalf("reported cost = %#v", reported)
	}
}

func TestIncrementalEvidencePreservesRawAndArtifact(t *testing.T) {
	out := t.TempDir()
	record := newRunRecord("v", "f", 1)
	record.RawFinalResult = textRecord("raw final", "fixture", "")
	record.Attachment = attachmentEvidence{OK: true, Body: "# Plan", SHA256: digest("# Plan")}
	record.ContentHashes["raw_final_result"] = record.RawFinalResult.SHA256
	if err := writeRunEvidence(out, record); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".json", ".raw.txt", ".artifact.md"} {
		path := filepath.Join(out, "runs", record.RunID+suffix)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("durable sidecar %s: %v", suffix, err)
		}
	}
	// A later interrupted sample never rewrites or removes completed run1.
	if _, err := os.Stat(filepath.Join(out, "runs", record.RunID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestFindExecutorOutputRetainsFinalResult(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "repo-key", "logs", "executor.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := "2026-01-01T00:00:00Z\tplanner\tsty_fixture\tstate plan\tok — output: " +
		`{"artifact":{"body":"# Plan\n\n## AC1"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := findExecutorOutput(root, "sty_fixture")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\n\n## AC1") {
		t.Fatalf("final result not retained: %q", out)
	}
}

func TestEvidenceProblemsDistinguishesQualityFromInfrastructure(t *testing.T) {
	quality := newRunRecord("v", "f", 1)
	quality.TransitionOK = true
	quality.ArtifactScore.OK = false
	if problems := evidenceProblems([]runRecord{quality}, []string{"v"}, []string{"f"}, 1); len(problems) != 0 {
		t.Fatalf("artifact quality should remain benchmark data: %v", problems)
	}

	infra := quality
	infra.InfrastructureFailure = true
	infra.Diagnostics = diagnosticEvidence{Class: "spawn", Detail: "missing binary"}
	if problems := evidenceProblems([]runRecord{infra}, []string{"v"}, []string{"f"}, 1); len(problems) < 2 {
		t.Fatalf("infrastructure + under-sample must fail: %v", problems)
	}
	if problems := evidenceProblems(nil, []string{"v"}, []string{"f"}, 2); len(problems) != 1 ||
		!strings.Contains(problems[0], "under-sampled") {
		t.Fatalf("minimum comparable sample not enforced: %v", problems)
	}
}

func TestPlannerBenchFailureModes(t *testing.T) {
	t.Run("spawn-or-auth", func(t *testing.T) {
		err := exec.Command(filepath.Join(t.TempDir(), "missing-agent")).Run()
		if err == nil {
			t.Fatal("missing agent unexpectedly spawned")
		}
		class, infrastructure := classifyRunFailure("executable file not found: missing-agent")
		assertDiagnostic(t, diagnosticEvidence{Class: class, Detail: err.Error()}, infrastructure, "spawn")
		authClass, authInfra := classifyRunFailure("authentication unauthorized")
		assertDiagnostic(t, diagnosticEvidence{Class: authClass, Detail: "unauthorized"}, authInfra, "auth")
	})

	t.Run("timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		err := exec.CommandContext(ctx, "sh", "-c", "sleep 1").Run()
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) || err == nil {
			t.Fatalf("timeout not exercised: ctx=%v err=%v", ctx.Err(), err)
		}
		assertDiagnostic(t, diagnosticEvidence{Class: "timeout", Detail: err.Error()}, true, "timeout")
	})

	t.Run("denied-mutation", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		before, _ := productDigest(root)
		deny := errors.New("policy denied mutation")
		after, _ := productDigest(root)
		if before != after || deny == nil {
			t.Fatal("denied mutation changed product tree")
		}
		assertDiagnostic(t, diagnosticEvidence{Class: "denied_mutation", Detail: deny.Error()}, false, "denied_mutation")
	})

	t.Run("malformed-output", func(t *testing.T) {
		_, err := parseAttachedBody("not-json")
		if err == nil {
			t.Fatal("malformed output accepted")
		}
		assertDiagnostic(t, diagnosticEvidence{Class: "malformed_output", Detail: err.Error()}, false, "malformed_output")
	})

	t.Run("attachment-failure", func(t *testing.T) {
		_, err := parseAttachedBody(`{"body":""}`)
		if err == nil {
			t.Fatal("empty attachment accepted")
		}
		assertDiagnostic(t, diagnosticEvidence{Class: "attachment", Detail: err.Error()}, true, "attachment")
	})

	t.Run("interrupted", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := exec.CommandContext(ctx, "sh", "-c", "sleep 1").Run()
		if err == nil {
			t.Fatal("interrupted command succeeded")
		}
		out := t.TempDir()
		completed := newRunRecord("v", "completed", 1)
		completed.RawFinalResult = textRecord("finished", "fixture", "")
		if err := writeRunEvidence(out, completed); err != nil {
			t.Fatal(err)
		}
		assertDiagnostic(t, diagnosticEvidence{Class: "interrupted", Detail: err.Error()}, true, "interrupted")
		if _, err := os.Stat(filepath.Join(out, "runs", completed.RunID+".json")); err != nil {
			t.Fatalf("interruption discarded completed evidence: %v", err)
		}
	})
}

func assertDiagnostic(t *testing.T, diagnostic diagnosticEvidence, infrastructure bool, want string) {
	t.Helper()
	if diagnostic.Class != want || strings.TrimSpace(diagnostic.Detail) == "" {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
	record := newRunRecord("failure-mode", want, 1)
	record.StartedAt = time.Now().UTC()
	record.FinishedAt = record.StartedAt
	record.Diagnostics = diagnostic
	record.InfrastructureFailure = infrastructure
	record.RawFinalResult = textRecord(diagnostic.Detail, "hermetic-failure-fixture", "")
	out := t.TempDir()
	if err := writeRunEvidence(out, record); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "runs", record.RunID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"class": "`+want+`"`) {
		t.Fatalf("structured diagnostic not recorded: %s", raw)
	}
}
