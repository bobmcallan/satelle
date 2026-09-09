package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRegisterLocationIdempotent(t *testing.T) {
	withLocationState(t)
	var posts atomic.Int32
	var lastID, lastLabel string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		var in struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		lastID, lastLabel = in.ID, in.Label
		if r.Header.Get(LocationHeader) != in.ID {
			t.Errorf("register request missing location header: %q vs %q", r.Header.Get(LocationHeader), in.ID)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": in.ID})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "tok", RefreshToken: "r"})
	c := NewClient(ts.URL, store, ts.Client())
	c.SetLocation("loc_register_test_aaaa")
	if err := c.RegisterLocation(context.Background(), "laptop /tmp/repo"); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 || lastID != "loc_register_test_aaaa" || lastLabel == "" {
		t.Fatalf("posts=%d id=%q label=%q", posts.Load(), lastID, lastLabel)
	}
}

func TestRegisterLocationAccepts201(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "loc_created_xxxxxxxx"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "tok", RefreshToken: "r"})
	c := NewClient(ts.URL, store, ts.Client())
	c.SetLocation("loc_created_xxxxxxxx")
	if err := c.RegisterLocation(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterLocation401(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "stale", RefreshToken: "dead"})
	c := NewClient(ts.URL, store, ts.Client())
	c.SetLocation("loc_unauth_xxxxxxxxxx")
	err := c.RegisterLocation(context.Background(), "")
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("err = %v, want ErrLoginRequired", err)
	}
}

func TestEnsureLocationRegisteredShortCircuit(t *testing.T) {
	withLocationState(t)
	repo := t.TempDir()
	id, err := LocationID(repo)
	if err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "tok", RefreshToken: "r"})
	c := NewClient(ts.URL, store, ts.Client())
	c.SetLocation(id)
	if err := EnsureLocationRegistered(context.Background(), c, repo, ts.URL); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatalf("first register posts = %d", posts.Load())
	}
	if err := EnsureLocationRegistered(context.Background(), c, repo, ts.URL); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatalf("short-circuit posts = %d, want 1", posts.Load())
	}
}
