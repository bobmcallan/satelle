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
		DisplayName: "Kept Name", Email: "kept@x.io", PrincipalID: "prin_p"})

	if _, err := NewClient(ts.URL, store, ts.Client()).Me(context.Background()); err != nil {
		t.Fatalf("Me: %v", err)
	}
	got, _ := store.Load(ts.URL)
	if got.AccessToken != "access-2" || got.RefreshToken != "refresh-2" {
		t.Fatalf("tokens not rotated: %+v", got)
	}
	if got.DisplayName != "Kept Name" || got.Email != "kept@x.io" || got.PrincipalID != "prin_p" {
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

func TestClientCreateProjectHappy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		var in map[string]string
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if in["slug"] != "acme" || in["name"] != "Acme Co" {
			t.Errorf("body = %+v, want slug=acme name=Acme Co", in)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Project{ID: "p1", Slug: "acme", Name: "Acme Co"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "good", RefreshToken: "r"})

	p, err := NewClient(ts.URL, store, ts.Client()).CreateProject(context.Background(), "acme", "Acme Co")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.ID != "p1" || p.Slug != "acme" || p.Name != "Acme Co" {
		t.Fatalf("project = %+v", p)
	}
}

// A 409 must surface ErrSlugConflict and never leak the raw response body.
func TestClientCreateProjectSlugConflict(t *testing.T) {
	const secret = "internal-server-detail-should-not-leak"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": secret})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "good", RefreshToken: "r"})

	_, err := NewClient(ts.URL, store, ts.Client()).CreateProject(context.Background(), "dup", "Dup")
	if !errors.Is(err, ErrSlugConflict) {
		t.Fatalf("expected ErrSlugConflict, got %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked raw body: %v", err)
	}
}

func TestClientCreateProjectNoCredential(t *testing.T) {
	_, err := NewClient("https://example", &memStore{}, nil).CreateProject(context.Background(), "s", "n")
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("expected ErrLoginRequired, got %v", err)
	}
}

// The POST body must survive the 401→refresh→retry path: an io.Reader handed to
// the first (rejected) attempt would be drained, so the retry must rebuild it.
func TestClientCreateProjectBodySurvivesRefresh(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		// Both the stale and the rotated attempt must carry the full body.
		if in["slug"] != "acme" || in["name"] != "Acme Co" {
			t.Errorf("body not resent on retry: %+v", in)
		}
		if r.Header.Get("Authorization") != "Bearer access-2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Project{ID: "p1", Slug: in["slug"], Name: in["name"]})
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
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "access-1", RefreshToken: "refresh-1"})

	p, err := NewClient(ts.URL, store, ts.Client()).CreateProject(context.Background(), "acme", "Acme Co")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.ID != "p1" {
		t.Fatalf("project = %+v", p)
	}
	got, _ := store.Load(ts.URL)
	if got.AccessToken != "access-2" || got.RefreshToken != "refresh-2" {
		t.Fatalf("tokens not rotated: %+v", got)
	}
}

func TestClientListProjects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]Project{
			{ID: "p1", Slug: "acme", Name: "Acme Co", Role: "owner"},
			{ID: "p2", Slug: "beta", Name: "Beta"},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "good", RefreshToken: "r"})

	ps, err := NewClient(ts.URL, store, ts.Client()).ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(ps) != 2 || ps[0].Slug != "acme" || ps[0].Role != "owner" || ps[1].Slug != "beta" {
		t.Fatalf("projects = %+v", ps)
	}
}

// TestClientWorkspaces covers GET /api/v1/workspaces: the server returns
// {id, kind, name} (no slug), personal|team kinds only.
func TestClientWorkspaces(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode([]Workspace{
			{ID: "w1", Kind: "personal", Name: "Dev Personal"},
			{ID: "w2", Kind: "team", Name: "Acme Team"},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "good", RefreshToken: "r"})

	ws, err := NewClient(ts.URL, store, ts.Client()).Workspaces(context.Background())
	if err != nil {
		t.Fatalf("Workspaces: %v", err)
	}
	if len(ws) != 2 {
		t.Fatalf("workspaces = %+v", ws)
	}
	if ws[0].ID != "w1" || ws[0].Kind != "personal" || ws[0].Name != "Dev Personal" {
		t.Fatalf("ws[0] = %+v", ws[0])
	}
	if ws[1].ID != "w2" || ws[1].Kind != "team" || ws[1].Name != "Acme Team" {
		t.Fatalf("ws[1] = %+v", ws[1])
	}
}

// A missing credential must surface ErrLoginRequired, never a raw 401 body.
func TestClientWorkspacesNoCredential(t *testing.T) {
	_, err := NewClient("https://example", &memStore{}, nil).Workspaces(context.Background())
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("expected ErrLoginRequired, got %v", err)
	}
}
