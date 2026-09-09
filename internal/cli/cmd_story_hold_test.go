package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/testutil"
)

func TestStoryHoldCommandsDistinctFromSeat(t *testing.T) {
	hold := storyHoldCommands()
	if hold.Name() != "hold" {
		t.Fatalf("name = %q", hold.Name())
	}
	names := map[string]bool{}
	for _, c := range hold.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"checkout", "release", "takeover"} {
		if !names[want] {
			t.Errorf("missing subcommand %s", want)
		}
	}
	if strings.Contains(hold.Long, "seat release") == false {
		t.Error("hold Long must contrast satelle story seat release")
	}
	rel := hold.Commands()
	var releaseLong string
	for _, c := range rel {
		if c.Name() == "release" {
			releaseLong = c.Long
		}
	}
	if !strings.Contains(releaseLong, "seat release") {
		t.Error("hold release Long must say it is not seat release")
	}

	seat := storySeatCommands()
	if len(seat) == 0 || seat[0].Name() != "seat" {
		t.Fatal("seat group missing")
	}
}

func TestStoryHoldUnboundRefusesWithoutHTTP(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := t.TempDir()
	if err := os.MkdirAll(repo+"/.satelle", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repo+"/.satelle/satelle.toml", []byte("[gate]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	cmd, _ := testCmd()
	err := runStoryHoldCheckout(cmd, "https://example.invalid", "sty_x")
	if err == nil {
		t.Fatal("unbound checkout should refuse")
	}
	if !strings.Contains(err.Error(), "bound") {
		t.Fatalf("err = %v, want unbound message", err)
	}
}

func TestStoryHoldCLIHasNoTTL(t *testing.T) {
	src, err := os.ReadFile("cmd_story_hold.go")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(src))
	for _, bad := range []string{"time.duration", "auto-release", "expires_at"} {
		if strings.Contains(body, bad) {
			t.Errorf("hold CLI must not implement %q", bad)
		}
	}
}

func TestTakeoverOutputNamesPrevious(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	hosted.LocationStatePathOverride = t.TempDir() + "/location-state.json"
	t.Cleanup(func() { hosted.LocationStatePathOverride = "" })

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/workstate/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "sty_h",
			"hold": map[string]string{
				"location_id":  "loc_prev_cccccccc",
				"last_seen_at": "2026-09-01T12:00:00Z",
				"label":        "old",
			},
		})
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/workstate/items/{id}/takeover", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"location_id": "loc_self"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	seedCred(t, ts.URL)
	workstateRepo(t, "[hosted]\nproject = \"probe\"\n")

	cmd, buf := testCmd()
	if err := runStoryHoldTakeover(cmd, ts.URL, "sty_h"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "loc_prev_cccccccc") || !strings.Contains(out, "last seen") {
		t.Fatalf("takeover output = %q", out)
	}
}

func TestTakeoverOutputServerOnly(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	hosted.LocationStatePathOverride = t.TempDir() + "/location-state.json"
	t.Cleanup(func() { hosted.LocationStatePathOverride = "" })

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	})
	mux.HandleFunc("GET /api/v1/projects/{project}/workstate/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "sty_h"})
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/workstate/items/{id}/takeover", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"location_id": "loc_self"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	seedCred(t, ts.URL)
	workstateRepo(t, "[hosted]\nproject = \"probe\"\n")

	cmd, buf := testCmd()
	if err := runStoryHoldTakeover(cmd, ts.URL, "sty_h"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "unheld") {
		t.Fatalf("unheld takeover = %q", buf.String())
	}
}
