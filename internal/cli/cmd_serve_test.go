package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/bobmcallan/satelle/internal/web"
)

func TestFirstSegment(t *testing.T) {
	cases := map[string]string{
		"/alpha/x":   "alpha",
		"/alpha":     "alpha",
		"/a/b/c":     "a",
		"/":          "",
		"":           "",
		"/projects":  "projects",
		"/sat-home/": "sat-home",
	}
	for in, want := range cases {
		if got := firstSegment(in); got != want {
			t.Errorf("firstSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAssignSlugDedupAndReserved(t *testing.T) {
	s := newSupervisor(context.Background(), io.Discard, io.Discard, "self")
	// Same base name → de-duplicated.
	if got := s.assignSlug("/a/repo"); got != "repo" {
		t.Errorf("first repo slug = %q, want repo", got)
	}
	if got := s.assignSlug("/b/repo"); got != "repo-2" {
		t.Errorf("colliding repo slug = %q, want repo-2", got)
	}
	// A name that collides with a reserved bound route is pushed off it.
	if got := s.assignSlug("/c/static"); got != "static-2" {
		t.Errorf("reserved-name slug = %q, want static-2", got)
	}
	// Stable: the same path keeps its slug.
	if got := s.assignSlug("/a/repo"); got != "repo" {
		t.Errorf("slug not stable for same path: %q", got)
	}
}

func TestChildRootsEmptyRegistry(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir()) // isolated, empty registry
	bound := t.TempDir()
	// bound must be a live satelle root (sty_7a8d5d44 filter).
	if err := os.MkdirAll(filepath.Join(bound, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots := registeredRoots(bound)
	if len(roots) != 1 || roots[0] != bound {
		t.Errorf("registeredRoots = %v, want [%s]", roots, bound)
	}
	// Every registered repo is a child now — including the launch repo — so an
	// empty registry still yields the launch repo as the sole child.
	if cr := childRoots(bound); len(cr) != 1 || cr[0] != bound {
		t.Errorf("childRoots with no registry = %v, want [%s]", cr, bound)
	}
}

// TestRegisteredRootsSkipsDeadPaths (sty_7a8d5d44 AC3): missing paths and dirs
// without .satelle are dropped — no child spawn surface for them.
func TestRegisteredRootsSkipsDeadPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)

	bound := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bound, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	live := t.TempDir()
	if err := os.MkdirAll(filepath.Join(live, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	deadMissing := filepath.Join(t.TempDir(), "gone") // never created
	deadNoSatelle := t.TempDir()                      // exists but no .satelle

	// Write global registry with live + dead entries.
	cfg := "\n[workspace]\nrepos = [\n"
	for _, p := range []string{live, deadMissing, deadNoSatelle} {
		cfg += "  " + strconv.Quote(p) + ",\n"
	}
	cfg += "]\n"
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	roots := registeredRoots(bound)
	want := map[string]bool{bound: true, live: true}
	if len(roots) != 2 {
		t.Fatalf("registeredRoots = %v, want bound+live only", roots)
	}
	for _, r := range roots {
		if !want[r] {
			t.Errorf("unexpected root %q", r)
		}
	}
	// Explicit: dead paths never appear.
	for _, dead := range []string{deadMissing, deadNoSatelle} {
		for _, r := range roots {
			if r == dead {
				t.Errorf("dead root %q not filtered", dead)
			}
		}
	}

	// AC3: childRoots is the sole multi-serve input — same filter as registeredRoots
	// (identity wrapper), so dead roots yield zero child + zero /<slug>/ route.
	cr := childRoots(bound)
	if len(cr) != 2 {
		t.Fatalf("childRoots = %v, want bound+live only (zero child for dead paths)", cr)
	}
	for _, r := range cr {
		if !want[r] {
			t.Errorf("childRoots unexpected %q", r)
		}
	}
	// assignSlug only for live set: dead paths never mint a route slug.
	s := newSupervisor(context.Background(), io.Discard, io.Discard, "self")
	gotSlugs := map[string]string{}
	for _, r := range cr {
		gotSlugs[r] = s.assignSlug(r)
	}
	if len(gotSlugs) != 2 {
		t.Fatalf("assignSlug over live set size = %d, want 2", len(gotSlugs))
	}
	for _, dead := range []string{deadMissing, deadNoSatelle} {
		if _, ok := gotSlugs[dead]; ok {
			t.Errorf("dead path %q received a slug (would emit /<slug>/ route)", dead)
		}
	}
}

func TestTopHandlerRouting(t *testing.T) {
	s := newSupervisor(context.Background(), io.Discard, io.Discard, "self")
	// Inject a child by hand (no real process).
	child := &childProc{
		project: web.Project{Slug: "alpha", Name: "alpha", Path: "/p/alpha"},
		handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) }),
	}
	s.children["/p/alpha"] = child
	s.bySlug["alpha"] = child
	s.order = []string{"/p/alpha"}

	sharedHit := false
	shared := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sharedHit = true
		w.WriteHeader(http.StatusNotFound) // the shared chrome mux 404s unknown paths
	})
	h := s.topHandler(shared)

	do := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	// A known slug routes to its child (not the shared handler).
	sharedHit = false
	if rec := do("/alpha/fragment/stories"); rec.Code != 299 || sharedHit {
		t.Errorf("/alpha/... did not route to its child (code=%d, shared=%v)", rec.Code, sharedHit)
	}
	// / renders the connected-projects landing, not the shared handler.
	sharedHit = false
	if rec := do("/"); sharedHit || rec.Code != http.StatusOK {
		t.Errorf("/ did not render the landing (code=%d, shared=%v)", rec.Code, sharedHit)
	}
	// /projects redirects to the landing at / (back-compat for older links).
	sharedHit = false
	if rec := do("/projects"); sharedHit || rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Errorf("/projects did not redirect to / (code=%d, loc=%q, shared=%v)", rec.Code, rec.Header().Get("Location"), sharedHit)
	}
	// An UNKNOWN prefix falls through to the shared handler, which 404s.
	sharedHit = false
	if rec := do("/bogus/fragment/stories"); !sharedHit || rec.Code != http.StatusNotFound {
		t.Errorf("unknown prefix should 404 via the shared handler (code=%d, shared=%v)", rec.Code, sharedHit)
	}
}

// TestWithProjectsHeader proves the supervisor injects its live child snapshot into
// each proxied request (in display order) and overwrites any spoofed inbound value
// (sty_2bc00a9d).
func TestWithProjectsHeader(t *testing.T) {
	s := &supervisor{
		children: map[string]*childProc{
			"/x/a": {project: web.Project{Slug: "a", Name: "a"}},
			"/x/b": {project: web.Project{Slug: "b", Name: "b"}},
		},
		order: []string{"/x/a", "/x/b"},
	}
	var got string
	rec := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(web.ProjectsHeader)
	})
	req := httptest.NewRequest(http.MethodGet, "/a/", nil)
	req.Header.Set(web.ProjectsHeader, "SPOOFED") // must be overwritten by the supervisor
	s.withProjects(rec).ServeHTTP(httptest.NewRecorder(), req)

	if got == "SPOOFED" || got == "" {
		t.Fatalf("supervisor did not overwrite/set the header: %q", got)
	}
	projs := web.DecodeProjects(got)
	if len(projs) != 2 || projs[0].Slug != "a" || projs[1].Slug != "b" {
		t.Fatalf("header did not carry both children in display order: %+v", projs)
	}
}
