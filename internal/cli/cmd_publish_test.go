package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakePublishStore is an in-memory team publish catalog for CLI tests.
type fakePublishStore struct {
	mu   sync.Mutex
	data map[string]map[string][]publishVer // wsID -> path -> versions
}

type publishVer struct {
	content []byte
	kind    string
	title   string
	pub     string
}

func newFakePublishStore() *fakePublishStore {
	return &fakePublishStore{data: map[string]map[string][]publishVer{}}
}

func (s *fakePublishStore) put(wsID, path, kind, title, publisher string, content []byte) (version int, created bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.data[wsID]
	if ws == nil {
		ws = map[string][]publishVer{}
		s.data[wsID] = ws
	}
	vers := ws[path]
	if len(vers) > 0 && string(vers[len(vers)-1].content) == string(content) {
		return len(vers), false
	}
	ws[path] = append(vers, publishVer{content: content, kind: kind, title: title, pub: publisher})
	return len(ws[path]), true
}

func (s *fakePublishStore) get(wsID, path string, version int) (publishVer, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vers := s.data[wsID][path]
	if len(vers) == 0 {
		return publishVer{}, 0, false
	}
	if version <= 0 {
		return vers[len(vers)-1], len(vers), true
	}
	if version > len(vers) {
		return publishVer{}, 0, false
	}
	return vers[version-1], version, true
}

func (s *fakePublishStore) list(wsID string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for path, vers := range s.data[wsID] {
		head := vers[len(vers)-1]
		sum := sha256.Sum256(head.content)
		out = append(out, map[string]any{
			"path": path, "kind": head.kind, "version": len(vers),
			"blob_sha256": hex.EncodeToString(sum[:]), "size": len(head.content),
			"publisher_id": head.pub, "title": head.title, "created_at": "t",
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out
}

// newFakePublishServer serves workspaces + /published catalog routes.
func newFakePublishServer(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store := newFakePublishStore()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"id": "ws-personal", "kind": "personal", "name": "personal"},
			{"id": "ws-team", "kind": "team", "name": "Acme"},
		})
	})
	mux.HandleFunc("/api/v1/workspaces/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/workspaces/")
		// wsID/published[/path...]
		segs := strings.SplitN(rest, "/", 3)
		if len(segs) < 2 || segs[1] != "published" {
			http.NotFound(w, r)
			return
		}
		wsID := segs[0]
		path := ""
		if len(segs) == 3 {
			path = segs[2]
		}
		switch {
		case r.Method == http.MethodGet && path == "":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": store.list(wsID)})
		case r.Method == http.MethodPut && path != "":
			body, _ := io.ReadAll(r.Body)
			kind := r.URL.Query().Get("kind")
			title := r.URL.Query().Get("title")
			ver, created := store.put(wsID, path, kind, title, "user-1", body)
			status := http.StatusOK
			if created {
				status = http.StatusCreated
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"path": path, "kind": kind, "version": ver, "created": created,
				"publisher_id": "user-1", "title": title,
			})
		case r.Method == http.MethodGet && path != "":
			ver := 0
			if v := r.URL.Query().Get("version"); v != "" {
				// already int via strconv in client; parse lightly
				for _, c := range v {
					if c < '0' || c > '9' {
						break
					}
					ver = ver*10 + int(c-'0')
				}
			}
			pv, n, ok := store.get(wsID, path, ver)
			if !ok {
				http.NotFound(w, r)
				return
			}
			sum := sha256.Sum256(pv.content)
			w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
			w.Header().Set("X-Satelle-Publish-Version", itoa(n))
			w.Header().Set("X-Satelle-Publish-Kind", pv.kind)
			w.Header().Set("X-Satelle-Publisher-Id", pv.pub)
			_, _ = w.Write(pv.content)
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestPublishPushRequiresTeam(t *testing.T) {
	ts := newFakePublishServer(t)
	seedCred(t, ts.URL)
	repo := syncConfigRepo(t, "") // no team binding
	writeRepoFile(t, repo, ".satelle/skills/x.md", "body\n")
	pointAt(t, repo)

	cmd, _ := testCmd()
	err := runPublishPush(cmd, ts.URL, "", "skill", "", false, []string{"skills/x.md"})
	if err == nil || !strings.Contains(err.Error(), "no team workspace") {
		t.Fatalf("expected no-team error, got: %v", err)
	}
}

