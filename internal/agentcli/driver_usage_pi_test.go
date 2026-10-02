package agentcli

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const piFixtureSession = "01a0f01e-fb15-7095-8798-9b7ab9d3d092"

// installPiFixture places body where pi would have written the session record for
// repo, under an isolated PI_CODING_AGENT_DIR.
func installPiFixture(t *testing.T, repo string, body []byte) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", home)
	writeAt(t, filepath.Join(home, "sessions", piSessionDirName(repo), "2026-09-30T02-22-37-333Z_"+piFixtureSession+".jsonl"), body)
}

// TestPiSessionDirName pins pi's directory convention against the real
// directory name captured on this machine.
func TestPiSessionDirName(t *testing.T) {
	if got, want := piSessionDirName("/home/bobmcallan/Development/satelle"), "--home-bobmcallan-Development-satelle--"; got != want {
		t.Fatalf("piSessionDirName = %q, want %q", got, want)
	}
}

// TestPiDriverSnapshot pins the reader against a real pi session capture: three
// usage-bearing assistant rows sum exactly, the errored all-zero assistant row is
// not counted as a call, and the cost pi reported as 0 beside real tokens is an
// unavailable cost, never a measured $0.
func TestPiDriverSnapshot(t *testing.T) {
	repo := "/home/bobmcallan/Development/satelle"
	installPiFixture(t, repo, readFixture(t, "pi_session.jsonl"))
	snap := SessionUsageSnapshot(HarnessPi, piFixtureSession, repo)
	if !snap.Available || snap.Executable != HarnessPi {
		t.Fatalf("snapshot = %+v, want available pi", snap)
	}
	if snap.FreshInputTokens != 14263 || snap.OutputTokens != 351 || snap.CacheReadInputTokens != 21128 || snap.CacheCreationInputTokens != 0 {
		t.Fatalf("tokens fresh=%d out=%d cacheRead=%d cacheWrite=%d, want 14263/351/21128/0",
			snap.FreshInputTokens, snap.OutputTokens, snap.CacheReadInputTokens, snap.CacheCreationInputTokens)
	}
	if snap.Turns != 3 || snap.ModelCalls != 3 || snap.ModelCallsUnavailableReason != "" {
		t.Fatalf("turns=%d modelCalls=%d (%q), want 3/3", snap.Turns, snap.ModelCalls, snap.ModelCallsUnavailableReason)
	}
	if snap.Model != "stealth/space-bunny-alpha" {
		t.Fatalf("model = %q", snap.Model)
	}
	if snap.CostUSD != nil {
		t.Fatalf("CostUSD = %v, want nil: pi priced nothing", *snap.CostUSD)
	}
	if !strings.HasPrefix(snap.CostUnavailableReason, "pi:") {
		t.Fatalf("CostUnavailableReason = %q, want a pi: reason", snap.CostUnavailableReason)
	}
	if snap.MayUndercountInFlightTurn {
		t.Fatal("pi persists an assistant row before its tool call runs (pi_inflight_probe.result.md); it must not be flagged as undercounting")
	}
}

// piProbeCalls parses pi_inflight_probe.log: for each probed tool call k, how many
// toolCall assistant rows the session file held at the moment the tool executed.
func piProbeCalls(t *testing.T) map[int]int {
	t.Helper()
	calls := map[int]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(readFixture(t, "pi_inflight_probe.log"))), "\n") {
		var k, seen, rows int
		var at string
		if _, err := fmt.Sscanf(line, "call=%d at=%s toolcall_rows_in_file=%d assistant_rows_in_file=%d", &k, &at, &seen, &rows); err != nil {
			t.Fatalf("probe log line %q: %v", line, err)
		}
		calls[k] = seen
	}
	if len(calls) < 3 {
		t.Fatalf("probe log has %d calls, want the three captured", len(calls))
	}
	return calls
}

// piFileAtToolCall is the probe session file as it stood when the k-th tool call
// executed: every row up to and including the k-th assistant toolCall row, and
// nothing after it (that call's own toolResult had not been written yet).
func piFileAtToolCall(t *testing.T, k int) []byte {
	t.Helper()
	var out strings.Builder
	seen := 0
	for _, line := range strings.SplitAfter(string(readFixture(t, "pi_inflight_probe.jsonl")), "\n") {
		if strings.Contains(line, `"type":"toolCall"`) {
			seen++
		}
		if seen > k {
			break
		}
		out.WriteString(line)
		if seen == k && strings.Contains(line, `"type":"toolCall"`) {
			break
		}
	}
	return []byte(out.String())
}

