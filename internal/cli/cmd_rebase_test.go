package cli

import (
	"github.com/bobmcallan/satelle/internal/config"
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

func TestRunRebaseBacksUpAndWipes(t *testing.T) {
	dataDir := t.TempDir()
	custom := seedCustomSubstrate(t, dataDir)

	var out strings.Builder
	if err := runRebase(&out, strings.NewReader(""), dataDir, dataDir, true, rebaseTestTime); err != nil {
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

	// No redeploy: the wipe IS the reset, because the embedded defaults govern
	// virtually through the read-time overlay (sty_cc550a88). What the dirs must
	// hold afterwards is asserted by TestRunRebaseLeavesDefaultsVirtual.
	//
	// The embedded default substrate-audit TASK is re-seeded though (additive — tasks is
	// NOT a rebaseKind, so authored tasks are never wiped; only the absent default
	// lands) (sty_d4360e90).
	if !fileExists(filepath.Join(dataDir, "tasks", "tsk_substrate-audit.md")) {
		t.Error("rebase did not re-seed the embedded substrate-audit task")
	}
	for _, kind := range rebaseKinds {
		if !fileExists(filepath.Join(dataDir, kind, "README.md")) {
			t.Errorf("rebase did not recreate %s/README.md", kind)
		}
	}

	// The report names the backup path and what it restored.
	if !strings.Contains(out.String(), backupDir) {
		t.Errorf("report does not name the backup path:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "restored") {
		t.Errorf("report does not summarise what was restored:\n%s", out.String())
	}
	// The help and the report must stop promising a redeploy that no longer
	// happens — a command that lies about its own effect is the defect here.
	if strings.Contains(out.String(), "deployed") {
		t.Errorf("report still claims a redeploy:\n%s", out.String())
	}
}

func TestRunRebaseAbortsWithoutConfirmation(t *testing.T) {
	dataDir := t.TempDir()
	custom := seedCustomSubstrate(t, dataDir)

	var out strings.Builder
	if err := runRebase(&out, strings.NewReader("no\n"), dataDir, dataDir, false, rebaseTestTime); err != nil {
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

	err := runRebase(&strings.Builder{}, strings.NewReader(""), dataDir, dataDir, true, rebaseTestTime)
	if err == nil {
		t.Fatal("expected an error when the backup cannot be written")
	}
	for kind, p := range custom {
		if !fileExists(p) {
			t.Errorf("failed backup still wiped %s (%s)", kind, p)
		}
	}
}

// TestRunRebaseLeavesDefaultsVirtual is the invariant sty_cc550a88 exists to
// establish: after a rebase, the authored dirs hold nothing but their READMEs.
//
// Under virtual sparse defaults (sty_29e5a9a5) the known-good default solution
// IS the empty authored dir — List/Get overlay the embedded bytes at read time.
// Seeding after the wipe DEFEATS the reset: it lands stamped copies that shadow
// the shipped defaults, so the next binary upgrade leaves the repo running a
// frozen fork. That is precisely the state sty_5604e741 had to delete by hand.
//
// Written to FAIL against the pre-change behaviour, where rebase restored all
// three representatives below.
func TestRunRebaseLeavesDefaultsVirtual(t *testing.T) {
	dataDir := t.TempDir()
	seedCustomSubstrate(t, dataDir)

	var out strings.Builder
	if err := runRebase(&out, strings.NewReader(""), dataDir, dataDir, true, rebaseTestTime); err != nil {
		t.Fatalf("runRebase: %v", err)
	}

	for _, kind := range []string{"workflows", "skills", "principles"} {
		ents, err := os.ReadDir(filepath.Join(dataDir, kind))
		if err != nil {
			t.Fatalf("read %s: %v", kind, err)
		}
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		if len(names) != 1 || names[0] != "README.md" {
			t.Errorf("%s/ must hold only its README after a rebase — the overlay IS the reset; got %v", kind, names)
		}
	}

	// One representative per seeding path that used to run, so a partial
	// re-wiring of any of the three is caught.
	for _, rel := range []string{
		"skills/satelle-story-done-review.md", // a route gate skill
		"skills/satelle-workflow-advisor.md",  // an advisory skill
		"principles/satelle-agent-goals.md",   // an embedded principle
	} {
		if _, err := os.Stat(filepath.Join(dataDir, filepath.FromSlash(rel))); err == nil {
			t.Errorf("rebase re-seeded %s — a materialised copy shadows the shipped default", rel)
		}
	}
}

// TestRunRebasePreservesAuthoredAgentsLayer: the agents layer lives inside a
// rebaseKind (workflows/) but is repo CONFIGURATION, not default substrate. It
// has no embedded counterpart, so the overlay cannot heal its absence and an
// initialized repo without it refuses to run — wiping it BRICKS the repo rather
// than resetting it (sty_72ccafaa). It must survive with its authored bytes.
func TestRunRebasePreservesAuthoredAgentsLayer(t *testing.T) {
	dataDir := t.TempDir()
	seedCustomSubstrate(t, dataDir)

	agents := filepath.Join(dataDir, filepath.FromSlash(config.AgentsRel))
	if err := os.MkdirAll(filepath.Dir(agents), 0o755); err != nil {
		t.Fatal(err)
	}
	authored := "[reviewer]\nmodel = \"authored-by-the-operator\"\n"
	if err := os.WriteFile(agents, []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runRebase(&out, strings.NewReader(""), dataDir, dataDir, true, rebaseTestTime); err != nil {
		t.Fatalf("runRebase: %v", err)
	}

	got, err := os.ReadFile(agents)
	if err != nil {
		t.Fatalf("rebase wiped the agents layer — the repo is now unrunnable: %v", err)
	}
	if string(got) != authored {
		t.Errorf("the authored agents layer must survive byte-for-byte, not be reseeded:\ngot  %q\nwant %q", got, authored)
	}
	// The backup still holds its copy, so the pre-rebase tree remains a complete undo.
	backed := filepath.Join(dataDir, "backups", "20260702-030405", filepath.FromSlash(config.AgentsRel))
	if _, serr := os.Stat(backed); serr != nil {
		t.Errorf("the backup must still carry the agents layer: %v", serr)
	}
	if !strings.Contains(out.String(), "preserved") {
		t.Errorf("rebase must REPORT what it preserved:\n%s", out.String())
	}
}

// TestRebaseHelpNamesEveryPreservedPath binds the help to the behaviour: a file
// added to rebasePreserve without a word in the help would be an invisible
// carve-out, and rebase's text promising something it does not do is the exact
// defect sty_72ccafaa was.
func TestRebaseHelpNamesEveryPreservedPath(t *testing.T) {
	long := findCommandLong(t, "rebase")
	for _, rel := range rebasePreserve {
		if !strings.Contains(long, rel) {
			t.Errorf("rebase help does not name the preserved path %q", rel)
		}
	}
}
