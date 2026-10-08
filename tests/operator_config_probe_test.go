//go:build integration && operatorconfig

package tests

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestHostMutationProbe proves the host-surface guard still watches the
// operator's REAL host when the suite runs under scripts/hermetic.sh: it creates
// a NEW runtime-key dir under the real SATELLE_HOME and a new file under the real
// ~/.config/satelle, so the suite must exit non-zero with the host-surface FATAL
// naming both. It is a probe that is MEANT to fail the run, so it only acts when
// SATELLE_TEST_PROBE_HOST_MUTATION=1. TestMain's exit hook removes both paths
// after the guard has compared (probePaths), leaving the host as it was found.
func TestHostMutationProbe(t *testing.T) {
	if os.Getenv("SATELLE_TEST_PROBE_HOST_MUTATION") != "1" {
		t.Skip("host-mutation probe is opt-in: set SATELLE_TEST_PROBE_HOST_MUTATION=1")
	}
	if hostRootsAtStart.satelleHome == "" || hostRootsAtStart.xdgConfig == "" {
		t.Fatal("host roots were not resolved at suite start")
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(b[:])

	// <name>-<8 hex> is the runtime-key shape the guard treats as pollution.
	keyDir := filepath.Join(hostRootsAtStart.satelleHome, "satelle-probe-"+suffix)
	xdgFile := filepath.Join(hostRootsAtStart.xdgConfig, "satelle-probe-"+suffix)
	// Register before creating so a crash mid-way still gets cleaned up.
	probePaths = append(probePaths, keyDir, xdgFile)
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hostRootsAtStart.xdgConfig); os.IsNotExist(err) {
		// Created by the probe: remove it with its file (a no-op file removal first).
		probePaths = append(probePaths, hostRootsAtStart.xdgConfig)
	}
	if err := os.MkdirAll(hostRootsAtStart.xdgConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xdgFile, []byte("probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
