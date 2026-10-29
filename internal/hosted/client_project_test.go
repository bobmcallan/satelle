package hosted

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// TestClientGetProjectSubstrate: GET /api/v1/projects/{p} decodes the substrate
// source and its holder; a 404 is ErrProjectNotFound; a body with no substrate
// (an older server) leaves it nil.
func TestClientGetProjectSubstrate(t *testing.T) {
	_, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/probe":
			_, _ = w.Write([]byte(`{"id":"p1","slug":"probe","name":"Probe","team_id":"t1","substrate":{"source":"hosted","repo":"","ref":"","holder":{"user_id":"usr_h","email":"holder@example.com"}}}`))
		case "/api/v1/projects/old":
			_, _ = w.Write([]byte(`{"id":"p2","slug":"old","name":"Old"}`))
		default:
			http.NotFound(w, r)
		}
	})

	p, err := c.GetProject(context.Background(), "probe")
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if p.TeamID != "t1" || p.Substrate == nil || p.Substrate.Source != SubstrateHosted || p.Substrate.Holder == nil {
		t.Fatalf("project = %+v", p)
	}
	if h := *p.Substrate.Holder; h.UserID != "usr_h" || h.Email != "holder@example.com" {
		t.Errorf("holder = %+v", h)
	}

	if old, err := c.GetProject(context.Background(), "old"); err != nil || old.Substrate != nil {
		t.Errorf("older server project = %+v, %v; want no substrate", old, err)
	}
	if _, err := c.GetProject(context.Background(), "ghost"); !errors.Is(err, ErrProjectNotFound) {
		t.Errorf("missing project err = %v, want ErrProjectNotFound", err)
	}
}
