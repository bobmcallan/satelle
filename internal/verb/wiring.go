package verb

import (
	"errors"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// ErrStoreNotConfigured is returned when a verb runs before its backing store
// was wired. The in-process bootstrap calls the SetXStore functions below at
// startup; a nil store means a wiring bug, not a user error.
var ErrStoreNotConfigured = errors.New("verb: store not configured")

// Package-global stores, wired by the bootstrap (app.Open → SetXStore). Verbs
// read these; a nil handle yields ErrStoreNotConfigured. Globals mirror
// satellites' SetXStore pattern — the single seam both CLI and web wire once.
var (
	workItemStore *workitem.Store
	ledgerStore   *ledger.Store
	docIndexStore *docindex.Store
)

// SetWorkItemStore wires the stories/tasks store. Pass nil to reset (tests).
func SetWorkItemStore(s *workitem.Store) { workItemStore = s }

// SetLedgerStore wires the evidence-ledger store.
func SetLedgerStore(s *ledger.Store) { ledgerStore = s }

// SetDocIndexStore wires the authored-doc index store.
func SetDocIndexStore(s *docindex.Store) { docIndexStore = s }

// requireWorkItem returns the wired store or ErrStoreNotConfigured.
func requireWorkItem() (*workitem.Store, error) {
	if workItemStore == nil {
		return nil, ErrStoreNotConfigured
	}
	return workItemStore, nil
}

func requireLedger() (*ledger.Store, error) {
	if ledgerStore == nil {
		return nil, ErrStoreNotConfigured
	}
	return ledgerStore, nil
}

func requireDocIndex() (*docindex.Store, error) {
	if docIndexStore == nil {
		return nil, ErrStoreNotConfigured
	}
	return docIndexStore, nil
}
