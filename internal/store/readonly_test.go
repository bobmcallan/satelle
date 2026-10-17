package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestReadOnlyDSNIsReadOnly(t *testing.T) {
	dsn := ReadOnlyDSN(filepath.Join("a", "b.db"))
	if !strings.Contains(dsn, "mode=ro") {
		t.Fatalf("ReadOnlyDSN must open mode=ro, got %q", dsn)
	}
	if strings.Contains(dsn, "query_only") {
		t.Fatalf("ReadOnlyDSN must leave query_only to OpenReadOnly (VACUUM INTO), got %q", dsn)
	}
}

func TestOpenReadOnlyRefusesAbsentPathWithoutCreatingIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "satelle.db")
	if db, err := OpenReadOnly(path); err == nil {
		db.Close()
		t.Fatal("OpenReadOnly of an absent path must error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the database must not be created, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("the parent dir must not be created, stat err = %v", err)
	}
}

func TestOpenReadOnlyReadsButRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".satelle", "satelle.db")
	rw, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := rw.Stories.Create(context.Background(), workitem.CreateInput{Kind: workitem.KindStory, Title: "seed"}, time.Now()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rw.Close()

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()

	items, err := ro.Stories.List(context.Background(), workitem.ListFilter{Kind: workitem.KindStory})
	if err != nil || len(items) != 1 {
		t.Fatalf("read-only list = %d items, err %v; want the seeded story", len(items), err)
	}
	if _, err := ro.SQL().Exec(`CREATE TABLE should_not_exist (x INTEGER)`); err == nil {
		t.Fatal("a read-only connection must reject a write")
	}
	if _, err := ro.SQL().Exec(`DELETE FROM authored_docs`); err == nil {
		t.Fatal("a read-only connection must reject a delete")
	}
}
