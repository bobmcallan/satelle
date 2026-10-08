package testutil

import (
	"os"
	"testing"
)

// Scratch negative proofs for sty_53c3f311 AC5, reverted after the run. Inert
// unless SATELLE_TEST_PROBE_53C3F311 selects one (the hermetic wrapper forwards
// only SATELLE_TEST_PROBE_* test variables).

func TestZZScratchUnlistedSkip(t *testing.T) {
	if os.Getenv("SATELLE_TEST_PROBE_53C3F311") != "skip" {
		return
	}
	t.Skip("scratch: an unlisted skip must fail the strict run")
}

func TestZZScratchFatal(t *testing.T) {
	if os.Getenv("SATELLE_TEST_PROBE_53C3F311") != "fatal" {
		return
	}
	t.Fatal("scratch: a failing test must fail the strict run through the checker")
}
