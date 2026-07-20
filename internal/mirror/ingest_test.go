package mirror_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/mirror"
)

func TestIngestRemove(t *testing.T) {
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h := &mirror.IngestHandler{Store: s}
	mux := http.NewServeMux()
	h.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Seed via snapshot.
	body, _ := json.Marshal(mirror.Snapshot{
		RepoKey: "rk-rm",
		Slug:    "rm",
		Stories: []json.RawMessage{[]byte(`{"id":"sty_x","title":"X"}`)},
	})
	resp, err := http.Post(srv.URL+"/ingest/snapshot", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("snapshot %d", resp.StatusCode)
	}

	rmBody, _ := json.Marshal(mirror.RemoveEvent{RepoKey: "rk-rm"})
	resp, err = http.Post(srv.URL+"/ingest/remove", "application/json", bytes.NewReader(rmBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("remove %d", resp.StatusCode)
	}
	parts, err := s.ListPartitions(t.Context())
	if err != nil || len(parts) != 0 {
		t.Fatalf("parts=%v err=%v", parts, err)
	}
	// Unknown key is still 200 (idempotent delete).
	resp, err = http.Post(srv.URL+"/ingest/remove", "application/json", bytes.NewReader(rmBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("idempotent remove %d", resp.StatusCode)
	}
	// Missing repo_key → 400.
	resp, err = http.Post(srv.URL+"/ingest/remove", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty key status %d", resp.StatusCode)
	}
}

func TestIngestChangeAndSnapshot(t *testing.T) {
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var doorbells []string
	h := &mirror.IngestHandler{Store: s, OnChange: func(topic string) { doorbells = append(doorbells, topic) }}
	mux := http.NewServeMux()
	h.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// change event
	body, _ := json.Marshal(mirror.ChangeEvent{RepoKey: "rk1", Topic: "stories", Entity: "story", At: "t"})
	resp, err := http.Post(srv.URL+"/ingest/change", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("change status %d", resp.StatusCode)
	}

	// snapshot — full kinds (sty_400c022b)
	snap := mirror.Snapshot{
		RepoKey:      "rk1",
		Slug:         "demo",
		Stories:      []json.RawMessage{[]byte(`{"id":"sty_a","title":"Hello"}`)},
		Tasks:        []json.RawMessage{[]byte(`{"id":"tsk_a","title":"Task"}`)},
		Docs:         []json.RawMessage{[]byte(`{"name":"wf","kind":"workflows","body":"x","mod_time":"2026-01-01T00:00:00Z","provenance":"authored","source":"/x"}`)},
		LedgerEvents: []json.RawMessage{[]byte(`{"id":"evt_1","story_id":"sty_a","kind":"status_transition"}`)},
		Seats:        []json.RawMessage{[]byte(`{"id":"sty_a","in_flight":true,"stale":false}`)},
		Identity:     json.RawMessage(`{"project_name":"demo","repo_root":"/tmp/demo","footer_email":"op@example.com"}`),
		Settings:     json.RawMessage(`{"repo_root":"/tmp/demo","rows":[]}`),
	}
	body, _ = json.Marshal(snap)
	resp, err = http.Post(srv.URL+"/ingest/snapshot", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("snapshot status %d", resp.StatusCode)
	}

	stories, err := s.ListItems(t.Context(), "rk1", "story")
	if err != nil || len(stories) != 1 {
		t.Fatalf("stories=%v err=%v", stories, err)
	}
	if !bytes.Contains([]byte(stories[0].Payload), []byte("Hello")) {
		t.Errorf("payload %s", stories[0].Payload)
	}
	for _, kind := range []string{"doc", "ledger_event", "seat", "identity", "settings"} {
		items, err := s.ListItems(t.Context(), "rk1", kind)
		if err != nil || len(items) == 0 {
			t.Errorf("kind %s: items=%v err=%v", kind, items, err)
		}
	}
	idPayload, err := s.GetItem(t.Context(), "rk1", "identity", "meta")
	if err != nil || !bytes.Contains([]byte(idPayload), []byte("op@example.com")) {
		t.Errorf("identity meta: %q err=%v", idPayload, err)
	}
	// Capture per-kind counts, then re-push — AC3: counts stable (ReplaceKind).
	counts := map[string]int{}
	for _, kind := range []string{"story", "task", "doc", "ledger_event", "seat", "identity", "settings"} {
		items, err := s.ListItems(t.Context(), "rk1", kind)
		if err != nil {
			t.Fatal(err)
		}
		counts[kind] = len(items)
	}
	// Enriched doc fields survive ListItems.
	docs, _ := s.ListItems(t.Context(), "rk1", "doc")
	if !bytes.Contains([]byte(docs[0].Payload), []byte("mod_time")) ||
		!bytes.Contains([]byte(docs[0].Payload), []byte("provenance")) ||
		!bytes.Contains([]byte(docs[0].Payload), []byte("source")) {
		t.Errorf("doc payload missing mod_time/provenance/source: %s", docs[0].Payload)
	}

	resp, err = http.Post(srv.URL+"/ingest/snapshot", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, kind := range []string{"story", "task", "doc", "ledger_event", "seat", "identity", "settings"} {
		items, err := s.ListItems(t.Context(), "rk1", kind)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != counts[kind] {
			t.Errorf("after re-push kind %s count = %d, want %d", kind, len(items), counts[kind])
		}
	}
	if len(doorbells) == 0 {
		t.Error("expected OnChange doorbells")
	}
}

func TestIngestRejectsNonPOST(t *testing.T) {
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h := &mirror.IngestHandler{Store: s}
	mux := http.NewServeMux()
	h.Mount(mux)
	req := httptest.NewRequest(http.MethodGet, "/ingest/change", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d", rr.Code)
	}
}

// TestIngestSnapshotSlugConflict (sty_57d5ce25): same slug + different repo_key
// is 409; same repo_key re-push and empty slugs stay OK.
func TestIngestSnapshotSlugConflict(t *testing.T) {
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h := &mirror.IngestHandler{Store: s}
	mux := http.NewServeMux()
	h.Mount(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	post := func(snap mirror.Snapshot) (int, string) {
		t.Helper()
		body, _ := json.Marshal(snap)
		resp, err := http.Post(srv.URL+"/ingest/snapshot", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(raw)
	}

	first := mirror.Snapshot{
		RepoKey:  "x-aaaa",
		Slug:     "x",
		Identity: json.RawMessage(`{"project_name":"x","repo_root":"/tmp/x-a","footer_email":""}`),
	}
	code, _ := post(first)
	if code != 200 {
		t.Fatalf("first seed status %d", code)
	}

	// Different repo_key, same slug → 409 with existing key + path in body.
	code, body := post(mirror.Snapshot{
		RepoKey:  "x-bbbb",
		Slug:     "x",
		Identity: json.RawMessage(`{"project_name":"x","repo_root":"/tmp/x-b","footer_email":""}`),
	})
	if code != http.StatusConflict {
		t.Fatalf("collision status %d, want 409; body=%s", code, body)
	}
	for _, want := range []string{"x-aaaa", "/tmp/x-a", "x-bbbb", `slug "x"`} {
		if !strings.Contains(body, want) {
			t.Errorf("409 body missing %q: %s", want, body)
		}
	}

	// Same repo_key re-push is fine.
	code, _ = post(first)
	if code != 200 {
		t.Fatalf("re-push same key status %d", code)
	}

	// Empty slugs never conflict.
	code, _ = post(mirror.Snapshot{RepoKey: "empty-1", Slug: ""})
	if code != 200 {
		t.Fatalf("empty slug 1 status %d", code)
	}
	code, _ = post(mirror.Snapshot{RepoKey: "empty-2", Slug: ""})
	if code != 200 {
		t.Fatalf("empty slug 2 status %d", code)
	}
}
