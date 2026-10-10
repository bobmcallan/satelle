package agentcli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cursorStopPayloads returns the stop hook payloads of the captured interactive
// session (testdata/cursor/7-stop-hooks.log: event<TAB>json lines). The capture
// truncated each line at 600 bytes, inside transcript_path, which comes after every
// usage field; the object is closed there so the captured values parse unchanged.
func cursorStopPayloads(t *testing.T) [][]byte {
	t.Helper()
	var out [][]byte
	for _, line := range strings.Split(cursorRead(t, "7-stop-hooks.log"), "\n") {
		event, body, ok := strings.Cut(line, "\t")
		if !ok || event != "stop" {
			continue
		}
		if i := strings.Index(body, `,"transcript_path"`); i >= 0 {
			body = body[:i] + "}"
		}
		out = append(out, []byte(body))
	}
	if len(out) != 2 {
		t.Fatalf("captured stop payloads = %d, want 2", len(out))
	}
	return out
}

// AC1: the captured json envelope reads EXCLUSIVELY: inputTokens is the fresh input
// and the cache counts are added to make the total.
func TestUnwrapUsage_Cursor1a(t *testing.T) {
	text, u := UnwrapUsage([]byte(cursorRead(t, "1a-json.out")))
	if string(text) != "PONG" {
		t.Errorf("text = %q, want PONG", text)
	}
	if !u.Available || !u.CacheSplitAvailable {
		t.Fatalf("usage = %+v, want Available with the cache split", u)
	}
	if u.InputTokens != 12678 || u.CacheReadInputTokens != 3864 || u.CacheCreationInputTokens != 0 ||
		u.FreshInputTokens != 8814 || u.OutputTokens != 32 {
		t.Errorf("tokens = in %d read %d write %d fresh %d out %d, want 12678/3864/0/8814/32",
			u.InputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens, u.FreshInputTokens, u.OutputTokens)
	}
	if u.TotalTokens != 12710 {
		t.Errorf("TotalTokens = %d, want input + output", u.TotalTokens)
	}
}

// AC1/AC2: the two resumed print turns of one session. The warm turn's cache read
// (12664) exceeds its inputTokens (74): that is the normal warm shape, kept as a
// valid split with fresh 74, never hidden or reduced.
func TestUnwrapUsage_CursorColdAndWarm(t *testing.T) {
	for name, tc := range map[string]struct {
		file                    string
		total, fresh, read, out int
	}{
		"cold": {"22-json-cold.out", 12665, 8801, 3864, 28},
		"warm": {"22-json-warm.out", 12738, 74, 12664, 25},
	} {
		t.Run(name, func(t *testing.T) {
			text, u := UnwrapUsage([]byte(cursorRead(t, tc.file)))
			if string(text) != "PONG" {
				t.Errorf("text = %q", text)
			}
			if !u.Available || !u.CacheSplitAvailable {
				t.Fatalf("usage = %+v, want Available with the cache split", u)
			}
			if u.InputTokens != tc.total || u.FreshInputTokens != tc.fresh || u.CacheReadInputTokens != tc.read ||
				u.CacheCreationInputTokens != 0 || u.OutputTokens != tc.out {
				t.Errorf("tokens = in %d fresh %d read %d write %d out %d, want %d/%d/%d/0/%d",
					u.InputTokens, u.FreshInputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens, u.OutputTokens,
					tc.total, tc.fresh, tc.read, tc.out)
			}
		})
	}
}

