package hosted

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClientApplyUsesWorkstatePOST(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody []byte
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"items":1,"ledger":0}`))
	})
	_ = ts
	res, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Items != 1 {
		t.Fatalf("items = %d", res.Items)
	}
	if gotPath != "/api/v1/projects/probe/workstate" {
		t.Fatalf("path = %s", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("auth = %q", gotAuth)
	}
	if strings.Contains(gotPath, "config") || strings.Contains(string(gotBody), "/documents") {
		t.Fatal("apply must not hit config/documents")
	}
}

func TestClientApplyTransportError(t *testing.T) {
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	})
	_ = ts
	_, err := c.Apply(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{json.RawMessage(`{"id":"sty_1"}`)},
	})
	if err == nil {
		t.Fatal("expected apply error")
	}
}

func TestClientSnapshotUsesWorkstateLists(t *testing.T) {
	seen := map[string]bool{}
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		switch r.URL.Path {
		case "/api/v1/projects/probe/workstate/items":
			_, _ = w.Write([]byte(`[{"id":"sty_1","kind":"story","title":"T"}]`))
		case "/api/v1/projects/probe/workstate/ledger":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	})
	_ = ts
	items, rows, err := c.Snapshot(context.Background(), "probe", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "sty_1" {
		t.Fatalf("snapshot items = %+v", items)
	}
	if rows == nil {
		t.Fatal("ledger nil")
	}
	if !seen["/api/v1/projects/probe/workstate/items"] || !seen["/api/v1/projects/probe/workstate/ledger"] {
		t.Fatalf("seen = %v", seen)
	}
}
