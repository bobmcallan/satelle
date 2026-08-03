//go:build plannerbench

package plannerbench

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// AC10: every supported failure class is exercised through the REAL harness path
// — an actual `satelle story set --status plan` transition against a stub agent
// binary — not asserted from a hand-constructed record. The previous suite
// handed classifyRunFailure the string it wanted and then asserted the class it
// had just supplied, which proved nothing about classification.
//
// These runs spend NO tokens: the "agent" is a shell script. They therefore run
// on the build tag alone, giving every class permanent hermetic coverage.

var (
	satelleOnce sync.Once
	satelleBin  string
	satelleErr  error
)

// buildSatelle compiles the binary under test once per package run. The live
// classification path needs a real satelle; building it here keeps the suite
// hermetic instead of skipping when SATELLE_BIN is unset.
func buildSatelle(t *testing.T) string {
	t.Helper()
	satelleOnce.Do(func() {
		dir, err := os.MkdirTemp("", "plannerbench-bin-")
		if err != nil {
			satelleErr = err
			return
		}
		satelleBin = filepath.Join(dir, "satelle")
		cmd := exec.Command("go", "build", "-o", satelleBin, "./cmd/satelle")
		cmd.Dir = filepath.Join("..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			satelleErr = fmt.Errorf("build satelle: %v\n%s", err, out)
		}
	})
	if satelleErr != nil {
		t.Fatalf("%v", satelleErr)
	}
	return satelleBin
}

