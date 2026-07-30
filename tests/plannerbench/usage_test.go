//go:build plannerbench

package plannerbench

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// AC9: usage must be summed across EVERY attempt, and an unreported total must
// never serialise as a numeric zero.

// attemptEntry builds one real-shaped telemetry_event ledger row. The payload
// shape mirrors internal/agentstep.recordArtifactAttempt exactly, including its
// omission of the token keys when the transport reported no usage.
func attemptEntry(t *testing.T, attempt int, durationMS int64, available bool, in, out, total int, validatorOK bool) ledgerEntry {
	t.Helper()
	data := map[string]any{
		"attempt": attempt, "phase": "initial", "binding": "planner",
		"model": "opus", "effort": "high", "duration_ms": durationMS,
		"usage_available": available, "validator_ok": validatorOK,
	}
	if available {
		data["tokens_in"], data["tokens_out"], data["tokens_total"] = in, out, total
	}
	payload, err := json.Marshal(map[string]any{"kind": "agent-attempt", "data": data})
	if err != nil {
		t.Fatal(err)
	}
	return ledgerEntry{
		Kind: "telemetry_event", Actor: "executor", Payload: payload,
		CreatedAt: time.Unix(int64(1700000000+attempt), 0).UTC(),
	}
}

func TestUsageIsSummedAcrossEveryAttempt(t *testing.T) {
	entries := []ledgerEntry{
		attemptEntry(t, 1, 1000, true, 10, 100, 110, false),
		attemptEntry(t, 2, 2000, false, 0, 0, 0, false),
		attemptEntry(t, 3, 3000, true, 20, 200, 220, true),
	}
	usage := aggregateUsage(entries, "")
	if usage.AttemptsTotal != 3 || usage.AttemptsWithUsage != 2 {
		t.Fatalf("attempts = %d total / %d with usage", usage.AttemptsTotal, usage.AttemptsWithUsage)
	}
	if !usage.Available {
		t.Fatal("some attempt reported usage, so the run's usage is available")
	}
	// The schema-1 version read the FIRST match and would have reported 110.
	if usage.TokensTotal == nil || *usage.TokensTotal != 330 {
		t.Fatalf("tokens_total = %v, want the sum 330 across all attempts", usage.TokensTotal)
	}
	if *usage.TokensIn != 30 || *usage.TokensOut != 300 {
		t.Fatalf("in/out = %d/%d, want 30/300", *usage.TokensIn, *usage.TokensOut)
	}
	if usage.DurationMS != 6000 {
		t.Fatalf("duration = %d, want the sum 6000", usage.DurationMS)
	}
	if usage.Provenance != "ledger-agent-attempt-sum" {
		t.Fatalf("provenance = %q", usage.Provenance)
	}
}

func TestUnreportedUsageIsNeverANumericZero(t *testing.T) {
	entries := []ledgerEntry{
		attemptEntry(t, 1, 500, false, 0, 0, 0, false),
		attemptEntry(t, 2, 500, false, 0, 0, 0, true),
	}
	usage := aggregateUsage(entries, "")
	raw, err := json.Marshal(usage)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Available {
		t.Fatal("no attempt reported usage, so it must be unavailable")
	}
	for _, key := range []string{"tokens_total", "tokens_in", "tokens_out"} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("unavailable usage serialised %s: %s", key, raw)
		}
	}
	if usage.Provenance != "transport-unreported" {
		t.Fatalf("provenance = %q", usage.Provenance)
	}
}

func TestAGenuinelyReportedZeroStaysAvailable(t *testing.T) {
	usage := aggregateUsage([]ledgerEntry{attemptEntry(t, 1, 10, true, 0, 0, 0, true)}, "")
	if !usage.Available || usage.TokensTotal == nil || *usage.TokensTotal != 0 {
		t.Fatalf("a reported zero must stay available with a zero total: %+v", usage)
	}
}

func TestStoryCostNeverFlipsAvailability(t *testing.T) {
	// The schema-1 version read `TOTAL 0` as available usage. The cost row is a
	// cross-check with its own provenance and cannot decide availability.
	usage := aggregateUsage([]ledgerEntry{attemptEntry(t, 1, 10, false, 0, 0, 0, true)}, "TOTAL 0 tokens\n")
	if usage.Available {
		t.Fatalf("a TOTAL 0 cost row must not make usage available: %+v", usage)
	}
	if usage.CostTotal == nil || *usage.CostTotal != 0 || usage.CostProvenance != "story-cost-total" {
		t.Fatalf("the cost cross-check must still be recorded: %+v", usage)
	}
	reported := aggregateUsage([]ledgerEntry{attemptEntry(t, 1, 10, true, 1, 2, 3, true)}, "TOTAL 999 tokens\n")
	if *reported.TokensTotal != 3 || *reported.CostTotal != 999 {
		t.Fatalf("attempt aggregation and the cost cross-check must stay separate: %+v", reported)
	}
}

func TestNoAttemptEventsIsDistinctFromUnreported(t *testing.T) {
	usage := aggregateUsage(nil, "")
	if usage.Available || usage.AttemptsTotal != 0 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.Provenance != "no-attempt-events-recorded" {
		t.Fatalf("provenance = %q; a run with no attempt events is a different fact from an unreporting transport",
			usage.Provenance)
	}
}

func TestLedgerDecodeErrorIsSurfacedNotSwallowed(t *testing.T) {
	if _, err := parseLedger("not json"); err == nil {
		t.Fatal("an undecodable ledger must be an error: silently reading zero events would report a real failure as class none")
	}
	entries, err := parseLedger("")
	if err != nil || entries != nil {
		t.Fatalf("an empty ledger is not an error: %v %v", entries, err)
	}
}
