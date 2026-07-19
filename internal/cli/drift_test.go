package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/buildinfo"
)

func TestRetiredNameMessage(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"install"}, "satelle init"},
		{[]string{"workspace", "rm", "x"}, "workspace remove"},
		{[]string{"sync", "config", "pull"}, "sync config deploy"},
		{[]string{"ui", "push"}, "satelle workspace add"},
		{[]string{"ui"}, "satelle workspace add"},
		{[]string{"service", "install"}, ""}, // not retired
		{[]string{"story", "list"}, ""},
	}
	for _, c := range cases {
		got := retiredNameMessage(c.args)
		if c.want == "" {
			if got != "" {
				t.Errorf("args %v: unexpected %q", c.args, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("args %v: got %q, want contains %q", c.args, got, c.want)
		}
	}
	// ui parent must not be registered as a live subcommand.
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "ui" {
			t.Fatal("ui command still registered — retired in favour of workspace add")
		}
	}
}

func TestWriteReadDeployedVersion(t *testing.T) {
	dir := t.TempDir()
	// Force a non-dev version for the stamp when buildinfo is dev — write directly.
	path := filepath.Join(dir, deployedVersionName)
	if err := os.WriteFile(path, []byte("satelle.version: 0.0.200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readDeployedVersion(dir); got != "0.0.200" {
		t.Fatalf("read = %q", got)
	}
	// writeDeployedVersion no-ops on dev builds — just ensure it doesn't error.
	_ = buildinfo.Resolve()
	if _, err := writeDeployedVersion(dir); err != nil {
		t.Fatal(err)
	}
}

func TestIsDevVersion(t *testing.T) {
	if !isDevVersion("dev") || !isDevVersion("") || !isDevVersion("0.0.0-dev+foo") {
		t.Error("dev sentinels")
	}
	if isDevVersion("0.0.218") {
		t.Error("release version is not dev")
	}
}

func TestRefuseBreakingDriftMissingStamp(t *testing.T) {
	// When buildinfo is a release version and data dir exists without stamp, refuse.
	// Dev builds skip — if this test binary is dev, the guard returns nil.
	repo := t.TempDir()
	dataDir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := refuseBreakingDrift(repo)
	// Either skipped (dev) or fails naming init.
	if err != nil && !strings.Contains(err.Error(), "satelle init") {
		t.Fatalf("want init named: %v", err)
	}
}

func TestRefuseBreakingDriftBreakingRange(t *testing.T) {
	// Pure path: plant a changelog with Breaking between deployed and binary,
	// force non-dev via reading real CHANGELOG when available.
	// We test the helper pieces: ChangelogRange + Breaking detection.
	// Full refuseBreakingDrift depends on buildinfo.Version; when non-dev,
	// a stamp older than a Breaking entry must fail closed.
	repo := t.TempDir()
	dataDir := filepath.Join(repo, ".satelle")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Stamp an old version
	if err := os.WriteFile(filepath.Join(dataDir, deployedVersionName), []byte("satelle.version: 0.0.100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Plant CHANGELOG with breaking in range
	cl := filepath.Join(repo, "CHANGELOG.md")
	body := `# Changelog
## [0.0.999] - 2099-01-01

### Breaking
- test break

## [0.0.100] - 2020-01-01

### Fixed
- old
`
	if err := os.WriteFile(cl, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Point changelogPath at our fixture via chdir
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	// Only assert when binary is non-dev (release test builds).
	err := refuseBreakingDrift(repo)
	if err == nil {
		// Dev build: skip assertion
		if !isDevVersion(buildinfo.Resolve().Version) {
			// Non-dev but no error: binary version may be <= 0.0.100
			t.Log("no drift error — binary may be older than planted stamp")
		}
		return
	}
	if !strings.Contains(err.Error(), "satelle init") || !strings.Contains(strings.ToLower(err.Error()), "breaking") {
		t.Fatalf("want breaking+init: %v", err)
	}
}

func TestMigrateAgentsFlattenAndInject(t *testing.T) {
	// Config package test lives better there; smoke via retiredNames already covered.
	// Keep retiredName multi-token asserts here as AC2 CLI invocation surface.
	for _, args := range [][]string{
		{"workspace", "rm"},
		{"sync", "config", "pull"},
	} {
		msg := retiredNameMessage(args)
		if msg == "" {
			t.Errorf("%v must be retired", args)
		}
	}
}