// TestPiInFlightTurnIsInTheSessionFileWhenTheToolRuns pins MayUndercountInFlightTurn
// for pi to a timestamped probe on a real session (sty_89768625): at each of three
// tool calls the calling assistant row was already persisted, so a snapshot taken by
// a `satelle story set` call includes the very turn that made it. The flag is
// derived from the probe log, not asserted free-standing, and the read at each call
// is replayed from the capture.
func TestPiInFlightTurnIsInTheSessionFileWhenTheToolRuns(t *testing.T) {
	repo := "/home/example/repo"
	wantUndercount := false
	for k, seen := range piProbeCalls(t) {
		if seen < k {
			wantUndercount = true
		}
	}
	// Cumulative usage after each calling row, from pi_inflight_probe.jsonl.
	type cum struct{ fresh, out, cacheRead int }
	want := map[int]cum{1: {2171, 126, 140}, 2: {2191, 247, 2575}, 3: {2211, 368, 5149}}
	for k := 1; k <= 3; k++ {
		installPiFixture(t, repo, piFileAtToolCall(t, k))
		snap := SessionUsageSnapshot(HarnessPi, piFixtureSession, repo)
		if !snap.Available || snap.Turns != k {
			t.Fatalf("tool call %d: snapshot = %+v, want available with the calling turn counted (turns=%d)", k, snap, k)
		}
		if got := (cum{snap.FreshInputTokens, snap.OutputTokens, snap.CacheReadInputTokens}); got != want[k] {
			t.Fatalf("tool call %d: usage = %+v, want %+v (calling row's own usage included)", k, got, want[k])
		}
		if snap.MayUndercountInFlightTurn != wantUndercount {
			t.Fatalf("tool call %d: MayUndercountInFlightTurn = %v, want %v from the probe log", k, snap.MayUndercountInFlightTurn, wantUndercount)
		}
	}
	if wantUndercount {
		t.Fatal("the checked-in probe shows the calling row absent at execution; this test and piDriverSnapshot were written for the measured present-at-execution result — re-derive both")
	}
}

// TestPiDriverSnapshotMeasuredCost: a row that carries a non-zero
// usage.cost.total is summed as pi's own figure.
func TestPiDriverSnapshotMeasuredCost(t *testing.T) {
	repo := "/home/example/repo"
	row := func(id string, in int, total string) string {
		return `{"type":"message","id":"` + id + `","message":{"role":"assistant","model":"m1","usage":{"input":` + strconv.Itoa(in) +
			`,"output":10,"cacheRead":5,"cacheWrite":7,"totalTokens":0,"cost":{"total":` + total + `}}}}` + "\n"
	}
	installPiFixture(t, repo, []byte(row("a", 100, "0.25")+row("b", 200, "0.5")))
	snap := SessionUsageSnapshot(HarnessPi, piFixtureSession, repo)
	if !snap.Available || snap.CostUSD == nil || *snap.CostUSD != 0.75 {
		t.Fatalf("snapshot = %+v, want cost 0.75", snap)
	}
	if snap.FreshInputTokens != 300 || snap.CacheCreationInputTokens != 14 || snap.CacheReadInputTokens != 10 || snap.OutputTokens != 20 {
		t.Fatalf("tokens = %+v", snap)
	}
}

// TestPiDriverSnapshotUnavailable: every reason the record yields no figure is an
// adapter-named unavailable carrying zero tokens — never a zero that reads as a
// measurement.
func TestPiDriverSnapshotUnavailable(t *testing.T) {
	repo := "/home/example/repo"
	cases := map[string][]byte{
		"empty record":       []byte(""),
		"header only":        []byte(`{"type":"session","version":3,"id":"x"}` + "\n"),
		"malformed lines":    []byte("not json\n{\"type\":\n"),
		"all-zero errored":   []byte(`{"type":"message","message":{"role":"assistant","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}}` + "\n"),
		"assistant no usage": []byte(`{"type":"message","message":{"role":"assistant","content":[]}}` + "\n"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			installPiFixture(t, repo, body)
			assertPiUnavailable(t, SessionUsageSnapshot(HarnessPi, piFixtureSession, repo))
		})
	}
	t.Run("missing file", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
		assertPiUnavailable(t, SessionUsageSnapshot(HarnessPi, piFixtureSession, repo))
	})
	t.Run("no session id", func(t *testing.T) {
		assertPiUnavailable(t, SessionUsageSnapshot(HarnessPi, "", repo))
	})
}

func assertPiUnavailable(t *testing.T, snap DriverSnapshot) {
	t.Helper()
	if snap.Available {
		t.Fatalf("snapshot = %+v, want Available=false", snap)
	}
	if !strings.HasPrefix(snap.UnavailableReason, "pi:") {
		t.Fatalf("UnavailableReason = %q, want a pi: reason", snap.UnavailableReason)
	}
	if snap.FreshInputTokens+snap.OutputTokens+snap.CacheReadInputTokens+snap.CacheCreationInputTokens+snap.Turns != 0 || snap.CostUSD != nil {
		t.Fatalf("snapshot = %+v, want no figures beside an unavailable", snap)
	}
}
