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

func TestClientPushMeFileRoundTrip(t *testing.T) {
	var putPath string
	var putBody []byte
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/me/files/agents.toml", func(w http.ResponseWriter, r *http.Request) {
		putPath = r.URL.Path
		putBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"path":"agents.toml","version":1}`))
	})
	mux.HandleFunc("GET /api/v1/me/files/agents.toml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "abc")
		_, _ = w.Write(putBody)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	store := &memStore{cred: Credential{ServerURL: ts.URL, AccessToken: "t", RefreshToken: "r"}, set: true}
	c := NewClient(ts.URL, store, ts.Client())
	res, err := c.PushMeFile(context.Background(), MeCatalogPath, []byte("catalog-body"))
	if err != nil {
		t.Fatalf("PushMeFile: %v", err)
	}
	if !res.Created || putPath != "/api/v1/me/files/agents.toml" {
		t.Fatalf("created=%v path=%q", res.Created, putPath)
	}
	if string(putBody) != "catalog-body" {
		t.Fatalf("body = %q", putBody)
	}
	got, etag, err := c.MeFileContent(context.Background(), MeCatalogPath)
	if err != nil {
		t.Fatalf("MeFileContent: %v", err)
	}
	if string(got) != "catalog-body" || etag != "abc" {
		t.Fatalf("got %q etag %q", got, etag)
	}
}

func TestClientPushMeFileNoCredential(t *testing.T) {
	c := NewClient("https://example", &memStore{}, nil)
	_, err := c.PushMeFile(context.Background(), MeCatalogPath, []byte("x"))
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("want ErrLoginRequired, got %v", err)
	}
}

func TestClientMeFileContentUnauthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me/files/agents.toml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c := NewClient(ts.URL, &memStore{cred: Credential{ServerURL: ts.URL, AccessToken: "t", RefreshToken: "r"}, set: true}, ts.Client())
	_, _, err := c.MeFileContent(context.Background(), MeCatalogPath)
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("want ErrLoginRequired, got %v", err)
	}
}

func TestClientMeFileContentMissing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me/files/agents.toml", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c := NewClient(ts.URL, &memStore{cred: Credential{ServerURL: ts.URL, AccessToken: "t", RefreshToken: "r"}, set: true}, ts.Client())
	_, _, err := c.MeFileContent(context.Background(), MeCatalogPath)
	if !errors.Is(err, ErrMeFileMissing) {
		t.Fatalf("want ErrMeFileMissing, got %v", err)
	}
	if !strings.Contains(err.Error(), "profiles push") {
		t.Errorf("missing error should name push: %v", err)
	}
}