// writeStub writes an executable shell script and returns its path.
func writeStub(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// validArtifactEnvelope renders a claude-shaped `--output-format json` envelope
// whose result carries a canonical artifact. The plan body covers every
// criterion AND names real seams, so a successful stub run is a genuinely
// well-scoring sample rather than a validator echo.
func validArtifactEnvelope(t *testing.T, f fixture) string {
	t.Helper()
	var body strings.Builder
	body.WriteString("# Plan\n\n")
	for i := range f.Acceptance {
		ordinal := i + 1
		seam, _ := f.seamFor(ordinal)
		fmt.Fprintf(&body, "## AC%d\n\n", ordinal)
		fmt.Fprintf(&body, "Touch %s, changing %s.\n",
			strings.Join(seam.Files, " and "), strings.Join(seam.Symbols, ", "))
		if seam.TestHint != "" {
			fmt.Fprintf(&body, "Proven by a case in %s: %s.\n", firstTestFile(f), seam.TestHint)
		}
		body.WriteString("\n")
	}
	artifact, err := json.Marshal(map[string]any{
		"artifact": map[string]string{"name": "plan", "type": "plan", "body": body.String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Four-field usage shape (sty_8178f1c6); live classify only needs a valid
	// envelope — totals are not asserted here.
	envelope, err := json.Marshal(map[string]any{
		"type": "result", "result": string(artifact),
		"usage": map[string]int{
			"input_tokens":                120,
			"cache_creation_input_tokens": 800,
			"cache_read_input_tokens":     200,
			"output_tokens":               480,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(envelope)
}

func firstTestFile(f fixture) string {
	best := ""
	for path := range f.treeFiles {
		if strings.HasSuffix(path, "_test.go") && (best == "" || path < best) {
			best = path
		}
	}
	return best
}

// stubBinding builds a resolved study binding around a stub command. The grant
// mirrors the shipped one so the sample is not gratuitously divergent.
func stubBinding(id, command, shippedGrant, timeout string) studyBinding {
	return studyBinding{
		ID: id, Provider: "stub", Model: "stub-model", Effort: "high",
		Command: command, Timeout: timeout,
		grant: shippedGrant, policy: shippedPolicyName, effortClass: "high",
		topology: topologyIsolated, iface: "command",
	}
}

func liveEnv(t *testing.T, s study, f fixture) sampleEnv {
	t.Helper()
	shipped, err := loadShippedGrant(filepath.Join("..", "..", ".satelle", "agents.toml"))
	if err != nil {
		t.Fatal(err)
	}
	skill, err := os.ReadFile(filepath.Join("..", "..", ".satelle", "skills", "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	return sampleEnv{
		Bin: buildSatelle(t), PlannerSkill: string(skill), Study: s, Shipped: shipped,
		Settings: map[string]string{"suite": "classify-live"},
	}
}

func liveStudy() study {
	return study{
		ID: "classify-live", Seed: 1, Runs: 1, MinSamples: 3,
		ContextBuckets: []contextBucket{{Name: "small", MaxBytes: 0}},
	}
}

func liveDims(t *testing.T, s study, b studyBinding, f fixture) sampleDims {
	t.Helper()
	return b.dims(s, f.Name, f.contextBytes, 1, 1)
}

func TestLiveClassificationOfEverySupportedClass(t *testing.T) {
	if testing.Short() {
		t.Skip("live classification builds satelle and runs real transitions")
	}
	f := loadSmallFixture(t)
	s := liveStudy()
	env := liveEnv(t, s, f)
	envelope := validArtifactEnvelope(t, f)

	t.Run("none: a valid stub commits and is scored", func(t *testing.T) {
		root := t.TempDir()
		stubDir := filepath.Join(root, "stubs")
		payload := filepath.Join(stubDir, "reply.json")
		stub := writeStub(t, stubDir, "stub-valid", "cat > /dev/null\ncat "+payload+"\n")
		if err := os.WriteFile(payload, []byte(envelope), 0o644); err != nil {
			t.Fatal(err)
		}
		b := stubBinding("stub-valid", stub+" --system {system} --tools {tools} --model {model}", env.Shipped.Grant, "")
		record := runSample(root, env, b, f, liveDims(t, s, b, f))
		if record.Diagnostics.Class != classNone {
			t.Fatalf("class = %q (%s), want none. detail=%s",
				record.Diagnostics.Class, record.Diagnostics.Signal, record.Diagnostics.Detail)
		}
		if !record.TransitionOK || record.InfrastructureFailure {
			t.Fatalf("a valid stub must commit: ok=%v infra=%v", record.TransitionOK, record.InfrastructureFailure)
		}
		if !record.Attachment.OK || !record.Score.Scored {
			t.Fatalf("a committed run must attach and score: %+v", record.Score)
		}
		if record.Score.Deterministic.Covered == 0 {
			t.Fatalf("the seam-naming stub plan should score: %+v", record.Score.Deterministic)
		}
		if !record.Policy.ReadOnlyFaithful {
			t.Fatal("a read-only stub must leave the seeded tree untouched")
		}
		// Usage came from the real ledger, aggregated across real attempts.
		if !record.Usage.Available || record.Usage.AttemptsTotal < 1 {
			t.Fatalf("usage must be aggregated from the real ledger: %+v", record.Usage)
		}
		if record.Usage.TokensTotal == nil || *record.Usage.TokensTotal != 600 {
			t.Fatalf("tokens_total = %v, want the stub's reported 600", record.Usage.TokensTotal)
		}
	})

	t.Run("exit_status: a non-zero stub is an infrastructure failure", func(t *testing.T) {
		root := t.TempDir()
		stub := writeStub(t, filepath.Join(root, "stubs"), "stub-exit",
			"cat > /dev/null\necho 'stub refusing' >&2\nexit 7\n")
		b := stubBinding("stub-exit", stub+" --system {system} --tools {tools}", env.Shipped.Grant, "")
		record := runSample(root, env, b, f, liveDims(t, s, b, f))
		if record.Diagnostics.Class != classExitStatus {
			t.Fatalf("class = %q (%s), want exit_status", record.Diagnostics.Class, record.Diagnostics.Signal)
		}
		if !record.InfrastructureFailure {
			t.Fatal("a dead transport must invalidate the sample")
		}
		if record.Diagnostics.Outcome == "" {
			t.Fatalf("the engine's typed outcome must be recorded: %+v", record.Diagnostics)
		}
	})

	t.Run("signal_killed: a hanging command stub is killed at its binding timeout", func(t *testing.T) {
		root := t.TempDir()
		// exec so the stub IS the sleeping process: a plain `sleep` child would
		// outlive the deadline kill and hold the pipes open until it exited.
		stub := writeStub(t, filepath.Join(root, "stubs"), "stub-sleep", "cat > /dev/null\nexec sleep 30\n")
		b := stubBinding("stub-sleep", stub+" --system {system} --tools {tools}", env.Shipped.Grant, "3s")
		record := runSample(root, env, b, f, liveDims(t, s, b, f))
		if record.Diagnostics.Class != classSignalKilled && record.Diagnostics.Class != classTimeout {
			t.Fatalf("class = %q (%s), want a transport deadline class",
				record.Diagnostics.Class, record.Diagnostics.Signal)
		}
		if !record.InfrastructureFailure {
			t.Fatal("a deadline is an infrastructure failure")
		}
		if record.Diagnostics.Signal != "ledger agent-failure.outcome" {
			t.Fatalf("the class must come from the engine's typed outcome, got %q", record.Diagnostics.Signal)
		}
	})

	t.Run("timeout: a silent ACP stub trips the deadline as a typed timeout", func(t *testing.T) {
		root := t.TempDir()
		stub := writeStub(t, filepath.Join(root, "stubs"), "stub-acp-silent", "sleep 60\n")
		b := stubBinding("stub-acp", stub+" stdio", env.Shipped.Grant, "3s")
		b.iface = "acp"
		record := runSample(root, env, b, f, liveDims(t, s, b, f))
		if record.Diagnostics.Class != classTimeout && record.Diagnostics.Class != classSignalKilled {
			t.Fatalf("class = %q (%s), want timeout", record.Diagnostics.Class, record.Diagnostics.Signal)
		}
		if !record.InfrastructureFailure {
			t.Fatal("a timeout is an infrastructure failure")
		}
	})

	t.Run("malformed_output: prose exhausts the attempt policy as a QUALITY result", func(t *testing.T) {
		root := t.TempDir()
		stub := writeStub(t, filepath.Join(root, "stubs"), "stub-garbage",
			"cat > /dev/null\nprintf 'I have considered the request and will get back to you.\\n'\n")
		b := stubBinding("stub-garbage", stub+" --system {system} --tools {tools}", env.Shipped.Grant, "")
		record := runSample(root, env, b, f, liveDims(t, s, b, f))
		if record.Diagnostics.Class != classMalformedOutput {
			t.Fatalf("class = %q (%s), want malformed_output. detail=%s",
				record.Diagnostics.Class, record.Diagnostics.Signal, record.Diagnostics.Detail)
		}
		// The critical assertion: a model that answered badly is DATA, not an
		// infrastructure fault. The old substring classifier could route this to
		// an infrastructure exit on an incidental word match.
		if record.InfrastructureFailure {
			t.Fatal("a malformed answer is a quality result and must not invalidate the sample")
		}
		if record.Attempts < 2 {
			t.Fatalf("the attempt policy should have retried: attempts = %d", record.Attempts)
		}
		if !strings.Contains(record.Diagnostics.Signal, "repair/escalate") {
			t.Fatalf("the signal must show the VALIDATOR drove the retries: %q", record.Diagnostics.Signal)
		}
	})

	t.Run("denied_mutation: a writing stub is caught by the seeded-tree digest", func(t *testing.T) {
		root := t.TempDir()
		stubDir := filepath.Join(root, "stubs")
		payload := filepath.Join(stubDir, "reply.json")
		repo := filepath.Join(root, "repo")
		stub := writeStub(t, stubDir, "stub-writer",
			"cat > /dev/null\nprintf 'package sneak\\n' > "+
				filepath.Join(repo, "internal", "cli", "sneak.go")+"\ncat "+payload+"\n")
		if err := os.WriteFile(payload, []byte(envelope), 0o644); err != nil {
			t.Fatal(err)
		}
		b := stubBinding("stub-writer", stub+" --system {system} --tools {tools}", env.Shipped.Grant, "")
		record := runSample(root, env, b, f, liveDims(t, s, b, f))
		if record.Policy.ReadOnlyFaithful {
			t.Fatal("a stub that wrote into the seeded tree must fail the read-only check")
		}
		if record.Diagnostics.Class != classDeniedMutation {
			t.Fatalf("class = %q (%s), want denied_mutation",
				record.Diagnostics.Class, record.Diagnostics.Signal)
		}
		// A policy violation is a finding about a run that HAPPENED, so it stays
		// a comparable sample rather than being discarded as infrastructure.
		if record.InfrastructureFailure {
			t.Fatal("a policy finding is data, not an infrastructure fault")
		}
	})

	t.Run("spawn: an unresolvable binary is classified before any run", func(t *testing.T) {
		root := t.TempDir()
		b := stubBinding("stub-missing",
			"definitely-not-a-real-binary-4a71 --system {system}", env.Shipped.Grant, "")
		record := runSample(root, env, b, f, liveDims(t, s, b, f))
		if record.Diagnostics.Class != classSpawn || !record.InfrastructureFailure {
			t.Fatalf("class = %q infra=%v, want spawn", record.Diagnostics.Class, record.InfrastructureFailure)
		}
		if !strings.Contains(record.Diagnostics.Signal, "not on PATH") {
			t.Fatalf("the signal must be the filesystem fact: %q", record.Diagnostics.Signal)
		}
	})

	t.Run("setup: a broken harness binary is a setup class, not a model result", func(t *testing.T) {
		root := t.TempDir()
		broken := env
		broken.Bin = filepath.Join(t.TempDir(), "not-satelle")
		if err := os.WriteFile(broken.Bin, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		b := stubBinding("stub-valid", "sh -c true", env.Shipped.Grant, "")
		record := runSample(root, broken, b, f, liveDims(t, s, b, f))
		if record.Diagnostics.Class != classSetupFailure || !record.InfrastructureFailure {
			t.Fatalf("class = %q infra=%v, want setup", record.Diagnostics.Class, record.InfrastructureFailure)
		}
	})

	t.Run("attachment: a committed run with no recoverable body", func(t *testing.T) {
		root := t.TempDir()
		stub := writeStub(t, filepath.Join(root, "stubs"), "stub-prose",
			"cat > /dev/null\nprintf 'No artifact here, just prose.\\n'\n")
		b := stubBinding("stub-prose", stub+" --system {system} --tools {tools}", env.Shipped.Grant, "")
		// An OPTIONAL contract with no attempt policy: the engine accepts the
		// absent artifact and commits, so nothing is attached and no body can be
		// recovered. That is the attachment class, reached through the real path.
		optional := env
		optional.PlannerSkill = optionalContractSkill()
		record := runSample(root, optional, b, f, liveDims(t, s, b, f))
		if record.Diagnostics.Class != classAttachment {
			t.Fatalf("class = %q (%s), want attachment. transition_ok=%v",
				record.Diagnostics.Class, record.Diagnostics.Signal, record.TransitionOK)
		}
		if record.Score.Scored || record.Score.Unscorable == "" {
			t.Fatalf("with no body the sample must be unscorable with a reason: %+v", record.Score)
		}
	})
}

// optionalContractSkill is a plan skill whose artifact contract is OPTIONAL and
// which declares no attempt policy — the configuration under which a prose reply
// commits with nothing attached.
func optionalContractSkill() string {
	return `---
name: plan
scope: project
type: skill
tags: [type:skill]
output_name: plan
output_type: plan
output_required: false
description: Benchmark-suite plan skill with an OPTIONAL artifact contract, used to exercise the attachment class.
---

# Plan (optional contract)

Produce an implementation plan for the work item on stdin. Plan only.
`
}

func TestLiveClassificationNeverReadsProgramTextToDecideAClass(t *testing.T) {
	// The regression the story names: "auth" inside an ordinary word must not
	// route a quality result into an infrastructure exit. classifyRun takes no
	// program text at all, so the property holds by construction — this asserts
	// the construction.
	prose := "The author considered an authoritative approach; authentication was unauthorized."
	quality := runSignals{
		Entries: []ledgerEntry{
			attemptEntry(t, 1, 10, true, 1, 2, 3, false),
			repairAttemptEntry(t, 2, 10, false),
		},
		TransitionErr: fmt.Errorf("%s", prose),
		BodyRecovered: true,
	}
	diagnostic, infra := classifyRun(quality)
	if diagnostic.Class != classMalformedOutput || infra {
		t.Fatalf("prose containing auth/author must not decide the class: %+v infra=%v", diagnostic, infra)
	}
	if strings.Contains(diagnostic.Class, "auth") {
		t.Fatal("class was derived from the text")
	}
	// And a clean run whose OUTPUT mentions a timeout is still a clean run.
	clean := runSignals{
		Entries:       []ledgerEntry{attemptEntry(t, 1, 10, true, 1, 2, 3, true)},
		BodyRecovered: true,
	}
	if diagnostic, infra := classifyRun(clean); diagnostic.Class != classNone || infra {
		t.Fatalf("a committed run must classify as none: %+v", diagnostic)
	}
}

// repairAttemptEntry is an attempt event in the repair phase — the structural
// proof that the VALIDATOR, not the transport, drove a retry.
func repairAttemptEntry(t *testing.T, attempt int, durationMS int64, validatorOK bool) ledgerEntry {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"kind": "agent-attempt",
		"data": map[string]any{
			"attempt": attempt, "phase": "repair", "binding": "planner",
			"model": "opus", "effort": "medium", "duration_ms": durationMS,
			"usage_available": false, "validator_ok": validatorOK,
			"escalation_reason": "validation-failed",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ledgerEntry{
		Kind: "telemetry_event", Actor: "executor", Payload: payload,
		CreatedAt: time.Unix(int64(1700000000+attempt), 0).UTC(),
	}
}
