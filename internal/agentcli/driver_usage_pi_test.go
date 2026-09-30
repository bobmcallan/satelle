package agentcli

import (
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
		t.Fatal("pi persists an assistant row before its tool call runs; it must not be flagged as undercounting")
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
