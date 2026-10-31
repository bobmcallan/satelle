package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

func disableServeProbe(t *testing.T) {
	t.Helper()
	prev := serveProbeEnabled
	serveProbeEnabled = false
	t.Cleanup(func() { serveProbeEnabled = prev })
}

// liveLegacyFixture builds a legacy-layout repo whose in-repo DB holds a
// transition in flight, started by this (live) process.
func liveLegacyFixture(t *testing.T) (home, repo, dataDir, legacyDB string, a *app.App) {
	t.Helper()
	disableServeProbe(t)
	home = t.TempDir()
	t.Setenv("SATELLE_HOME", home)

	repo = t.TempDir()
	dataDir = filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "agents.toml"), []byte("[executor]\nharness = \"in-loop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "satelle.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyDB = filepath.Join(dataDir, config.DefaultDBName)
	db, err := store.Open(legacyDB)
	if err != nil {
		t.Fatal(err)
	}
	startTransitionOn(t, db, "sty_livetest", "integration")
	_ = db.Close()

	a = &app.App{
		Config:     config.Config{},
		RepoRoot:   repo,
		DataDir:    dataDir,
		RuntimeDir: dataDir,
		DBPath:     legacyDB,
	}
	return home, repo, dataDir, legacyDB, a
}

// TestMigrateRefusesOpenTransition (sty_5308eb60 AC1+AC2): a transition in
// flight in the legacy DB causes migrate --yes to exit non-zero without creating
// the home-keyed DB or removing residue.
func TestMigrateRefusesOpenTransition(t *testing.T) {
	home, _, dataDir, legacyDB, a := liveLegacyFixture(t)
	// Residue bait: logs/ under dataDir must survive a refused migrate.
	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	err := runMigrate(&out, a, true, false)
	if err == nil {
		t.Fatalf("expected refuse, got nil\n%s", out.String())
	}
	msg := err.Error()
	for _, want := range []string{"LIVE", "sty_livetest", "transition in flight", "integration"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q:\n%s", want, msg)
		}
	}
	homeDB := filepath.Join(home, config.RepoKey(a.RepoRoot), config.DefaultDBName)
	if fileExists(homeDB) {
		t.Errorf("home-keyed DB must not be created on refuse: %s", homeDB)
	}
	if !fileExists(legacyDB) {
		t.Error("legacy DB must remain on refuse")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "logs")); err != nil {
		t.Error("legacy residue must not be removed on refuse")
	}
}

// TestRuntimeMigrateRefusesOpenTransition covers the public runtime migrate
// entrypoint.
func TestRuntimeMigrateRefusesOpenTransition(t *testing.T) {
	home, _, _, _, a := liveLegacyFixture(t)
	var out strings.Builder
	err := runRuntimeMigrate(&out, a, false, false, false)
	if err == nil {
		t.Fatalf("expected refuse, got nil\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "sty_livetest") {
		t.Errorf("refusal should name the transition's story: %v", err)
	}
	homeDB := filepath.Join(home, config.RepoKey(a.RepoRoot), config.DefaultDBName)
	if fileExists(homeDB) {
		t.Errorf("home DB must not exist after refuse: %s", homeDB)
	}
}

// holdersGone makes the pid probe say the process that started a planted
// transition is gone: a planted transition carries this test process's own
// (live) pid, so a simulated kill needs the probe to agree.
func holdersGone(t *testing.T) {
	t.Helper()
	prev := ledger.PidAlive
	ledger.PidAlive = func(int) bool { return false }
	t.Cleanup(func() { ledger.PidAlive = prev })
}

// TestMigrateProceedsWhenTransitionDead: the guard keys on liveness, not row
// presence — a transition whose process died is not in flight.
func TestMigrateProceedsWhenTransitionDead(t *testing.T) {
	holdersGone(t)
	home, _, _, legacyDB, a := liveLegacyFixture(t)

	var out strings.Builder
	if err := runMigrate(&out, a, true, false); err != nil {
		t.Fatalf("a dead transition must not refuse: %v\n%s", err, out.String())
	}
	homeDB := filepath.Join(home, config.RepoKey(a.RepoRoot), config.DefaultDBName)
	if !fileExists(homeDB) {
		t.Fatalf("home DB missing after migrate with a dead transition\n%s", out.String())
	}
	// Residue removed once home exists.
	if fileExists(legacyDB) {
		t.Error("legacy DB should be removed as residue after successful migrate")
	}
}

// TestMigrateAllowLiveWarnsAndProceeds (AC3): --allow-live proceeds and states risk.
func TestMigrateAllowLiveWarnsAndProceeds(t *testing.T) {
	home, _, _, _, a := liveLegacyFixture(t)
	var out strings.Builder
	if err := runMigrate(&out, a, true, true); err != nil {
		t.Fatalf("allow-live should proceed: %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"--allow-live", "STRANDED", "sty_livetest"} {
		if !strings.Contains(s, want) {
			t.Errorf("allow-live output missing %q:\n%s", want, s)
		}
	}
	homeDB := filepath.Join(home, config.RepoKey(a.RepoRoot), config.DefaultDBName)
	if !fileExists(homeDB) {
		t.Error("home DB should exist after allow-live migrate")
	}
}

// TestMigrateHelpDocumentsSplitBrainRecovery (AC4).
func TestMigrateHelpDocumentsSplitBrainRecovery(t *testing.T) {
	if !strings.Contains(migrateSplitBrainHelp, "satelle ledger list --story") {
		t.Error("split-brain help must name satelle ledger list")
	}
	if !strings.Contains(migrateSplitBrainHelp, "through the workflow") {
		t.Error("split-brain help must tell operators to re-drive through gates")
	}
	// Command Long embeds the const.
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"migrate"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd.Long, migrateSplitBrainHelp) {
		t.Error("satelle migrate Long must embed migrateSplitBrainHelp")
	}
	if !strings.Contains(cmd.Long, "--allow-live") && !strings.Contains(cmd.Long, "LIVE RUNTIME") {
		t.Error("Long should document live-runtime refusal")
	}
}

// TestProbeServeViaHealthz: serve probe returns a holder when /healthz is ok.
func TestProbeServeViaHealthz(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	prev := healthzOK
	healthzOK = func(url string) bool {
		// Redirect any probe URL to our test server.
		resp, err := http.Get(srv.URL + "/healthz")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == 200
	}
	t.Cleanup(func() { healthzOK = prev })

	prevProbe := serveProbeEnabled
	serveProbeEnabled = true
	t.Cleanup(func() { serveProbeEnabled = prevProbe })

	h, ok := probeServe(config.Config{}, "/unused")
	if !ok {
		t.Fatal("expected serve holder")
	}
	if h.Kind != "serve" {
		t.Errorf("kind = %q", h.Kind)
	}
}

// TestScanOpenTransitionsMissingTable: a DB that predates the ledger is quiet.
func TestScanOpenTransitionsMissingTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`DROP TABLE evidence`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()
	holders, err := scanOpenTransitions(path)
	if err != nil {
		t.Fatalf("missing table should be quiet: %v", err)
	}
	if len(holders) != 0 {
		t.Fatalf("want no holders, got %v", holders)
	}
}
