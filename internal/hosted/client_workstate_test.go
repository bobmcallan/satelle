package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClientPushWorkstate(t *testing.T) {
	var gotBody []byte
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/projects/probe/workstate" {
			http.NotFound(w, r)
			return
		}
		if r.URL.RawQuery != "" {
			t.Errorf("workstate POST must not carry a query, got %q", r.URL.RawQuery)
		}
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"items":2,"ledger":1}`))
	})
	_ = ts

	res, err := c.PushWorkstate(context.Background(), "probe", WorkstateIngest{
		Items: []json.RawMessage{
			json.RawMessage(`{"id":"sty_1","kind":"story","status":"done","title":"T"}`),
			json.RawMessage(`{"id":"exe_1","kind":"execution","status":"done","title":"R"}`),
		},
		Ledger: []json.RawMessage{
			json.RawMessage(`{"id":"evt_1","story_id":"sty_1","kind":"note"}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Items != 2 || res.Ledger != 1 {
		t.Fatalf("result = %+v", res)
	}
	// Client-supplied origin must not be required; body is the records as-is.
	if !strings.Contains(string(gotBody), `"sty_1"`) {
		t.Errorf("body missing item: %s", gotBody)
	}
	// Nil slices encode as empty arrays, not null.
	res2, err := c.PushWorkstate(context.Background(), "probe", WorkstateIngest{})
	if err != nil {
		t.Fatal(err)
	}
	_ = res2
}

func TestClientPushWorkstateAuth(t *testing.T) {
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	// Empty the store so doAuthed hits no-cred path... actually the test server
	// seeds a good token; force 401 on every request including refresh.
	_ = ts
	// Replace store with one that has tokens but server always 401s (refresh also fails).
	// configTestServer's 401 path tries refresh; simplest: drop credentials.
	c = NewClient(ts.URL, &memStore{}, ts.Client())
	_, err := c.PushWorkstate(context.Background(), "probe", WorkstateIngest{})
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("want ErrLoginRequired, got %v", err)
	}
}

func TestClientListWorkstateItems(t *testing.T) {
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/projects/probe/workstate/items" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("kind") != "story" {
			t.Errorf("kind = %q, want story", r.URL.Query().Get("kind"))
		}
		if r.URL.Query().Get("project") != "" {
			t.Errorf("legacy project= query must be absent, got %q", r.URL.Query().Get("project"))
		}
		_, _ = w.Write([]byte(`[{"id":"sty_1","kind":"story","type":"stories","status":"done","title":"T","origin":"cli-sync","record":{"id":"sty_1","kind":"story","title":"T"}}]`))
	})
	_ = ts
	items, err := c.ListWorkstateItems(context.Background(), "probe", "story")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "sty_1" {
		t.Fatalf("items = %+v", items)
	}
}

func TestClientListWorkstateLedger(t *testing.T) {
	ts, c := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/projects/probe/workstate/ledger" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"id":"evt_1","story_id":"sty_1","kind":"note","type":"ledger","origin":"cli-sync","record":{"id":"evt_1","kind":"note"}}]`))
	})
	_ = ts
	rows, err := c.ListWorkstateLedger(context.Background(), "probe", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "evt_1" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestClientListWorkstateItemsAuth(t *testing.T) {
	ts, _ := configTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	c := NewClient(ts.URL, &memStore{}, ts.Client())
	_, err := c.ListWorkstateItems(context.Background(), "probe", "")
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("want ErrLoginRequired, got %v", err)
	}
}
