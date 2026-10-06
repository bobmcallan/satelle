package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// TestSessionTokenIsBearerThroughCLIClientConstruction (sty_6ed6318d AC1): with
// SATELLE_TOKEN set and an empty home — no login, no credentials file — the
// CLI's own client construction sends the token as the bearer.
func TestSessionTokenIsBearerThroughCLIClientConstruction(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv(hosted.SessionTokenEnv, "sat_pat_bearer_check")

	var projectAuth, locationAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		projectAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(hosted.Project{ID: "p1", Slug: "acme", Name: "Acme"})
	})
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		locationAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "loc", "user_id": "u_1"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	cmd, _ := testCmd()
	if err := runProjectCreate(cmd, ts.URL, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	if projectAuth != "Bearer sat_pat_bearer_check" {
		t.Errorf("project create Authorization = %q", projectAuth)
	}

	hosted.LocationStatePathOverride = t.TempDir() + "/location-state.json"
	t.Cleanup(func() { hosted.LocationStatePathOverride = "" })
	newHostedClient(context.Background(), ts.URL, t.TempDir())
	if locationAuth != "Bearer sat_pat_bearer_check" {
		t.Errorf("location registration Authorization = %q", locationAuth)
	}
}
