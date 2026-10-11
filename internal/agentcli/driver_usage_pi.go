package agentcli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	rec, reason := readPiSession(sessionID, repoRoot)
	if reason != "" {
		return fail(reason)
	}
	var cost float64
	for _, r := range rec.rows {
		snap.FreshInputTokens += r.input
		snap.OutputTokens += r.output
		snap.CacheReadInputTokens += r.cacheRead
		snap.CacheCreationInputTokens += r.cacheWrite
		snap.Turns++
		cost += r.cost
	}
	if snap.Turns == 0 {
		return fail("pi: session record carries no assistant usage yet")
	}
	snap.Available = true
	snap.MayUndercountInFlightTurn = false // pi_inflight_probe.result.md: the calling row precedes the tool run
	snap.Model = rec.lastModel
	snap.ModelCalls = snap.Turns
	if cost > 0 {
		snap.CostUSD = &cost
	} else {
		snap.CostUnavailableReason = piUnpricedCostReason
	}
	return snap
}

// piUnpricedCostReason is the one reason both pi readers (cumulative and windowed)
// give for a zero usage.cost.total beside real token counts.
const piUnpricedCostReason = "pi: usage.cost.total is zero beside real token counts (model unpriced by pi)"

// piUsageRow is one assistant message that made a model call: its own usage and
// the time pi wrote it.
type piUsageRow struct {
	at                                   time.Time // zero when the row carries no parseable timestamp
	model                                string
	input, output, cacheRead, cacheWrite int
	cost                                 float64
}

// piSessionRecord is every usage-bearing assistant row of one pi session, in file
// order, plus the model the session last named.
type piSessionRecord struct {
	rows      []piUsageRow
	lastModel string
	// created is when pi created the session: the {"type":"session"} header's
	// timestamp, else the file name's UTC prefix. Zero when neither parses.
	created time.Time
}

// piFileNameTime parses the UTC prefix of a pi session file name
// (<2006-01-02T15-04-05-000Z>_<sessionId>.jsonl).
func piFileNameTime(path string) time.Time {
	prefix, _, ok := strings.Cut(filepath.Base(path), "_")
	if !ok {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02T15-04-05-000Z", prefix)
	if err != nil {
		return time.Time{}
	}
	return t
}

// readPiSession is the one pi session-record iterator: the cumulative reader and
// the windowed reader both fold its rows, so the two cannot drift apart. A non-empty
// reason says why the record could not be read, pi-named.
func readPiSession(sessionID, repoRoot string) (piSessionRecord, string) {
	var rec piSessionRecord
	dir := filepath.Join(piHomeDir(), "sessions", piSessionDirName(repoRoot))
	matches, _ := filepath.Glob(filepath.Join(dir, "*_"+sessionID+".jsonl"))
	if len(matches) == 0 {
		return rec, fmt.Sprintf("pi: session record for %s not found under %s", sessionID, dir)
	}
	f, err := os.Open(matches[len(matches)-1])
	if err != nil {
		return rec, fmt.Sprintf("pi: session record unreadable: %v", err)
	}
	defer f.Close()
	rec.created = piFileNameTime(matches[len(matches)-1])
	headerSeen := false

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row struct {
			Type      string `json:"type"`
			ModelID   string `json:"modelId"`
			Timestamp string `json:"timestamp"`
			Message   struct {
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
		if row.Type == "session" && !headerSeen {
			headerSeen = true
			if t, err := time.Parse(time.RFC3339Nano, row.Timestamp); err == nil {
				rec.created = t
			}
		}
		if row.Type == "model_change" && row.ModelID != "" {
			rec.lastModel = row.ModelID
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
		r := piUsageRow{input: *u.Input, output: *u.Output, cacheRead: u.CacheRead, cacheWrite: u.CacheWrite}
		if u.Cost != nil && u.Cost.Total != nil {
			r.cost = *u.Cost.Total
		}
		if row.Message.Model != "" {
			rec.lastModel = row.Message.Model
		}
		r.model = rec.lastModel
		if t, err := time.Parse(time.RFC3339Nano, row.Timestamp); err == nil {
			r.at = t
		}
		rec.rows = append(rec.rows, r)
	}
	if err := scanner.Err(); err != nil {
		return piSessionRecord{}, fmt.Sprintf("pi: session record unreadable: %v", err)
	}
	return rec, ""
}

// piWindowRows is the rows of rec timestamped inside [from, to] (inclusive at
// both ends) and how many rows carry no parseable timestamp, so cannot be placed.
// It is the one window filter: piWindowUsage and the per-run reader both fold it.
func piWindowRows(rec piSessionRecord, from, to time.Time) (rows []piUsageRow, untimed int) {
	for _, r := range rec.rows {
		switch {
		case r.at.IsZero():
			untimed++
		case r.at.Before(from) || r.at.After(to):
		default:
			rows = append(rows, r)
		}
	}
	return rows, untimed
}

// piWindowUsage sums the assistant rows pi wrote inside [from, to] (inclusive at
// both ends). The attribution is by each row's own timestamp, so the figure is
// derived from timestamps, not a live-measured delta. A window with no usage in it,
// or a record whose rows cannot all be placed in time, is a named unavailable —
// never a zero.
func piWindowUsage(sessionID, repoRoot string, from, to time.Time) DriverWindowUsage {
	w := DriverWindowUsage{SessionID: sessionID, Executable: HarnessPi}
	fail := func(reason string) DriverWindowUsage {
		w.UnavailableReason = reason
		w.CostUnavailableReason = reason
		return w
	}
	rec, reason := readPiSession(sessionID, repoRoot)
	if reason != "" {
		return fail(reason)
	}
	rows, untimed := piWindowRows(rec, from, to)
	var cost float64
	for _, r := range rows {
		w.FreshInputTokens += r.input
		w.OutputTokens += r.output
		w.CacheReadInputTokens += r.cacheRead
		w.CacheCreationInputTokens += r.cacheWrite
		w.ModelCalls++
		cost += r.cost
		w.Model = r.model
	}
	if untimed > 0 {
		return fail(fmt.Sprintf("pi: %d assistant rows in the session record carry no parseable timestamp; the window cannot be attributed", untimed))
	}
	if w.ModelCalls == 0 {
		return fail(fmt.Sprintf("pi: session record has no assistant usage between %s and %s", from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano)))
	}
	w.Available = true
	if cost > 0 {
		w.CostUSD = &cost
	} else {
		w.CostUnavailableReason = piUnpricedCostReason
	}
	return w
}
