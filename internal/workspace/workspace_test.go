package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
	"github.com/bobmcallan/satelle/internal/workspace"
)

// seedRepo creates a repo at dir with the given story titles under the
// home-keyed runtime plane (sty_4660bbe1). Isolates SATELLE_HOME for this test.
func seedRepo(t *testing.T, dir string, titles ...string) {
	t.Helper()
	// Ensure config exists so ResolveDB can load when needed.
	dataDir := filepath.Join(dir, config.DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dataDir, config.ConfigName)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := os.WriteFile(cfgPath, []byte("web_port = 8181\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := config.Config{}.ResolveDB(dir)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ti := range titles {
		if _, err := db.Stories.Create(context.Background(),
			workitem.CreateInput{Kind: workitem.KindStory, Title: ti}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadAggregatesAcrossRepos(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	a, b := t.TempDir(), t.TempDir()
	seedRepo(t, a, "a-one", "a-two")
	seedRepo(t, b, "b-one")

	agg := workspace.Load(context.Background(), []string{a, b})
	if len(agg.Repos) != 2 {
		t.Fatalf("want 2 repo views, got %d", len(agg.Repos))
	}
	stories, _, _ := agg.Totals()
	if stories != 3 {
		t.Errorf("aggregated stories = %d, want 3", stories)
	}
	if agg.Repos[0].Name != filepath.Base(a) || len(agg.Repos[0].Stories) != 2 {
		t.Errorf("repo A view wrong: %+v", agg.Repos[0])
	}
	if len(agg.Repos[1].Stories) != 1 {
		t.Errorf("repo B should have 1 story, got %d", len(agg.Repos[1].Stories))
	}
}

func TestLoadEmptyRepoIsBenign(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	// A path with no prior db yields an empty (created) repo view, not an error.
	agg := workspace.Load(context.Background(), []string{t.TempDir()})
	if len(agg.Repos) != 1 || agg.Repos[0].Err != "" {
		t.Fatalf("empty repo should load cleanly: %+v", agg.Repos)
	}
	if s, _, _ := agg.Totals(); s != 0 {
		t.Errorf("empty repo totals should be 0, got %d", s)
	}
}
