//go:build plannerbench

package plannerbench

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Classification reads STRUCTURED signals only: the engine's typed
// agent-failure outcome, the attempt event's validator flag, the process exit
// status, and the harness's own filesystem/digest facts. The previous
// classifier scanned combined output with strings.Contains, where a match on
// "auth" inside "author" routed a quality failure into an infrastructure exit.
// No function here reads program text to decide a class; free text survives
// only in diagnostics.detail, for humans.

// Failure classes. infrastructureClasses names the ones that invalidate a
// sample; the others are real benchmark data.
const (
	classNone            = "none"
	classSpawn           = "spawn"
	classTimeout         = "timeout"
	classSignalKilled    = "signal_killed"
	classExitStatus      = "exit_status"
	classMalformedOutput = "malformed_output"
	classAttachment      = "attachment"
	classDeniedMutation  = "denied_mutation"
	classSetupFailure    = "setup"
)

// infrastructureClasses invalidate the sample: the transport, not the model,
// decided the outcome. malformed_output and denied_mutation are deliberately
// NOT here — they are quality and policy findings the study wants to count.
var infrastructureClasses = map[string]bool{
	classSpawn: true, classTimeout: true, classSignalKilled: true,
	classExitStatus: true, classAttachment: true, classSetupFailure: true,
}

// ledgerEntry is one `satelle ledger list --story <id>` row. The ledger is a
// JSON feed (internal/verb/ledger.go), so this is a decode, not a regex.
type ledgerEntry struct {
	ID        string          `json:"id"`
	StoryID   string          `json:"story_id"`
	Kind      string          `json:"kind"`
	Actor     string          `json:"actor"`
	Body      string          `json:"body"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// telemetryPayload wraps an engine telemetry event: kind names the event
// (agent-attempt / agent-failure) and data carries its typed fields.
type telemetryPayload struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// attemptEvent is the engine's per-attempt telemetry. Token fields are POINTERS:
// the engine omits them when the transport reported no usage, and an omitted
// count must never read as a numeric zero (AC9).
type attemptEvent struct {
	Attempt          int    `json:"attempt"`
	Phase            string `json:"phase"`
	Binding          string `json:"binding"`
	Model            string `json:"model"`
	Effort           string `json:"effort"`
	DurationMS       int64  `json:"duration_ms"`
	UsageAvailable   bool   `json:"usage_available"`
	TokensIn         *int   `json:"tokens_in"`
	TokensOut        *int   `json:"tokens_out"`
	TokensTotal      *int   `json:"tokens_total"`
	ValidatorOK      *bool  `json:"validator_ok"`
	EscalationReason string `json:"escalation_reason"`
}

// failureEvent is the engine's typed dispatch failure. Outcome comes from
// agentstep.classifyOutcome — a typed value, not a text scan.
type failureEvent struct {
	Agent       string `json:"agent"`
	Step        string `json:"step"`
	Outcome     string `json:"outcome"`
	TokensTotal *int   `json:"tokens_total"`
	DurationMS  int64  `json:"duration_ms"`
}

// parseLedger decodes the ledger feed. A decode error is returned, never
// swallowed: classification that silently falls back to no events would report
// a real failure as class none.
func parseLedger(raw string) ([]ledgerEntry, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var entries []ledgerEntry
	if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
		return nil, fmt.Errorf("decode ledger feed: %w", err)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].CreatedAt.Before(entries[j].CreatedAt)
	})
	return entries, nil
}

// attemptEvents returns every recorded agent attempt, in order.
func attemptEvents(entries []ledgerEntry) []attemptEvent {
	var events []attemptEvent
	for _, entry := range entries {
		payload, ok := telemetryOf(entry, "agent-attempt")
		if !ok {
			continue
		}
		var ev attemptEvent
		if err := json.Unmarshal(payload, &ev); err == nil {
			events = append(events, ev)
		}
	}
	return events
}

// failureEvents returns every recorded dispatch failure, in order.
func failureEvents(entries []ledgerEntry) []failureEvent {
	var events []failureEvent
	for _, entry := range entries {
		payload, ok := telemetryOf(entry, "agent-failure")
		if !ok {
			continue
		}
		var ev failureEvent
		if err := json.Unmarshal(payload, &ev); err == nil {
			events = append(events, ev)
		}
	}
	return events
}

func telemetryOf(entry ledgerEntry, kind string) (json.RawMessage, bool) {
	if entry.Kind != "telemetry_event" {
		return nil, false
	}
	var payload telemetryPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil || payload.Kind != kind {
		return nil, false
	}
	return payload.Data, true
}

// runSignals are the structured facts one sample produced. Every field is a
// typed value or a filesystem/ledger fact — none is program text.
type runSignals struct {
	// SpawnUnresolvable is set when the binding's binary did not resolve on
	// PATH. Resolution is a filesystem fact known BEFORE the run.
	SpawnUnresolvable bool
	// SetupError is a harness-side failure (init, story create, digest).
	SetupError error
	// TransitionErr is the error from `satelle story set`, kept only so its exit
	// code can be read; its text is never classified.
	TransitionErr error
	Entries       []ledgerEntry
	LedgerErr     error
	// BodyRecovered reports whether ANY plan body was obtained — the attached
	// document or the executor-log fallback. A refused run with a recovered body
	// is still a scored sample.
	BodyRecovered bool
	DigestChanged bool
}

type diagnosticEvidence struct {
	Class         string `json:"class"`
	Detail        string `json:"detail,omitempty"`
	Signal        string `json:"signal"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	Outcome       string `json:"typed_outcome,omitempty"`
	LedgerExcerpt string `json:"ledger_excerpt,omitempty"`
}

