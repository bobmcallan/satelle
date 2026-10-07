package cli

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
)

const ledgerPushToml = "[sync]\nstories = \"personal\"\nledger = \"personal\"\n\n[hosted]\nproject = \"probe\"\n"

func (f *fakeWorkstateServer) hasLedger(project, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.ledgerByID[project][id]
	return ok
}

// withRepoStore opens the repo's database between runRoot calls (which close
// theirs), the way the other fixtures here do.
func withRepoStore(t *testing.T, fn func(db *store.DB)) {
	t.Helper()
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	fn(db)
}

func upsertLedgerRow(t *testing.T, db *store.DB, id string, at time.Time) {
	t.Helper()
	if _, err := db.Ledger.Upsert(context.Background(), ledger.Entry{
		ID: id, StoryID: "sty_cursor", Kind: ledger.KindStoryUpdated, Body: id, CreatedAt: at,
	}, time.Now()); err != nil {
		t.Fatalf("upsert %s: %v", id, err)
	}
}

func pushOK(t *testing.T, server string) string {
	t.Helper()
	out, err := runRoot(t, "sync", "workstate", "push", "--server", server)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	return out
}

func createProbeStory(t *testing.T) {
	t.Helper()
	if out, err := runRoot(t, "story", "create", "--title", "Ledger cursor probe",
		"--body", "A story for the ledger cursor tests.", "--acceptance", "1. pushed"); err != nil {
		t.Fatalf("story create: %v\n%s", err, out)
	}
}

// TestWorkstatePushSendsLateCommittedLedgerRow pins sty_4a31e1ed AC2: a ledger
// row inserted after a push is sent by the next push even when its created_at is
// behind the high-water mark the first push saved.
func TestWorkstatePushSendsLateCommittedLedgerRow(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	workstateRepo(t, ledgerPushToml)
	createProbeStory(t)
	pushOK(t, ts.URL)

	withRepoStore(t, func(db *store.DB) {
		upsertLedgerRow(t, db, "evt_late0001", time.Now().Add(-time.Hour))
	})
	pushOK(t, ts.URL)
	if !f.hasLedger("probe", "evt_late0001") {
		t.Fatal("a ledger row inserted after the push with an older created_at was never sent")
	}
}

// TestWorkstatePushLegacyCursorHealsStrandedRows pins AC3: a cursor saved by a
// created_at-only binary does not hide the rows it stranded, and the push
// upgrades it to the insertion-order fields.
func TestWorkstatePushLegacyCursorHealsStrandedRows(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	repo := workstateRepo(t, ledgerPushToml)
	createProbeStory(t)

	var stranded string
	withRepoStore(t, func(db *store.DB) {
		stranded = "evt_stranded"
		upsertLedgerRow(t, db, stranded, time.Now().Add(-time.Hour))
	})
	future := time.Now().Add(time.Hour)
	if err := hosted.SaveWorkstateCursor(ts.URL, "probe", repo, hosted.WorkstateCursor{
		ItemsUpdatedAt: future, LedgerCreatedAt: future,
	}); err != nil {
		t.Fatal(err)
	}

	pushOK(t, ts.URL)
	if !f.hasLedger("probe", stranded) {
		t.Fatal("the legacy cursor hid a ledger row the server lacks")
	}
	got, err := hosted.LoadWorkstateCursor(ts.URL, "probe", repo)
	if err != nil {
		t.Fatal(err)
	}
	if got.LedgerSeq <= 0 || got.LedgerAnchorID == "" || got.LedgerStoreID == "" {
		t.Fatalf("saved cursor lacks the insertion-order fields: %+v", got)
	}
}

// TestWorkstatePushCursorFromReplacedStoreResets pins AC4 (rehydrate): a saved
// position from another database — high above this one's rows — is dropped and
// the next push sends the full ledger, including a row inserted since.
func TestWorkstatePushCursorFromReplacedStoreResets(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	repo := workstateRepo(t, ledgerPushToml)
	createProbeStory(t)
	pushOK(t, ts.URL)

	// Rehydrate: the database under the same repo key is replaced by a fresh one.
	dbPath := runtimeDBPath(t)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}
	if err := hosted.SaveWorkstateCursor(ts.URL, "probe", repo, hosted.WorkstateCursor{
		ItemsUpdatedAt: time.Now().Add(time.Hour),
		LedgerSeq:      10000, LedgerAnchorID: "evt_foreign", LedgerStoreID: "another-database",
	}); err != nil {
		t.Fatal(err)
	}
	withRepoStore(t, func(db *store.DB) {
		upsertLedgerRow(t, db, "evt_fresh001", time.Now())
	})

	out := pushOK(t, ts.URL)
	if !f.hasLedger("probe", "evt_fresh001") {
		t.Fatalf("a row in the replaced store was never sent\n%s", out)
	}
	if !strings.Contains(out, "ledger cursor reset") {
		t.Errorf("the reset should be announced: %q", out)
	}
}

// TestWorkstatePushCursorAfterVacuumCopyResets pins AC4 (rewrite): a VACUUM INTO
// copy keeps the instance id but may renumber rowids, so a saved position can
// land on a different row — here exactly on the next row inserted. The anchor
// check catches it and the row is sent.
func TestWorkstatePushCursorAfterVacuumCopyResets(t *testing.T) {
	ts, f := newFakeWorkstateServer(t)
	seedCred(t, ts.URL)
	repo := workstateRepo(t, ledgerPushToml)
	createProbeStory(t)
	pushOK(t, ts.URL)
	saved, err := hosted.LoadWorkstateCursor(ts.URL, "probe", repo)
	if err != nil || saved.LedgerSeq <= 0 {
		t.Fatalf("first push saved no position: %+v, %v", saved, err)
	}

	dbPath := runtimeDBPath(t)
	copyPath := dbPath + ".vacuum"
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Whatever VACUUM does with rowids, a copy missing the anchor row hands the
	// saved position to the next row inserted: its rowid is MAX+1, which is the
	// saved seq once the last row is gone.
	if _, err := raw.Exec(`DELETE FROM evidence WHERE rowid = (SELECT MAX(rowid) FROM evidence)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`VACUUM INTO '` + copyPath + `'`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}
	if err := os.Rename(copyPath, dbPath); err != nil {
		t.Fatal(err)
	}

	withRepoStore(t, func(db *store.DB) {
		upsertLedgerRow(t, db, "evt_aftervac", time.Now())
	})
	pushOK(t, ts.URL)
	if !f.hasLedger("probe", "evt_aftervac") {
		t.Fatal("a row inserted after a VACUUM INTO copy was never sent")
	}
}