func TestPublishPushListAdoptCheck(t *testing.T) {
	ts := newFakePublishServer(t)
	seedCred(t, ts.URL)

	src := syncConfigRepo(t, "[hosted]\nworkspace = \"Acme\"\n")
	writeRepoFile(t, src, ".satelle/skills/shared.md", "version one\n")
	pointAt(t, src)

	cmd, buf := testCmd()
	if err := runPublishPush(cmd, ts.URL, "", "", "", false, []string{"skills/shared.md"}); err != nil {
		t.Fatalf("publish push: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "new") {
		t.Fatalf("first publish should be new: %q", buf.String())
	}

	// Idempotent re-publish
	cmd2, buf2 := testCmd()
	if err := runPublishPush(cmd2, ts.URL, "", "", "", false, []string{"skills/shared.md"}); err != nil {
		t.Fatalf("re-publish: %v", err)
	}
	if !strings.Contains(buf2.String(), "unchanged") {
		t.Fatalf("identical re-publish should be unchanged: %q", buf2.String())
	}

	// New version
	writeRepoFile(t, src, ".satelle/skills/shared.md", "version two\n")
	cmd3, buf3 := testCmd()
	if err := runPublishPush(cmd3, ts.URL, "", "", "", false, []string{"skills/shared.md"}); err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	if !strings.Contains(buf3.String(), "v2") {
		t.Fatalf("expected v2: %q", buf3.String())
	}

	cmdL, bufL := testCmd()
	if err := runPublishList(cmdL, ts.URL, "Acme"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(bufL.String(), "skills/shared.md") {
		t.Fatalf("list missing path: %q", bufL.String())
	}

	// Adopt into a fresh personal repo
	dst := syncConfigRepo(t, "[hosted]\nworkspace = \"Acme\"\n")
	pointAt(t, dst)
	cmdA, bufA := testCmd()
	if err := runPublishAdopt(cmdA, ts.URL, "Acme", 0, "skills/shared.md"); err != nil {
		t.Fatalf("adopt: %v\n%s", err, bufA.String())
	}
	got, err := os.ReadFile(filepath.Join(dst, ".satelle", "skills", "shared.md"))
	if err != nil {
		t.Fatalf("adopted file: %v", err)
	}
	if string(got) != "version two\n" {
		t.Errorf("adopted content = %q, want version two", got)
	}
	recs, err := loadAdoptions(filepath.Join(dst, ".satelle"))
	if err != nil || len(recs) != 1 || recs[0].Version != 2 {
		t.Fatalf("adoptions record: %+v err=%v", recs, err)
	}

	// check: up to date
	cmdC, bufC := testCmd()
	if err := runPublishCheck(cmdC, ts.URL, "Acme", false); err != nil {
		t.Fatalf("check: %v", err)
	}
	if !strings.Contains(bufC.String(), "up to date") {
		t.Fatalf("expected up to date: %q", bufC.String())
	}

	// Publisher ships v3; consumer sees newer, declines without --update
	pointAt(t, src)
	writeRepoFile(t, src, ".satelle/skills/shared.md", "version three\n")
	cmd4, _ := testCmd()
	if err := runPublishPush(cmd4, ts.URL, "", "", "", false, []string{"skills/shared.md"}); err != nil {
		t.Fatalf("publish v3: %v", err)
	}
	pointAt(t, dst)
	cmdC2, bufC2 := testCmd()
	if err := runPublishCheck(cmdC2, ts.URL, "Acme", false); err != nil {
		t.Fatalf("check newer: %v", err)
	}
	if !strings.Contains(bufC2.String(), "new version") {
		t.Fatalf("expected new version notice: %q", bufC2.String())
	}
	still, _ := os.ReadFile(filepath.Join(dst, ".satelle", "skills", "shared.md"))
	if string(still) != "version two\n" {
		t.Errorf("without --update local must stay v2, got %q", still)
	}

	// Opt-in update
	cmdU, bufU := testCmd()
	if err := runPublishCheck(cmdU, ts.URL, "Acme", true); err != nil {
		t.Fatalf("check --update: %v\n%s", err, bufU.String())
	}
	updated, _ := os.ReadFile(filepath.Join(dst, ".satelle", "skills", "shared.md"))
	if string(updated) != "version three\n" {
		t.Errorf("after --update want v3, got %q", updated)
	}
	recs2, _ := loadAdoptions(filepath.Join(dst, ".satelle"))
	if len(recs2) != 1 || recs2[0].Version != 3 {
		t.Fatalf("adoption version after update: %+v", recs2)
	}
}
