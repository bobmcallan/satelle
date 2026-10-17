package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workspace"
)

func register(t *testing.T, roots ...string) {
	t.Helper()
	t.Setenv("SATELLE_HOME", t.TempDir())
	if err := config.SaveGlobal(config.GlobalConfig{Workspace: config.WorkspaceConfig{Repos: roots}}); err != nil {
		t.Fatal(err)
	}
}

func TestFindRegisteredByRepoName(t *testing.T) {
	base := t.TempDir()
	srv := filepath.Join(base, "satelle-server")
	cli := filepath.Join(base, "satelle")
	for _, d := range []string{srv, cli} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	register(t, srv, cli, srv) // a duplicate registry entry is one repo

	got, err := workspace.FindRegistered("satelle-server")
	if err != nil || len(got) != 1 || got[0] != srv {
		t.Fatalf("FindRegistered(satelle-server) = %v, %v; want [%s]", got, err, srv)
	}
	got, err = workspace.FindRegistered("nope")
	if err != nil || len(got) != 0 {
		t.Fatalf("FindRegistered(nope) = %v, %v; want none", got, err)
	}
}

func TestFindRegisteredDuplicateBasenamesYieldTwoRoots(t *testing.T) {
	base := t.TempDir()
	a, b := filepath.Join(base, "a", "x"), filepath.Join(base, "b", "x")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	register(t, a, b)

	got, err := workspace.FindRegistered("x")
	if err != nil || len(got) != 2 {
		t.Fatalf("FindRegistered(x) = %v, %v; want the two roots", got, err)
	}
}

func TestLoadRepoReadOnlyReadsStoriesAndLeavesTheDBAlone(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	dir := t.TempDir()
	seedRepo(t, dir, "one", "two")
	db := config.Config{}.ResolveDB(dir)

	before, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	rv, err := workspace.LoadRepoReadOnly(context.Background(), dir)
	if err != nil {
		t.Fatalf("LoadRepoReadOnly: %v", err)
	}
	if len(rv.Stories) != 2 || len(rv.Workflows) == 0 {
		t.Fatalf("want 2 stories and the workflow docs, got %d stories, %d workflows", len(rv.Stories), len(rv.Workflows))
	}
	after, err := os.ReadFile(db)
	if err != nil || string(before) != string(after) {
		t.Fatalf("the sibling database file changed (err %v)", err)
	}
}

func TestLoadRepoReadOnlyNamesWhatCannotBeRead(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())

	notRepo := t.TempDir()
	if _, err := workspace.LoadRepoReadOnly(context.Background(), notRepo); err == nil {
		t.Error("a dir with no .satelle must be an error")
	}

	noDB := t.TempDir()
	if err := os.MkdirAll(filepath.Join(noDB, config.DefaultDataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := workspace.LoadRepoReadOnly(context.Background(), noDB)
	if err == nil {
		t.Fatal("a repo with no database must be an error")
	}
	if _, serr := os.Stat(config.Config{}.ResolveDB(noDB)); !os.IsNotExist(serr) {
		t.Errorf("reading must not create the database, stat err = %v", serr)
	}

	badWF := t.TempDir()
	seedRepo(t, badWF, "x")
	wf := filepath.Join(badWF, config.DefaultDataDir, "workflows")
	if err := os.WriteFile(wf, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.LoadRepoReadOnly(context.Background(), badWF); err == nil || !strings.Contains(err.Error(), "workflows") {
		t.Errorf("an unreadable workflows dir must be named, got %v", err)
	}
}