// AC2: a missing figure is a cursor-named unavailable and never a zero.
func TestUnwrapUsage_CursorShapes(t *testing.T) {
	env := func(usage string) []byte {
		return []byte(`{"type":"result","result":"PONG","request_id":"r1"` + usage + `}`)
	}
	for name, tc := range map[string]struct {
		body []byte
		want func(UsageResult) bool
	}{
		"no usage": {env(``), func(u UsageResult) bool { return !u.Available }},
		"no inputTokens": {env(`,"usage":{"outputTokens":3,"cacheReadTokens":1,"cacheWriteTokens":0}`),
			func(u UsageResult) bool { return !u.Available }},
		"no outputTokens": {env(`,"usage":{"inputTokens":9,"cacheReadTokens":1,"cacheWriteTokens":0}`),
			func(u UsageResult) bool { return !u.Available }},
	} {
		t.Run(name, func(t *testing.T) {
			text, u := UnwrapUsage(tc.body)
			if string(text) != "PONG" {
				t.Errorf("text = %q", text)
			}
			if !tc.want(u) || !strings.Contains(u.UnavailableReason, "cursor command") || u.InputTokens != 0 || u.TotalTokens != 0 {
				t.Errorf("usage = %+v, want a cursor command-named unavailable, no invented figure", u)
			}
		})
	}
	t.Run("no cache fields leaves the split unreported", func(t *testing.T) {
		_, u := UnwrapUsage(env(`,"usage":{"inputTokens":9,"outputTokens":3}`))
		if !u.Available || u.InputTokens != 9 || u.OutputTokens != 3 {
			t.Fatalf("usage = %+v", u)
		}
		if u.CacheSplitAvailable || u.FreshInputTokens != 0 || u.CacheReadInputTokens != 0 {
			t.Errorf("split must stay unreported: %+v", u)
		}
	})
	t.Run("one cache field leaves the split unreported", func(t *testing.T) {
		_, u := UnwrapUsage(env(`,"usage":{"inputTokens":9,"outputTokens":3,"cacheReadTokens":2}`))
		if !u.Available || u.CacheSplitAvailable {
			t.Errorf("usage = %+v", u)
		}
	})
	t.Run("a cache sum above inputTokens is a valid split", func(t *testing.T) {
		_, u := UnwrapUsage(env(`,"usage":{"inputTokens":9,"outputTokens":3,"cacheReadTokens":8,"cacheWriteTokens":4}`))
		if !u.Available || !u.CacheSplitAvailable || u.FreshInputTokens != 9 || u.InputTokens != 21 ||
			u.CacheReadInputTokens != 8 || u.CacheCreationInputTokens != 4 {
			t.Errorf("usage = %+v, want fresh 9 and total 21, never hidden or reduced", u)
		}
	})
	t.Run("a claude envelope decodes as claude", func(t *testing.T) {
		_, u := UnwrapUsage([]byte(`{"type":"result","result":"OK","usage":{"input_tokens":3,"output_tokens":4}}`))
		if !u.Available || u.InputTokens != 3 || u.OutputTokens != 4 {
			t.Errorf("claude envelope regressed: %+v", u)
		}
	})
}

// AC3: cost is a cursor-named unavailable, never priced from another table.
func TestUnwrapUsage_CursorCostIsUnavailable(t *testing.T) {
	_, u := UnwrapUsage([]byte(cursorRead(t, "1a-json.out")))
	if u.CostUSD != nil {
		t.Errorf("CostUSD = %v, want nil", *u.CostUSD)
	}
	if !strings.Contains(u.CostUnavailableReason, "cursor") || !strings.Contains(u.CostUnavailableReason, "no per-token price") {
		t.Errorf("CostUnavailableReason = %q, want a cursor-named no-price reason", u.CostUnavailableReason)
	}
}

// AC4: the captured stop payloads, folded by the driver reader.
func TestCursorDriverSnapshotFromCapturedStops(t *testing.T) {
	t.Setenv("SATELLE_CURSOR_USAGE_DIR", t.TempDir())
	const sid = "de3328ed-ba7c-44f9-9eeb-3e5d59dee4a8"
	stops := cursorStopPayloads(t)
	for _, raw := range stops {
		if err := RecordCursorStop(sid, raw); err != nil {
			t.Fatal(err)
		}
	}
	// A replayed stop is the same turn.
	if err := RecordCursorStop(sid, stops[0]); err != nil {
		t.Fatal(err)
	}

	snap := SessionUsageSnapshot(HarnessCursor, sid, t.TempDir())
	if !snap.Available || snap.Executable != HarnessCursor || snap.Model != "composer-2.5" {
		t.Fatalf("snapshot = %+v", snap)
	}
	// 38919 input in total, of which 29620 read and 0 written.
	if snap.FreshInputTokens != 38919-29620 || snap.CacheReadInputTokens != 29620 || snap.CacheCreationInputTokens != 0 || snap.OutputTokens != 289 {
		t.Errorf("tokens = fresh %d read %d write %d out %d, want 9299/29620/0/289",
			snap.FreshInputTokens, snap.CacheReadInputTokens, snap.CacheCreationInputTokens, snap.OutputTokens)
	}
	if snap.CostUSD != nil || !strings.Contains(snap.CostUnavailableReason, "cursor") {
		t.Errorf("cost = %v %q, want a cursor-named unavailable", snap.CostUSD, snap.CostUnavailableReason)
	}
	if snap.ModelCallsUnavailableReason == "" || !strings.Contains(snap.ModelCallsUnavailableReason, "cursor") || snap.ModelCalls != 0 {
		t.Errorf("model calls = %d %q, want a cursor-named unavailable", snap.ModelCalls, snap.ModelCallsUnavailableReason)
	}
	if !snap.MayUndercountInFlightTurn {
		t.Error("cursor reports usage only after a turn ends")
	}
	if snap.Turns != 2 || len(snap.TurnBreakdown) != 2 {
		t.Fatalf("Turns = %d breakdown = %d, want 2 (a replay counts once)", snap.Turns, len(snap.TurnBreakdown))
	}
	if b := snap.TurnBreakdown[1]; b.FreshInput != 13161-13057 || b.CacheRead != 13057 || b.Output != 23 {
		t.Errorf("second turn = %+v", b)
	}
}

