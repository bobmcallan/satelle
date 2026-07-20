package mirror_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/mirror"
)

func TestMirrorPartitionedUpsertAndList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mirror.db")
	s, err := mirror.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	now := time.Now()

	if _, err := s.TouchPartition(ctx, "repo-a", "alpha", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TouchPartition(ctx, "repo-b", "beta", now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertItem(ctx, "repo-a", "story", "sty_1", map[string]any{"id": "sty_1", "title": "A"}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertItem(ctx, "repo-b", "story", "sty_1", map[string]any{"id": "sty_1", "title": "B"}, now); err != nil {
		t.Fatal(err)
	}

	a, err := s.ListItems(ctx, "repo-a", "story")
	if err != nil || len(a) != 1 {
		t.Fatalf("repo-a items = %v err=%v", a, err)
	}
	b, err := s.ListItems(ctx, "repo-b", "story")
	if err != nil || len(b) != 1 {
		t.Fatalf("repo-b items = %v err=%v", b, err)
	}
	if a[0].Payload == b[0].Payload {
		t.Error("partitions must not share item payloads")
	}

	parts, err := s.ListPartitions(ctx)
	if err != nil || len(parts) != 2 {
		t.Fatalf("partitions = %v err=%v", parts, err)
	}
}

func TestFindBySlug(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mirror.db")
	s, err := mirror.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	now := time.Now()
	if _, err := s.TouchPartition(ctx, "rk1", "alpha", now); err != nil {
		t.Fatal(err)
	}
	p, ok, err := s.FindBySlug(ctx, "alpha")
	if err != nil || !ok || p.RepoKey != "rk1" {
		t.Fatalf("FindBySlug alpha = %+v ok=%v err=%v", p, ok, err)
	}
	_, ok, err = s.FindBySlug(ctx, "missing")
	if err != nil || ok {
		t.Fatalf("missing: ok=%v err=%v", ok, err)
	}
	_, ok, err = s.FindBySlug(ctx, "")
	if err != nil || ok {
		t.Fatalf("empty: ok=%v err=%v", ok, err)
	}
}

func TestReplaceKindIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	s, err := mirror.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	now := time.Now()
	_ = s.UpsertItem(ctx, "r", "story", "old", map[string]string{"id": "old"}, now)

	items := []mirror.ItemRow{
		{ID: "new1", Payload: `{"id":"new1"}`},
		{ID: "new2", Payload: `{"id":"new2"}`},
	}
	if err := s.ReplaceKind(ctx, "r", "story", items, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceKind(ctx, "r", "story", items, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListItems(ctx, "r", "story")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v err=%v", got, err)
	}
	for _, g := range got {
		if g.ID == "old" {
			t.Error("old item should be deleted by replace")
		}
	}
}
