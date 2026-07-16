package docindex

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// defaultsStore returns a Store seeded with one embedded default workflow.
func defaultsStore(t *testing.T) *Store {
	t.Helper()
	st := New(openDB(t))
	st.SetDefaults([]Doc{
		{Kind: "workflows", Name: "satelle-baseline-workflow", Body: "# Canonical\n\nembedded default body"},
		{Kind: "principles", Name: "satelle-agent-goals", Body: "# Goals\n\ngoals body"},
	})
	return st
}

func TestDefaultResolvesAndIsListedWhenDiskEmpty(t *testing.T) {
	st := defaultsStore(t)
	ctx := context.Background()

	got, err := st.Get(ctx, "workflows", "satelle-baseline-workflow")
	if err != nil {
		t.Fatalf("Get embedded default: %v", err)
	}
	if !got.Embedded {
		t.Errorf("expected Embedded=true for a default-sourced doc")
	}
	if got.Headline != "Canonical" {
		t.Errorf("headline not derived from default body: %q", got.Headline)
	}

	// Virtual sparse defaults (sty_29e5a9a5): List overlays defaults when disk empty.
	list, err := st.List(ctx, "workflows")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Embedded {
		t.Fatalf("List should return the virtual default, got %+v", list)
	}
	if n, _ := st.Count(ctx, "workflows"); n != 1 {
		t.Errorf("Count = %d, want 1", n)
	}
	// All-kinds List is sorted by (kind,name).
	all, err := st.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all-kinds List = %d, want 2", len(all))
	}
	if all[0].Kind != "principles" || all[1].Kind != "workflows" {
		t.Errorf("expected principles then workflows, got %s then %s", all[0].Kind, all[1].Kind)
	}
	if n, _ := st.Count(ctx, ""); n != len(all) {
		t.Errorf("Count != len(List): %d vs %d", n, len(all))
	}
}

func TestDiskDocShadowsDefault(t *testing.T) {
	st := defaultsStore(t)
	ctx := context.Background()

	// A disk file with the same (kind, name) overrides the embedded default.
	dir := filepath.Join(t.TempDir(), "workflows")
	write(t, dir, "satelle-baseline-workflow.md", "# Override\n\nrepo override body")
	if _, err := st.Sync(ctx, map[string]string{"workflows": dir}, time.Now()); err != nil {
		t.Fatal(err)
	}

	got, err := st.Get(ctx, "workflows", "satelle-baseline-workflow")
	if err != nil {
		t.Fatal(err)
	}
	if got.Embedded {
		t.Errorf("disk override should not be marked Embedded")
	}
	if got.Headline != "Override" {
		t.Errorf("expected the disk override to win, got headline %q", got.Headline)
	}

	list, err := st.List(ctx, "workflows")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("override must not duplicate the default: got %d rows", len(list))
	}
	if list[0].Embedded {
		t.Errorf("listed row should be the disk override, not the default")
	}
	if n, _ := st.Count(ctx, "workflows"); n != 1 {
		t.Errorf("Count = %d, want 1 (default shadowed by disk)", n)
	}
}

func TestListDoesNotInsertDefaultRows(t *testing.T) {
	st := defaultsStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	// Sync empty dirs — must not materialise defaults into the table.
	if _, err := st.Sync(ctx, map[string]string{
		"workflows":  filepath.Join(dir, "workflows"),
		"principles": filepath.Join(dir, "principles"),
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Virtual list still sees defaults.
	if n, _ := st.Count(ctx, "workflows"); n != 1 {
		t.Errorf("virtual Count workflows = %d, want 1", n)
	}
	// But raw SQL has zero rows.
	var raw int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM authored_docs`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != 0 {
		t.Errorf("Sync must not insert default rows into authored_docs, got %d", raw)
	}
}