func TestCursorDriverReasonsAreNotNoReader(t *testing.T) {
	for _, r := range []string{cursorNoStopReason, cursorDriverCostReason, cursorDriverModelCallsReason} {
		if IsNoDriverReaderReason(r) {
			t.Errorf("%q must not read as a no-reader reason", r)
		}
	}
}

// A payload that is not a usage report records nothing.
func TestRecordCursorStopIgnoresNonUsage(t *testing.T) {
	t.Setenv("SATELLE_CURSOR_USAGE_DIR", t.TempDir())
	for _, raw := range []string{
		`{"hook_event_name":"stop","generation_id":"g","status":"completed"}`,
		`{"hook_event_name":"stop","input_tokens":1,"output_tokens":1}`,
		`{"hook_event_name":"preToolUse","generation_id":"g","input_tokens":1,"output_tokens":1}`,
		`not json`,
	} {
		if err := RecordCursorStop("s", []byte(raw)); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	if snap := SessionUsageSnapshot(HarnessCursor, "s", ""); snap.Available || snap.UnavailableReason != cursorNoStopReason {
		t.Errorf("snapshot = %+v, want nothing recorded", snap)
	}
}

func TestCursorWindowUsageAttributesByStopTime(t *testing.T) {
	t.Setenv("SATELLE_CURSOR_USAGE_DIR", t.TempDir())
	const sid = "sess-w"
	stops := cursorStopPayloads(t)
	before := time.Now().Add(-time.Minute)
	if err := RecordCursorStop(sid, stops[0]); err != nil {
		t.Fatal(err)
	}
	after := time.Now().Add(time.Minute)

	w := SessionWindowUsage(HarnessCursor, sid, "", before, after)
	if !w.Available || w.FreshInputTokens != 25758-16563 || w.CacheReadInputTokens != 16563 || w.OutputTokens != 266 || w.Model != "composer-2.5" {
		t.Fatalf("window = %+v", w)
	}
	if w.CostUSD != nil || !strings.Contains(w.CostUnavailableReason, "cursor") || !strings.Contains(w.ModelCallsUnavailableReason, "cursor") {
		t.Errorf("window reasons = %q / %q", w.CostUnavailableReason, w.ModelCallsUnavailableReason)
	}

	out := SessionWindowUsage(HarnessCursor, sid, "", after, after.Add(time.Hour))
	if out.Available || !strings.Contains(out.UnavailableReason, "cursor") {
		t.Errorf("an empty window = %+v, want a cursor-named unavailable", out)
	}
	none := SessionWindowUsage(HarnessCursor, "never-recorded", "", before, after)
	if none.Available || none.UnavailableReason != cursorNoStopReason {
		t.Errorf("no stops = %+v", none)
	}
}

// A hostile session id cannot name a path outside the usage directory.
func TestCursorUsagePathStaysInDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SATELLE_CURSOR_USAGE_DIR", dir)
	if p := cursorUsagePath("../../etc/passwd"); filepath.Dir(p) != dir {
		t.Errorf("path = %q escapes %q", p, dir)
	}
}
