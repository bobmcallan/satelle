package agentcli

import (
	"math"
	"strings"
	"testing"
	"time"
)

// piWindowRepo is the repo path pi_session_multistory.jsonl is installed under.
const piWindowRepo = "/home/example/pi-repo"

func piAt(t *testing.T, hhmmss string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, "2026-09-30T"+hhmmss+"Z")
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func installMultistory(t *testing.T) {
	t.Helper()
	installPiFixture(t, piWindowRepo, readFixture(t, "pi_session_multistory.jsonl"))
}

// TestPiSessionWindowUsageDisjointWindows pins the windowed reader against the
// multi-story pi capture: each story's window sums exactly its own assistant rows —
// the errored all-zero row inside the first window is not a call, the row between
// windows belongs to nobody, and both boundaries are inclusive.
func TestPiSessionWindowUsageDisjointWindows(t *testing.T) {
	installMultistory(t)
	cases := []struct {
		name                    string
		from, to                string
		fresh, out, read, write int
		calls                   int
		model                   string
		wantCost                float64 // 0 means unpriced by pi
	}{
		// The row at 10:10:00.000 sits exactly on the window's closing edge.
		{"story A (unpriced, to-edge row)", "10:00:00.000", "10:10:00.000", 3500, 350, 17500, 100, 3, "stealth/space-bunny-alpha", 0},
		{"story B (priced)", "10:20:00.000", "10:30:00.000", 4000, 400, 9000, 0, 2, "priced/model-one", 0.0323},
		{"story C (unpriced, model switched back)", "10:40:00.000", "10:50:00.000", 1000, 100, 2000, 0, 2, "stealth/space-bunny-alpha", 0},
		// A window opening exactly on a row includes it.
		{"from-edge row", "10:01:00.000", "10:04:00.000", 1000, 100, 5000, 0, 1, "stealth/space-bunny-alpha", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := SessionWindowUsage(HarnessPi, piFixtureSession, piWindowRepo, piAt(t, c.from), piAt(t, c.to))
			if !w.Available || w.Executable != HarnessPi || w.SessionID != piFixtureSession {
				t.Fatalf("window = %+v, want available pi", w)
			}
			if w.FreshInputTokens != c.fresh || w.OutputTokens != c.out || w.CacheReadInputTokens != c.read || w.CacheCreationInputTokens != c.write {
				t.Fatalf("tokens fresh=%d out=%d read=%d write=%d, want %d/%d/%d/%d", w.FreshInputTokens, w.OutputTokens, w.CacheReadInputTokens, w.CacheCreationInputTokens, c.fresh, c.out, c.read, c.write)
			}
			if w.ModelCalls != c.calls || w.Model != c.model {
				t.Fatalf("calls=%d model=%q, want %d %q", w.ModelCalls, w.Model, c.calls, c.model)
			}
			if c.wantCost == 0 {
				// AC3: pi reports zero cost beside real tokens — unpriced, never a measured $0.
				if w.CostUSD != nil || w.CostUnavailableReason != piUnpricedCostReason {
					t.Fatalf("cost = %v (%q), want nil with the named unpriced-model reason", w.CostUSD, w.CostUnavailableReason)
				}
				return
			}
			if w.CostUSD == nil || math.Abs(*w.CostUSD-c.wantCost) > 1e-9 || w.CostUnavailableReason != "" {
				t.Fatalf("cost = %v (%q), want %v", w.CostUSD, w.CostUnavailableReason, c.wantCost)
			}
		})
	}
}

// TestPiWindowsPartitionTheCumulative: the windowed and cumulative readers fold the
// same rows, so disjoint windows that cover the whole record sum to the cumulative
// snapshot exactly — they cannot drift apart.
func TestPiWindowsPartitionTheCumulative(t *testing.T) {
	installMultistory(t)
	snap := SessionUsageSnapshot(HarnessPi, piFixtureSession, piWindowRepo)
	if !snap.Available {
		t.Fatalf("snapshot = %+v", snap)
	}
	whole := SessionWindowUsage(HarnessPi, piFixtureSession, piWindowRepo, piAt(t, "00:00:00"), piAt(t, "23:59:59"))
	first := SessionWindowUsage(HarnessPi, piFixtureSession, piWindowRepo, piAt(t, "00:00:00"), piAt(t, "10:59:59"))
	rest := SessionWindowUsage(HarnessPi, piFixtureSession, piWindowRepo, piAt(t, "11:00:00"), piAt(t, "23:59:59"))
	if whole.FreshInputTokens != snap.FreshInputTokens || whole.OutputTokens != snap.OutputTokens ||
		whole.CacheReadInputTokens != snap.CacheReadInputTokens || whole.CacheCreationInputTokens != snap.CacheCreationInputTokens ||
		whole.ModelCalls != snap.ModelCalls {
		t.Fatalf("whole-record window %+v disagrees with cumulative %+v", whole, snap)
	}
	if first.FreshInputTokens+rest.FreshInputTokens != snap.FreshInputTokens || first.ModelCalls+rest.ModelCalls != snap.ModelCalls {
		t.Fatalf("two windows %+v + %+v do not partition the cumulative %+v", first, rest, snap)
	}
	if whole.CostUSD == nil || snap.CostUSD == nil || math.Abs(*whole.CostUSD-*snap.CostUSD) > 1e-9 {
		t.Fatalf("cost: window %v vs cumulative %v", whole.CostUSD, snap.CostUSD)
	}
}

