package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/buildinfo"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/doctor"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/syncstate"
	"github.com/bobmcallan/satelle/internal/verb"
)

func TestStampBehind(t *testing.T) {
	for _, c := range []struct {
		name, deployed, bin string
		want                bool
	}{
		{"dev binary never heals a stamp", "0.0.1", "0.0.0-dev+abc", false},
		{"unstamped repo is behind", "", "0.0.761", true},
		{"equal stamp is current", "0.0.761", "0.0.761", false},
		{"stamp ahead of the binary is current", "0.0.770", "0.0.761", false},
		{"stamp behind the binary", "0.0.705", "0.0.761", true},
	} {
		if got := stampBehind(c.deployed, c.bin); got != c.want {
			t.Errorf("%s: stampBehind(%q, %q) = %v, want %v", c.name, c.deployed, c.bin, got, c.want)
		}
	}
}

// TestStampRefusalCarriesTheSharedMarker: doctor and init recognise a recorded
// refusal by health.IsStampRefusal, so the gate's own refusal must satisfy it
// and its warning (which never refuses) is not mistaken for one by the push.
func TestStampRefusalCarriesTheSharedMarker(t *testing.T) {
	_, err := breakingDrift("0.0.380", "0.0.396", []verb.ChangelogEntry{healingEntry("0.0.390", "init-heals: rewrite")})
	if err == nil || !health.IsStampRefusal(err.Error()) {
		t.Fatalf("the drift gate's refusal must be recognised as a stamp refusal: %v", err)
	}
	if !strings.Contains(err.Error(), "across BREAKING release 0.0.390") {
		t.Errorf("refusal text changed: %v", err)
	}
	if health.IsStampRefusal("hosted down") {
		t.Error("an ordinary push failure is not a stamp refusal")
	}
}

func fakeRelease(t *testing.T, v string) {
	t.Helper()
	old := buildinfo.Version
	buildinfo.Version = v
	t.Cleanup(func() { buildinfo.Version = old })
}

// TestInitRestoresStampWhenValidationFails: the stamp is written before
// validation, and still means aligned and validated — a failed init leaves the
// previous stamp, or none, exactly as it found it.
func TestInitRestoresStampWhenValidationFails(t *testing.T) {
	fakeRelease(t, "0.0.900")
	for name, prev := range map[string]string{"behind": "satelle.version: 0.0.100\n", "unstamped": ""} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			if err := runInitTest(t, io.Discard, repo); err != nil {
				t.Fatalf("clean init: %v", err)
			}
			stamp := filepath.Join(repo, config.DefaultDataDir, deployedVersionName)
			if prev == "" {
				_ = os.Remove(stamp)
			} else if err := os.WriteFile(stamp, []byte(prev), 0o644); err != nil {
				t.Fatal(err)
			}
			broken := filepath.Join(repo, ".satelle", "workflows", "broken.md")
			if err := os.WriteFile(broken, []byte("---\nname: broken\n---\n# broken\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := runInit(io.Discard, repo, false, nil); err == nil || !strings.Contains(err.Error(), "failed validation") {
				t.Fatalf("init over broken substrate must fail validation: %v", err)
			}
			got, rerr := os.ReadFile(stamp)
			switch {
			case prev == "" && !os.IsNotExist(rerr):
				t.Errorf("a failed init must not leave a new stamp: %q %v", got, rerr)
			case prev != "" && string(got) != prev:
				t.Errorf("a failed init must restore the previous stamp: got %q, want %q", got, prev)
			}
		})
	}
}

// TestInitClearsTheRecordedStampRefusal (AC3, AC6): a push the drift gate refused
// before the heal is replayed by doctor as sync.failing until a push succeeds.
// One init re-stamps, clears that record before validating, and so prints
// nothing about the stamp it replaced; doctor names init as the fix before it.
func TestInitClearsTheRecordedStampRefusal(t *testing.T) {
	fakeRelease(t, "0.0.900")
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("clean init: %v", err)
	}
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	tomlPath := filepath.Join(dataDir, config.ConfigName)
	raw, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	nonLocal := strings.Replace(string(raw), "[sync]\nstale_after = \"24h\"\n", "[sync]\nstale_after = \"24h\"\nstories = \"personal\"\n", 1)
	if nonLocal == string(raw) {
		t.Fatal("fixture: scaffold [sync] block not found")
	}
	if err := os.WriteFile(tomlPath, []byte(nonLocal), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, deployedVersionName), []byte("satelle.version: 0.0.100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, refusal := breakingDrift("0.0.100", "0.0.900", []verb.ChangelogEntry{healingEntry("0.0.761", "init-heals: rewrite")})
	if err := syncstate.RecordPush(config.GlobalDir(), repo, false, refusal.Error(), "", time.Now()); err != nil {
		t.Fatal(err)
	}

	failing := func() *health.Finding {
		rep := doctor.Check(context.Background(), doctor.Opts{RepoRoot: repo, DataDir: dataDir})
		for _, f := range rep.Findings {
			if f.ID == health.IDSyncFailing {
				return &f
			}
		}
		return nil
	}
	before := failing()
	if before == nil || !strings.Contains(before.Remediation, "satelle init") {
		t.Fatalf("guard: doctor must replay the refusal as sync.failing naming satelle init: %+v", before)
	}

	var out strings.Builder
	if err := runInit(&out, repo, false, nil); err != nil {
		t.Fatalf("re-init: %v\n%s", err, out.String())
	}
	for _, bad := range []string{health.StampRefusalMarker, "[" + health.IDSyncFailing + "]"} {
		if strings.Contains(out.String(), bad) {
			t.Errorf("one init must print nothing about the stamp it replaced (%q):\n%s", bad, out.String())
		}
	}
	if after := failing(); after != nil {
		t.Errorf("the re-stamp must clear the recorded refusal: %+v", after)
	}
}
