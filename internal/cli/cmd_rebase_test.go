package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rebaseTestTime pins the backup timestamp so tests can name the backup dir.
var rebaseTestTime = time.Date(2026, 7, 2, 3, 4, 5, 0, time.UTC)

// seedCustomSubstrate lays down a customized substrate in dataDir: one custom
// file per rebase-owned kind. Returns the custom file paths by kind.
func seedCustomSubstrate(t *testing.T, dataDir string) map[string]string {
	t.Helper()
	custom := map[string]string{
		"workflows":  "my-workflow.md",
		"skills":     "my-skill.md",
		"principles": "my-principle.md",
	}
	paths := map[string]string{}
	for kind, name := range custom {
		dir := filepath.Join(dataDir, kind)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("# custom "+kind+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths[kind] = p
	}
	return paths
}

func TestRunRebaseBacksUpWipesRedeploys(t *testing.T) {
	dataDir := t.TempDir()
	custom := seedCustomSubstrate(t, dataDir)

	var out strings.Builder
	if err := runRebase(&out, strings.NewReader(""), dataDir, true, rebaseTestTime); err != nil {
		t.Fatalf("runRebase: %v", err)
	}

	// The backup holds every customized file (the wipe IS the move).
	backupDir := filepath.Join(dataDir, "backups", "20260702-030405")
	for kind := range custom {
		backed := filepath.Join(backupDir, kind, filepath.Base(custom[kind]))
		if !fileExists(backed) {
			t.Errorf("backup missing %s", backed)
		}
		if fileExists(custom[kind]) {
			t.Errorf("custom file %s survived the wipe", custom[kind])
		}
	}

	// The complete default solution is redeployed: workflows + referenced skills
	// + embedded principles, and each kind dir has its README keep-file back.
	for _, wf := range defaultSolutionWorkflows {
		if !fileExists(filepath.Join(dataDir, "workflows", wf+".md")) {
			t.Errorf("rebase did not redeploy workflows/%s.md", wf)
		}
	}
	for _, sk := range defaultSolutionSkills {
		if !fileExists(filepath.Join(dataDir, "skills", sk+".md")) {
			t.Errorf("rebase did not redeploy skills/%s.md", sk)
		}
	}
	if !fileExists(filepath.Join(dataDir, "principles", "satelle-agent-goals.md")) {
		t.Error("rebase did not redeploy the embedded principles")
	}
	for _, kind := range rebaseKinds {
		if !fileExists(filepath.Join(dataDir, kind, "README.md")) {
			t.Errorf("rebase did not recreate %s/README.md", kind)
		}
	}

	// The report names the backup path and the deployed set.
	if !strings.Contains(out.String(), backupDir) {
		t.Errorf("report does not name the backup path:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "deployed") {
		t.Errorf("report does not summarise the deployment:\n%s", out.String())
	}
}

func TestRunRebaseAbortsWithoutConfirmation(t *testing.T) {
	dataDir := t.TempDir()
	custom := seedCustomSubstrate(t, dataDir)

	var out strings.Builder
	if err := runRebase(&out, strings.NewReader("no\n"), dataDir, false, rebaseTestTime); err != nil {
		t.Fatalf("runRebase: %v", err)
	}
	if !strings.Contains(out.String(), "aborted") {
		t.Errorf("expected an abort report:\n%s", out.String())
	}
	for kind, p := range custom {
		if !fileExists(p) {
			t.Errorf("abort still wiped %s (%s)", kind, p)
		}
	}
	if fileExists(filepath.Join(dataDir, "backups")) {
		t.Error("abort still created a backup dir")
	}
}

func TestRunRebaseAbortsWhenBackupCannotBeWritten(t *testing.T) {
	dataDir := t.TempDir()
	custom := seedCustomSubstrate(t, dataDir)
	// A FILE named "backups" makes the backup dir uncreatable.
	if err := os.WriteFile(filepath.Join(dataDir, "backups"), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := runRebase(&strings.Builder{}, strings.NewReader(""), dataDir, true, rebaseTestTime)
	if err == nil {
		t.Fatal("expected an error when the backup cannot be written")
	}
	for kind, p := range custom {
		if !fileExists(p) {
			t.Errorf("failed backup still wiped %s (%s)", kind, p)
		}
	}
}
