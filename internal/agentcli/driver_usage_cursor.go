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

// Cursor keeps no readable per-turn usage record: its agent-transcripts JSONL and
// chat store.db carry no token field (sty_a3258bb3 note cursor-usage-storage-evidence).
// The one source is the interactive `stop` hook payload (input_tokens, output_tokens,
// cache_read_tokens, cache_write_tokens, generation_id, model; testdata/cursor/
// 7-stop-hooks.log), and print mode emits no stop. So satelle records each stop
// itself, one line per generation_id, in a per-session JSONL file that the driver
// reader folds. A turn killed before its stop is never recorded, which is why the
// snapshot says it may undercount.

const (
	cursorDriverCostReason = "cursor: no per-token price is published for cursor models; usage is billed through the cursor account"
	// The stop payload marks a turn and carries no per-request count.
	cursorDriverModelCallsReason = "cursor: the stop payload reports per-turn usage and carries no model-call count"
	cursorNoStopReason           = "cursor: no stop-hook usage recorded for this session (cursor reports usage only on interactive stop events; print mode emits none)"
)

// cursorStopLine is one recorded stop: a turn's usage and the time satelle saw it.
type cursorStopLine struct {
	At         time.Time `json:"at"`
	Generation string    `json:"generation_id"`
	Model      string    `json:"model,omitempty"`
	LoopCount  int       `json:"loop_count"`
	Input      *int      `json:"input_tokens,omitempty"`
	Output     *int      `json:"output_tokens,omitempty"`
	CacheRead  *int      `json:"cache_read_tokens,omitempty"`
	CacheWrite *int      `json:"cache_write_tokens,omitempty"`
}

// cursorUsageDir is where recorded stops live: $SATELLE_CURSOR_USAGE_DIR (tests, an
// unusual install), else the user cache dir.
func cursorUsageDir() string {
	if v := strings.TrimSpace(os.Getenv("SATELLE_CURSOR_USAGE_DIR")); v != "" {
		return v
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "satelle", "cursor-usage")
	}
	return filepath.Join(os.TempDir(), "satelle-cursor-usage")
}

// cursorUsagePath is the session's record file; the id is reduced to a file-safe
// name so a hostile payload cannot name a path.
func cursorUsagePath(sessionID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, strings.TrimSpace(sessionID))
	return filepath.Join(cursorUsageDir(), strings.TrimLeft(safe, ".")+".jsonl")
}

// readCursorStops reads the session's recorded stops in file order, one per
// generation_id (a replayed stop keeps its first line). A missing file is no stops.
func readCursorStops(sessionID string) ([]cursorStopLine, error) {
	f, err := os.Open(cursorUsagePath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []cursorStopLine
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var l cursorStopLine
		if json.Unmarshal(line, &l) != nil || seen[l.Generation] {
			continue
		}
		seen[l.Generation] = true
		out = append(out, l)
	}
	return out, sc.Err()
}

