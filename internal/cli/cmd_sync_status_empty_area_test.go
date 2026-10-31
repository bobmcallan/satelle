package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/doctor"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// Tests for sty_09b6b0e4: an opted-in area holding only init scaffold has nothing
// to push and nothing hosted, so it is in step — in `sync status`, in doctor and
// in the session-start warning — and no area's NEXT is a push that cannot act.

const scaffoldOnlyToml = "[sync]\nall = \"personal\"\n" + boundProjectToml

// scaffoldOnlyMachine is a checkout opted in to every area whose only content is
// unedited embedded defaults (which a push withholds), with a signed-in server.
func scaffoldOnlyMachine(t *testing.T) (*snapMachine, *snapFake) {
	t.Helper()
	testutil.IsolateHome(t)
	ts, f := newSnapServer(t)
	seedCred(t, ts.URL)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}
	m := &snapMachine{t: t, url: ts.URL, repo: syncConfigRepo(t, scaffoldOnlyToml), state: filepath.Join(t.TempDir(), "document-sync-state.json")}
	if err := os.Remove(filepath.Join(m.repo, ".satelle", "agents.toml")); err != nil {
		t.Fatal(err)
	}
	m.write("skills/satelle-step-summary.md", stampedDefault(embeddedBodyOf(t, "skills", "satelle-step-summary")))
	m.write("principles/satelle-agent-goals.md", stampedDefault(embeddedBodyOf(t, "principles", "satelle-agent-goals")))
	m.write("tasks/tsk_substrate-audit.md", stampedDefault(embeddedBodyOf(t, "tasks", "tsk_substrate-audit")))
	return m, f
}

func TestSyncStatusScaffoldOnlyInStep(t *testing.T) {
	m, _ := scaffoldOnlyMachine(t)
	m.pushConfig()
	m.pushDocs()

	out, err := m.status()
	if err != nil {
		t.Fatalf("a scaffold-only repo must exit zero: %v\n%s", err, out)
	}
	areas := append(append([]string{}, config.ConfigAreas...), "documents")
	for _, area := range areas {
		cols := wantState(t, out, area, "in step")
		if cols[4] != "-" {
			t.Errorf("%s next = %q, want -", area, cols[4])
		}
	}
	if strings.Contains(out, "not in step") || strings.Contains(out, "satelle sync push") {
		t.Errorf("a scaffold-only repo must report nothing out of step nor suggest a push:\n%s", out)
	}

	// The session-start warning (the live advisory) stays silent.
	m.use()
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		t.Fatal(err)
	}
	if line := substrateAdvisoryLive(context.Background(), cfg, repoRoot, dataDir); line != "" {
		t.Errorf("session-start advisory = %q, want none", line)
	}

	// doctor reads the state that check recorded and raises nothing.
	if got := doctor.CheckSubstrateSync(repoRoot, dataDir, config.GlobalDir(), time.Now()); !got.OK() {
		t.Errorf("doctor raised %v for a scaffold-only repo", got)
	}
}

// An area whose only content is hosted (per-file heads, no snapshot) is behind
// and says to pull; once it gains authored content it is not in step until pushed.
func TestSyncStatusHostedOnlyAreaIsBehindThenAuthoredIsUnpushed(t *testing.T) {
	m, f := scaffoldOnlyMachine(t)
	f.cfg.put("probe", "skills/team.md", []byte("team skill\n"))

	out, _ := m.status()
	if cols := wantState(t, out, "skills", "behind"); cols[4] != "satelle sync pull" {
		t.Errorf("skills next = %q, want satelle sync pull", cols[4])
	}
	wantState(t, out, "principles", "in step")

	m.use()
	cfg, repoRoot, dataDir, err := loadRepoConfig()
	if err != nil {
		t.Fatal(err)
	}
	if line := substrateAdvisoryLive(context.Background(), cfg, repoRoot, dataDir); !strings.Contains(line, "skills behind") {
		t.Errorf("session-start advisory = %q, want it to name skills behind", line)
	}

	m.write("principles/mine.md", "authored\n")
	out, _ = m.status()
	if cols := wantState(t, out, "principles", "never synced"); cols[4] != "satelle sync push" {
		t.Errorf("principles next = %q, want satelle sync push", cols[4])
	}
}
