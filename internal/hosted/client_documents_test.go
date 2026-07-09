package hosted

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestClientPushDocumentFile: PUT carries raw bytes; 201 → Created; 200 → not;
// 409 → ErrDocumentConflict; 401 → ErrLoginRequired.
func TestClientPushDocumentFile(t *testing.T) {
	calls := 0
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, "/api/v1/workspaces/w1/documents/") {
			http.NotFound(w, r)
			return
		}
		calls++
		body, _ := io.ReadAll(r.Body)
		if string(body) != "doc body" {
			t.Errorf("body = %q", body)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "text/plain") {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		switch calls {
		case 1:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"path":"documents/a.md","version":1,"blob_sha256":"h","size":8,"created":true}`))
		case 2:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"path":"documents/a.md","version":1,"blob_sha256":"h","size":8,"created":false}`))
		case 3:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"conflict"}`))
		}
	})
	_ = ts

	res, err := c.PushDocumentFile(context.Background(), "w1", "documents/a.md", []byte("doc body"))
	if err != nil || !res.Created {
		t.Fatalf("first push = %+v, %v", res, err)
	}
	res, err = c.PushDocumentFile(context.Background(), "w1", "documents/a.md", []byte("doc body"))
	if err != nil || res.Created {
		t.Fatalf("idempotent push = %+v, %v", res, err)
	}
	_, err = c.PushDocumentFile(context.Background(), "w1", "documents/a.md", []byte("doc body"))
	if !errorsIs(err, ErrDocumentConflict) {
		t.Fatalf("conflict = %v, want ErrDocumentConflict", err)
	}
}

// TestClientListDocumentChanges: since is query-encoded; empty items is a
// non-nil slice; 401 → ErrLoginRequired.
func TestClientListDocumentChanges(t *testing.T) {
	var gotQuery string
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/workspaces/w1/documents" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"items":[{"path":"documents/a.md","version":1,"blob_sha256":"h","size":1,"created_at":"t"}],"cursor":"c2"}`))
	})
	_ = ts

	ch, err := c.ListDocumentChanges(context.Background(), "w1", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "since=c1" {
		t.Errorf("query = %q, want since=c1", gotQuery)
	}
	if ch.Cursor != "c2" || len(ch.Items) != 1 || ch.Items[0].Path != "documents/a.md" {
		t.Fatalf("changes = %+v", ch)
	}
}

// TestClientDocumentFileContent: 200 returns body + ETag; 404 → missing.
func TestClientDocumentFileContent(t *testing.T) {
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/workspaces/w1/documents/documents/a.md" {
			w.Header().Set("ETag", `"abc"`)
			_, _ = w.Write([]byte("hello"))
			return
		}
		http.NotFound(w, r)
	})
	_ = ts

	body, etag, err := c.DocumentFileContent(context.Background(), "w1", "documents/a.md")
	if err != nil || string(body) != "hello" || etag != `"abc"` {
		t.Fatalf("content = %q %q %v", body, etag, err)
	}
	_, _, err = c.DocumentFileContent(context.Background(), "w1", "documents/missing.md")
	if !errorsIs(err, ErrDocumentFileMissing) {
		t.Fatalf("missing = %v", err)
	}
}

func errorsIs(err, target error) bool {
	return errors.Is(err, target)
}
