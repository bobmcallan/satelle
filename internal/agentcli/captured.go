package agentcli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Captured behavior and the version it was captured against (sty_32c61e68).
//
// Some of a harness adapter's behavior was MEASURED against a real CLI rather
// than read from its documentation: the Stop cap, the shell timeout handling, the
// result envelope shapes, the ACP methods, the usage fields. Each measurement is
// only true of the build it was taken on, so this table is the one owner of that
// build and of the contracts that rest on it. The fixtures' own metadata is
// checked against it (cursor_fixtures_test.go), and every drift report — the
// session-start line, `satelle doctor --live`, `satelle agent recheck` — names
// the same contracts from here. A harness with no entry has no recorded capture,
// which callers report by name rather than treating as a match.

// The captured contract names. They are what a drift report lists and what
// `satelle agent recheck` reports a status for.
const (
	ContractStopCap     = "stop cap"
	ContractHookTimeout = "hook timeout handling"
	ContractEnvelopes   = "envelope shapes"
	ContractACPMethods  = "ACP methods"
	ContractUsageFields = "usage fields"
)

// capturedCursorVersion is the cursor-agent build every testdata/cursor fixture
// was captured on. It is the only place the string is written.
const capturedCursorVersion = "2026.10.01-e373342"

// CapturedContract is one measured behavior, with the evidence it rests on.
type CapturedContract struct {
	// Name is one of the Contract* names.
	Name string
	// Basis says what was measured, in a sentence.
	Basis string
	// Fixture names the capture to re-record when the contract has to be
	// re-verified (paths under internal/agentcli).
	Fixture string
}

// CapturedBasis is what one harness's measured behavior was captured against.
type CapturedBasis struct {
	Version   string
	Contracts []CapturedContract
}

// CapturedVersions records, per harness, the CLI build its measured behavior was
// captured against. A harness absent from the map has no recorded capture.
var CapturedVersions = map[string]CapturedBasis{
	HarnessCursor: {
		Version: capturedCursorVersion,
		Contracts: []CapturedContract{
			{
				Name:    ContractStopCap,
				Basis:   "probe 20-cap: with loop_limit unset, stop fired at loop_count 0-4; a followup at 0-3 produced another turn, the followup at 4 none",
				Fixture: "testdata/cursor/20-cap-hooks.log",
			},
			{
				Name:    ContractHookTimeout,
				Basis:   "every Shell tool call carries timeout=30000 with timeoutBehavior=TIMEOUT_BEHAVIOR_BACKGROUND",
				Fixture: "testdata/cursor/3-abs-hooks.log",
			},
			{
				Name:    ContractEnvelopes,
				Basis:   "--output-format json prints one result envelope with result, session_id and camelCase usage",
				Fixture: "testdata/cursor/1a-json.out",
			},
			{
				Name:    ContractACPMethods,
				Basis:   "cursor-agent acp answers initialize, session/new and session/prompt over stdio and streams session/update",
				Fixture: "testdata/cursor/5-acp.json",
			},
			{
				Name:    ContractUsageFields,
				Basis:   "inputTokens, outputTokens, cacheReadTokens and cacheWriteTokens, read exclusively (cache counts added to the fresh input)",
				Fixture: "testdata/cursor/1a-json.out",
			},
		},
	},
}

// CapturedFor returns harness's capture record. ok is false for a harness with
// none.
func CapturedFor(harness string) (CapturedBasis, bool) {
	b, ok := CapturedVersions[strings.TrimSpace(harness)]
	if !ok || b.Version == "" {
		return CapturedBasis{}, false
	}
	return b, true
}

// CapturedVersion returns the build harness's measured behavior was captured
// against. ok is false when none is recorded.
func CapturedVersion(harness string) (string, bool) {
	b, ok := CapturedFor(harness)
	return b.Version, ok
}

// capturedBasis returns the Basis of one of harness's captured contracts, or ""
// when there is none. Tables that cite a measurement read it from here so the
// prose has one owner.
func capturedBasis(harness, contract string) string {
	b, _ := CapturedFor(harness)
	for _, c := range b.Contracts {
		if c.Name == contract {
			return c.Basis
		}
	}
	return ""
}

// PayloadVersion reads the CLI build a hook payload reports for itself. Only
// cursor's envelope carries one (cursor_version); every other payload answers "".
func PayloadVersion(raw []byte) string {
	var top struct {
		CursorVersion string `json:"cursor_version"`
	}
	if json.Unmarshal(raw, &top) != nil {
		return ""
	}
	return strings.TrimSpace(top.CursorVersion)
}

// NormalizeVersion reduces a CLI's `--version` output to the version string: its
// first non-empty line, trimmed. "" means the output named none.
func NormalizeVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			return l
		}
	}
	return ""
}

// RecheckCommand is the command that re-checks harness's captured contracts
// against the installed CLI.
func RecheckCommand(harness string) string {
	return "satelle agent recheck " + harness
}

// DriftContracts renders the contracts harness's capture covers, each with its
// basis, as one line: the contracts that may no longer hold on a different build.
func DriftContracts(harness string) string {
	b, _ := CapturedFor(harness)
	parts := make([]string, 0, len(b.Contracts))
	for _, c := range b.Contracts {
		parts = append(parts, fmt.Sprintf("%s (%s)", c.Name, c.Basis))
	}
	return strings.Join(parts, "; ")
}

// DriftSummary is the shared sentence of every drift report: the installed build,
// the captured one, the contracts that may no longer hold and the way to re-check
// them. installed is already known to differ from the captured version.
func DriftSummary(harness, installed string) string {
	captured, _ := CapturedVersion(harness)
	return fmt.Sprintf("installed %s %s differs from %s, the build satelle's measured behavior was captured against. Captured contracts that may no longer hold: %s. Re-check them against the installed CLI: %s",
		CLIName(harness), installed, captured, DriftContracts(harness), RecheckCommand(harness))
}

// CLIName is the executable a harness's CLI is installed as.
func CLIName(harness string) string {
	if harness == HarnessCursor {
		return "cursor-agent"
	}
	return harness
}

// SessionDriftLine is the session-start advisory for a hook payload: the line
// naming both versions and the contracts at risk when the session's CLI reports a
// build other than the captured one, and "" when the harness has no capture, the
// payload names no version, or the versions match. It reads the payload only.
func SessionDriftLine(harness string, raw []byte) string {
	captured, ok := CapturedVersion(harness)
	if !ok {
		return ""
	}
	installed := PayloadVersion(raw)
	if installed == "" || installed == captured {
		return ""
	}
	return "⚠️ satelle: " + DriftSummary(harness, installed) + "."
}

// RecheckHelpFlags are the flags harness's command templates rely on, which its
// `--help` must still list for the envelope contract to hold: cursor dispatch is
// `cursor-agent -p --trust --output-format json --model …`, with `--mode ask` for
// a read-only seat (cursor_seat.go). nil for a harness with no capture.
func RecheckHelpFlags(harness string) []string {
	if harness == HarnessCursor {
		return []string{"--print", "--output-format", "--mode", "--trust", "--model"}
	}
	return nil
}
