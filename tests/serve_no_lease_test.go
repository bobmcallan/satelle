//go:build integration

package tests

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestServeDoesNotAcquireEngagementLease (sty_bf797fa9 AC3 negative): a running
// satelle serve must not create or refresh engagement leases. The story's
// original theory — leaked serve heartbeating a seat — is false; serve holds no
// lease. The stuck-flag fix lives in the lease package; this asserts the
// negative so a future serve-side lease acquisition is caught.
func TestServeDoesNotAcquireEngagementLease(t *testing.T) {
	// Shared home for CLI and serve so seat queries see the same DB plane.
	home := isolatedHome(t)
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	out := mustRun(t, testBin, repo, "story", "create",
		"--title", "No Lease From Serve",
		"--body", "serve must not claim the seat",
		"--acceptance", "1. no lease row from serve alone",
		"--category", "chore",
	)
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}

	port := freeListenPort(t)
	env := append(os.Environ(), "SATELLE_HOME="+home)
	_ = StartServeHealthy(t, testBin, repo, env, 8*time.Second,
		"--addr", "127.0.0.1", "--port", port, "--no-watch")
	base := "http://127.0.0.1:" + port
	httpGet(t, base+"/healthz")
	httpGet(t, base+"/")
	time.Sleep(200 * time.Millisecond)

	// Seat under the same home must not list this story from serve alone.
	seat := mustRun(t, testBin, repo, "story", "seat")
	if strings.Contains(seat, id) {
		t.Fatalf("serve must not create an engagement lease for %s; seat=%s", id, seat)
	}
}
