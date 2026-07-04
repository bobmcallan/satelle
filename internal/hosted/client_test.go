package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// memStore is an in-memory Store for client tests.
type memStore struct {
	mu   sync.Mutex
	cred Credential
	set  bool
}

func (m *memStore) Load(server string) (Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.set {
		return Credential{}, ErrNoCredential
	}
	return m.cred, nil
}
func (m *memStore) Save(c Credential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cred, m.set = c, true
	return nil
}
func (m *memStore) Delete(server string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cred, m.set = Credential{}, false
	return nil
}

func TestClientRefreshRotationOn401(t *testing.T) {
	var mu sync.Mutex
	meCalls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		meCalls++
		mu.Unlock()
		// The stale access token is rejected; the rotated one is accepted.
		if r.Header.Get("Authorization") != "Bearer access-2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(Principal{ID: "u1", Email: "a@b.c", DisplayName: "A", Role: "member"})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-1" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		// Rotate: new access AND new refresh; old refresh now dead.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-2", "refresh_token": "refresh-2",
			"token_type": "Bearer", "expires_in": 3600,
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "access-1", RefreshToken: "refresh-1"})

	who, err := NewClient(ts.URL, store, ts.Client()).Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if who.ID != "u1" {
		t.Fatalf("principal = %+v", who)
	}
	// The store must now hold the ROTATED refresh + new access.
	got, _ := store.Load(ts.URL)
	if got.AccessToken != "access-2" || got.RefreshToken != "refresh-2" {
		t.Fatalf("store not updated with rotated tokens: %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if meCalls != 2 {
		t.Fatalf("expected exactly one retry (2 /me calls), got %d", meCalls)
	}
}

// The 401→refresh path must PRESERVE the stored identity onto the rotated
// credential, else display_name/email vanish ~1h after login (sty_467c6944).
func TestClientRefreshPreservesIdentity(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(Principal{ID: "u1", Email: "a@b.c"})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-2", "refresh_token": "refresh-2",
			"token_type": "Bearer", "expires_in": 3600,
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "access-1", RefreshToken: "refresh-1",
		DisplayName: "Kept Name", Email: "kept@x.io"})

	if _, err := NewClient(ts.URL, store, ts.Client()).Me(context.Background()); err != nil {
		t.Fatalf("Me: %v", err)
	}
	got, _ := store.Load(ts.URL)
	if got.AccessToken != "access-2" || got.RefreshToken != "refresh-2" {
		t.Fatalf("tokens not rotated: %+v", got)
	}
	if got.DisplayName != "Kept Name" || got.Email != "kept@x.io" {
		t.Fatalf("identity lost across refresh: %+v", got)
	}
}

func TestClientFailedRefreshIsLoginRequired(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "stale", RefreshToken: "dead"})

	_, err := NewClient(ts.URL, store, ts.Client()).Me(context.Background())
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("expected ErrLoginRequired, got %v", err)
	}
	if !strings.Contains(err.Error(), "satelle login") {
		t.Errorf("error should mention satelle login: %v", err)
	}
}

func TestClientNoCredentialIsLoginRequired(t *testing.T) {
	_, err := NewClient("https://example", &memStore{}, nil).Me(context.Background())
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("expected ErrLoginRequired, got %v", err)
	}
}
