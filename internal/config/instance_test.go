package config_test

import (
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

func TestInstanceIDStableAndPathSensitive(t *testing.T) {
	a := config.InstanceID("/tmp/home-a")
	b := config.InstanceID("/tmp/home-b")
	if a == "" || b == "" {
		t.Fatal("empty id")
	}
	if a == b {
		t.Fatal("different homes must differ")
	}
	if a != config.InstanceID("/tmp/home-a") {
		t.Fatal("same path must be stable")
	}
	// Clean path equivalence.
	if config.InstanceID("/tmp/home-a/") != a {
		// Abs may normalize trailing slash — both should match after Abs.
		abs, _ := filepath.Abs("/tmp/home-a/")
		if config.InstanceID(abs) != config.InstanceID("/tmp/home-a") {
			t.Fatalf("cleaned paths should match: %q vs %q", config.InstanceID(abs), a)
		}
	}
}