// RecordCursorStop appends a cursor `stop` hook payload to sessionID's usage
// record, once per generation_id. A payload with no generation_id or no token
// fields records nothing (it is not a usage report). Callers fail open.
func RecordCursorStop(sessionID string, raw []byte) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	var ev struct {
		Event      string `json:"hook_event_name"`
		Generation string `json:"generation_id"`
		Model      string `json:"model"`
		LoopCount  int    `json:"loop_count"`
		Input      *int   `json:"input_tokens"`
		Output     *int   `json:"output_tokens"`
		CacheRead  *int   `json:"cache_read_tokens"`
		CacheWrite *int   `json:"cache_write_tokens"`
	}
	if json.Unmarshal(raw, &ev) != nil || ev.Generation == "" || ev.Input == nil || ev.Output == nil {
		return nil
	}
	if ev.Event != "" && ev.Event != "stop" {
		return nil
	}
	have, err := readCursorStops(sessionID)
	if err != nil {
		return err
	}
	for _, l := range have {
		if l.Generation == ev.Generation {
			return nil
		}
	}
	line, err := json.Marshal(cursorStopLine{
		At: time.Now().UTC(), Generation: ev.Generation, Model: ev.Model, LoopCount: ev.LoopCount,
		Input: ev.Input, Output: ev.Output, CacheRead: ev.CacheRead, CacheWrite: ev.CacheWrite,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cursorUsageDir(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(cursorUsagePath(sessionID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// cursorTurn is one stop folded to the driver's disjoint components. A stop whose
// cache split cannot be subtracted (cursorTokens withholds it) counts all of its
// input as fresh rather than a negative figure.
func cursorTurn(l cursorStopLine) (DriverTurn, bool) {
	u := cursorTokens(l.Input, l.Output, l.CacheRead, l.CacheWrite, "cursor stop")
	if !u.Available {
		return DriverTurn{}, false
	}
	t := DriverTurn{EndedAt: l.At, Output: u.OutputTokens}
	if u.CacheSplitAvailable {
		t.FreshInput, t.CacheRead, t.CacheWrite = u.FreshInputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens
	} else {
		t.FreshInput = u.InputTokens
	}
	return t, true
}

// cursorDriverSnapshot is the cumulative usage of the stops recorded for sessionID.
func cursorDriverSnapshot(sessionID string) DriverSnapshot {
	snap := DriverSnapshot{SessionID: sessionID, Executable: HarnessCursor}
	fail := func(reason string) DriverSnapshot {
		snap.UnavailableReason = reason
		snap.CostUnavailableReason = reason
		return snap
	}
	stops, err := readCursorStops(sessionID)
	if err != nil {
		return fail(fmt.Sprintf("cursor: recorded stop usage unreadable: %v", err))
	}
	for _, l := range stops {
		t, ok := cursorTurn(l)
		if !ok {
			continue
		}
		snap.FreshInputTokens += t.FreshInput
		snap.CacheReadInputTokens += t.CacheRead
		snap.CacheCreationInputTokens += t.CacheWrite
		snap.OutputTokens += t.Output
		snap.TurnBreakdown = append(snap.TurnBreakdown, t)
		if l.Model != "" {
			snap.Model = l.Model
		}
	}
	snap.Turns = len(snap.TurnBreakdown)
	if snap.Turns == 0 {
		return fail(cursorNoStopReason)
	}
	snap.Available = true
	snap.MayUndercountInFlightTurn = true // usage arrives only when a turn ends
	snap.CostUnavailableReason = cursorDriverCostReason
	snap.ModelCallsUnavailableReason = cursorDriverModelCallsReason
	return snap
}

// cursorWindowUsage attributes the recorded stops to [from, to] by each stop's own
// time (inclusive at both ends), derived from when satelle saw the stop rather than
// a live-measured delta.
func cursorWindowUsage(sessionID string, from, to time.Time) DriverWindowUsage {
	w := DriverWindowUsage{SessionID: sessionID, Executable: HarnessCursor}
	fail := func(reason string) DriverWindowUsage {
		w.UnavailableReason = reason
		w.CostUnavailableReason = reason
		return w
	}
	stops, err := readCursorStops(sessionID)
	if err != nil {
		return fail(fmt.Sprintf("cursor: recorded stop usage unreadable: %v", err))
	}
	if len(stops) == 0 {
		return fail(cursorNoStopReason)
	}
	n := 0
	for _, l := range stops {
		t, ok := cursorTurn(l)
		if !ok || l.At.Before(from) || l.At.After(to) {
			continue
		}
		w.FreshInputTokens += t.FreshInput
		w.CacheReadInputTokens += t.CacheRead
		w.CacheCreationInputTokens += t.CacheWrite
		w.OutputTokens += t.Output
		if l.Model != "" {
			w.Model = l.Model
		}
		n++
	}
	if n == 0 {
		return fail(fmt.Sprintf("cursor: no recorded stop usage between %s and %s", from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano)))
	}
	w.Available = true
	w.CostUnavailableReason = cursorDriverCostReason
	w.ModelCallsUnavailableReason = cursorDriverModelCallsReason
	return w
}
