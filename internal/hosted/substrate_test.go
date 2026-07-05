package hosted

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// substrateMock is a contract-faithful in-memory mirror of the server's substrate
// store (satelle-server internal/substrate): a PUT is a full-snapshot REPLACE
// (absent paths deleted), sha256 is computed server-side, and the summary reports
// added/updated/unchanged/removed so idempotence is observable. GET ?full=1
// returns every file with base64 bytes.
type substrateMock struct {
	files map[string][]byte // path -> content
}

func newSubstrateMock() *substrateMock { return &substrateMock{files: map[string][]byte{}} }

func (m *substrateMock) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/projects/proj/substrate", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer acc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodPut:
			var in struct {
				Files []substrateFileWire `json:"files"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			incoming := map[string][]byte{}
			for _, f := range in.Files {
				b, err := base64.StdEncoding.DecodeString(f.Content)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				incoming[f.Path] = b
			}
			var added, updated, unchanged, removed int
			for p, b := range incoming {
				if old, ok := m.files[p]; ok {
					if sha(old) == sha(b) {
						unchanged++
					} else {
						updated++
					}
				} else {
					added++
				}
			}
			for p := range m.files {
				if _, keep := incoming[p]; !keep {
					removed++
				}
			}
			m.files = incoming
			writeJSON(w, map[string]int{"added": added, "updated": updated, "unchanged": unchanged, "removed": removed})
		case http.MethodGet:
			if r.URL.Query().Get("full") != "1" {
				writeJSON(w, map[string]any{"files": []substrateFileWire{}})
				return
			}
			paths := make([]string, 0, len(m.files))
			for p := range m.files {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			out := make([]substrateFileWire, 0, len(paths))
			for _, p := range paths {
				b := m.files[p]
				out = append(out, substrateFileWire{Path: p, SHA256: sha(b), Size: int64(len(b)), Content: base64.StdEncoding.EncodeToString(b)})
			}
			writeJSON(w, map[string]any{"files": out})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func seededStore(t *testing.T, server string) Store {
	t.Helper()
	m := &memStore{}
	_ = m.Save(Credential{ServerURL: server, AccessToken: "acc", RefreshToken: "ref"})
	return m
}

// TestPushPullSubstrateRoundTrip proves the client half of the round-trip against
// the faithful mock: a push reports added counts, an identical re-push is all
// Unchanged (AC2 idempotence), and a full pull returns every file byte-exact
// (binary-safe), sorted.
func TestPushPullSubstrateRoundTrip(t *testing.T) {
	ts := httptest.NewServer(newSubstrateMock().handler(t))
	defer ts.Close()
	c := NewClient(ts.URL, seededStore(t, ts.URL), nil)
	ctx := context.Background()

	files := []SubstrateFile{
		{Path: "constitution.md", Content: []byte("# c\n")},
		{Path: "skills/commit.md", Kind: "skills", Content: []byte("a\r\nb")},
		{Path: "documents/testdata/blob.bin", Kind: "documents", Content: []byte{0, 1, 2, 0xff}},
	}

	sum, err := c.PushSubstrate(ctx, "proj", files)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if sum.Added != 3 || sum.Updated != 0 || sum.Removed != 0 {
		t.Fatalf("first push summary = %+v, want 3 added", sum)
	}

	// Idempotent re-push: nothing changes.
	sum2, err := c.PushSubstrate(ctx, "proj", files)
	if err != nil {
		t.Fatalf("re-push: %v", err)
	}
	if sum2.Unchanged != 3 || sum2.Added != 0 || sum2.Updated != 0 || sum2.Removed != 0 {
		t.Fatalf("re-push summary = %+v, want all unchanged (idempotent)", sum2)
	}

	// Pull reconstructs every file byte-exact.
	got, err := c.PullSubstrate(ctx, "proj")
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("pulled %d files, want 3", len(got))
	}
	byPath := map[string][]byte{}
	for _, f := range got {
		byPath[f.Path] = f.Content
	}
	for _, f := range files {
		if g := byPath[f.Path]; string(g) != string(f.Content) {
			t.Errorf("pulled %s = %q, want %q", f.Path, g, f.Content)
		}
	}
}

// TestPushSubstrateErrorMapping maps auth/role/not-found to clean sentinels/messages.
func TestPushSubstrateErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		code   int
		verify func(t *testing.T, err error)
	}{
		{"unauthorized", http.StatusUnauthorized, func(t *testing.T, err error) {
			if !errors.Is(err, ErrLoginRequired) {
				t.Errorf("401 should map to ErrLoginRequired, got %v", err)
			}
		}},
		{"forbidden", http.StatusForbidden, func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "editor or owner") {
				t.Errorf("403 should mention editor/owner role, got %v", err)
			}
		}},
		{"notfound", http.StatusNotFound, func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "not found") {
				t.Errorf("404 should mention project not found, got %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
			}))
			defer ts.Close()
			c := NewClient(ts.URL, seededStore(t, ts.URL), nil)
			_, err := c.PushSubstrate(context.Background(), "proj", []SubstrateFile{{Path: "a.md", Content: []byte("x")}})
			tc.verify(t, err)
		})
	}
}

// TestPullSubstrateEmpty: a project with no backup yet yields an empty slice, no error.
func TestPullSubstrateEmpty(t *testing.T) {
	ts := httptest.NewServer(newSubstrateMock().handler(t))
	defer ts.Close()
	c := NewClient(ts.URL, seededStore(t, ts.URL), nil)
	got, err := c.PullSubstrate(context.Background(), "proj")
	if err != nil {
		t.Fatalf("pull empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty backup should yield 0 files, got %d", len(got))
	}
}

// TestPushSubstrateNoCredential yields ErrLoginRequired without a stored credential.
func TestPushSubstrateNoCredential(t *testing.T) {
	ts := httptest.NewServer(newSubstrateMock().handler(t))
	defer ts.Close()
	c := NewClient(ts.URL, &memStore{}, nil) // empty store
	_, err := c.PushSubstrate(context.Background(), "proj", []SubstrateFile{{Path: "a.md", Content: []byte("x")}})
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("no credential should yield ErrLoginRequired, got %v", err)
	}
}
