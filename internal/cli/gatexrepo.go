package cli

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/gatehandle"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

// A gate handle can live in a different repo's store from the one whose hooks
// serve its session (sty_8f10499d, epic:gate-wake): a session anchored in repo A
// runs a satelle command that acts on repo B, and the handle is written under B.
// Two planes are told apart throughout the delivery and resume code:
//
//   - the TURN plane belongs to the session, so it lives in the serving repo's
//     store (A): the stop count, the open/closed turn, the recorded session, and
//     the resume lock;
//   - the HANDLE plane belongs to the run, so it lives in the store that holds it
//     (B): the handle, its observation, its claim, its resume arming and its
//     delivery row.
//
// Same-repo, A and B are one store and there is nothing to tell apart.

// repoAt resolves the satelle repo that holds dir: its config and root. ok is
// false outside a governed repo.
func repoAt(dir string) (cfg config.Config, root string, ok bool) {
	if dir == "" {
		return config.Config{}, "", false
	}
	data, found := config.FindDataDir(dir)
	if !found {
		return config.Config{}, "", false
	}
	if cfgPath := filepath.Join(data, config.ConfigName); fileExists(cfgPath) {
		c, _, err := config.Load(cfgPath)
		if err != nil {
			return config.Config{}, "", false
		}
		cfg = c
	}
	return cfg, filepath.Dir(data), true
}

// gateStoreAt is the handle store of the repo holding dir.
func gateStoreAt(dir string) (*gatehandle.Store, bool) {
	cfg, root, ok := repoAt(dir)
	if !ok {
		return nil, false
	}
	return gatehandle.New(cfg.ResolveRuntimeDir(root).Dir), true
}

// publishServeRoot records the repo this hook fires in as the one serving the
// session, for a later command in another repo to find. A dispatched process is
// not the session a verdict is for.
func publishServeRoot(session string) {
	if session == "" || isDispatchedProcess() {
		return
	}
	if _, root, ok := firingRepo(); ok {
		config.PublishSessionRoot(session, root)
	}
}

// serveTarget names the repo whose hooks serve the session this process runs in,
// when that is a different store from the one the gate is being written to
// (actingRuntime). The session's repo is never the working directory, which is
// where the command acts: it is the repo its environment pins, or failing that
// the one its own hooks last published (publishServeRoot) — a harness need not
// hand a pin to the commands its session runs. ok is false when neither names a
// governed repo, or it names this very store.
func serveTarget(session, actingRuntime string) (serve *gatehandle.Store, root string, ok bool) {
	anchor := anchorFromEnv(os.Getenv)
	if anchor == "" {
		anchor = config.PublishedSessionRoot(session)
	}
	cfg, root, found := repoAt(anchor)
	if !found {
		return nil, "", false
	}
	serve = gatehandle.New(cfg.ResolveRuntimeDir(root).Dir)
	if filepath.Clean(serve.RuntimeDir()) == filepath.Clean(actingRuntime) {
		return nil, "", false
	}
	return serve, root, true
}

// gateRef names one run in the store that holds it.
type gateRef struct {
	store *gatehandle.Store
	id    string
}

// handleStore is a store whose handles a session may be told about. only is nil
// for the serving repo's own store, where every handle is a candidate; for a
// foreign store it is the handles the serving repo points at — never the rest of
// that store, which belongs to its own sessions.
type handleStore struct {
	store *gatehandle.Store
	only  map[string]bool
}

func (h handleStore) undelivered() []string {
	ids := h.store.Undelivered()
	if h.only == nil {
		return ids
	}
	var out []string
	for _, id := range ids {
		if h.only[id] {
			out = append(out, id)
		}
	}
	return out
}

// handleStoresFor is the one definition of where a session's handles are: its
// serving store, then each foreign store that store points at for it. A pointer
// whose handle is gone or already delivered is dropped here, so a delivery by any
// route leaves nothing stale behind.
func handleStoresFor(home *gatehandle.Store, session string) []handleStore {
	out := []handleStore{{store: home}}
	if session == "" {
		return out
	}
	at := map[string]int{}
	for _, f := range home.Forwards(session) {
		dir := filepath.Clean(f.RuntimeDir)
		if dir == filepath.Clean(home.RuntimeDir()) {
			home.Unforward(session, f.ID)
			continue
		}
		foreign := gatehandle.New(f.RuntimeDir)
		if _, err := foreign.Meta(f.ID); err != nil || foreign.Delivered(f.ID) {
			home.Unforward(session, f.ID)
			continue
		}
		i, seen := at[dir]
		if !seen {
			i = len(out)
			at[dir] = i
			out = append(out, handleStore{store: foreign, only: map[string]bool{}})
		}
		out[i].only[f.ID] = true
	}
	return out
}

// ownedGates lists every undelivered run of session across stores, oldest first
// within each store.
func ownedGates(stores []handleStore, session string) []gateRef {
	var refs []gateRef
	for _, hs := range stores {
		for _, id := range hs.undelivered() {
			if m, err := hs.store.Meta(id); err == nil && ownsGate(session, m) {
				refs = append(refs, gateRef{store: hs.store, id: id})
			}
		}
	}
	return refs
}

// recordDeliveredIn writes each delivered run's delivery row into the ledger of
// the repo its story lives in: the repo holding the handle, which for a run
// started across repos is not the one whose hook delivered it.
func recordDeliveredIn(root string, stories []gatehandle.Meta) {
	cfg, repoRoot, ok := repoAt(root)
	if !ok {
		return
	}
	db, err := store.Open(cfg.ResolveDB(repoRoot))
	if err != nil {
		return
	}
	defer db.Close()
	verb.SetLedgerStore(db.Ledger)
	recordDeliveryRows(stories)
}

func recordDeliveryRows(stories []gatehandle.Meta) {
	for _, m := range stories {
		verb.RecordGateWait(context.Background(), m.Story, costview.GatePhaseDelivered, m.ID, time.Now().UTC())
	}
}
