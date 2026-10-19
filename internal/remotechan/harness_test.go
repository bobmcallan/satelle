package remotechan

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/remotechan/relaytest"
)

// fakeClock is a Clock a test advances. Each Advance step delivers its ticks
// with a blocking send, so a tick is never dropped under load.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
}

type fakeTicker struct {
	d       time.Duration
	next    time.Time
	c       chan time.Time
	stopped bool
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)} }

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Tick(d time.Duration) (<-chan time.Time, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTicker{d: d, next: f.now.Add(d), c: make(chan time.Time, 1)}
	f.tickers = append(f.tickers, t)
	return t.c, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		t.stopped = true
	}
}

// Advance moves time forward by d, firing every ticker whose deadline passes.
func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	target := f.now.Add(d)
	f.mu.Unlock()
	for {
		f.mu.Lock()
		var due *fakeTicker
		for _, t := range f.tickers {
			if !t.stopped && !t.next.After(target) && (due == nil || t.next.Before(due.next)) {
				due = t
			}
		}
		if due == nil {
			f.now = target
			f.mu.Unlock()
			return
		}
		f.now = due.next
		due.next = due.next.Add(due.d)
		at := f.now
		f.mu.Unlock()
		select {
		case due.c <- at:
		case <-time.After(2 * time.Second):
		}
	}
}

// eventually polls cond for up to five seconds of real time.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// memStore is an in-memory hosted.Store.
type memStore struct {
	mu   sync.Mutex
	cred hosted.Credential
	set  bool
}

func (m *memStore) Load(string) (hosted.Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.set {
		return hosted.Credential{}, hosted.ErrNoCredential
	}
	return m.cred, nil
}

func (m *memStore) Save(c hosted.Credential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cred, m.set = c, true
	return nil
}

func (m *memStore) Delete(string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cred, m.set = hosted.Credential{}, false
	return nil
}

// idp is a stub token endpoint: it rotates the pair on each refresh, or answers
// invalid_grant when deny is set. It counts the refreshes it served.
type idp struct {
	ts   *httptest.Server
	mu   sync.Mutex
	n    int
	deny bool
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	p := &idp{}
	p.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.n++
		n, deny := p.n, p.deny
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if deny {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": fmt.Sprintf("access-%d", n+1), "refresh_token": fmt.Sprintf("refresh-%d", n+1),
			"token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(p.ts.Close)
	return p
}

func (p *idp) refreshes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

// harness is one signed-in (or signed-out) local satelle and a stand-in relay.
type harness struct {
	relay  *relaytest.Relay
	store  *memStore
	client *hosted.Client
	idp    *idp
	clock  *fakeClock
	tokens *TokenBook
	ledger *ledger.Store
}

const harnessServer = "https://satelle.test"

func newHarness(t *testing.T, signedIn bool) *harness {
	t.Helper()
	h := &harness{relay: relaytest.New(t), store: &memStore{}, idp: newIDP(t), clock: newFakeClock()}
	h.tokens = NewTokenBook(h.clock)
	if signedIn {
		_ = h.store.Save(hosted.Credential{
			ServerURL: harnessServer, AccessToken: "access-1", RefreshToken: "refresh-1",
			TokenEndpoint: h.idp.ts.URL + "/token", ClientID: "client-abc", Resource: "https://api.example/",
		})
	}
	h.client = hosted.NewClient(harnessServer, h.store, nil)

	db, err := sql.Open("sqlite", "file:remotechan-"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := ledger.Migrate(db); err != nil {
		t.Fatal(err)
	}
	h.ledger = ledger.New(db)
	return h
}

func (h *harness) opts() []Option {
	return []Option{WithHTTPClient(h.relay.Client()), WithClock(h.clock), WithTokens(h.tokens), WithRecorder(h.ledger)}
}

// refusals reads the recorded enrollment refusals back as their reasons.
func (h *harness) refusals(t *testing.T) []string {
	t.Helper()
	rows, err := h.ledger.List(context.Background(), ledger.ListFilter{Kind: ledger.KindRemoteEnrollRefused})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range rows {
		var p struct{ Session, Reason string }
		if err := json.Unmarshal(e.Payload, &p); err != nil || p.Session == "" {
			t.Fatalf("refusal payload %s: %v", e.Payload, err)
		}
		out = append(out, p.Reason)
	}
	return out
}

type joined struct {
	s   *Session
	err error
}

// launcher returns a Launcher that reports pub as the node's key and starts a
// remote that joins with id and presents present(spec) as its token. The
// remote's outcome arrives on the returned channel; the spec it was given on
// the second.
func (h *harness) launcher(t *testing.T, id *Identity, pub []byte, present func(LaunchSpec) string) (Launcher, <-chan joined, <-chan LaunchSpec) {
	t.Helper()
	res := make(chan joined, 1)
	specs := make(chan LaunchSpec, 1)
	ctx := testCtx(t)
	l := LauncherFunc(func(_ context.Context, spec LaunchSpec) ([]byte, error) {
		specs <- spec
		go func() {
			s, err := Join(ctx, spec.RelayURL, spec.Session, id, present(spec), h.opts()...)
			res <- joined{s, err}
		}()
		return pub, nil
	})
	return l, res, specs
}

// connect opens a fully enrolled pair: local satelle and the remote it started.
func (h *harness) connect(t *testing.T) (local, remote *Session, spec LaunchSpec) {
	t.Helper()
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	l, res, specs := h.launcher(t, id, id.Public(), func(s LaunchSpec) string { return s.Token })
	local, err = Open(testCtx(t), h.client, h.relay.URL(), l, h.opts()...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { local.Close() })
	r := <-res
	if r.err != nil {
		t.Fatalf("Join: %v", r.err)
	}
	t.Cleanup(func() { r.s.Close() })
	return local, r.s, <-specs
}