// classifyRun names the failure class from structured signals, in a fixed
// precedence order, and reports whether the class invalidates the sample.
func classifyRun(sig runSignals) (diagnosticEvidence, bool) {
	switch {
	case sig.SpawnUnresolvable:
		return diagnosticEvidence{
			Class: classSpawn, Signal: "exec.LookPath: binding binary unresolvable",
		}, true
	case sig.SetupError != nil:
		return diagnosticEvidence{
			Class: classSetupFailure, Signal: "harness setup error", Detail: sig.SetupError.Error(),
		}, true
	case sig.LedgerErr != nil:
		return diagnosticEvidence{
			Class: classSetupFailure, Signal: "ledger feed undecodable", Detail: sig.LedgerErr.Error(),
		}, true
	}
	failures := failureEvents(sig.Entries)
	// 1. A TRANSPORT-decided outcome outranks everything: the run never got to
	//    produce a judgeable result, so nothing else about it is informative.
	if len(failures) > 0 {
		last := failures[len(failures)-1]
		var class string
		switch last.Outcome {
		case "timeout":
			class = classTimeout
		case "signal:killed":
			class = classSignalKilled
		}
		if class != "" {
			return diagnosticEvidence{
				Class: class, Signal: "ledger agent-failure.outcome", Outcome: last.Outcome,
				ExitCode: exitCodeOf(sig.TransitionErr),
			}, true
		}
	}
	// 2. The final attempt's validator verdict. This outranks the engine's
	//    GENERIC "error" outcome because it is the more specific signal: an
	//    exhausted attempt policy reports itself as a plain error, and reading
	//    that as a transport fault would discard a real quality result.
	if attempts := attemptEvents(sig.Entries); len(attempts) > 0 {
		last := attempts[len(attempts)-1]
		if last.ValidatorOK != nil && !*last.ValidatorOK &&
			(len(failures) == 0 || validatorDrivenRetry(attempts)) {
			return diagnosticEvidence{
				Class: classMalformedOutput, Signal: validatorSignal(attempts),
				ExitCode: exitCodeOf(sig.TransitionErr),
			}, false
		}
	}
	// 3. Any other typed failure outcome.
	if len(failures) > 0 {
		last := failures[len(failures)-1]
		return diagnosticEvidence{
			Class: classExitStatus, Signal: "ledger agent-failure.outcome", Outcome: last.Outcome,
			ExitCode: exitCodeOf(sig.TransitionErr),
		}, true
	}
	// 4. A non-zero exit with no engine event of its own.
	if code := exitCodeOf(sig.TransitionErr); code != nil {
		return diagnosticEvidence{
			Class: classExitStatus, Signal: "process exit status", ExitCode: code,
		}, true
	}
	if sig.TransitionErr != nil {
		return diagnosticEvidence{
			Class: classExitStatus, Signal: "transition error without exit status",
			Detail: sig.TransitionErr.Error(),
		}, true
	}
	// 5. No body to score at all.
	if !sig.BodyRecovered {
		return diagnosticEvidence{
			Class: classAttachment, Signal: "no artifact body from document or executor log",
		}, true
	}
	// 6. The read-only policy was violated — real data, not an infrastructure
	//    fault: the run happened and its policy outcome is the finding.
	if sig.DigestChanged {
		return diagnosticEvidence{
			Class: classDeniedMutation, Signal: "product tree digest changed across the run",
		}, false
	}
	return diagnosticEvidence{Class: classNone, Signal: "transition committed"}, false
}

// validatorDrivenRetry reports whether the VALIDATOR — not the transport — drove
// the retries. The engine records validator_ok=false for a run error as well as
// for a validation finding (internal/agentstep.recordArtifactAttempt passes the
// run error as its findings list), so validator_ok alone cannot separate a
// malformed answer from a dead transport. A repair or escalate PHASE can: the
// attempt loop returns immediately on a run error and never enters one, so a
// recorded repair/escalate attempt proves the answer arrived and failed
// validation.
//
// Boundary: a skill declaring zero repair and zero escalate budget produces one
// attempt in both cases, and the two are then indistinguishable from telemetry
// alone — such a run is classified by its typed failure outcome. This harness
// does not read program text to close that gap; it reports the outcome the
// engine typed.
func validatorDrivenRetry(attempts []attemptEvent) bool {
	for _, a := range attempts {
		switch a.Phase {
		case "repair", "escalate":
			return true
		}
	}
	return false
}

func validatorSignal(attempts []attemptEvent) string {
	if validatorDrivenRetry(attempts) {
		return fmt.Sprintf(
			"ledger agent-attempt.validator_ok=false across %d attempts including a validator-driven repair/escalate phase",
			len(attempts))
	}
	return "ledger agent-attempt.validator_ok=false with no transport failure recorded"
}

// exitCodeOf reads the process exit status as a typed value. It never inspects
// the error's message.
func exitCodeOf(err error) *int {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return nil
	}
	code := exitErr.ExitCode()
	return &code
}
