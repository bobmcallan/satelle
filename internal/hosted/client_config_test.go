package hosted

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func configTestServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", h)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	store := &memStore{}
	if err := store.Save(Credential{ServerURL: ts.URL, AccessToken: "good", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	return ts, NewClient(ts.URL, store, ts.Client())
}

// TestClientActiveWorkspaceID: the personal sentinel resolves to the kind=personal
// workspace; a team name resolves to its exact match; a miss lists the available
// workspaces (order:5 name→id bridge). Still used by publish catalog routes.
func TestClientActiveWorkspaceID(t *testing.T) {
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workspaces" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"id":"w1","kind":"personal","name":"Dev Personal"},{"id":"w2","kind":"team","name":"Acme Team"}]`))
	})
	_ = ts

	if id, err := c.ActiveWorkspaceID(context.Background(), "personal"); err != nil || id != "w1" {
		t.Fatalf("personal sentinel = %q, %v; want w1, nil", id, err)
	}
	if id, err := c.ActiveWorkspaceID(context.Background(), ""); err != nil || id != "w1" {
		t.Fatalf("empty name = %q, %v; want w1 (personal), nil", id, err)
	}
	if id, err := c.ActiveWorkspaceID(context.Background(), "Acme Team"); err != nil || id != "w2" {
		t.Fatalf("team name = %q, %v; want w2, nil", id, err)
	}
	if _, err := c.ActiveWorkspaceID(context.Background(), "Ghost"); err == nil {
		t.Error("unknown workspace name did not error")
	}
}

// TestClientPushConfigFile: a PUT carries the raw bytes (text/plain, not JSON),
// a new head returns Created=true (201), and an identical re-push returns
// Created=false (200) — the per-file idempotency signal.
func TestClientPushConfigFile(t *testing.T) {
	var gotBody string
	var gotCT string
	calls := 0
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, "/api/v1/projects/probe/config/") {
			http.NotFound(w, r)
			return
		}
		calls++
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotCT = r.Header.Get("Content-Type")
		if calls == 1 {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"path":"skills/x.md","version":1,"blob_sha256":"abc","size":4,"created":true}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"path":"skills/x.md","version":1,"blob_sha256":"abc","size":4,"created":false}`))
	})
	_ = ts

	res, err := c.PushConfigFile(context.Background(), "probe", "skills/x.md", []byte("body"))
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if gotBody != "body" {
		t.Errorf("server received body %q, want %q", gotBody, "body")
	}
	if !strings.HasPrefix(gotCT, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain (raw bytes, not JSON)", gotCT)
	}
	if !res.Created || res.Version != 1 || res.BlobSHA256 != "abc" {
		t.Errorf("first push result = %+v, want Created=true v1", res)
	}
	// Second identical push -> idempotent (200, Created=false).
	res2, err := c.PushConfigFile(context.Background(), "probe", "skills/x.md", []byte("body"))
	if err != nil {
		t.Fatalf("second push: %v", err)
	}
	if res2.Created {
		t.Errorf("idempotent re-push Created = true, want false")
	}
}

// TestClientConfigManifest: GET .../config returns the deploy set list.
func TestClientConfigManifest(t *testing.T) {
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects/probe/config" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"path":"skills/x.md","version":2,"blob_sha256":"h","size":4,"created_at":"t"},{"path":"agents.toml","version":1,"blob_sha256":"g","size":9,"created_at":"t"}]`))
	})
	_ = ts
	items, err := c.ConfigManifest(context.Background(), "probe")
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(items) != 2 || items[0].Path != "skills/x.md" || items[0].Version != 2 || items[1].Path != "agents.toml" {
		t.Fatalf("manifest = %+v", items)
	}
}

// TestClientConfigFileContent: GET returns the content + the ETag blob sha; a
// pinned ?version=N is requested as a proper query (not &version=); a 404
// yields ErrConfigFileMissing.
func TestClientConfigFileContent(t *testing.T) {
	var gotURI string
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/v1/projects/probe/config/skills/x.md") {
			http.NotFound(w, r)
			return
		}
		gotURI = r.URL.RequestURI()
		if r.URL.Query().Get("version") == "1" {
			// version 1 does not exist for this file -> 404
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", `"sha-abc"`)
		_, _ = w.Write([]byte("file body"))
	})
	_ = ts

	body, etag, err := c.ConfigFileContent(context.Background(), "probe", "skills/x.md", 0)
	if err != nil {
		t.Fatalf("content: %v", err)
	}
	if string(body) != "file body" || etag != `"sha-abc"` {
		t.Errorf("content = %q, etag = %q", body, etag)
	}
	if strings.Contains(gotURI, "version=") {
		t.Errorf("latest fetch URI %q must not pin version", gotURI)
	}
	// Pinned version that 404s -> ErrConfigFileMissing (deploy skips it).
	// Assert full URI uses ?version= (not &version=) now that routes have no query.
	if _, _, err := c.ConfigFileContent(context.Background(), "probe", "skills/x.md", 1); !errors.Is(err, ErrConfigFileMissing) {
		t.Errorf("pinned-missing = %v, want ErrConfigFileMissing", err)
	}
	if !strings.Contains(gotURI, "?version=1") {
		t.Errorf("pinned URI %q, want ?version=1", gotURI)
	}
	if strings.Contains(gotURI, "&version=") {
		t.Errorf("pinned URI %q must not use &version= without a prior query", gotURI)
	}
}

// TestClientPushConfigFileNoCredential: no credential -> ErrLoginRequired.
func TestClientPushConfigFileNoCredential(t *testing.T) {
	_, err := NewClient("https://example", &memStore{}, nil).PushConfigFile(context.Background(), "probe", "x", []byte("b"))
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("expected ErrLoginRequired, got %v", err)
	}
}
