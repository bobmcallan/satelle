package verb_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_5262592e AC2: when the route cannot be resolved the freeze fails CLOSED — an
// edit is refused rather than permitted by a rule nobody could evaluate.
func TestDefinitionEditRefusedWhenRouteUnresolvable(t *testing.T) {
	withWiring(t)
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "satelle.db"))
	if err != nil {
		t.Fatal(err)
	}
	wfDir := filepath.Join(dir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRouteFiles(t, wfDir, readinessWF)
	sync := func() {
		if _, err := db.DocIndex.Sync(context.Background(), map[string]string{"workflows": wfDir}, time.Now()); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}
	sync()
	verb.SetWorkItemStore(db.Stories)
	verb.SetLedgerStore(db.Ledger)
	verb.SetTxRunner(db.InTx)
	verb.SetDocIndexStore(db.DocIndex)
	verb.SetLeaseStore(db.Leases)
	t.Cleanup(func() {
		db.Close()
	})
	it := newFeature(t)
	call(t, "story-set", map[string]any{"id": it.ID, "title": "resolvable"}) // editable while the route resolves

	broken := strings.Replace(readinessWF["step"], "freeze = true", "freeze = true\nfreeez = true", 1)
	writeRouteFiles(t, wfDir, map[string]string{"step": broken})
	sync()
	err = dispatchErr(t, "story-set", map[string]any{"id": it.ID, "title": "unjudged"})
	if !strings.Contains(err.Error(), "cannot resolve") {
		t.Fatalf("want the fail-closed refusal, got %v", err)
	}
	if got := statusOf(t, it.ID); got.Title != "resolvable" {
		t.Errorf("a refused edit must not land, title = %q", got.Title)
	}
}
