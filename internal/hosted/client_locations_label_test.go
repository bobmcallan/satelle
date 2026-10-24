package hosted

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeLocations is a server that remembers the label each location last
// registered under.
type fakeLocations struct {
	mu     sync.Mutex
	posts  int
	labels map[string]string
	ts     *httptest.Server
}

func newFakeLocations(t *testing.T) *fakeLocations {
	t.Helper()
	f := &fakeLocations{labels: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.posts++
		f.labels[in.ID] = in.Label
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"id": in.ID, "user_id": "u_1"})
	})
	f.ts = httptest.NewServer(mux)
	t.Cleanup(f.ts.Close)
	return f
}

// snapshot is the number of registrations received and the label id last sent.
func (f *fakeLocations) snapshot(id string) (int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts, f.labels[id]
}

// A location registered under an older label is registered again under the
// host · project label, once, for an API-key or login client.
func TestEnsureLocationRegisteredResendsChangedLabel(t *testing.T) {
	withLocationState(t)
	f := newFakeLocations(t)
	repo := t.TempDir()
	id, err := LocationID(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkLocationRegistered(repo, f.ts.URL, "bobs-box /old/path"); err != nil {
		t.Fatal(err)
	}
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: f.ts.URL, AccessToken: "tok", RefreshToken: "r"})
	c := NewClient(f.ts.URL, store, f.ts.Client())
	c.SetLocation(id)

	want := SessionName(repo, "")
	for i := 0; i < 2; i++ {
		if err := EnsureLocationRegistered(context.Background(), c, repo, f.ts.URL); err != nil {
			t.Fatal(err)
		}
	}
	if posts, got := f.snapshot(id); posts != 1 || got != want {
		t.Fatalf("posts = %d, label = %q; want 1 post of %q (the second call sends nothing)", posts, got, want)
	}
}

// Under a session token the registered-once shortcut also needs the cached
// principal; a cached principal with an older label must still re-register.
func TestEnsureLocationRegisteredResendsChangedLabelUnderSessionToken(t *testing.T) {
	f := newFakeLocations(t)
	store, _ := sessionEnv(t, f.ts.URL)
	withLocationState(t)
	repo := t.TempDir()
	id, err := LocationID(repo)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(f.ts.URL, store, f.ts.Client())
	c.SetLocation(id)
	if _, err := ResolveSessionPrincipal(context.Background(), c, repo, f.ts.URL); err != nil {
		t.Fatal(err)
	}
	if err := MarkLocationRegistered(repo, f.ts.URL, "bobs-box /old/path"); err != nil {
		t.Fatal(err)
	}
	before, _ := f.snapshot(id)
	want := SessionName(repo, "")
	for i := 0; i < 2; i++ {
		if err := EnsureLocationRegistered(context.Background(), c, repo, f.ts.URL); err != nil {
			t.Fatal(err)
		}
	}
	if posts, got := f.snapshot(id); posts != before+1 || got != want {
		t.Fatalf("posts = %d (was %d), label = %q; want exactly one more post of %q", posts, before, got, want)
	}
}
