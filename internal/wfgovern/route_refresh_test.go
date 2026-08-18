package wfgovern_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestSpecForSeesRevertedFileWithoutReindex(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	wfDir := filepath.Join(root, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	donePath := filepath.Join(wfDir, "done.toml")
	if err := os.WriteFile(donePath, []byte(routeDone), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "step.toml"), []byte(routeStep), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.DocIndex.SetRoots(map[string]string{"workflows": wfDir})
	if _, err := db.DocIndex.Sync(ctx, map[string]string{"workflows": wfDir}, time.Now()); err != nil {
		t.Fatal(err)
	}

	item := workitem.Item{ID: "sty_1", Category: "feature"}
	docs, err := db.DocIndex.List(ctx, "workflows")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := wfgovern.SpecFor(docs, item); err != nil {
		t.Fatalf("good file must resolve: %v", err)
	}

	broken := `[meta]
name = "done"
type = "workflow"
scope = "system"
description = "broken park.agent."

["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked", agent = "reviewer", gate = "blocked-review" }
`
	if err := os.WriteFile(donePath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(donePath, time.Now().Add(2*time.Second), time.Now().Add(2*time.Second))
	docs, err = db.DocIndex.List(ctx, "workflows")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := wfgovern.SpecFor(docs, item); err == nil {
		t.Fatal("unknown park.agent must refuse")
	}

	if err := os.WriteFile(donePath, []byte(routeDone), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(donePath, time.Now().Add(4*time.Second), time.Now().Add(4*time.Second))
	docs, err = db.DocIndex.List(ctx, "workflows")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := wfgovern.SpecFor(docs, item); err != nil {
		t.Fatalf("reverted file must restore governance without reindex: %v", err)
	}
}
