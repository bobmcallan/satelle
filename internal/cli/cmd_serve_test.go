package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
