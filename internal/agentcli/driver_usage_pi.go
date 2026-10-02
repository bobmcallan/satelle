package agentcli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// piSessionDirName is pi's per-repo session directory name under
// ~/.pi/agent/sessions/: the absolute cwd with its leading separator removed and
// every "/", "\" and ":" turned into "-", wrapped in "--…--" (pi's
// docs/session-format.md; confirmed against the real directory
// ~/.pi/agent/sessions/--home-bobmcallan-Development-satelle--/, sty_ca1ca935).
func piSessionDirName(repoRoot string) string {
	p := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(repoRoot)), "/")
	return "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(p) + "--"
}

// piDriverSnapshot reads a pi session record,
// ~/.pi/agent/sessions/<dir>/<UTC timestamp>_<sessionId>.jsonl
// (PI_CODING_AGENT_DIR relocates the agent dir). It is JSONL: a
// {"type":"session","id","cwd"} header, then {"type":"model_change","modelId"}
// and {"type":"message","message":{...}} rows. An assistant message row carries
// that request's own usage, written once when the message completes (there are
// no streamed partials to dedupe):
//
//	"usage":{"input","output","cacheRead","cacheWrite","reasoning","totalTokens",
//	         "cost":{"input","output","cacheRead","cacheWrite","total"}}
//
// The split is disjoint — input is the fresh, uncached share (totalTokens =
// input+output+cacheRead+cacheWrite, confirmed against a real capture) — so it
// maps straight onto the snapshot fields. One assistant row is one model call.
//
// The dollar figure is pi's own (usage.cost.total), never a price-table
// derivation (sty_c4df7376). A model pi holds no price for records cost 0 beside
// real token counts (the capture's openrouter "stealth/…" model does), and that
// zero means "unpriced", not "free": it is reported as an unavailable cost, never
// as a measured $0.
//
// Pi persists an assistant row when the message completes — before the tool call
// it requested runs — so the row carrying a closing `satelle story set` call is
// already in the file when this reads it, and MayUndercountInFlightTurn stays
// false. Measured, not inferred: testdata/driver/pi_inflight_probe.{jsonl,log,
// result.md} is a timestamped probe on a real pi session in which all three
// tool calls found their own calling row already in the file.
func piDriverSnapshot(sessionID, repoRoot string) DriverSnapshot {
	snap := DriverSnapshot{SessionID: sessionID, Executable: HarnessPi}
	fail := func(reason string) DriverSnapshot {
		snap.UnavailableReason = reason
		snap.CostUnavailableReason = reason
		return snap
	}
	dir := filepath.Join(piHomeDir(), "sessions", piSessionDirName(repoRoot))
	matches, _ := filepath.Glob(filepath.Join(dir, "*_"+sessionID+".jsonl"))
	if len(matches) == 0 {
		return fail(fmt.Sprintf("pi: session record for %s not found under %s", sessionID, dir))
	}
	f, err := os.Open(matches[len(matches)-1])
	if err != nil {
		return fail(fmt.Sprintf("pi: session record unreadable: %v", err))
	}
	defer f.Close()

	var lastModel string
	var cost float64
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row struct {
			Type    string `json:"type"`
			ModelID string `json:"modelId"`
			Message struct {
				Role  string `json:"role"`
				Model string `json:"model"`
				Usage *struct {
					Input      *int `json:"input"`
					Output     *int `json:"output"`
					CacheRead  int  `json:"cacheRead"`
					CacheWrite int  `json:"cacheWrite"`
					Cost       *struct {
						Total *float64 `json:"total"`
					} `json:"cost"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		if row.Type == "model_change" && row.ModelID != "" {
			lastModel = row.ModelID
		}
		u := row.Message.Usage
		if row.Type != "message" || row.Message.Role != "assistant" || u == nil || u.Input == nil || u.Output == nil {
			continue
		}
		if *u.Input+*u.Output+u.CacheRead+u.CacheWrite == 0 {
			// pi records a failed request as an assistant row with all-zero usage: it
			// made no measured call.
			continue
		}
		snap.FreshInputTokens += *u.Input
		snap.OutputTokens += *u.Output
		snap.CacheReadInputTokens += u.CacheRead
		snap.CacheCreationInputTokens += u.CacheWrite
		snap.Turns++
		if u.Cost != nil && u.Cost.Total != nil {
			cost += *u.Cost.Total
		}
		if row.Message.Model != "" {
			lastModel = row.Message.Model
		}
	}
	if err := scanner.Err(); err != nil {
		return fail(fmt.Sprintf("pi: session record unreadable: %v", err))
	}
	if snap.Turns == 0 {
		return fail("pi: session record carries no assistant usage yet")
	}
	snap.Available = true
	snap.MayUndercountInFlightTurn = false // pi_inflight_probe.result.md: the calling row precedes the tool run
	snap.Model = lastModel
	snap.ModelCalls = snap.Turns
	if cost > 0 {
		snap.CostUSD = &cost
	} else {
		snap.CostUnavailableReason = "pi: usage.cost.total is zero beside real token counts (model unpriced by pi)"
	}
	return snap
}