// TestPiSessionWindowUsageUnavailableIsNamed: a window with nothing in it, a session
// record that is gone, and a harness with no windowed reader each say so by name — never
// a zero that reads as a measurement.
func TestPiSessionWindowUsageUnavailableIsNamed(t *testing.T) {
	installMultistory(t)
	empty := SessionWindowUsage(HarnessPi, piFixtureSession, piWindowRepo, piAt(t, "12:00:00"), piAt(t, "12:10:00"))
	if empty.Available || !strings.HasPrefix(empty.UnavailableReason, "pi: session record has no assistant usage between") ||
		empty.ModelCalls != 0 || empty.FreshInputTokens != 0 {
		t.Fatalf("empty window = %+v, want a named pi unavailable", empty)
	}

	// The session file is gone: the record sits under another repo's directory.
	gone := SessionWindowUsage(HarnessPi, piFixtureSession, "/home/example/other-repo", piAt(t, "10:00:00"), piAt(t, "10:10:00"))
	if gone.Available || !strings.HasPrefix(gone.UnavailableReason, "pi: session record for "+piFixtureSession+" not found under ") {
		t.Fatalf("missing file = %+v, want a named pi unavailable", gone)
	}

	for _, harness := range []string{HarnessClaude, HarnessGrok, "mystery", ""} {
		w := SessionWindowUsage(harness, piFixtureSession, piWindowRepo, piAt(t, "10:00:00"), piAt(t, "10:10:00"))
		if w.Available || !strings.Contains(w.UnavailableReason, "backfill not supported") {
			t.Fatalf("harness %q = %+v, want backfill-not-supported unavailable", harness, w)
		}
	}
	if w := SessionWindowUsage(HarnessPi, " ", piWindowRepo, piAt(t, "10:00:00"), piAt(t, "10:10:00")); w.Available || !strings.Contains(w.UnavailableReason, "no session id") {
		t.Fatalf("blank session = %+v", w)
	}
}

// TestPiSessionWindowUsageUntimedRowIsNotGuessed: an assistant row with no parseable
// timestamp cannot be placed in any window, so the record is unavailable by name rather
// than silently undercounted.
func TestPiSessionWindowUsageUntimedRowIsNotGuessed(t *testing.T) {
	body := `{"type":"session","version":3,"id":"` + piFixtureSession + `","cwd":"/x"}` + "\n" +
		`{"type":"message","id":"a1","message":{"role":"assistant","model":"m","usage":{"input":5,"output":1,"cacheRead":0,"cacheWrite":0,"cost":{"total":0}}}}` + "\n"
	installPiFixture(t, piWindowRepo, []byte(body))
	w := SessionWindowUsage(HarnessPi, piFixtureSession, piWindowRepo, piAt(t, "00:00:00"), piAt(t, "23:59:59"))
	if w.Available || !strings.Contains(w.UnavailableReason, "no parseable timestamp") {
		t.Fatalf("untimed row = %+v, want a named unavailable", w)
	}
}

// TestIsNoDriverReaderReason: the reason the default reader case writes is the one a
// backfill recognises on a recorded row, for any adapter name.
func TestIsNoDriverReaderReason(t *testing.T) {
	snap := SessionUsageSnapshot("someharness", "s1", "/r")
	if snap.Available || !IsNoDriverReaderReason(snap.UnavailableReason) {
		t.Fatalf("unrecognised harness = %+v, want the recognised no-reader reason", snap)
	}
	if IsNoDriverReaderReason("pi: session record for x not found under y") {
		t.Fatal("a missing record is not a missing reader")
	}
}
