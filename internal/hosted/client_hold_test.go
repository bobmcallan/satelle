package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted/syncpb"
)

func holdTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	store := &memStore{}
	_ = store.Save(Credential{ServerURL: ts.URL, AccessToken: "tok", RefreshToken: "r"})
	c := NewClient(ts.URL, store, ts.Client())
	c.SetLocation("loc_self_hold_aaaaaaaa")
	return c
}

func TestCheckoutPostsLocationAndIdempotent(t *testing.T) {
	var n int
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{project}/workstate/items/{id}/checkout", func(w http.ResponseWriter, r *http.Request) {
		n++
		if r.Header.Get(LocationHeader) != "loc_self_hold_aaaaaaaa" {
			t.Errorf("header = %q", r.Header.Get(LocationHeader))
		}
		var in struct {
			LocationID string `json:"location_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.LocationID != "loc_self_hold_aaaaaaaa" {
			t.Errorf("body location_id = %q", in.LocationID)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"location_id": "loc_self_hold_aaaaaaaa", "label": "dev",
			"last_seen_at": "2026-09-09T00:00:00Z",
		})
	})
	c := holdTestClient(t, mux)
	h, err := c.Checkout(context.Background(), "probe", "sty_h")
	if err != nil {
		t.Fatal(err)
	}
	if h.LocationID != "loc_self_hold_aaaaaaaa" {
		t.Fatalf("hold = %+v", h)
	}
	if _, err := c.Checkout(context.Background(), "probe", "sty_h"); err != nil {
		t.Fatalf("idempotent: %v", err)
	}
	if n != 2 {
		t.Fatalf("posts = %d", n)
	}
}

func TestCheckoutHeldElsewhere(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{project}/workstate/items/{id}/checkout", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "held", "code": "held",
			"hold": map[string]string{"location_id": "loc_other_bbbbbbbb", "last_seen_at": "2026-09-01T00:00:00Z", "label": "desk"},
		})
	})
	c := holdTestClient(t, mux)
	_, err := c.Checkout(context.Background(), "probe", "sty_h")
	var held *HeldError
	if !errors.As(err, &held) || held.Hold.LocationID != "loc_other_bbbbbbbb" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "last seen") || !strings.Contains(err.Error(), "hold takeover") {
		t.Errorf("message = %v", err)
	}
}

func TestCheckoutSameLocation409IsSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{project}/workstate/items/{id}/checkout", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "held",
			"hold": map[string]string{"location_id": "loc_self_hold_aaaaaaaa"},
		})
	})
	c := holdTestClient(t, mux)
	h, err := c.Checkout(context.Background(), "probe", "sty_h")
	if err != nil {
		t.Fatal(err)
	}
	if h.LocationID != "loc_self_hold_aaaaaaaa" {
		t.Fatalf("hold = %+v", h)
	}
}

func TestReleaseHoldNotOwner(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{project}/workstate/items/{id}/release", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "held",
			"hold": map[string]string{"location_id": "loc_other_bbbbbbbb", "last_seen_at": "2026-09-01T00:00:00Z"},
		})
	})
	c := holdTestClient(t, mux)
	err := c.ReleaseHold(context.Background(), "probe", "sty_h")
	if !errors.Is(err, ErrHeldElsewhere) {
		t.Fatalf("err = %v", err)
	}
}

func TestTakeoverHoldNamesPrevious(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{project}/workstate/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":   "sty_h",
			"hold": map[string]string{"location_id": "loc_prev_cccccccc", "last_seen_at": "2026-09-01T12:00:00Z", "label": "old"},
		})
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/workstate/items/{id}/takeover", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			LocationID string `json:"location_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.LocationID != "loc_self_hold_aaaaaaaa" {
			t.Errorf("takeover body = %q", in.LocationID)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"location_id": "loc_self_hold_aaaaaaaa"})
	})
	c := holdTestClient(t, mux)
	res, err := c.TakeoverHold(context.Background(), "probe", "sty_h")
	if err != nil {
		t.Fatal(err)
	}
	if res.Previous.LocationID != "loc_prev_cccccccc" || res.Hold.LocationID != "loc_self_hold_aaaaaaaa" {
		t.Fatalf("result = %+v", res)
	}
}

func TestFormatLastSeen(t *testing.T) {
	got := FormatLastSeen("2026-09-01T00:00:00Z")
	if !strings.Contains(got, "2026-09-01T00:00:00Z") || !strings.Contains(got, "ago") {
		t.Fatalf("FormatLastSeen = %q", got)
	}
	if FormatLastSeen("") != "" {
		t.Fatal("empty last_seen should stay empty")
	}
}

func TestHoldClientHasNoTTL(t *testing.T) {
	src, err := os.ReadFile("client_hold.go")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(src))
	for _, bad := range []string{"ttl", "expir", "auto-release", "time.sleep"} {
		if strings.Contains(body, bad) {
			t.Errorf("hold client must not implement %q", bad)
		}
	}
}

func TestSyncServiceHasNoHoldRPC(t *testing.T) {
	got := map[string]bool{}
	for _, m := range syncpb.Sync_ServiceDesc.Methods {
		got[m.MethodName] = true
	}
	for _, want := range []string{"Apply", "Snapshot", "Refresh"} {
		if !got[want] {
			t.Errorf("missing RPC %s", want)
		}
	}
	if len(syncpb.Sync_ServiceDesc.Methods) != 3 {
		t.Fatalf("Sync RPCs = %d, want exactly Apply/Snapshot/Refresh", len(syncpb.Sync_ServiceDesc.Methods))
	}
}
