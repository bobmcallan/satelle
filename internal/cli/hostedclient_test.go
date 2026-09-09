package cli

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/testutil"
)

func TestNewHostedClientBoundSendsHeaderAndRegisters(t *testing.T) {
	testutil.IsolateHome(t)
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	hosted.LocationStatePathOverride = filepath.Join(xdg, "satelle", "location-state.json")
	t.Cleanup(func() { hosted.LocationStatePathOverride = "" })

	var gotHeader, gotID string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/locations", func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(hosted.LocationHeader)
		var in struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		gotID = in.ID
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": in.ID})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	store := hosted.FileStore{}
	_ = store.Save(hosted.Credential{ServerURL: ts.URL, AccessToken: "tok", RefreshToken: "r"})

	repo := t.TempDir()
	c := newHostedClient(context.Background(), ts.URL, repo)
	if c.Location() == "" {
		t.Fatal("bound client has empty location")
	}
	if gotID == "" || gotHeader != gotID || gotID != c.Location() {
		t.Fatalf("register id=%q header=%q client=%q", gotID, gotHeader, c.Location())
	}
}

func TestNewHostedClientEmptyRepoDoesNotWriteState(t *testing.T) {
	testutil.IsolateHome(t)
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	path := filepath.Join(xdg, "satelle", "location-state.json")
	hosted.LocationStatePathOverride = path
	t.Cleanup(func() { hosted.LocationStatePathOverride = "" })

	c := newHostedClient(context.Background(), "https://example.invalid", "")
	if c.Location() != "" {
		t.Fatalf("empty repoRoot attached location %q", c.Location())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("state file created for empty repoRoot: %v", err)
	}
}

func TestSyncStoryCallSitesUseNewHostedClient(t *testing.T) {
	// Production sync/story files must not call hosted.NewClient directly —
	// that path skips x-satelle-location. Tests and login/project/publish/backup
	// stay on NewClient by design (architecture review, sty_e88d77ce).
	files := []string{
		"cmd_sync.go",
		"cmd_sync_documents.go",
		"cmd_sync_workstate.go",
	}
	fset := token.NewFileSet()
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		body := string(src)
		if !strings.Contains(body, "newHostedClient(") {
			t.Errorf("%s: missing newHostedClient", name)
		}
		// hosted.NewClient in a production .go (not _test.go) is the silent-open
		// failure the architecture review named.
		_ = f
		if strings.Contains(body, "hosted.NewClient(") {
			t.Errorf("%s: production sync file still calls hosted.NewClient", name)
		}
	}
}
