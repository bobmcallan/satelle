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
	s := newSupervisor(context.Background(), io.Discard, io.Discard, "self", "/repos/bound")
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
	if cr := childRoots(bound); cr != nil {
		t.Errorf("childRoots with no registry = %v, want nil", cr)
	}
}

func TestTopHandlerRouting(t *testing.T) {
	s := newSupervisor(context.Background(), io.Discard, io.Discard, "self", "/repos/bound")
	// Inject a child by hand (no real process).
	child := &childProc{
		project: web.Project{Slug: "alpha", Name: "alpha", Path: "/p/alpha"},
		handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) }),
	}
	s.children["/p/alpha"] = child
	s.bySlug["alpha"] = child
	s.order = []string{"/p/alpha"}

	boundHit := false
	bound := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		boundHit = true
		w.WriteHeader(http.StatusNotFound) // the real bound mux 404s unknown paths
	})
	h := s.topHandler(bound)

	do := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	// A known slug routes to its child (not the bound handler).
	boundHit = false
	if rec := do("/alpha/fragment/stories"); rec.Code != 299 || boundHit {
		t.Errorf("/alpha/... did not route to its child (code=%d, bound=%v)", rec.Code, boundHit)
	}
	// /projects renders the launcher, not the bound handler.
	boundHit = false
	if rec := do("/projects"); boundHit || rec.Code != http.StatusOK {
		t.Errorf("/projects did not render the launcher (code=%d, bound=%v)", rec.Code, boundHit)
	}
	// An UNKNOWN prefix falls through to the bound handler, which 404s (AC3).
	boundHit = false
	if rec := do("/bogus/fragment/stories"); !boundHit || rec.Code != http.StatusNotFound {
		t.Errorf("unknown prefix should 404 via the bound handler (code=%d, bound=%v)", rec.Code, boundHit)
	}
	// A normal root path also falls through to the bound handler.
	boundHit = false
	if do("/"); !boundHit {
		t.Error("root path should fall through to the bound handler")
	}
}
