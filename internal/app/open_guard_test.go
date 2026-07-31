package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// homeEntries snapshots the top-level listing of the isolated home plane, so a
// test can assert that a command created NOTHING there.
func homeEntries(t *testing.T, home string) []string {
	t.Helper()
	ents, err := os.ReadDir(home)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read home %s: %v", home, err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// TestOpenRefusesUngovernedRepoWithoutWriting (sty_20a7824c AC1, AC4): a repo
// with no .satelle/ is refused BEFORE store.Open and WriteRepoPathMarker run, so
// no plane directory, no repo.path and no satelle.db appear under the home
// plane. Before the guard, any store-backed verb materialised all three for a
// repo satelle does not govern.
func TestOpenRefusesUngovernedRepoWithoutWriting(t *testing.T) {
	home := testutil.IsolateHome(t)
	t.Setenv("SATELLE_CONFIG", "")
	repo := t.TempDir()
	t.Chdir(repo)

	before := homeEntries(t, home)

	a, err := Open()
	if err == nil {
		_ = a.Close()
		t.Fatal("Open must refuse a repo with no .satelle/")
	}
	if !errors.Is(err, ErrNotInitialised) {
		t.Fatalf("want ErrNotInitialised, got %T: %v", err, err)
	}

	after := homeEntries(t, home)
	if len(after) != len(before) {
		t.Fatalf("Open must not write to the home plane: before=%v after=%v", before, after)
	}
	// Belt and braces: nothing anywhere under home, at any depth.
	var found []string
	_ = filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
		if err != nil || p == home {
			return nil //nolint:nilerr // walk best-effort
		}
		found = append(found, p)
		return nil
	})
	if len(found) != 0 {
		t.Fatalf("home plane must be untouched, found:\n%v", found)
	}
}

// TestOpenErrorNamesTheRemedy (AC2): the refusal is an answer, not just a
// failure — it names the path checked and the command that fixes it.
func TestOpenErrorNamesTheRemedy(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("SATELLE_CONFIG", "")
	t.Chdir(t.TempDir())

	_, err := Open()
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"not a satelle repo", "satelle init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q, got: %v", want, err)
		}
	}
}

// TestOpenAcceptsZeroConfigRepo (AC7, and the guard's key design point): the
// guard keys on the .satelle DIRECTORY, not on satelle.toml. Zero-config is
// supported, so a repo carrying .satelle/ with no satelle.toml is governed and
// must open — keying on the toml would refuse it.
func TestOpenAcceptsZeroConfigRepo(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("SATELLE_CONFIG", "")
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, config.DefaultDataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	a, err := Open()
	if err != nil {
		t.Fatalf("a .satelle/ directory with no satelle.toml is a governed repo: %v", err)
	}
	defer func() { _ = a.Close() }()
	if a.Store == nil {
		t.Fatal("expected a wired store")
	}
}

// TestOpenGovernedRepoStillWritesBothMarkers (AC6): the guard must not have
// disabled the two writes for a governed repo — the runtime plane, the
// repo.path marker and a migrated database are all still produced.
func TestOpenGovernedRepoStillWritesBothMarkers(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("SATELLE_CONFIG", "")
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, config.DefaultDataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	a, err := Open()
	if err != nil {
		t.Fatalf("governed repo must open: %v", err)
	}
	defer func() { _ = a.Close() }()

	marker := filepath.Join(a.RuntimeDir, config.RepoPathMarkerName)
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("repo.path marker must still be written: %v", err)
	}
	if got := string(body); !strings.Contains(got, filepath.Base(repo)) {
		t.Errorf("repo.path = %q, want it to name the repo root", got)
	}
	if _, err := os.Stat(a.DBPath); err != nil {
		t.Errorf("database must still be created: %v", err)
	}
}

// TestStoreOpenStillCreatesOnDemand (AC6): the guard lives at app.Open, NOT in
// store.Open — `satelle init` reaches store.Open directly and must keep its
// create-on-open behaviour. This is the fence that stops a future change from
// "fixing" the defect one layer too deep and breaking init.
func TestStoreOpenStillCreatesOnDemand(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "satelle.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open must still create parent dirs and the db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("db not created: %v", err)
	}
}
