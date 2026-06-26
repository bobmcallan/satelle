package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestIsReleaseVersion(t *testing.T) {
	cases := map[string]bool{
		"0.0.1":               true,
		"1.2.3":               true,
		"dev":                 false,
		"":                    false,
		"0.0.0-dev+abc123":    false,
		"0.0.0-dev+abc-dirty": false,
	}
	for v, want := range cases {
		if got := IsReleaseVersion(v); got != want {
			t.Errorf("IsReleaseVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestResolveFrom_ReleaseVerbatim(t *testing.T) {
	in := Info{Version: "0.0.5", Commit: "deadbeef", BuildTime: "2026-06-26"}
	// VCS settings present but must be ignored for a stamped release.
	got := resolveFrom(in, []debug.BuildSetting{{Key: "vcs.revision", Value: "ffffffffffffffff"}})
	if got != in {
		t.Errorf("release build mutated: got %+v, want %+v", got, in)
	}
}

func TestResolveFrom_DevFallsBackToVCS(t *testing.T) {
	settings := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "0123456789abcdef0000"},
		{Key: "vcs.modified", Value: "true"},
		{Key: "vcs.time", Value: "2026-06-26T00:00:00Z"},
	}
	got := resolveFrom(Info{Version: "dev", Commit: "none", BuildTime: "unknown"}, settings)
	if got.Version != "0.0.0-dev+0123456789ab-dirty" {
		t.Errorf("Version = %q, want dev+short-dirty", got.Version)
	}
	if got.Commit != "0123456789ab" {
		t.Errorf("Commit = %q, want short rev", got.Commit)
	}
	if got.BuildTime != "2026-06-26T00:00:00Z" {
		t.Errorf("BuildTime = %q, want vcs.time", got.BuildTime)
	}
}

func TestResolveFrom_DevNoVCS(t *testing.T) {
	in := Info{Version: "dev", Commit: "none", BuildTime: "unknown"}
	got := resolveFrom(in, nil)
	if got != in {
		t.Errorf("no-VCS dev build mutated: got %+v, want %+v", got, in)
	}
}
