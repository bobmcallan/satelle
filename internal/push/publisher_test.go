package push_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/push"
)

func TestPublisherInactiveNoNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	// Empty endpoint: Notify must not hit the server.
	p := &push.Publisher{Endpoint: "", RepoKey: "repo-x"}
	p.Notify("stories")
	time.Sleep(50 * time.Millisecond)
	if hits.Load() != 0 {
		t.Fatalf("inert publisher made %d network calls", hits.Load())
	}

	// Nil receiver: also inert.
	var nilP *push.Publisher
	nilP.Notify("stories")
	time.Sleep(20 * time.Millisecond)
	if hits.Load() != 0 {
		t.Fatalf("nil publisher made network calls")
	}
}

func TestPublisherPostsEvent(t *testing.T) {
	var got push.Event
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		if r.Method != http.MethodPost || r.URL.Path != "/ingest/change" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("json: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	p := &push.Publisher{Endpoint: srv.URL, RepoKey: "satelle-deadbeef"}
	p.Notify("stories")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for POST")
	}
	if got.RepoKey != "satelle-deadbeef" || got.Topic != "stories" || got.Entity != "story" {
		t.Fatalf("event = %+v", got)
	}
	if got.At == "" {
		t.Error("missing at")
	}
}

func TestPublisherFailSilentUnreachable(t *testing.T) {
	// Black-hole: closed port, short client timeout. Notify returns immediately;
	// the goroutine dies without affecting the caller.
	p := &push.Publisher{
		Endpoint: "http://127.0.0.1:1",
		RepoKey:  "repo",
		Client:   &http.Client{Timeout: 50 * time.Millisecond},
	}
	start := time.Now()
	p.Notify("tasks")
	// Caller path must not wait for the HTTP attempt.
	if d := time.Since(start); d > 20*time.Millisecond {
		t.Fatalf("Notify blocked for %v — must be fire-and-forget", d)
	}
	// Give the goroutine a moment to finish without panicking.
	time.Sleep(100 * time.Millisecond)
}
