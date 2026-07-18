package mirror_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/mirror"
)

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

	// snapshot
	snap := mirror.Snapshot{
		RepoKey: "rk1",
		Slug:    "demo",
		Stories: []json.RawMessage{[]byte(`{"id":"sty_a","title":"Hello"}`)},
		Tasks:   []json.RawMessage{[]byte(`{"id":"tsk_a","title":"Task"}`)},
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
