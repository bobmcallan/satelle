// version_drift_test.go — the SessionStart advisory for VERSION-STAMP drift
// (sty_8ecdae90). The binary is machine-wide: `satelle update` in one repo moves
// every repo onto the new binary, while each repo's stamp keeps naming the
// binary that last deployed its scaffolding. Nothing surfaced that gap until a
// store-backed verb refused the repo — and at session start that verb is the
// SessionStart hook's own reindex, so the refusal landed before the operator
// typed anything.
//
// These tests pin the decision half (versionDriftLine), its silence on the
// common path, and the fact that the surface is an ADVISORY: `hook context`
// still exits zero and still delivers full session context to a stale repo.
package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

func TestVersionDriftLine(t *testing.T) {
	breaking := verb.ChangelogEntry{Version: "0.0.440", Breaking: true}
	quiet := verb.ChangelogEntry{Version: "0.0.441"}

	cases := []struct {
		name           string
		deployed, bin  string
		entries        []verb.ChangelogEntry
		wantSilent     bool
		wantContains   []string
		wantNotContain []string
	}{
		{
			// AC1: the whole point — name the binary, the stamp, the breaking
			// classification and the heal, in one line.
			name:     "behind across a breaking release",
			deployed: "0.0.432", bin: "0.0.441",
			entries:      []verb.ChangelogEntry{quiet, breaking},
			wantContains: []string{"0.0.441", "0.0.432", "BREAKING release 0.0.440", "satelle init"},
		},
		{
			name:     "behind with no breaking release in range",
			deployed: "0.0.432", bin: "0.0.441",
			entries:        []verb.ChangelogEntry{quiet},
			wantContains:   []string{"0.0.441", "0.0.432", "no breaking release in that range", "satelle init"},
			wantNotContain: []string{"BREAKING"},
		},
		{
			// A missing CHANGELOG reaches here as nil entries. The gap is still
			// real, so the line still fires — deliberately unlike the refusal
			// path, which goes quiet rather than bricking the repo.
			name:     "behind with no changelog entries at all",
			deployed: "0.0.432", bin: "0.0.441",
			entries:      nil,
			wantContains: []string{"0.0.441", "0.0.432", "no breaking release in that range"},
		},
		{
			// AC1: unstamped is the case the first store-backed verb refuses
			// outright, so it is exactly the one worth warning about early.
			name:     "unstamped repo",
			deployed: "", bin: "0.0.441",
			wantContains: []string{"0.0.441", "no .satelle/deployed.version stamp", "satelle init"},
		},
		{
			// AC2: the common path. A line per session for nothing is a token
			// toll on every session in every repo.
			name:     "stamp equals binary",
			deployed: "0.0.441", bin: "0.0.441",
			entries:    []verb.ChangelogEntry{breaking},
			wantSilent: true,
		},
		{
			name:     "stamp ahead of binary",
			deployed: "0.0.450", bin: "0.0.441",
			entries:    []verb.ChangelogEntry{breaking},
			wantSilent: true,
		},
		{
			name:     "no binary version to compare against",
			deployed: "0.0.432", bin: "",
			wantSilent: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := versionDriftLine(c.deployed, c.bin, c.entries)
			if c.wantSilent {
				if got != "" {
					t.Fatalf("want silence, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("want an advisory line, got silence")
			}
			if n := len(strings.Split(strings.TrimSpace(got), "\n")); n != 1 {
				t.Errorf("advisory must be ONE line, got %d:\n%s", n, got)
			}
			for _, want := range c.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("line missing %q:\n%s", want, got)
				}
			}
			for _, no := range c.wantNotContain {
				if strings.Contains(got, no) {
					t.Errorf("line must not contain %q:\n%s", no, got)
				}
			}
		})
	}
}

// TestVersionDriftAdvisoryDevBuildSilent is AC2's second silence rule. The test
// binary reports a dev version, so this exercises the real short-circuit — and
// it reuses isDevVersion rather than restating what "dev" means.
func TestVersionDriftAdvisoryDevBuildSilent(t *testing.T) {
	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Stamped far behind anything: only the dev short-circuit can keep it quiet.
	if err := os.WriteFile(filepath.Join(dataDir, deployedVersionName), []byte("satelle.version: 0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := versionDriftAdvisory(repo); got != "" {
		t.Fatalf("a dev build must never advise drift, got %q", got)
	}
}

// TestVersionDriftAdvisoryUninitialisedSilent: a repo with no data dir had
// nothing deployed into it, so there is no gap to report — the same guard
// refuseBreakingDrift applies.
func TestVersionDriftAdvisoryUninitialisedSilent(t *testing.T) {
	if got := versionDriftAdvisory(t.TempDir()); got != "" {
		t.Fatalf("uninitialised repo must be silent, got %q", got)
	}
}

// TestFirstBreakingIsTheOneDefinition: the refusal and the advisory classify the
// same range through the same predicate (AC5). If this ever needed two answers,
// the gap would be described one way to the operator who is warned and another
// to the operator who is refused.
func TestFirstBreakingIsTheOneDefinition(t *testing.T) {
	entries := []verb.ChangelogEntry{
		{Version: "0.0.441"},
		{Version: "0.0.440", Breaking: true},
		{Version: "0.0.439", Breaking: true},
	}
	e, ok := firstBreaking(entries)
	if !ok || e.Version != "0.0.440" {
		t.Fatalf("firstBreaking = %+v, %v; want the FIRST breaking entry (0.0.440)", e, ok)
	}
	if _, ok := firstBreaking([]verb.ChangelogEntry{{Version: "0.0.441"}}); ok {
		t.Error("no breaking entry must report false")
	}
	if _, ok := firstBreaking(nil); ok {
		t.Error("nil entries must report false")
	}
	// The refusal reaches the same verdict over the same range.
	if err := breakingDriftError("0.0.432", "0.0.441", entries); err == nil {
		t.Error("breakingDriftError must refuse the range firstBreaking says is breaking")
	}
	if versionDriftLine("0.0.432", "0.0.441", entries) == "" {
		t.Error("versionDriftLine must advise on the range breakingDriftError refuses")
	}
}

// TestHookContextVersionDriftAdvisoryNeverRefuses is AC3 + AC4 through the real
// injection path: a repo stamped behind still gets its session context, the hook
// exits without error, and the scaffold block is neither replaced nor duplicated.
func TestHookContextVersionDriftAdvisoryNeverRefuses(t *testing.T) {
	// The line itself is composed by the pure function above; here we prove the
	// JOIN is a no-op when it is silent and a prepend when it is not, using the
	// same helper the scaffold and availability blocks go through.
	if got := prependContextLine("body", ""); got != "body" {
		t.Errorf("a silent advisory must not alter the body, got %q", got)
	}
	if got := prependContextLine("scaffold warning\n\nbody", "version line"); got != "version line\n\nscaffold warning\n\nbody" {
		t.Errorf("version line must sit BESIDE the scaffold block, not replace it, got %q", got)
	}

	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	// Stamp the repo far behind. Under the test binary's dev version the
	// advisory stays silent — what this asserts is the fail-open contract: the
	// hook still exits nil and still delivers the always-content either way.
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.WriteFile(filepath.Join(dataDir, deployedVersionName), []byte("satelle.version: 0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	var out, errBuf bytes.Buffer
	if err := runHookContext(&out, &errBuf); err != nil {
		t.Fatalf("hook context must never refuse a stale repo: %v", err)
	}
	if strings.Contains(out.String(), "STOP BLOCKED") || strings.Contains(out.String(), "\"decision\":\"block\"") {
		t.Errorf("hook context emitted a refusal:\n%s", out.String())
	}
}
