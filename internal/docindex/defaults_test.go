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
	})
	return st
}

func TestDefaultResolvesByGetButIsNotListed(t *testing.T) {
	st := defaultsStore(t)
	ctx := context.Background()

	// Get STILL resolves an embedded default by name (the gating baseline +
	// on-demand principles) — the by-name fallback is retained (sty_94da9ac9, Option B).
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
	if got.Hash == "" || got.Path == "" {
		t.Errorf("default not normalised: hash=%q path=%q", got.Hash, got.Path)
	}

	// But List/Count enumerate ONLY on-disk docs — an embedded default is never
	// listed as a project doc (so e.g. the web doesn't show the embedded baseline).
	list, err := st.List(ctx, "workflows")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("embedded defaults must NOT appear in List, got %d", len(list))
	}
	if n, _ := st.Count(ctx, "workflows"); n != 0 {
		t.Errorf("Count = %d, want 0 (embedded defaults are not listed)", n)
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
